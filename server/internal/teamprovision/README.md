# Autonomous project team provisioning

This package is the project-level agent/team provisioning layer used by the
autonomous software workflow.

The direction is:

```text
Mika / project
      |
      v
Intent Analyzer (deterministic)
      |
      v
Team Planner
      |
      v
Agent Provisioner
      |
      +--> PM
      +--> Architect
      +--> Backend / Frontend / Mobile / Full-stack
      +--> Code Reviewer
      +--> QA
      |
      v
Technology Team squad
      |
      v
Project Team Registry
      |
      v
workflow role resolution
```

## Determinism

Provisioning is infrastructure, so the current planner is deliberately
heuristic and deterministic rather than a free-running LLM loop. The selected
plan is snapshotted in PostgreSQL. A model-backed planner can later implement
the same Planner interface while keeping the resulting plan immutable for that
team version.

## Runtime selection

Generated specialists reuse Mika's runtime, model, thinking level and service
tier. Runtime secrets/MCP configuration are not copied into the generated agent
rows. Project resources are supplied by Multica's normal task claim context.

## Idempotency

EnsureProject uses a PostgreSQL advisory lock scoped to workspace+project.
Agents, the squad and registry are written in one transaction. Concurrent
project-created and workflow-demand triggers therefore converge to one team.

## Routing

The project registry stores one agent per role. Software implementation issues
are routed by issue text:

- mobile / React Native / Flutter / Android / iOS -> mobile_engineer
- backend / API / DB / server -> backend_engineer
- frontend / React / UI -> frontend_engineer
- cross-cutting / unknown -> fullstack_engineer

Review is always resolved through code_reviewer. Product Manager, Architect,
Reviewer and QA are intentionally excluded from the implementation-owner role.

## Bootstrap modes

1. Eager: software-like project creation provisions the team when autonomous
   workflow is enabled and Mika exists.
2. Demand-driven: an unassigned or Mika-owned project issue entering the
   software workflow provisions the team even if eager classification did not
   match.

Project deletion archives generated agents and the squad while preserving the
registry as archived audit state.
