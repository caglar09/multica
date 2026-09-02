package teamprovision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ReconcileForIssue asks the planner for the complete desired team for this
// issue revision, then applies an add-only diff to the durable project team.
// Existing generated roles are retained so an LLM replan cannot disrupt
// in-flight work. Missing roles are provisioned immediately.
func (p *Provisioner) ReconcileForIssue(ctx context.Context, issue db.Issue) (Team, Plan, error) {
	if p == nil || p.pool == nil || p.queries == nil || p.planner == nil {
		return Team{}, Plan{}, errors.New("team provisioner is not configured")
	}
	if !issue.ProjectID.Valid {
		return Team{}, Plan{}, errors.New("issue has no project for team reconciliation")
	}

	team, err := p.EnsureProject(ctx, issue.WorkspaceID, issue.ProjectID)
	if err != nil {
		return Team{}, Plan{}, err
	}

	revision := strconv.FormatInt(issue.Revision, 10)
	if cached, ok, err := p.loadAnalysisPlan(ctx, team.ID, "issue", issue.ID, revision); err != nil {
		return Team{}, Plan{}, err
	} else if ok {
		return team, cached, nil
	}

	project, err := p.queries.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{
		ID: issue.ProjectID, WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		return Team{}, Plan{}, fmt.Errorf("load project for team replan: %w", err)
	}
	current := team.Plan
	desired, err := p.planner.Plan(ctx, PlanningInput{
		Project: project,
		Issue: &issue,
		CurrentPlan: &current,
	})
	if err != nil {
		return Team{}, Plan{}, fmt.Errorf("LLM team reconciliation failed: %w", err)
	}

	updated, err := p.applyIssuePlan(ctx, project, issue, team, desired, revision)
	if err != nil {
		return Team{}, Plan{}, err
	}
	return updated, desired, nil
}

