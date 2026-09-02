package teamprovision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var ErrTeamConfigurationRequired = errors.New("autonomous team runtime configuration is required before provisioning")

type TeamDraft struct {
	WorkspaceID  pgtype.UUID
	ProjectID    pgtype.UUID
	Plan         Plan
	PlannerName  string
	PlannerModel string
	Status       string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type RoleRuntimeSelection struct {
	Role            string
	RuntimeID       pgtype.UUID
	SkillIDs        []pgtype.UUID
	SkillsSpecified bool
}

func (p *Provisioner) FindDraft(ctx context.Context, workspaceID, projectID pgtype.UUID) (TeamDraft, bool, error) {
	if p == nil || p.pool == nil || !workspaceID.Valid || !projectID.Valid {
		return TeamDraft{}, false, nil
	}

	var draft TeamDraft
	var planJSON []byte
	var plannerModel pgtype.Text
	err := p.pool.QueryRow(ctx, `
		SELECT workspace_id, project_id, plan, planner_name, planner_model,
		       status, created_at, updated_at
		FROM autonomous_project_team_draft
		WHERE workspace_id = $1 AND project_id = $2
		  AND status IN ('awaiting_configuration', 'provisioning')
	`, workspaceID, projectID).Scan(
		&draft.WorkspaceID,
		&draft.ProjectID,
		&planJSON,
		&draft.PlannerName,
		&plannerModel,
		&draft.Status,
		&draft.CreatedAt,
		&draft.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return TeamDraft{}, false, nil
	}
	if err != nil {
		return TeamDraft{}, false, fmt.Errorf("load autonomous team draft: %w", err)
	}
	if err := json.Unmarshal(planJSON, &draft.Plan); err != nil {
		return TeamDraft{}, false, fmt.Errorf("decode autonomous team draft: %w", err)
	}
	if plannerModel.Valid {
		draft.PlannerModel = plannerModel.String
	}
	return draft, true, nil
}

// PrepareProject asks the runtime-backed planner for the proposed organization
// and persists it without creating any user-visible agents. Initial provisioning
// is therefore a two-phase operation: plan first, then a human chooses the
// runtime/skills for each role and confirms the draft.
func (p *Provisioner) PrepareProject(ctx context.Context, workspaceID, projectID pgtype.UUID) (TeamDraft, error) {
	if p == nil || p.pool == nil || p.queries == nil || p.planner == nil {
		return TeamDraft{}, errors.New("team provisioner is not configured")
	}
	if !workspaceID.Valid || !projectID.Valid {
		return TeamDraft{}, errors.New("workspace_id and project_id are required for team planning")
	}

	if team, ok, err := p.loadTeam(ctx, workspaceID, projectID); err != nil {
		return TeamDraft{}, err
	} else if ok {
		return TeamDraft{
			WorkspaceID: workspaceID,
			ProjectID: projectID,
			Plan: team.Plan,
			PlannerName: team.Plan.PlannerName,
			PlannerModel: team.Plan.PlannerModel,
			Status: "applied",
		}, nil
	}
	if draft, ok, err := p.FindDraft(ctx, workspaceID, projectID); err != nil {
		return TeamDraft{}, err
	} else if ok {
		return draft, nil
	}

	project, err := p.queries.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{
		ID: projectID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		return TeamDraft{}, fmt.Errorf("load project for autonomous team draft: %w", err)
	}
	plan, err := p.planner.Plan(ctx, PlanningInput{Project: project})
	if err != nil {
		return TeamDraft{}, fmt.Errorf("plan autonomous project team: %w", err)
	}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		return TeamDraft{}, fmt.Errorf("encode autonomous team draft: %w", err)
	}

	var draft TeamDraft
	var storedPlan []byte
	var plannerModel pgtype.Text
	err = p.pool.QueryRow(ctx, `
		INSERT INTO autonomous_project_team_draft (
			project_id, workspace_id, plan, planner_name, planner_model,
			status, updated_at
		)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), 'awaiting_configuration', now())
		ON CONFLICT (project_id) DO UPDATE
		SET plan = EXCLUDED.plan,
		    planner_name = EXCLUDED.planner_name,
		    planner_model = EXCLUDED.planner_model,
		    status = 'awaiting_configuration',
		    selections = '{}'::jsonb,
		    confirmed_at = NULL,
		    confirmed_by = NULL,
		    updated_at = now()
		WHERE autonomous_project_team_draft.workspace_id = EXCLUDED.workspace_id
		  AND autonomous_project_team_draft.status <> 'applied'
		RETURNING workspace_id, project_id, plan, planner_name, planner_model,
		          status, created_at, updated_at
	`, projectID, workspaceID, planJSON, plan.PlannerName, plan.PlannerModel).Scan(
		&draft.WorkspaceID,
		&draft.ProjectID,
		&storedPlan,
		&draft.PlannerName,
		&plannerModel,
		&draft.Status,
		&draft.CreatedAt,
		&draft.UpdatedAt,
	)
	if err != nil {
		return TeamDraft{}, fmt.Errorf("persist autonomous team draft: %w", err)
	}
	if err := json.Unmarshal(storedPlan, &draft.Plan); err != nil {
		return TeamDraft{}, fmt.Errorf("decode persisted autonomous team draft: %w", err)
	}
	if plannerModel.Valid {
		draft.PlannerModel = plannerModel.String
	}
	return draft, nil
}

