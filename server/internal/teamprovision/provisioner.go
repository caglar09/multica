package teamprovision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var ErrMikaUnavailable = errors.New("Mika is not available for autonomous team provisioning")

type Team struct {
	ID          pgtype.UUID
	WorkspaceID pgtype.UUID
	ProjectID   pgtype.UUID
	SquadID     pgtype.UUID
	Intent      string
	Plan        Plan
	Members     map[string]pgtype.UUID
}

func (t Team) Agent(role string) (pgtype.UUID, bool) {
	id, ok := t.Members[role]
	return id, ok && id.Valid
}

func (t Team) RoleForAgent(agentID pgtype.UUID) (string, bool) {
	if !agentID.Valid {
		return "", false
	}
	for role, id := range t.Members {
		if id == agentID {
			return role, true
		}
	}
	return "", false
}

type Provisioner struct {
	pool    *pgxpool.Pool
	queries *db.Queries
	planner Planner
}

func New(pool *pgxpool.Pool, queries *db.Queries) *Provisioner {
	return &Provisioner{
		pool:    pool,
		queries: queries,
		planner: NewHeuristicPlanner(),
	}
}

func (p *Provisioner) ShouldBootstrap(ctx context.Context, workspaceID, projectID pgtype.UUID) (bool, error) {
	if p == nil || p.queries == nil || !workspaceID.Valid || !projectID.Valid {
		return false, nil
	}
	project, err := p.queries.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{
		ID: projectID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return false, err
	}
	return LooksLikeSoftwareProject(project), nil
}

func (p *Provisioner) IsMikaAgent(ctx context.Context, workspaceID, agentID pgtype.UUID) bool {
	if p == nil || p.queries == nil || !workspaceID.Valid || !agentID.Valid {
		return false
	}
	agent, err := p.queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
		ID: agentID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return false
	}
	return agent.SystemKey.Valid && agent.SystemKey.String == service.MikaSystemKey
}

