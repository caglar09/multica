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

func (t Team) RoleSpec(role string) (RoleSpec, bool) {
	for _, spec := range t.Plan.Roles {
		if spec.Role == role {
			return spec, true
		}
	}
	return RoleSpec{}, false
}

func (t Team) AgentByFamily(family string) (pgtype.UUID, RoleSpec, bool) {
	for _, spec := range t.Plan.Roles {
		if spec.Family != family {
			continue
		}
		if id, ok := t.Agent(spec.Role); ok {
			return id, spec, true
		}
	}
	return pgtype.UUID{}, RoleSpec{}, false
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

func New(pool *pgxpool.Pool, queries *db.Queries, planners ...Planner) *Provisioner {
	planner := Planner(NewHeuristicPlanner())
	if len(planners) > 0 && planners[0] != nil {
		planner = planners[0]
	}
	return &Provisioner{pool: pool, queries: queries, planner: planner}
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

	// Initial team creation is deliberately two-phase. The model proposes the
	// organization first; a human then chooses the runtime/skills for each role.
	// Demand-driven issue routing may prepare a missing draft, but it must never
	// silently materialize user-visible agents before that configuration gate.
	if _, ok, err := p.FindDraft(ctx, workspaceID, projectID); err != nil {
		return Team{}, err
	} else if !ok {
		if _, err := p.PrepareProject(ctx, workspaceID, projectID); err != nil {
			return Team{}, err
		}
	}
	return Team{}, ErrTeamConfigurationRequired
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
	team, plan, err := p.ReconcileForIssue(ctx, issue)
	if err != nil {
		return pgtype.UUID{}, Team{}, err
	}
	role := plan.RouteRole
	if role == "" {
		role = plan.ImplementationRole
	}
	id, ok := team.Agent(role)
	if !ok {
		return pgtype.UUID{}, Team{}, fmt.Errorf("project team has no agent for routed role %q", role)
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
	team.Plan = normalizeLegacyPlan(team.Plan)

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

func normalizeLegacyPlan(plan Plan) Plan {
	for i, role := range plan.Roles {
		if role.Family != "" {
			continue
		}
		known := roleSpec(role.Role)
		if known.Family == "" || known.Family == "engineering" {
			continue
		}
		if role.DisplayName == "" {
			role.DisplayName = known.DisplayName
		}
		if role.Description == "" {
			role.Description = known.Description
		}
		role.Family = known.Family
		if len(role.Capabilities) == 0 {
			role.Capabilities = append([]string(nil), known.Capabilities...)
		}
		if role.Instructions == "" {
			role.Instructions = known.Instructions
		}
		plan.Roles[i] = role
	}
	if plan.ImplementationRole == "" {
		plan.ImplementationRole = firstImplementationRole(plan.Roles)
	}
	return plan
}

func chooseTeamLeader(plan Plan, members map[string]pgtype.UUID) (pgtype.UUID, bool) {
	for _, preferredFamily := range []string{"product", "architecture", "fullstack", "backend", "frontend", "mobile"} {
		for _, role := range plan.Roles {
			if role.Family != preferredFamily {
				continue
			}
			if id, ok := members[role.Role]; ok && id.Valid {
				return id, true
			}
		}
	}
	for _, role := range plan.Roles {
		if id, ok := members[role.Role]; ok && id.Valid {
			return id, true
		}
	}
	return pgtype.UUID{}, false
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