func selectionJSON(assignments []RoleRuntimeSelection) []byte {
	payload := make(map[string]any, len(assignments))
	for _, assignment := range assignments {
		skills := make([]string, 0, len(assignment.SkillIDs))
		for _, skillID := range assignment.SkillIDs {
			if skillID.Valid {
				skills = append(skills, util.UUIDToString(skillID))
			}
		}
		payload[assignment.Role] = map[string]any{
			"runtime_id": util.UUIDToString(assignment.RuntimeID),
			"skill_ids": skills,
		}
	}
	raw, _ := json.Marshal(payload)
	return raw
}

// ProvisionDraft materializes the proposed team after the member chooses a
// runtime for every role. Workspace skills are explicit per-role bindings.
// Runtime-local skills remain inherited by the selected runtime unless disabled
// later from the agent detail page.
func (p *Provisioner) ProvisionDraft(
	ctx context.Context,
	workspaceID, projectID, confirmedBy pgtype.UUID,
	assignments []RoleRuntimeSelection,
) (Team, error) {
	if p == nil || p.pool == nil || p.queries == nil {
		return Team{}, errors.New("team provisioner is not configured")
	}
	if !workspaceID.Valid || !projectID.Valid || !confirmedBy.Valid {
		return Team{}, errors.New("workspace_id, project_id and confirmed_by are required")
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return Team{}, fmt.Errorf("begin autonomous team provisioning: %w", err)
	}
	defer tx.Rollback(ctx)
	qtx := p.queries.WithTx(tx)

	lockKey := "autonomous-project-team:" + util.UUIDToString(workspaceID) + ":" + util.UUIDToString(projectID)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", lockKey); err != nil {
		return Team{}, fmt.Errorf("lock autonomous team provisioning: %w", err)
	}

	if team, ok, err := loadTeamWithQuerier(ctx, tx, workspaceID, projectID); err != nil {
		return Team{}, err
	} else if ok {
		if err := tx.Commit(ctx); err != nil {
			return Team{}, err
		}
		return team, nil
	}

	var planJSON []byte
	var draftStatus string
	err = tx.QueryRow(ctx, `
		SELECT plan, status
		FROM autonomous_project_team_draft
		WHERE workspace_id = $1 AND project_id = $2
		FOR UPDATE
	`, workspaceID, projectID).Scan(&planJSON, &draftStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return Team{}, ErrTeamConfigurationRequired
	}
	if err != nil {
		return Team{}, fmt.Errorf("lock autonomous team draft: %w", err)
	}
	if draftStatus != "awaiting_configuration" && draftStatus != "provisioning" {
		return Team{}, fmt.Errorf("autonomous team draft cannot be provisioned from status %q", draftStatus)
	}

	var plan Plan
	if err := json.Unmarshal(planJSON, &plan); err != nil {
		return Team{}, fmt.Errorf("decode autonomous team draft plan: %w", err)
	}

	project, err := qtx.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{
		ID: projectID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		return Team{}, fmt.Errorf("load project for autonomous team provisioning: %w", err)
	}
	mika, err := qtx.GetAgentBySystemKey(ctx, db.GetAgentBySystemKeyParams{
		WorkspaceID: workspaceID,
		SystemKey: pgtype.Text{String: service.MikaSystemKey, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Team{}, ErrMikaUnavailable
	}
	if err != nil {
		return Team{}, fmt.Errorf("resolve Mika for autonomous team provisioning: %w", err)
	}
	if !mika.RuntimeID.Valid {
		return Team{}, fmt.Errorf("%w: Mika has no runtime", ErrMikaUnavailable)
	}

	defaultSkills, err := qtx.ListAgentSkills(ctx, mika.ID)
	if err != nil {
		return Team{}, fmt.Errorf("load Mika workspace skills: %w", err)
	}
	defaultSkillIDs := make([]pgtype.UUID, 0, len(defaultSkills))
	for _, skill := range defaultSkills {
		defaultSkillIDs = append(defaultSkillIDs, skill.ID)
	}

	byRole := make(map[string]RoleRuntimeSelection, len(assignments))
	for _, assignment := range assignments {
		if assignment.Role == "" {
			continue
		}
		byRole[assignment.Role] = assignment
	}

	members := make(map[string]pgtype.UUID, len(plan.Roles))
	for _, role := range plan.Roles {
		assignment, hasAssignment := byRole[role.Role]
		runtimeID := mika.RuntimeID
		if hasAssignment && assignment.RuntimeID.Valid {
			runtimeID = assignment.RuntimeID
		}

		var runtimeMode, runtimeProvider, runtimeStatus, runtimeVisibility, runtimeName string
		var runtimeOwner pgtype.UUID
		if err := tx.QueryRow(ctx, `
			SELECT runtime_mode, provider, status, visibility, name, owner_id
			FROM agent_runtime
			WHERE id = $1 AND workspace_id = $2
		`, runtimeID, workspaceID).Scan(
			&runtimeMode,
			&runtimeProvider,
			&runtimeStatus,
			&runtimeVisibility,
			&runtimeName,
			&runtimeOwner,
		); err != nil {
			return Team{}, fmt.Errorf("resolve runtime for role %s: %w", role.Role, err)
		}
		if runtimeStatus != "online" {
			return Team{}, fmt.Errorf("runtime %q for role %s is %s", runtimeName, role.Role, runtimeStatus)
		}
		if runtimeOwner.Valid && runtimeOwner != confirmedBy && runtimeVisibility != "public" {
			return Team{}, fmt.Errorf("runtime %q for role %s is private to another member", runtimeName, role.Role)
		}

		model := pgtype.Text{}
		thinking := pgtype.Text{}
		serviceTier := pgtype.Text{}
		if runtimeID == mika.RuntimeID {
			model = mika.Model
			thinking = mika.ThinkingLevel
			serviceTier = mika.ServiceTier
		}

		agent, err := qtx.CreateAgent(ctx, db.CreateAgentParams{
			WorkspaceID: workspaceID,
			Name: generatedAgentName(project, role),
			Description: role.Description,
			AvatarUrl: pgtype.Text{},
			RuntimeMode: runtimeMode,
			RuntimeConfig: []byte("{}"),
			RuntimeID: runtimeID,
			Visibility: "workspace",
			MaxConcurrentTasks: 2,
			OwnerID: mika.OwnerID,
			Instructions: role.Instructions,
			CustomEnv: []byte("{}"),
			CustomArgs: []byte("[]"),
			McpConfig: nil,
			Model: model,
			ThinkingLevel: thinking,
			ServiceTier: serviceTier,
			ConversationStarters: []byte("[]"),
			PermissionMode: "public_to",
		})
		if err != nil {
			return Team{}, fmt.Errorf("create %s agent: %w", role.Role, err)
		}
		if err := qtx.CreateAgentInvocationTarget(ctx, db.CreateAgentInvocationTargetParams{
			AgentID: agent.ID,
			TargetType: "workspace",
			TargetID: workspaceID,
			CreatedBy: mika.OwnerID,
		}); err != nil {
			return Team{}, fmt.Errorf("grant workspace access to %s agent: %w", role.Role, err)
		}

		skillIDs := defaultSkillIDs
		if hasAssignment && assignment.SkillsSpecified {
			skillIDs = assignment.SkillIDs
		}
		seenSkills := make(map[pgtype.UUID]struct{}, len(skillIDs))
		for _, skillID := range skillIDs {
			if !skillID.Valid {
				continue
			}
			if _, duplicate := seenSkills[skillID]; duplicate {
				continue
			}
			seenSkills[skillID] = struct{}{}
			if _, err := qtx.GetSkillInWorkspace(ctx, db.GetSkillInWorkspaceParams{
				ID: skillID,
				WorkspaceID: workspaceID,
			}); err != nil {
				return Team{}, fmt.Errorf("validate workspace skill for role %s: %w", role.Role, err)
			}
			if err := qtx.AddAgentSkill(ctx, db.AddAgentSkillParams{
				AgentID: agent.ID,
				SkillID: skillID,
			}); err != nil {
				return Team{}, fmt.Errorf("attach workspace skill to role %s: %w", role.Role, err)
			}
		}

		_ = runtimeProvider // kept in the runtime validation path for diagnostics/extensions.
		members[role.Role] = agent.ID
	}

	leaderID, ok := chooseTeamLeader(plan, members)
	if !ok {
		return Team{}, errors.New("project team plan does not contain any provisioned role")
	}
	squad, err := qtx.CreateSquad(ctx, db.CreateSquadParams{
		WorkspaceID: workspaceID,
		Name: generatedSquadName(project),
		Description: "Autonomously provisioned technology team for " + project.Title,
		LeaderID: leaderID,
		CreatorID: mika.OwnerID,
		AvatarUrl: pgtype.Text{},
	})
	if err != nil {
		return Team{}, fmt.Errorf("create autonomous project squad: %w", err)
	}
	for _, role := range plan.Roles {
		if _, err := qtx.AddSquadMember(ctx, db.AddSquadMemberParams{
			SquadID: squad.ID,
			MemberType: "agent",
			MemberID: members[role.Role],
			Role: role.Role,
		}); err != nil {
			return Team{}, fmt.Errorf("add %s to project squad: %w", role.Role, err)
		}
	}
	if _, err := qtx.AddSquadMember(ctx, db.AddSquadMemberParams{
		SquadID: squad.ID,
		MemberType: "agent",
		MemberID: mika.ID,
		Role: "chief_of_staff",
	}); err != nil {
		return Team{}, fmt.Errorf("add Mika to project squad: %w", err)
	}

	var teamID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO autonomous_project_team (
			id, workspace_id, project_id, squad_id, planner_version,
			intent, plan, status, created_by_agent_id, owner_user_id
		)
		VALUES (
			gen_random_uuid(), $1, $2, $3, $4,
			$5, $6, 'active', $7, $8
		)
		RETURNING id
	`, workspaceID, projectID, squad.ID, plan.Version, plan.Intent, planJSON, mika.ID, mika.OwnerID).Scan(&teamID); err != nil {
		return Team{}, fmt.Errorf("create project team registry: %w", err)
	}
	if err := stampInitialTeamPlan(ctx, tx, teamID, plan); err != nil {
		return Team{}, fmt.Errorf("stamp initial project team plan: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO autonomous_project_team_analysis (
			id, team_id, source_type, source_id, source_revision,
			input_hash, planner_name, planner_model, plan
		)
		VALUES (
			gen_random_uuid(), $1, 'project', $2, 'initial',
			$3, $4, NULLIF($5, ''), $6
		)
		ON CONFLICT (team_id, source_type, source_id, source_revision) DO NOTHING
	`,
		teamID,
		projectID,
		"project:"+util.UUIDToString(projectID)+":initial",
		plan.PlannerName,
		plan.PlannerModel,
		planJSON,
	); err != nil {
		return Team{}, fmt.Errorf("record initial project team analysis: %w", err)
	}
	for _, role := range plan.Roles {
		if err := upsertTeamMemberRegistry(ctx, tx, teamID, role, members[role.Role]); err != nil {
			return Team{}, fmt.Errorf("register %s project agent: %w", role.Role, err)
		}
	}

	// Project leads are intentionally limited by the core project contract to
	// members or agents. The autonomous squad remains linked through
	// autonomous_project_team.squad_id; expose its deterministic leader agent
	// as the project lead rather than inventing an unsupported lead_type.
	if _, err := tx.Exec(ctx, `
		UPDATE project
		SET lead_type = 'agent', lead_id = $3, updated_at = now()
		WHERE id = $1
		  AND workspace_id = $2
		  AND lead_id IS NULL
	`, projectID, workspaceID, leaderID); err != nil {
		return Team{}, fmt.Errorf("attach autonomous team leader as project lead: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE autonomous_project_team_draft
		SET status = 'applied',
		    selections = $3,
		    confirmed_at = now(),
		    confirmed_by = $4,
		    updated_at = now()
		WHERE workspace_id = $1 AND project_id = $2
	`, workspaceID, projectID, selectionJSON(assignments), confirmedBy); err != nil {
		return Team{}, fmt.Errorf("mark autonomous team draft applied: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Team{}, fmt.Errorf("commit autonomous team provisioning: %w", err)
	}

	return Team{
		ID: teamID,
		WorkspaceID: workspaceID,
		ProjectID: projectID,
		SquadID: squad.ID,
		Intent: plan.Intent,
		Plan: plan,
		Members: members,
	}, nil
}