func (p *Provisioner) applyIssuePlan(
	ctx context.Context,
	project db.Project,
	issue db.Issue,
	current Team,
	desired Plan,
	sourceRevision string,
) (Team, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return Team{}, fmt.Errorf("begin team reconciliation: %w", err)
	}
	defer tx.Rollback(ctx)
	qtx := p.queries.WithTx(tx)

	lockKey := "autonomous-project-team:" + util.UUIDToString(issue.WorkspaceID) + ":" + util.UUIDToString(issue.ProjectID)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", lockKey); err != nil {
		return Team{}, fmt.Errorf("lock team reconciliation: %w", err)
	}

	lockedTeam, ok, err := loadTeamWithQuerier(ctx, tx, issue.WorkspaceID, issue.ProjectID)
	if err != nil {
		return Team{}, err
	}
	if !ok {
		return Team{}, errors.New("project team disappeared during reconciliation")
	}

	if cached, ok, err := loadAnalysisPlanWithQuerier(ctx, tx, lockedTeam.ID, "issue", issue.ID, sourceRevision); err != nil {
		return Team{}, err
	} else if ok {
		if err := tx.Commit(ctx); err != nil {
			return Team{}, err
		}
		lockedTeam.Plan = mergeTeamPlans(lockedTeam.Plan, cached)
		return lockedTeam, nil
	}

	mika, err := qtx.GetAgentBySystemKey(ctx, db.GetAgentBySystemKeyParams{
		WorkspaceID: issue.WorkspaceID,
		SystemKey: pgtype.Text{String: service.MikaSystemKey, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Team{}, ErrMikaUnavailable
	}
	if err != nil {
		return Team{}, fmt.Errorf("resolve Mika for team reconciliation: %w", err)
	}
	if !mika.RuntimeID.Valid {
		return Team{}, fmt.Errorf("%w: Mika has no runtime", ErrMikaUnavailable)
	}

	for _, role := range desired.Roles {
		agentID, exists := lockedTeam.Members[role.Role]
		if !exists || !agentID.Valid {
			agent, err := qtx.CreateAgent(ctx, db.CreateAgentParams{
				WorkspaceID: issue.WorkspaceID,
				Name: generatedAgentName(project, role),
				Description: role.Description,
				AvatarUrl: pgtype.Text{},
				RuntimeMode: mika.RuntimeMode,
				RuntimeConfig: []byte("{}"),
				RuntimeID: mika.RuntimeID,
				Visibility: "workspace",
				MaxConcurrentTasks: 2,
				OwnerID: mika.OwnerID,
				Instructions: role.Instructions,
				CustomEnv: []byte("{}"),
				CustomArgs: []byte("[]"),
				McpConfig: nil,
				Model: mika.Model,
				ThinkingLevel: mika.ThinkingLevel,
				ServiceTier: mika.ServiceTier,
				ConversationStarters: []byte("[]"),
				PermissionMode: "public_to",
			})
			if err != nil {
				return Team{}, fmt.Errorf("create missing %s agent: %w", role.Role, err)
			}
			if err := qtx.CreateAgentInvocationTarget(ctx, db.CreateAgentInvocationTargetParams{
				AgentID: agent.ID,
				TargetType: "workspace",
				TargetID: issue.WorkspaceID,
				CreatedBy: mika.OwnerID,
			}); err != nil {
				return Team{}, fmt.Errorf("grant workspace access to %s agent: %w", role.Role, err)
			}
			if _, err := qtx.AddSquadMember(ctx, db.AddSquadMemberParams{
				SquadID: lockedTeam.SquadID,
				MemberType: "agent",
				MemberID: agent.ID,
				Role: role.Role,
			}); err != nil {
				return Team{}, fmt.Errorf("add missing %s agent to squad: %w", role.Role, err)
			}
			agentID = agent.ID
			lockedTeam.Members[role.Role] = agent.ID
		} else {
			// Refresh generated role instructions from backend-owned safe
			// templates. Model prose never becomes an agent system prompt.
			if _, err := tx.Exec(ctx, `
				UPDATE agent
				SET description = $2,
				    instructions = $3,
				    updated_at = now()
				WHERE id = $1
				  AND workspace_id = $4
				  AND archived_at IS NULL
			`, agentID, role.Description, role.Instructions, issue.WorkspaceID); err != nil {
				return Team{}, fmt.Errorf("refresh %s agent profile: %w", role.Role, err)
			}
		}

		capabilities, _ := json.Marshal(role.Capabilities)
		responsibilities, _ := json.Marshal(role.Responsibilities)
		if _, err := tx.Exec(ctx, `
			INSERT INTO autonomous_project_team_member (
				team_id, role, agent_id, role_family, capabilities,
				responsibilities, reason, active
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, TRUE)
			ON CONFLICT (team_id, role) DO UPDATE
			SET agent_id = EXCLUDED.agent_id,
			    role_family = EXCLUDED.role_family,
			    capabilities = EXCLUDED.capabilities,
			    responsibilities = EXCLUDED.responsibilities,
			    reason = EXCLUDED.reason,
			    active = TRUE
		`, lockedTeam.ID, role.Role, agentID, role.Family, capabilities, responsibilities, role.Reason); err != nil {
			return Team{}, fmt.Errorf("upsert %s team registry entry: %w", role.Role, err)
		}
	}

	merged := mergeTeamPlans(lockedTeam.Plan, desired)
	stored := merged
	stored.RouteRole = ""
	planJSON, err := json.Marshal(stored)
	if err != nil {
		return Team{}, fmt.Errorf("encode reconciled team plan: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE autonomous_project_team
		SET planner_version = $2,
		    intent = $3,
		    plan = $4,
		    planner_name = $5,
		    planner_model = NULLIF($6, ''),
		    plan_revision = plan_revision + 1,
		    last_planned_at = now(),
		    updated_at = now()
		WHERE id = $1
	`, lockedTeam.ID, stored.Version, stored.Intent, planJSON, desired.PlannerName, desired.PlannerModel); err != nil {
		return Team{}, fmt.Errorf("update reconciled team plan: %w", err)
	}

	analysisJSON, err := json.Marshal(desired)
	if err != nil {
		return Team{}, fmt.Errorf("encode issue team analysis: %w", err)
	}
	inputHash := "issue:" + util.UUIDToString(issue.ID) + ":" + sourceRevision
	if _, err := tx.Exec(ctx, `
		INSERT INTO autonomous_project_team_analysis (
			id, team_id, source_type, source_id, source_revision,
			input_hash, planner_name, planner_model, plan
		)
		VALUES (
			gen_random_uuid(), $1, 'issue', $2, $3,
			$4, $5, NULLIF($6, ''), $7
		)
		ON CONFLICT (team_id, source_type, source_id, source_revision) DO NOTHING
	`, lockedTeam.ID, issue.ID, sourceRevision, inputHash, desired.PlannerName, desired.PlannerModel, analysisJSON); err != nil {
		return Team{}, fmt.Errorf("record issue team analysis: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Team{}, fmt.Errorf("commit team reconciliation: %w", err)
	}
	lockedTeam.Plan = stored
	lockedTeam.Intent = stored.Intent
	return lockedTeam, nil
}

func (p *Provisioner) loadAnalysisPlan(
	ctx context.Context,
	teamID pgtype.UUID,
	sourceType string,
	sourceID pgtype.UUID,
	sourceRevision string,
) (Plan, bool, error) {
	return loadAnalysisPlanWithQuerier(ctx, p.pool, teamID, sourceType, sourceID, sourceRevision)
}

func loadAnalysisPlanWithQuerier(
	ctx context.Context,
	q rowQuerier,
	teamID pgtype.UUID,
	sourceType string,
	sourceID pgtype.UUID,
	sourceRevision string,
) (Plan, bool, error) {
	var raw []byte
	err := q.QueryRow(ctx, `
		SELECT plan
		FROM autonomous_project_team_analysis
		WHERE team_id = $1
		  AND source_type = $2
		  AND source_id = $3
		  AND source_revision = $4
	`, teamID, sourceType, sourceID, sourceRevision).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return Plan{}, false, nil
	}
	if err != nil {
		return Plan{}, false, fmt.Errorf("load cached team analysis: %w", err)
	}
	var plan Plan
	if err := json.Unmarshal(raw, &plan); err != nil {
		return Plan{}, false, fmt.Errorf("decode cached team analysis: %w", err)
	}
	return plan, true, nil
}

func upsertTeamMemberRegistry(
	ctx context.Context,
	tx pgx.Tx,
	teamID pgtype.UUID,
	role RoleSpec,
	agentID pgtype.UUID,
) error {
	capabilities, _ := json.Marshal(role.Capabilities)
	responsibilities, _ := json.Marshal(role.Responsibilities)
	_, err := tx.Exec(ctx, `
		INSERT INTO autonomous_project_team_member (
			team_id, role, agent_id, role_family, capabilities,
			responsibilities, reason, active
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, TRUE)
		ON CONFLICT (team_id, role) DO UPDATE
		SET agent_id = EXCLUDED.agent_id,
		    role_family = EXCLUDED.role_family,
		    capabilities = EXCLUDED.capabilities,
		    responsibilities = EXCLUDED.responsibilities,
		    reason = EXCLUDED.reason,
		    active = TRUE
	`, teamID, role.Role, agentID, role.Family, capabilities, responsibilities, role.Reason)
	return err
}

func stampInitialTeamPlan(ctx context.Context, tx pgx.Tx, teamID pgtype.UUID, plan Plan) error {
	planJSON, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE autonomous_project_team
		SET planner_version = $2,
		    intent = $3,
		    plan = $4,
		    planner_name = $5,
		    planner_model = NULLIF($6, ''),
		    last_planned_at = now(),
		    updated_at = now()
		WHERE id = $1
	`, teamID, plan.Version, plan.Intent, planJSON, plan.PlannerName, plan.PlannerModel)
	return err
}

func mergeTeamPlans(current, desired Plan) Plan {
	merged := desired
	if merged.Version < current.Version {
		merged.Version = current.Version
	}
	byRole := make(map[string]RoleSpec, len(current.Roles)+len(desired.Roles))
	order := make([]string, 0, len(current.Roles)+len(desired.Roles))
	for _, role := range current.Roles {
		if _, ok := byRole[role.Role]; !ok {
			order = append(order, role.Role)
		}
		byRole[role.Role] = role
	}
	for _, role := range desired.Roles {
		if _, ok := byRole[role.Role]; !ok {
			order = append(order, role.Role)
		}
		byRole[role.Role] = role
	}
	merged.Roles = make([]RoleSpec, 0, len(order))
	for _, role := range order {
		merged.Roles = append(merged.Roles, byRole[role])
	}
	if merged.ImplementationRole == "" {
		merged.ImplementationRole = firstImplementationRole(merged.Roles)
	}
	return merged
}