// EnsureProject is the idempotent technology-team bootstrap boundary.
//
// It creates project-specific specialist agents on Mika's runtime, a visible
// squad, and a durable role->agent registry in ONE transaction. An advisory
// lock serializes concurrent project-created / workflow-demand triggers.
func (p *Provisioner) EnsureProject(ctx context.Context, workspaceID, projectID pgtype.UUID) (Team, error) {
	if p == nil || p.pool == nil || p.queries == nil {
		return Team{}, errors.New("team provisioner is not configured")
	}
	if !workspaceID.Valid || !projectID.Valid {
		return Team{}, errors.New("workspace_id and project_id are required for team provisioning")
	}

	if team, ok, err := p.loadTeam(ctx, workspaceID, projectID); err != nil {
		return Team{}, err
	} else if ok {
		return team, nil
	}

	project, err := p.queries.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{
		ID: projectID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return Team{}, fmt.Errorf("load project for team provisioning: %w", err)
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return Team{}, fmt.Errorf("begin project team provisioning: %w", err)
	}
	defer tx.Rollback(ctx)
	qtx := p.queries.WithTx(tx)

	lockKey := "autonomous-project-team:" + util.UUIDToString(workspaceID) + ":" + util.UUIDToString(projectID)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", lockKey); err != nil {
		return Team{}, fmt.Errorf("lock project team provisioning: %w", err)
	}

	// Re-check inside the lock.
	if team, ok, err := loadTeamWithQuerier(ctx, tx, workspaceID, projectID); err != nil {
		return Team{}, err
	} else if ok {
		if err := tx.Commit(ctx); err != nil {
			return Team{}, err
		}
		return team, nil
	}

	mika, err := qtx.GetAgentBySystemKey(ctx, db.GetAgentBySystemKeyParams{
		WorkspaceID: workspaceID,
		SystemKey:   pgtype.Text{String: service.MikaSystemKey, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Team{}, ErrMikaUnavailable
	}
	if err != nil {
		return Team{}, fmt.Errorf("resolve Mika for project team: %w", err)
	}
	if !mika.RuntimeID.Valid {
		return Team{}, fmt.Errorf("%w: Mika has no runtime", ErrMikaUnavailable)
	}

	plan := p.planner.PlanProject(project)
	planJSON, err := json.Marshal(plan)
	if err != nil {
		return Team{}, fmt.Errorf("encode project team plan: %w", err)
	}

	members := make(map[string]pgtype.UUID, len(plan.Roles))
	for _, role := range plan.Roles {
		agent, err := qtx.CreateAgent(ctx, db.CreateAgentParams{
			WorkspaceID:          workspaceID,
			Name:                 generatedAgentName(project, role),
			Description:          role.Description,
			AvatarUrl:            pgtype.Text{},
			RuntimeMode:          mika.RuntimeMode,
			RuntimeConfig:        []byte("{}"),
			RuntimeID:            mika.RuntimeID,
			Visibility:           "workspace",
			MaxConcurrentTasks:   2,
			OwnerID:              mika.OwnerID,
			Instructions:         role.Instructions,
			CustomEnv:            []byte("{}"),
			CustomArgs:           []byte("[]"),
			McpConfig:            nil,
			Model:                mika.Model,
			ThinkingLevel:        mika.ThinkingLevel,
			ServiceTier:          mika.ServiceTier,
			ConversationStarters: []byte("[]"),
			PermissionMode:       "public_to",
		})
		if err != nil {
			return Team{}, fmt.Errorf("create %s agent: %w", role.Role, err)
		}
		if err := qtx.CreateAgentInvocationTarget(ctx, db.CreateAgentInvocationTargetParams{
			AgentID:    agent.ID,
			TargetType: "workspace",
			TargetID:   workspaceID,
			CreatedBy:  mika.OwnerID,
		}); err != nil {
			return Team{}, fmt.Errorf("grant workspace access to %s agent: %w", role.Role, err)
		}
		members[role.Role] = agent.ID
	}

	leaderID, ok := members[RoleProductManager]
	if !ok {
		return Team{}, errors.New("project team plan does not contain a product manager")
	}
	squad, err := qtx.CreateSquad(ctx, db.CreateSquadParams{
		WorkspaceID: workspaceID,
		Name:        generatedSquadName(project),
		Description: "Autonomously provisioned technology team for " + project.Title,
		LeaderID:    leaderID,
		CreatorID:   mika.OwnerID,
		AvatarUrl:   pgtype.Text{},
	})
	if err != nil {
		return Team{}, fmt.Errorf("create autonomous project squad: %w", err)
	}

	for _, role := range plan.Roles {
		agentID := members[role.Role]
		if _, err := qtx.AddSquadMember(ctx, db.AddSquadMemberParams{
			SquadID: squad.ID, MemberType: "agent", MemberID: agentID, Role: role.Role,
		}); err != nil {
			return Team{}, fmt.Errorf("add %s to project squad: %w", role.Role, err)
		}
	}
	// Mika remains the Chief of Staff above the project team and is visible in
	// the squad, but is intentionally NOT written into the role registry.
	if _, err := qtx.AddSquadMember(ctx, db.AddSquadMemberParams{
		SquadID: squad.ID, MemberType: "agent", MemberID: mika.ID, Role: "chief_of_staff",
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

	for role, agentID := range members {
		if _, err := tx.Exec(ctx, `
			INSERT INTO autonomous_project_team_member (team_id, role, agent_id)
			VALUES ($1, $2, $3)
		`, teamID, role, agentID); err != nil {
			return Team{}, fmt.Errorf("register %s project agent: %w", role, err)
		}
	}

	// Make the generated squad visible as the project lead without overwriting a
	// lead a member explicitly selected.
	if _, err := tx.Exec(ctx, `
		UPDATE project
		SET lead_type = 'squad', lead_id = $3, updated_at = now()
		WHERE id = $1
		  AND workspace_id = $2
		  AND lead_id IS NULL
	`, projectID, workspaceID, squad.ID); err != nil {
		return Team{}, fmt.Errorf("attach project team as project lead: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Team{}, fmt.Errorf("commit project team provisioning: %w", err)
	}

	return Team{
		ID:          teamID,
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
		SquadID:     squad.ID,
		Intent:      plan.Intent,
		Plan:        plan,
		Members:     members,
	}, nil
}

// FindProject returns an already-provisioned active team without creating one.
func (p *Provisioner) FindProject(ctx context.Context, workspaceID, projectID pgtype.UUID) (Team, bool, error) {
	if p == nil || p.pool == nil || !workspaceID.Valid || !projectID.Valid {
		return Team{}, false, nil
	}
	return p.loadTeam(ctx, workspaceID, projectID)
}

func (p *Provisioner) ImplementationAgent(ctx context.Context, issue db.Issue) (pgtype.UUID, Team, error) {
	if !issue.ProjectID.Valid {
		return pgtype.UUID{}, Team{}, errors.New("issue has no project for autonomous team routing")
	}
	team, err := p.EnsureProject(ctx, issue.WorkspaceID, issue.ProjectID)
	if err != nil {
		return pgtype.UUID{}, Team{}, err
	}
	role := p.planner.ImplementationRole(issue, team.Plan)
	id, ok := team.Agent(role)
	if !ok {
		return pgtype.UUID{}, Team{}, fmt.Errorf("project team has no agent for implementation role %q", role)
	}
	return id, team, nil
}

func (p *Provisioner) ResolveRole(ctx context.Context, workspaceID, projectID pgtype.UUID, role string) (pgtype.UUID, Team, error) {
	team, err := p.EnsureProject(ctx, workspaceID, projectID)
	if err != nil {
		return pgtype.UUID{}, Team{}, err
	}
	id, ok := team.Agent(role)
	if !ok {
		return pgtype.UUID{}, Team{}, fmt.Errorf("project team has no agent for role %q", role)
	}
	return id, team, nil
}

func (p *Provisioner) ArchiveProject(ctx context.Context, workspaceID, projectID pgtype.UUID) error {
	if p == nil || p.pool == nil || !workspaceID.Valid || !projectID.Valid {
		return nil
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var teamID, squadID, ownerID pgtype.UUID
	err = tx.QueryRow(ctx, `
		SELECT id, squad_id, owner_user_id
		FROM autonomous_project_team
		WHERE workspace_id = $1 AND project_id = $2 AND status = 'active'
		FOR UPDATE
	`, workspaceID, projectID).Scan(&teamID, &squadID, &ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE agent
		SET archived_at = COALESCE(archived_at, now()),
		    archived_by = COALESCE(archived_by, $2),
		    updated_at = now()
		WHERE id IN (
			SELECT agent_id FROM autonomous_project_team_member WHERE team_id = $1
		)
	`, teamID, ownerID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE squad
		SET archived_at = COALESCE(archived_at, now()),
		    archived_by = COALESCE(archived_by, $2),
		    updated_at = now()
		WHERE id = $1
	`, squadID, ownerID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE autonomous_project_team
		SET status = 'archived', updated_at = now()
		WHERE id = $1
	`, teamID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Provisioner) loadTeam(ctx context.Context, workspaceID, projectID pgtype.UUID) (Team, bool, error) {
	return loadTeamWithQuerier(ctx, p.pool, workspaceID, projectID)
}

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func loadTeamWithQuerier(ctx context.Context, q rowQuerier, workspaceID, projectID pgtype.UUID) (Team, bool, error) {
	var (
		team     Team
		planJSON []byte
		status   string
	)
	err := q.QueryRow(ctx, `
		SELECT id, workspace_id, project_id, squad_id, intent, plan, status
		FROM autonomous_project_team
		WHERE workspace_id = $1 AND project_id = $2
	`, workspaceID, projectID).Scan(
		&team.ID, &team.WorkspaceID, &team.ProjectID, &team.SquadID,
		&team.Intent, &planJSON, &status,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Team{}, false, nil
	}
	if err != nil {
		return Team{}, false, fmt.Errorf("load project team: %w", err)
	}
	if status != "active" {
		return Team{}, false, nil
	}
	if err := json.Unmarshal(planJSON, &team.Plan); err != nil {
		return Team{}, false, fmt.Errorf("decode project team plan: %w", err)
	}

	rows, err := q.Query(ctx, `
		SELECT role, agent_id
		FROM autonomous_project_team_member
		WHERE team_id = $1
		ORDER BY role
	`, team.ID)
	if err != nil {
		return Team{}, false, fmt.Errorf("load project team members: %w", err)
	}
	defer rows.Close()
	team.Members = make(map[string]pgtype.UUID)
	for rows.Next() {
		var role string
		var agentID pgtype.UUID
		if err := rows.Scan(&role, &agentID); err != nil {
			return Team{}, false, err
		}
		team.Members[role] = agentID
	}
	if err := rows.Err(); err != nil {
		return Team{}, false, err
	}
	return team, true, nil
}

func generatedAgentName(project db.Project, role RoleSpec) string {
	title := strings.TrimSpace(project.Title)
	if title == "" {
		title = "Project"
	}
	title = truncateRunes(title, 36)
	suffix := shortProjectID(project.ID)
	name := title + " · " + role.DisplayName + " · " + suffix
	return truncateRunes(name, 96)
}

func generatedSquadName(project db.Project) string {
	title := truncateRunes(strings.TrimSpace(project.Title), 52)
	if title == "" {
		title = "Project"
	}
	return truncateRunes(title+" · Technology Team · "+shortProjectID(project.ID), 110)
}

func shortProjectID(id pgtype.UUID) string {
	value := util.UUIDToString(id)
	if len(value) >= 8 {
		return value[:8]
	}
	return value
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit])
}
