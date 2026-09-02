# Autonomous project team management

This package is the project-level organization layer for autonomous software
delivery.

The user supplies the project/product idea. Mika turns that intent into project
and issue artifacts. Team composition itself is owned centrally by the
server-side LLM planner, not by agent prompt choreography.

```text
User idea
   |
   v
Mika -> Project / Issues
              |
              v
       LLM Team Planner
              |
     validated TeamPlan
              |
              v
      Policy / Reconciler
       |             |
       | exact role  | missing capability/role
       v             v
      reuse       create agent
         \          /
          Technology Team
                |
        project role registry
                |
                v
        Workflow Scheduler
```

## LLM responsibilities

The LLM decides the smallest useful team for the actual project and the current
issue. It may introduce roles such as:

- frontend / backend / mobile / full-stack
- DevOps / release engineering
- Security
- SRE / observability
- Data / database engineering
- AI/ML
- UX/UI
- QA
- architecture / product
- independent code review

A project does not receive every possible role by default. For example, a small
static frontend may need no DevOps or Security specialist, while a payment
platform deployed to AWS may need both.

When a later issue introduces a capability the existing team does not have, the
same planner receives the current team plan and can add the missing specialist.

## Backend remains authoritative

The model does NOT directly create agents or mutate workflow state.

It returns machine-readable JSON containing role family, stable role slug,
capabilities, responsibilities, rationale and the role that should execute the
current issue. The backend then validates the plan before applying it.

Hard policy includes:

- allowed role families only;
- maximum project team size;
- unique stable role slugs;
- an implementation-capable route for implementation issues;
- an independent review-family agent for the current review workflow;
- transactional/idempotent provisioning;
- advisory-lock protection against concurrent duplicate teams;
- per-issue-revision plan caching/audit.

Invalid model output gets one semantic repair attempt. When
`MULTICA_AUTONOMOUS_TEAM_LLM_REQUIRED=true`, an unavailable/invalid planner
fails closed instead of silently inventing a heuristic team.

## Prompt-injection boundary

Project and issue text is explicitly treated as untrusted product context.

Model-generated responsibility/reason prose is stored only as planning metadata.
It is never copied into a generated agent's system instructions.

Persistent agent instructions are produced from backend-owned templates for the
validated role family plus normalized capability slugs. This prevents text in a
project requirement from turning into persistent agent instructions.

## Reconciliation

Team reconciliation is add-only during an active project:

1. exact existing role -> reuse the agent;
2. missing required role -> create it on Mika's runtime and add it to the same
   Technology Team squad;
3. update validated capabilities/responsibilities in the registry;
4. retain existing generated agents so a replan cannot invalidate in-flight
   work;
5. enforce `MULTICA_AUTONOMOUS_TEAM_MAX_AGENTS` against the cumulative team,
   not merely one LLM response.

Generated specialists reuse Mika's runtime/model/thinking/service-tier for task
execution. The control-plane team planner uses Multica's existing internal
OpenAI-compatible `pkg/llm` client.

## Dynamic routing

Each new issue revision can be re-evaluated. A backend-owned issue can therefore
be rerouted to a newly created Security or DevOps specialist if the new
requirement materially changes what expertise is needed.

The selected plan is cached in `autonomous_project_team_analysis` by
team + issue + issue revision. Duplicate events/restarts therefore do not cause
another LLM decision for the same revision.

Review rejection is also a replan point: before the workflow returns from
In Review to In Progress, the current issue revision is analyzed again and the
live workflow owner/reviewer IDs are refreshed.

## Durability

LLM calls are not performed on Multica's synchronous event-publish request
thread. Planning uses bounded asynchronous workers.

A periodic PostgreSQL reconciler also finds project issues that remain
In Progress without a workflow run and replays planning. This closes the crash
window where the API could terminate after committing an issue state but before
an asynchronous planner completed.

## Configuration

The autonomous workflow must be enabled:

```env
MULTICA_AUTONOMOUS_WORKFLOW_ENABLED=true
```

LLM-controlled team management defaults to fail-closed:

```env
MULTICA_AUTONOMOUS_TEAM_LLM_REQUIRED=true
MULTICA_AUTONOMOUS_TEAM_MAX_AGENTS=12
MULTICA_AUTONOMOUS_TEAM_MODEL=
```

The planner reuses the existing internal LLM configuration:

```env
MULTICA_LLM_API_KEY=
MULTICA_LLM_BASE_URL=
MULTICA_LLM_DEFAULT_MODEL=
MULTICA_LLM_MAX_RETRIES=
```

`MULTICA_AUTONOMOUS_TEAM_MODEL` overrides the model only for team planning.
An empty value uses `MULTICA_LLM_DEFAULT_MODEL`.

Setting `MULTICA_AUTONOMOUS_TEAM_LLM_REQUIRED=false` permits the deterministic
heuristic planner as a degraded fallback; that is intended for development, not
for the fully LLM-managed mode.
