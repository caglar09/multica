package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type AutonomousControlResponse struct {
	Paused              bool    `json:"paused"`
	PausedAt            *string `json:"paused_at"`
	ReplanRequestedAt   *string `json:"replan_requested_at"`
	ReplanCompletedAt   *string `json:"replan_completed_at"`
	LastError           *string `json:"last_error"`
}

type AutonomousProjectBootstrapResponse struct {
	AutonomyMode  string          `json:"autonomy_mode"`
	AutonomyLevel string          `json:"autonomy_level"`
	Brief         string          `json:"brief"`
	Knowledge     json.RawMessage `json:"knowledge"`
	Policy        json.RawMessage `json:"policy"`
	Budget        json.RawMessage `json:"budget"`
	Status        string          `json:"status"`
	UpdatedAt     string          `json:"updated_at"`
}

type AutonomousTeamDraftResponse struct {
	Status           string          `json:"status"`
	PlannerName      string          `json:"planner_name"`
	PlannerModel     *string         `json:"planner_model"`
	Plan             json.RawMessage `json:"plan"`
	DefaultRuntimeID *string         `json:"default_runtime_id"`
	DefaultSkillIDs  []string        `json:"default_skill_ids"`
	CreatedAt        string          `json:"created_at"`
	UpdatedAt        string          `json:"updated_at"`
}

type AutonomousRuntimeOptionResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Provider    string `json:"provider"`
	RuntimeMode string `json:"runtime_mode"`
	Status      string `json:"status"`
}

type AutonomousSkillOptionResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type AutonomousTeamMemberResponse struct {
	Role             string          `json:"role"`
	Family           string          `json:"family"`
	AgentID          string          `json:"agent_id"`
	AgentName        string          `json:"agent_name"`
	Capabilities     json.RawMessage `json:"capabilities"`
	Responsibilities json.RawMessage `json:"responsibilities"`
	Reason           string          `json:"reason"`
	Active           bool            `json:"active"`
	CurrentTaskID    *string         `json:"current_task_id"`
	CurrentTaskTitle *string         `json:"current_task_title"`
	CurrentTaskStatus *string        `json:"current_task_status"`
	CreatedAt        string          `json:"created_at"`
}

type AutonomousTeamResponse struct {
	ID            string                         `json:"id"`
	SquadID       string                         `json:"squad_id"`
	Intent        string                         `json:"intent"`
	Status        string                         `json:"status"`
	PlannerName   string                         `json:"planner_name"`
	PlannerModel  *string                        `json:"planner_model"`
	PlanRevision  int64                          `json:"plan_revision"`
	LastPlannedAt *string                        `json:"last_planned_at"`
	UpdatedAt     string                         `json:"updated_at"`
	Members       []AutonomousTeamMemberResponse `json:"members"`
}

type AutonomousActionResponse struct {
	ID          string  `json:"id"`
	RunID       string  `json:"run_id"`
	ActionType  string  `json:"action_type"`
	Status      string  `json:"status"`
	Attempts    int     `json:"attempts"`
	MaxAttempts int     `json:"max_attempts"`
	LastError   *string `json:"last_error"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

type AutonomousWorkflowResponse struct {
	ID              string  `json:"id"`
	IssueID         string  `json:"issue_id"`
	IssueTitle      string  `json:"issue_title"`
	State           string  `json:"state"`
	Revision        int64   `json:"revision"`
	ReviewCycles    int     `json:"review_cycles"`
	OwnerAgentID    *string `json:"owner_agent_id"`
	OwnerAgentName  *string `json:"owner_agent_name"`
	ReviewerAgentID *string `json:"reviewer_agent_id"`
	ReviewerName    *string `json:"reviewer_agent_name"`
	PendingActions  int64   `json:"pending_actions"`
	FailedActions   int64   `json:"failed_actions"`
	UpdatedAt       string  `json:"updated_at"`
}

type AutonomousActivityResponse struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	Title     string         `json:"title"`
	Detail    string         `json:"detail,omitempty"`
	IssueID   *string        `json:"issue_id,omitempty"`
	AgentID   *string        `json:"agent_id,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt string         `json:"created_at"`
}

type AutonomousDecisionResponse struct {
	ID             string          `json:"id"`
	SourceType     string          `json:"source_type"`
	SourceID       string          `json:"source_id"`
	SourceRevision string          `json:"source_revision"`
	PlannerName    string          `json:"planner_name"`
	PlannerModel   *string         `json:"planner_model"`
	Plan           json.RawMessage `json:"plan"`
	CreatedAt      string          `json:"created_at"`
}

type AutonomousProjectHealthResponse struct {
	Status          string `json:"status"`
	ActiveWorkflows int    `json:"active_workflows"`
	Blocked         int    `json:"blocked"`
	FailedActions   int64  `json:"failed_actions"`
	Waiting         int    `json:"waiting"`
	Resumable       int    `json:"resumable"`
}

type AutonomousDiagnosticResponse struct {
	Code         string         `json:"code"`
	Severity     string         `json:"severity"`
	Title        string         `json:"title"`
	Detail       string         `json:"detail"`
	NodeKey      *string        `json:"node_key,omitempty"`
	IssueID      *string        `json:"issue_id,omitempty"`
	IssueTitle   *string        `json:"issue_title,omitempty"`
	ActionID     *string        `json:"action_id,omitempty"`
	ResumeAction string         `json:"resume_action,omitempty"`
	CanResume    bool           `json:"can_resume"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	UpdatedAt    string         `json:"updated_at"`
}

type AutonomousBrainResponse struct {
	Enabled             bool    `json:"enabled"`
	RuntimeMode         string  `json:"runtime_mode"`
	RuntimeID           *string `json:"runtime_id"`
	Model               *string `json:"model"`
	ThinkingLevel       *string `json:"thinking_level"`
	ServiceTier         *string `json:"service_tier"`
	LearningMode        string  `json:"learning_mode"`
	ActiveMemories      int64   `json:"active_memories"`
	SupersededMemories  int64   `json:"superseded_memories"`
	PendingLearningJobs int64   `json:"pending_learning_jobs"`
	DeferredLearningJobs int64  `json:"deferred_learning_jobs"`
}

type AutonomousProjectResponse struct {
	Enabled   bool                              `json:"enabled"`
	Control   AutonomousControlResponse         `json:"control"`
	Health    AutonomousProjectHealthResponse   `json:"health"`
	Bootstrap *AutonomousProjectBootstrapResponse `json:"bootstrap"`
	Draft     *AutonomousTeamDraftResponse       `json:"draft"`
	Runtimes  []AutonomousRuntimeOptionResponse  `json:"runtimes"`
	Skills    []AutonomousSkillOptionResponse    `json:"skills"`
	Team      *AutonomousTeamResponse            `json:"team"`
	Workflows []AutonomousWorkflowResponse       `json:"workflows"`
	Actions   []AutonomousActionResponse         `json:"actions"`
	Activity  []AutonomousActivityResponse       `json:"activity"`
	Decisions    []AutonomousDecisionResponse       `json:"decisions"`
	Plan         *AutonomousProjectPlanResponse      `json:"plan"`
	QualityGates []AutonomousQualityGateResponse     `json:"quality_gates"`
	Escalations  []AutonomousEscalationResponse      `json:"escalations"`
	Diagnostics  []AutonomousDiagnosticResponse       `json:"diagnostics"`
	Budget       *AutonomousBudgetResponse            `json:"budget"`
	Brain        *AutonomousBrainResponse             `json:"brain"`
}

type AutonomousProjectPlanNodeResponse struct {
	ID                  string          `json:"id"`
	Key                 string          `json:"key"`
	Kind                string          `json:"kind"`
	Title               string          `json:"title"`
	Status              string          `json:"status"`
	Priority            int             `json:"priority"`
	Risk                string          `json:"risk"`
	RequiredRoleFamily  *string         `json:"required_role_family"`
	AssignedRole        *string         `json:"assigned_role"`
	AssignedAgentID     *string         `json:"assigned_agent_id"`
	MaterializedIssueID *string         `json:"materialized_issue_id"`
	Attempt             int             `json:"attempt"`
	MaxAttempts         int             `json:"max_attempts"`
	BlockedCategory     *string         `json:"blocked_category"`
	BlockedReason       *string         `json:"blocked_reason"`
	IssueStatus         *string         `json:"issue_status"`
	WorkflowState       *string         `json:"workflow_state"`
	AcceptanceCriteria  json.RawMessage `json:"acceptance_criteria"`
	UpdatedAt           string          `json:"updated_at"`
}

type AutonomousProjectPlanEdgeResponse struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`
}

type AutonomousProjectPlanResponse struct {
	ID            string                              `json:"id"`
	Revision      int64                               `json:"revision"`
	Goal          string                              `json:"goal"`
	Status        string                              `json:"status"`
	PlannerName   string                              `json:"planner_name"`
	PlannerModel  *string                             `json:"planner_model"`
	Specification json.RawMessage                     `json:"specification"`
	Policy        json.RawMessage                     `json:"policy"`
	Nodes         []AutonomousProjectPlanNodeResponse `json:"nodes"`
	Edges         []AutonomousProjectPlanEdgeResponse `json:"edges"`
	UpdatedAt     string                              `json:"updated_at"`
}

type AutonomousQualityGateResponse struct {
	ID        string          `json:"id"`
	NodeID    string          `json:"node_id"`
	GateType  string          `json:"gate_type"`
	Status    string          `json:"status"`
	Required  bool            `json:"required"`
	Evidence  json.RawMessage `json:"evidence"`
	LastError *string         `json:"last_error"`
	UpdatedAt string          `json:"updated_at"`
}

type AutonomousEscalationResponse struct {
	ID         string          `json:"id"`
	NodeID     *string         `json:"node_id"`
	Category   string          `json:"category"`
	Status     string          `json:"status"`
	Severity   string          `json:"severity"`
	Summary    string          `json:"summary"`
	Context    json.RawMessage `json:"context"`
	Resolution json.RawMessage `json:"resolution,omitempty"`
	OpenedAt   string          `json:"opened_at"`
	ResolvedAt *string         `json:"resolved_at"`
}

type AutonomousBudgetResponse struct {
	TokenLimit         *int64 `json:"token_limit"`
	RuntimeSecondsLimit *int64 `json:"runtime_seconds_limit"`
	CostMicrounitsLimit *int64 `json:"cost_microunits_limit"`
	MaxParallelNodes   int    `json:"max_parallel_nodes"`
	MaxTotalAttempts   int    `json:"max_total_attempts"`
	TokensUsed         int64  `json:"tokens_used"`
	RuntimeSecondsUsed int64  `json:"runtime_seconds_used"`
	CostMicrounitsUsed int64  `json:"cost_microunits_used"`
	TotalAttempts      int    `json:"total_attempts"`
}

type autonomousActivitySortable struct {
	At   time.Time
	Item AutonomousActivityResponse
}

func nullableTimestampString(ts pgtype.Timestamptz) *string {
	if !ts.Valid {
		return nil
	}
	value := ts.Time.UTC().Format(time.RFC3339Nano)
	return &value
}

func nullableTextString(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	out := value.String
	return &out
}

func nullableUUIDString(value pgtype.UUID) *string {
	if !value.Valid {
		return nil
	}
	out := uuidToString(value)
	return &out
}

func (h *Handler) GetProjectAutonomousControlCenter(w http.ResponseWriter, r *http.Request) {
	projectID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "project id")
	if !ok {
		return
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	project, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{
		ID: projectID, WorkspaceID: workspaceID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}

	resp := AutonomousProjectResponse{
		Runtimes:  []AutonomousRuntimeOptionResponse{},
		Skills:    []AutonomousSkillOptionResponse{},
		Workflows: []AutonomousWorkflowResponse{},
		Actions:   []AutonomousActionResponse{},
		Activity:  []AutonomousActivityResponse{},
		Decisions: []AutonomousDecisionResponse{},
		QualityGates: []AutonomousQualityGateResponse{},
		Escalations: []AutonomousEscalationResponse{},
		Diagnostics: []AutonomousDiagnosticResponse{},
		Health: AutonomousProjectHealthResponse{Status: "idle"},
	}

	var pausedAt, replanRequestedAt, replanCompletedAt pgtype.Timestamptz
	var lastError pgtype.Text
	err = h.DB.QueryRow(r.Context(), `
		SELECT paused, paused_at, replan_requested_at, replan_completed_at, last_error
		FROM autonomous_project_control
		WHERE workspace_id = $1 AND project_id = $2
	`, workspaceID, projectID).Scan(
		&resp.Control.Paused, &pausedAt, &replanRequestedAt, &replanCompletedAt, &lastError,
	)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to load autonomous project control")
		return
	}
	resp.Control.PausedAt = nullableTimestampString(pausedAt)
	resp.Control.ReplanRequestedAt = nullableTimestampString(replanRequestedAt)
	resp.Control.ReplanCompletedAt = nullableTimestampString(replanCompletedAt)
	resp.Control.LastError = nullableTextString(lastError)

	brain := AutonomousBrainResponse{
		Enabled: true,
		RuntimeMode: "inherit_mika",
		LearningMode: "adaptive",
	}
	var brainRuntimeID pgtype.UUID
	var brainModel, brainThinking, brainTier pgtype.Text
	brainErr := h.DB.QueryRow(r.Context(), `
		SELECT enabled, runtime_mode, runtime_id, model, thinking_level, service_tier, learning_mode
		FROM autonomous_project_brain_config
		WHERE workspace_id = $1 AND project_id = $2
	`, workspaceID, projectID).Scan(
		&brain.Enabled, &brain.RuntimeMode, &brainRuntimeID,
		&brainModel, &brainThinking, &brainTier, &brain.LearningMode,
	)
	if brainErr != nil && !errors.Is(brainErr, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to load project brain configuration")
		return
	}
	brain.RuntimeID = nullableUUIDString(brainRuntimeID)
	brain.Model = nullableTextString(brainModel)
	brain.ThinkingLevel = nullableTextString(brainThinking)
	brain.ServiceTier = nullableTextString(brainTier)
	if err := h.DB.QueryRow(r.Context(), `
		SELECT
			COUNT(*) FILTER (WHERE status='active' AND superseded_by IS NULL),
			COUNT(*) FILTER (WHERE status='superseded' OR superseded_by IS NOT NULL)
		FROM autonomous_project_brain_entry
		WHERE workspace_id=$1 AND project_id=$2
	`, workspaceID, projectID).Scan(&brain.ActiveMemories, &brain.SupersededMemories); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load project brain memory statistics")
		return
	}
	if err := h.DB.QueryRow(r.Context(), `
		SELECT
			COUNT(*) FILTER (WHERE status IN ('pending','running')),
			COUNT(*) FILTER (WHERE status='deferred')
		FROM autonomous_project_brain_learning_job
		WHERE workspace_id=$1 AND project_id=$2
	`, workspaceID, projectID).Scan(&brain.PendingLearningJobs, &brain.DeferredLearningJobs); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load project brain learning statistics")
		return
	}
	resp.Brain = &brain

	var bootstrap AutonomousProjectBootstrapResponse
	var bootstrapKnowledge, bootstrapPolicy, bootstrapBudget []byte
	var bootstrapUpdatedAt time.Time
	bootstrapErr := h.DB.QueryRow(r.Context(), `
		SELECT autonomy_mode, autonomy_level, brief, knowledge, policy, budget, status, updated_at
		FROM autonomous_project_bootstrap
		WHERE workspace_id = $1 AND project_id = $2
	`, workspaceID, projectID).Scan(
		&bootstrap.AutonomyMode,
		&bootstrap.AutonomyLevel,
		&bootstrap.Brief,
		&bootstrapKnowledge,
		&bootstrapPolicy,
		&bootstrapBudget,
		&bootstrap.Status,
		&bootstrapUpdatedAt,
	)
	if bootstrapErr == nil {
		bootstrap.Knowledge = append(json.RawMessage(nil), bootstrapKnowledge...)
		bootstrap.Policy = append(json.RawMessage(nil), bootstrapPolicy...)
		bootstrap.Budget = append(json.RawMessage(nil), bootstrapBudget...)
		bootstrap.UpdatedAt = bootstrapUpdatedAt.UTC().Format(time.RFC3339Nano)
		resp.Bootstrap = &bootstrap
		if bootstrap.AutonomyMode == "autonomous" {
			resp.Enabled = true
		}
	} else if !errors.Is(bootstrapErr, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to load autonomous project bootstrap")
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	userUUID, ok := parseUUIDOrBadRequest(w, userID, "user id")
	if !ok {
		return
	}

	var mikaRuntimeID pgtype.UUID
	mika, mikaErr := h.Queries.GetAgentBySystemKey(r.Context(), db.GetAgentBySystemKeyParams{
		WorkspaceID: workspaceID,
		SystemKey: pgtype.Text{String: service.MikaSystemKey, Valid: true},
	})
	if mikaErr == nil && mika.RuntimeID.Valid {
		mikaRuntimeID = mika.RuntimeID
	}

	runtimeRows, runtimeErr := h.DB.Query(r.Context(), `
		SELECT id, COALESCE(NULLIF(custom_name, ''), name), provider, runtime_mode, status
		FROM agent_runtime
		WHERE workspace_id = $1
		  AND (owner_id = $2 OR visibility = 'public' OR id = $3)
		ORDER BY CASE WHEN id = $3 THEN 0 ELSE 1 END,
		         CASE WHEN status = 'online' THEN 0 ELSE 1 END,
		         provider, name
	`, workspaceID, userUUID, mikaRuntimeID)
	if runtimeErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to load autonomous runtime options")
		return
	}
	for runtimeRows.Next() {
		var runtime AutonomousRuntimeOptionResponse
		var runtimeID pgtype.UUID
		if err := runtimeRows.Scan(&runtimeID, &runtime.Name, &runtime.Provider, &runtime.RuntimeMode, &runtime.Status); err != nil {
			runtimeRows.Close()
			writeError(w, http.StatusInternalServerError, "failed to decode autonomous runtime option")
			return
		}
		runtime.ID = uuidToString(runtimeID)
		resp.Runtimes = append(resp.Runtimes, runtime)
	}
	if err := runtimeRows.Err(); err != nil {
		runtimeRows.Close()
		writeError(w, http.StatusInternalServerError, "failed to load autonomous runtime options")
		return
	}
	runtimeRows.Close()

	skillRows, skillErr := h.DB.Query(r.Context(), `
		SELECT id, name, description
		FROM skill
		WHERE workspace_id = $1
		ORDER BY name ASC
	`, workspaceID)
	if skillErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to load autonomous skill options")
		return
	}
	for skillRows.Next() {
		var skill AutonomousSkillOptionResponse
		var skillID pgtype.UUID
		if err := skillRows.Scan(&skillID, &skill.Name, &skill.Description); err != nil {
			skillRows.Close()
			writeError(w, http.StatusInternalServerError, "failed to decode autonomous skill option")
			return
		}
		skill.ID = uuidToString(skillID)
		resp.Skills = append(resp.Skills, skill)
	}
	if err := skillRows.Err(); err != nil {
		skillRows.Close()
		writeError(w, http.StatusInternalServerError, "failed to load autonomous skill options")
		return
	}
	skillRows.Close()

	var draftPlan []byte
	var draftPlannerName, draftStatus string
	var draftPlannerModel pgtype.Text
	var draftCreatedAt, draftUpdatedAt time.Time
	draftErr := h.DB.QueryRow(r.Context(), `
		SELECT plan, planner_name, planner_model, status, created_at, updated_at
		FROM autonomous_project_team_draft
		WHERE workspace_id = $1 AND project_id = $2
		  AND status IN ('awaiting_configuration', 'provisioning')
	`, workspaceID, projectID).Scan(
		&draftPlan,
		&draftPlannerName,
		&draftPlannerModel,
		&draftStatus,
		&draftCreatedAt,
		&draftUpdatedAt,
	)
	if draftErr != nil && !errors.Is(draftErr, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to load autonomous team draft")
		return
	}
	if draftErr == nil {
		defaultSkillIDs := make([]string, 0, len(resp.Skills))
		for _, skill := range resp.Skills {
			defaultSkillIDs = append(defaultSkillIDs, skill.ID)
		}
		resp.Enabled = true
		resp.Draft = &AutonomousTeamDraftResponse{
			Status: draftStatus,
			PlannerName: draftPlannerName,
			PlannerModel: nullableTextString(draftPlannerModel),
			Plan: append(json.RawMessage(nil), draftPlan...),
			DefaultRuntimeID: nullableUUIDString(mikaRuntimeID),
			DefaultSkillIDs: defaultSkillIDs,
			CreatedAt: draftCreatedAt.UTC().Format(time.RFC3339Nano),
			UpdatedAt: draftUpdatedAt.UTC().Format(time.RFC3339Nano),
		}
	}

	var teamID, squadID pgtype.UUID
	var team AutonomousTeamResponse
	var plannerModel pgtype.Text
	var lastPlannedAt pgtype.Timestamptz
	var teamCreatedAt, teamUpdatedAt time.Time
	err = h.DB.QueryRow(r.Context(), `
		SELECT id, squad_id, intent, status, planner_name, planner_model,
		       plan_revision, last_planned_at, created_at, updated_at
		FROM autonomous_project_team
		WHERE workspace_id = $1 AND project_id = $2 AND status = 'active'
	`, workspaceID, projectID).Scan(
		&teamID, &squadID, &team.Intent, &team.Status, &team.PlannerName, &plannerModel,
		&team.PlanRevision, &lastPlannedAt, &teamCreatedAt, &teamUpdatedAt,
	)
	if err == nil {
		resp.Enabled = true
		team.ID = uuidToString(teamID)
		team.SquadID = uuidToString(squadID)
		team.PlannerModel = nullableTextString(plannerModel)
		team.LastPlannedAt = nullableTimestampString(lastPlannedAt)
		team.UpdatedAt = teamUpdatedAt.UTC().Format(time.RFC3339Nano)
		team.Members = []AutonomousTeamMemberResponse{}

		rows, queryErr := h.DB.Query(r.Context(), `
			SELECT
				m.role, m.role_family, m.agent_id, a.name,
				m.capabilities, m.responsibilities, m.reason, m.active, m.created_at,
				active_task.id, active_issue.title, active_task.status
			FROM autonomous_project_team_member m
			JOIN agent a ON a.id = m.agent_id
			LEFT JOIN LATERAL (
				SELECT t.id, t.issue_id, t.status
				FROM agent_task_queue t
				JOIN issue ti ON ti.id = t.issue_id
				WHERE t.agent_id = m.agent_id
				  AND ti.project_id = $2
				  AND t.status IN ('queued','dispatched','running','waiting_local_directory','deferred')
				ORDER BY t.created_at DESC
				LIMIT 1
			) active_task ON TRUE
			LEFT JOIN issue active_issue ON active_issue.id = active_task.issue_id
			WHERE m.team_id = $1
			ORDER BY m.active DESC, m.created_at ASC, m.role ASC
		`, teamID, projectID)
		if queryErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to load autonomous project team")
			return
		}
		for rows.Next() {
			var member AutonomousTeamMemberResponse
			var agentID, taskID pgtype.UUID
			var taskTitle, taskStatus pgtype.Text
			var capabilities, responsibilities []byte
			var createdAt time.Time
			if scanErr := rows.Scan(
				&member.Role, &member.Family, &agentID, &member.AgentName,
				&capabilities, &responsibilities, &member.Reason, &member.Active, &createdAt,
				&taskID, &taskTitle, &taskStatus,
			); scanErr != nil {
				rows.Close()
				writeError(w, http.StatusInternalServerError, "failed to decode autonomous team member")
				return
			}
			member.AgentID = uuidToString(agentID)
			member.Capabilities = append(json.RawMessage(nil), capabilities...)
			member.Responsibilities = append(json.RawMessage(nil), responsibilities...)
			member.CurrentTaskID = nullableUUIDString(taskID)
			member.CurrentTaskTitle = nullableTextString(taskTitle)
			member.CurrentTaskStatus = nullableTextString(taskStatus)
			member.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
			team.Members = append(team.Members, member)
		}
		rows.Close()
		resp.Team = &team
	} else if !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to load autonomous project team")
		return
	}

	// Project OS projection: durable plan/DAG, evidence, escalations and budget.
	var projectPlanID pgtype.UUID
	var projectPlan AutonomousProjectPlanResponse
	var projectPlanModel pgtype.Text
	var specJSON, policyJSON []byte
	var projectPlanUpdatedAt time.Time
	planErr := h.DB.QueryRow(r.Context(), `
		SELECT id, revision, goal, status, planner_name, planner_model,
		       specification, policy, updated_at
		FROM autonomous_project_plan
		WHERE workspace_id = $1 AND project_id = $2
		ORDER BY revision DESC
		LIMIT 1
	`, workspaceID, projectID).Scan(
		&projectPlanID, &projectPlan.Revision, &projectPlan.Goal, &projectPlan.Status,
		&projectPlan.PlannerName, &projectPlanModel, &specJSON, &policyJSON, &projectPlanUpdatedAt,
	)
	if planErr == nil {
		resp.Enabled = true
		projectPlan.ID = uuidToString(projectPlanID)
		projectPlan.PlannerModel = nullableTextString(projectPlanModel)
		projectPlan.Specification = append(json.RawMessage(nil), specJSON...)
		projectPlan.Policy = append(json.RawMessage(nil), policyJSON...)
		projectPlan.UpdatedAt = projectPlanUpdatedAt.UTC().Format(time.RFC3339Nano)
		projectPlan.Nodes = []AutonomousProjectPlanNodeResponse{}
		projectPlan.Edges = []AutonomousProjectPlanEdgeResponse{}

		nodeRows, queryErr := h.DB.Query(r.Context(), `
			SELECT n.id, n.node_key, n.kind, n.title, n.status, n.priority, n.risk_level,
			       n.required_role_family, n.assigned_role, n.assigned_agent_id,
			       n.materialized_issue_id, n.attempt, n.max_attempts,
			       n.blocked_category, n.blocked_reason, i.status, wr.state,
			       n.acceptance_criteria, n.updated_at
			FROM autonomous_project_plan_node n
			LEFT JOIN issue i ON i.id = n.materialized_issue_id
			LEFT JOIN autonomous_workflow_run wr
			  ON wr.issue_id = n.materialized_issue_id
			 AND wr.workspace_id = n.workspace_id
			 AND wr.project_id = n.project_id
			 AND wr.workflow_name = 'software-development'
			WHERE n.plan_id = $1
			ORDER BY n.priority DESC, n.created_at ASC
		`, projectPlanID)
		if queryErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to load autonomous project plan nodes")
			return
		}
		for nodeRows.Next() {
			var node AutonomousProjectPlanNodeResponse
			var nodeID, agentID, issueID pgtype.UUID
			var family, assignedRole, blockedCategory, blockedReason, issueStatus, workflowState pgtype.Text
			var criteria []byte
			var updatedAt time.Time
			if err := nodeRows.Scan(
				&nodeID, &node.Key, &node.Kind, &node.Title, &node.Status, &node.Priority, &node.Risk,
				&family, &assignedRole, &agentID, &issueID, &node.Attempt, &node.MaxAttempts,
				&blockedCategory, &blockedReason, &issueStatus, &workflowState,
				&criteria, &updatedAt,
			); err != nil {
				nodeRows.Close()
				writeError(w, http.StatusInternalServerError, "failed to decode autonomous project plan node")
				return
			}
			node.ID = uuidToString(nodeID)
			node.RequiredRoleFamily = nullableTextString(family)
			node.AssignedRole = nullableTextString(assignedRole)
			node.AssignedAgentID = nullableUUIDString(agentID)
			node.MaterializedIssueID = nullableUUIDString(issueID)
			node.BlockedCategory = nullableTextString(blockedCategory)
			node.BlockedReason = nullableTextString(blockedReason)
			node.IssueStatus = nullableTextString(issueStatus)
			node.WorkflowState = nullableTextString(workflowState)
			node.AcceptanceCriteria = append(json.RawMessage(nil), criteria...)
			node.UpdatedAt = updatedAt.UTC().Format(time.RFC3339Nano)
			projectPlan.Nodes = append(projectPlan.Nodes, node)
		}
		nodeRows.Close()

		edgeRows, queryErr := h.DB.Query(r.Context(), `
			SELECT from_node_key, to_node_key, dependency_type
			FROM autonomous_project_plan_edge
			WHERE plan_id = $1
			ORDER BY created_at ASC
		`, projectPlanID)
		if queryErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to load autonomous project plan edges")
			return
		}
		for edgeRows.Next() {
			var edge AutonomousProjectPlanEdgeResponse
			if edgeRows.Scan(&edge.From, &edge.To, &edge.Type) == nil {
				projectPlan.Edges = append(projectPlan.Edges, edge)
			}
		}
		edgeRows.Close()
		resp.Plan = &projectPlan
	} else if !errors.Is(planErr, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to load autonomous project plan")
		return
	}

	gateRows, gateErr := h.DB.Query(r.Context(), `
		SELECT g.id, g.node_id, g.gate_type, g.status, g.required,
		       g.evidence, g.last_error, g.updated_at
		FROM autonomous_project_quality_gate_run g
		WHERE g.workspace_id = $1 AND g.project_id = $2
		ORDER BY g.updated_at DESC
		LIMIT 100
	`, workspaceID, projectID)
	if gateErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to load autonomous quality gates")
		return
	}
	for gateRows.Next() {
		var gate AutonomousQualityGateResponse
		var id, nodeID pgtype.UUID
		var lastError pgtype.Text
		var evidence []byte
		var updatedAt time.Time
		if gateRows.Scan(&id, &nodeID, &gate.GateType, &gate.Status, &gate.Required,
			&evidence, &lastError, &updatedAt) == nil {
			gate.ID = uuidToString(id)
			gate.NodeID = uuidToString(nodeID)
			gate.Evidence = append(json.RawMessage(nil), evidence...)
			gate.LastError = nullableTextString(lastError)
			gate.UpdatedAt = updatedAt.UTC().Format(time.RFC3339Nano)
			resp.QualityGates = append(resp.QualityGates, gate)
		}
	}
	gateRows.Close()

	escalationRows, escalationErr := h.DB.Query(r.Context(), `
		SELECT id, node_id, category, status, severity, summary, context,
		       resolution, opened_at, resolved_at
		FROM autonomous_project_escalation
		WHERE workspace_id = $1 AND project_id = $2
		ORDER BY CASE WHEN status IN ('open','acknowledged') THEN 0 ELSE 1 END,
		         opened_at DESC
		LIMIT 100
	`, workspaceID, projectID)
	if escalationErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to load autonomous escalations")
		return
	}
	for escalationRows.Next() {
		var item AutonomousEscalationResponse
		var id, nodeID pgtype.UUID
		var contextJSON, resolutionJSON []byte
		var openedAt time.Time
		var resolvedAt pgtype.Timestamptz
		if escalationRows.Scan(&id, &nodeID, &item.Category, &item.Status, &item.Severity,
			&item.Summary, &contextJSON, &resolutionJSON, &openedAt, &resolvedAt) == nil {
			item.ID = uuidToString(id)
			item.NodeID = nullableUUIDString(nodeID)
			item.Context = append(json.RawMessage(nil), contextJSON...)
			if len(resolutionJSON) > 0 {
				item.Resolution = append(json.RawMessage(nil), resolutionJSON...)
			}
			item.OpenedAt = openedAt.UTC().Format(time.RFC3339Nano)
			item.ResolvedAt = nullableTimestampString(resolvedAt)
			resp.Escalations = append(resp.Escalations, item)
		}
	}
	escalationRows.Close()

	var tokenLimit, runtimeLimit, costLimit pgtype.Int8
	var budget AutonomousBudgetResponse
	budgetErr := h.DB.QueryRow(r.Context(), `
		SELECT token_limit, runtime_seconds_limit, cost_microunits_limit,
		       max_parallel_nodes, max_total_attempts, tokens_used,
		       runtime_seconds_used, cost_microunits_used, total_attempts
		FROM autonomous_project_budget
		WHERE workspace_id = $1 AND project_id = $2
	`, workspaceID, projectID).Scan(
		&tokenLimit, &runtimeLimit, &costLimit, &budget.MaxParallelNodes,
		&budget.MaxTotalAttempts, &budget.TokensUsed, &budget.RuntimeSecondsUsed,
		&budget.CostMicrounitsUsed, &budget.TotalAttempts,
	)
	if budgetErr == nil {
		if tokenLimit.Valid { v := tokenLimit.Int64; budget.TokenLimit = &v }
		if runtimeLimit.Valid { v := runtimeLimit.Int64; budget.RuntimeSecondsLimit = &v }
		if costLimit.Valid { v := costLimit.Int64; budget.CostMicrounitsLimit = &v }
		resp.Budget = &budget
	} else if !errors.Is(budgetErr, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to load autonomous project budget")
		return
	}

	rows, err := h.DB.Query(r.Context(), `
		SELECT
			wr.id, wr.issue_id, i.title, wr.state, wr.revision, wr.review_cycles,
			wr.owner_agent_id, owner.name, wr.reviewer_agent_id, reviewer.name,
			COALESCE(actions.pending_count, 0),
			COALESCE(actions.failed_count, 0),
			wr.updated_at
		FROM autonomous_workflow_run wr
		JOIN issue i ON i.id = wr.issue_id
		LEFT JOIN agent owner ON owner.id = wr.owner_agent_id
		LEFT JOIN agent reviewer ON reviewer.id = wr.reviewer_agent_id
		LEFT JOIN LATERAL (
			SELECT
				COUNT(*) FILTER (WHERE status IN ('pending','running')) AS pending_count,
				COUNT(*) FILTER (WHERE status = 'failed') AS failed_count
			FROM autonomous_workflow_action a
			WHERE a.run_id = wr.id
		) actions ON TRUE
		WHERE wr.workspace_id = $1 AND wr.project_id = $2
		ORDER BY wr.updated_at DESC
		LIMIT 200
	`, workspaceID, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load autonomous workflows")
		return
	}
	var totalFailed int64
	for rows.Next() {
		var run AutonomousWorkflowResponse
		var id, issueID, ownerID, reviewerID pgtype.UUID
		var ownerName, reviewerName pgtype.Text
		var updatedAt time.Time
		if err := rows.Scan(
			&id, &issueID, &run.IssueTitle, &run.State, &run.Revision, &run.ReviewCycles,
			&ownerID, &ownerName, &reviewerID, &reviewerName,
			&run.PendingActions, &run.FailedActions, &updatedAt,
		); err != nil {
			rows.Close()
			writeError(w, http.StatusInternalServerError, "failed to decode autonomous workflow")
			return
		}
		run.ID = uuidToString(id)
		run.IssueID = uuidToString(issueID)
		run.OwnerAgentID = nullableUUIDString(ownerID)
		run.OwnerAgentName = nullableTextString(ownerName)
		run.ReviewerAgentID = nullableUUIDString(reviewerID)
		run.ReviewerName = nullableTextString(reviewerName)
		run.UpdatedAt = updatedAt.UTC().Format(time.RFC3339Nano)
		resp.Workflows = append(resp.Workflows, run)
		totalFailed += run.FailedActions
		if run.State == "blocked" {
			resp.Health.Blocked++
		} else if run.State != "done" && run.State != "cancelled" {
			resp.Health.ActiveWorkflows++
		}
	}
	rows.Close()
	resp.Health.FailedActions = totalFailed

	actionRows, err := h.DB.Query(r.Context(), `
		SELECT a.id, a.run_id, a.action_type, a.status, a.attempts,
		       a.max_attempts, a.last_error, a.created_at, a.updated_at
		FROM autonomous_workflow_action a
		JOIN autonomous_workflow_run wr ON wr.id = a.run_id
		WHERE wr.workspace_id = $1 AND wr.project_id = $2
		  AND a.status IN ('pending','running','failed')
		ORDER BY a.updated_at DESC
		LIMIT 100
	`, workspaceID, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load autonomous workflow actions")
		return
	}
	for actionRows.Next() {
		var action AutonomousActionResponse
		var id, runID pgtype.UUID
		var lastError pgtype.Text
		var createdAt, updatedAt time.Time
		if err := actionRows.Scan(
			&id, &runID, &action.ActionType, &action.Status, &action.Attempts,
			&action.MaxAttempts, &lastError, &createdAt, &updatedAt,
		); err != nil {
			actionRows.Close()
			writeError(w, http.StatusInternalServerError, "failed to decode autonomous workflow action")
			return
		}
		action.ID = uuidToString(id)
		action.RunID = uuidToString(runID)
		action.LastError = nullableTextString(lastError)
		action.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
		action.UpdatedAt = updatedAt.UTC().Format(time.RFC3339Nano)
		resp.Actions = append(resp.Actions, action)
	}
	actionRows.Close()

	diagnostics, diagErr := h.loadAutonomousDiagnostics(
		r.Context(), workspaceID, projectID, resp.Plan, resp.Workflows,
		resp.Actions, resp.Escalations, resp.Control.Paused,
	)
	if diagErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to diagnose autonomous project state")
		return
	}
	resp.Diagnostics = diagnostics
	for _, diagnostic := range diagnostics {
		resp.Health.Waiting++
		if diagnostic.CanResume {
			resp.Health.Resumable++
		}
		if diagnostic.Severity == "warning" || diagnostic.Severity == "error" {
			resp.Health.Status = "attention"
		}
	}

	switch {
	case resp.Control.Paused:
		resp.Health.Status = "paused"
	case resp.Health.Status == "attention" || resp.Health.Blocked > 0 || totalFailed > 0:
		resp.Health.Status = "attention"
	case resp.Health.ActiveWorkflows > 0:
		resp.Health.Status = "running"
	case resp.Enabled:
		resp.Health.Status = "ready"
	}

	sortable := make([]autonomousActivitySortable, 0, 128)
	if project.CreatedAt.Valid {
		projectIDString := uuidToString(project.ID)
		sortable = append(sortable, autonomousActivitySortable{
			At: project.CreatedAt.Time,
			Item: AutonomousActivityResponse{
				ID:        "project-created:" + projectIDString,
				Type:      "project.created",
				Title:     "Project created",
				Detail:    project.Title,
				CreatedAt: project.CreatedAt.Time.UTC().Format(time.RFC3339Nano),
			},
		})
	}
	if teamID.Valid && !teamCreatedAt.IsZero() {
		sortable = append(sortable, autonomousActivitySortable{
			At: teamCreatedAt,
			Item: AutonomousActivityResponse{
				ID:        "team-created:" + uuidToString(teamID),
				Type:      "team.created",
				Title:     "Technology Team created",
				Detail:    team.Intent,
				Metadata:  map[string]any{"squad_id": uuidToString(squadID)},
				CreatedAt: teamCreatedAt.UTC().Format(time.RFC3339Nano),
			},
		})
	}

	if teamID.Valid {
		memberRows, err := h.DB.Query(r.Context(), `
			SELECT m.role, m.role_family, m.agent_id, a.name, m.reason, m.created_at
			FROM autonomous_project_team_member m
			JOIN agent a ON a.id = m.agent_id
			WHERE m.team_id = $1
			ORDER BY m.created_at DESC
			LIMIT 100
		`, teamID)
		if err == nil {
			for memberRows.Next() {
				var role, family, name, reason string
				var agentID pgtype.UUID
				var createdAt time.Time
				if memberRows.Scan(&role, &family, &agentID, &name, &reason, &createdAt) == nil {
					agentIDString := uuidToString(agentID)
					sortable = append(sortable, autonomousActivitySortable{
						At: createdAt,
						Item: AutonomousActivityResponse{
							ID: "team-member:" + agentIDString,
							Type: "team.member.added",
							Title: name + " added to Technology Team",
							Detail: reason,
							AgentID: &agentIDString,
							Metadata: map[string]any{"role": role, "family": family},
							CreatedAt: createdAt.UTC().Format(time.RFC3339Nano),
						},
					})
				}
			}
			memberRows.Close()
		}

		decisionRows, err := h.DB.Query(r.Context(), `
			SELECT id, source_type, source_id, source_revision, planner_name,
			       planner_model, plan, created_at
			FROM autonomous_project_team_analysis
			WHERE team_id = $1
			ORDER BY created_at DESC
			LIMIT 100
		`, teamID)
		if err == nil {
			for decisionRows.Next() {
				var decision AutonomousDecisionResponse
				var id, sourceID pgtype.UUID
				var plannerModel pgtype.Text
				var plan []byte
				var createdAt time.Time
				if decisionRows.Scan(
					&id, &decision.SourceType, &sourceID, &decision.SourceRevision,
					&decision.PlannerName, &plannerModel, &plan, &createdAt,
				) == nil {
					decision.ID = uuidToString(id)
					decision.SourceID = uuidToString(sourceID)
					decision.PlannerModel = nullableTextString(plannerModel)
					decision.Plan = append(json.RawMessage(nil), plan...)
					decision.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
					resp.Decisions = append(resp.Decisions, decision)

					detail := decision.PlannerName
					if decision.PlannerModel != nil && *decision.PlannerModel != "" {
						detail += " · " + *decision.PlannerModel
					}
					sortable = append(sortable, autonomousActivitySortable{
						At: createdAt,
						Item: AutonomousActivityResponse{
							ID: "decision:" + decision.ID,
							Type: "team.planned",
							Title: "LLM team plan evaluated",
							Detail: detail,
							Metadata: map[string]any{
								"source_type": decision.SourceType,
								"source_id": decision.SourceID,
								"source_revision": decision.SourceRevision,
							},
							CreatedAt: decision.CreatedAt,
						},
					})
				}
			}
			decisionRows.Close()
		}
	}

	eventRows, err := h.DB.Query(r.Context(), `
		SELECT pe.event_id, pe.event_type, wr.issue_id, i.title, pe.created_at
		FROM autonomous_workflow_processed_event pe
		JOIN autonomous_workflow_run wr ON wr.id = pe.run_id
		JOIN issue i ON i.id = wr.issue_id
		WHERE wr.workspace_id = $1 AND wr.project_id = $2
		ORDER BY pe.created_at DESC
		LIMIT 150
	`, workspaceID, projectID)
	if err == nil {
		for eventRows.Next() {
			var eventID, eventType, issueTitle string
			var issueID pgtype.UUID
			var createdAt time.Time
			if eventRows.Scan(&eventID, &eventType, &issueID, &issueTitle, &createdAt) == nil {
				issueIDString := uuidToString(issueID)
				sortable = append(sortable, autonomousActivitySortable{
					At: createdAt,
					Item: AutonomousActivityResponse{
						ID: "workflow-event:" + eventID,
						Type: eventType,
						Title: strings.ReplaceAll(eventType, ".", " "),
						Detail: issueTitle,
						IssueID: &issueIDString,
						CreatedAt: createdAt.UTC().Format(time.RFC3339Nano),
					},
				})
			}
		}
		eventRows.Close()
	}

	taskRows, err := h.DB.Query(r.Context(), `
		SELECT t.id, t.agent_id, a.name, t.issue_id, i.title, t.status,
		       t.failure_reason, t.error, t.fire_at, t.retry_of_task_id, t.rerun_of_task_id,
		       t.created_at, t.completed_at
		FROM agent_task_queue t
		JOIN issue i ON i.id = t.issue_id
		JOIN agent a ON a.id = t.agent_id
		WHERE i.workspace_id = $1 AND i.project_id = $2
		ORDER BY COALESCE(t.completed_at, t.created_at) DESC
		LIMIT 100
	`, workspaceID, projectID)
	if err == nil {
		for taskRows.Next() {
			var taskID, agentID, issueID, retryOf, rerunOf pgtype.UUID
			var agentName, issueTitle, status string
			var failureReason, taskError pgtype.Text
			var fireAt, completedAt pgtype.Timestamptz
			var createdAt time.Time
			if taskRows.Scan(
				&taskID, &agentID, &agentName, &issueID, &issueTitle, &status,
				&failureReason, &taskError, &fireAt, &retryOf, &rerunOf,
				&createdAt, &completedAt,
			) == nil {
				at := createdAt
				if completedAt.Valid {
					at = completedAt.Time
				}
				taskIDString := uuidToString(taskID)
				agentIDString := uuidToString(agentID)
				issueIDString := uuidToString(issueID)
				detail := issueTitle
				if failureReason.Valid && strings.TrimSpace(failureReason.String) != "" {
					detail += " · " + failureReason.String
				}
				if taskError.Valid && strings.TrimSpace(taskError.String) != "" {
					detail += " · " + taskError.String
				}
				metadata := map[string]any{"task_id": taskIDString}
				if failureReason.Valid {
					metadata["failure_reason"] = failureReason.String
				}
				if fireAt.Valid {
					metadata["fire_at"] = fireAt.Time.UTC().Format(time.RFC3339Nano)
				}
				if retryOf.Valid {
					metadata["retry_of_task_id"] = uuidToString(retryOf)
				}
				if rerunOf.Valid {
					metadata["rerun_of_task_id"] = uuidToString(rerunOf)
				}
				sortable = append(sortable, autonomousActivitySortable{
					At: at,
					Item: AutonomousActivityResponse{
						ID: "task:" + taskIDString + ":" + status,
						Type: "task." + status,
						Title: agentName + " · " + status,
						Detail: detail,
						IssueID: &issueIDString,
						AgentID: &agentIDString,
						Metadata: metadata,
						CreatedAt: at.UTC().Format(time.RFC3339Nano),
					},
				})
			}
		}
		taskRows.Close()
	}

	sort.SliceStable(sortable, func(i, j int) bool { return sortable[i].At.After(sortable[j].At) })
	if len(sortable) > 200 {
		sortable = sortable[:200]
	}
	resp.Activity = make([]AutonomousActivityResponse, len(sortable))
	for i := range sortable {
		resp.Activity[i] = sortable[i].Item
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) loadAutonomousDiagnostics(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	plan *AutonomousProjectPlanResponse,
	workflows []AutonomousWorkflowResponse,
	actions []AutonomousActionResponse,
	escalations []AutonomousEscalationResponse,
	paused bool,
) ([]AutonomousDiagnosticResponse, error) {
	out := make([]AutonomousDiagnosticResponse, 0, 16)
	now := time.Now().UTC()
	add := func(item AutonomousDiagnosticResponse) {
		if item.UpdatedAt == "" {
			item.UpdatedAt = now.Format(time.RFC3339Nano)
		}
		out = append(out, item)
	}

	if paused {
		add(AutonomousDiagnosticResponse{
			Code: "project_paused", Severity: "info",
			Title: "Autonomous project is paused",
			Detail: "Scheduling is intentionally stopped. Resume the project to continue from durable state.",
			CanResume: true, ResumeAction: "resume_project",
		})
	}

	workflowByIssue := make(map[string]AutonomousWorkflowResponse, len(workflows))
	for _, run := range workflows {
		workflowByIssue[run.IssueID] = run
	}
	nodeByKey := map[string]AutonomousProjectPlanNodeResponse{}
	blockers := map[string][]string{}
	if plan != nil {
		for _, node := range plan.Nodes {
			nodeByKey[node.Key] = node
		}
		for _, edge := range plan.Edges {
			if edge.Type != "hard" && edge.Type != "artifact" {
				continue
			}
			dep, ok := nodeByKey[edge.From]
			if !ok || dep.Status == "completed" || dep.Status == "cancelled" {
				continue
			}
			blockers[edge.To] = append(blockers[edge.To], edge.From+" ("+dep.Status+")")
		}
	}

	type taskState struct {
		Status        string
		FailureReason string
		Error         string
		FireAt        *time.Time
		At            time.Time
	}
	tasksByIssue := map[string]taskState{}
	if plan != nil {
		planID, err := util.ParseUUID(plan.ID)
		if err != nil {
			return nil, fmt.Errorf("parse autonomous project plan id: %w", err)
		}
		rows, err := h.DB.Query(ctx, `
			SELECT DISTINCT ON (t.issue_id)
			       t.issue_id, t.status, COALESCE(t.failure_reason, ''),
			       COALESCE(t.error, ''), t.fire_at,
			       COALESCE(t.completed_at, t.started_at, t.dispatched_at, t.created_at)
			FROM agent_task_queue t
			JOIN autonomous_project_plan_node n ON n.materialized_issue_id = t.issue_id
			WHERE n.plan_id = $1
			ORDER BY t.issue_id, t.created_at DESC
		`, planID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var issueID pgtype.UUID
			var state taskState
			var fireAt pgtype.Timestamptz
			if err := rows.Scan(&issueID, &state.Status, &state.FailureReason, &state.Error, &fireAt, &state.At); err != nil {
				rows.Close()
				return nil, err
			}
			if fireAt.Valid {
				v := fireAt.Time.UTC()
				state.FireAt = &v
			}
			tasksByIssue[uuidToString(issueID)] = state
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	if plan != nil {
		for _, node := range plan.Nodes {
			nodeKey := node.Key
			title := node.Title
			issueID := node.MaterializedIssueID
			updatedAt, _ := time.Parse(time.RFC3339Nano, node.UpdatedAt)
			age := now.Sub(updatedAt)
			if updatedAt.IsZero() {
				age = 0
			}
			var latest taskState
			if issueID != nil {
				latest = tasksByIssue[*issueID]
			}

			// A scheduled retry is a healthy wait, but it must be visible.
			if latest.Status == "deferred" && latest.FireAt != nil {
				detail := "Automatic retry is deferred until " + latest.FireAt.Format(time.RFC3339)
				canResume := latest.FireAt.Before(now)
				action := ""
				if canResume {
					detail += ". The retry deadline has passed; repair and continue can wake reconciliation immediately."
					action = "restart_workflow"
				}
				add(AutonomousDiagnosticResponse{
					Code: "retry_scheduled", Severity: "info", Title: title,
					Detail: detail, NodeKey: &nodeKey, IssueID: issueID, IssueTitle: &title,
					CanResume: canResume, ResumeAction: action,
					Metadata: map[string]any{"fire_at": latest.FireAt.Format(time.RFC3339Nano), "failure_reason": latest.FailureReason},
					UpdatedAt: latest.At.UTC().Format(time.RFC3339Nano),
				})
				continue
			}

			switch node.Status {
			case "pending":
				if deps := blockers[node.Key]; len(deps) > 0 {
					add(AutonomousDiagnosticResponse{
						Code: "dependency_wait", Severity: "info", Title: title,
						Detail: "Waiting for: " + strings.Join(deps, ", "),
						NodeKey: &nodeKey, IssueID: issueID, IssueTitle: &title,
						Metadata: map[string]any{"blockers": deps}, UpdatedAt: node.UpdatedAt,
					})
				} else if age > 30*time.Second && plan.Status != "completed" {
					add(AutonomousDiagnosticResponse{
						Code: "scheduler_stall", Severity: "warning", Title: title,
						Detail: "Node is pending with no unresolved hard dependency, but it was not promoted to ready.",
						NodeKey: &nodeKey, IssueID: issueID, IssueTitle: &title,
						CanResume: true, ResumeAction: "restart_workflow", UpdatedAt: node.UpdatedAt,
					})
				}
			case "ready":
				if !paused && age > 30*time.Second {
					add(AutonomousDiagnosticResponse{
						Code: "scheduler_stall", Severity: "warning", Title: title,
						Detail: "Node is ready and eligible but has not been dispatched by the project conductor.",
						NodeKey: &nodeKey, IssueID: issueID, IssueTitle: &title,
						CanResume: true, ResumeAction: "restart_workflow", UpdatedAt: node.UpdatedAt,
					})
				}
			case "blocked":
				code := "node_blocked"
				detail := "Project node is blocked."
				if node.BlockedReason != nil && *node.BlockedReason != "" {
					detail = *node.BlockedReason
				}
				action := "restart_workflow"
				canResume := true
				if node.BlockedCategory != nil {
					switch *node.BlockedCategory {
					case "approval":
						code, action, canResume = "approval_wait", "resolve_escalation", false
					case "dependency":
						code, action, canResume = "dependency_wait", "", false
					case "budget":
						code, action, canResume = "budget_exhausted", "replan", true
					case "external_dependency":
						code, action, canResume = "external_dependency", "restart_workflow", true
					case "technical_failure":
						code = "technical_failure"
					}
				}
				if latest.FailureReason == "agent_error.provider_quota_limit" {
					code = "provider_quota_wait"
					if latest.Error != "" {
						detail = latest.Error
					}
				}
				add(AutonomousDiagnosticResponse{
					Code: code, Severity: "warning", Title: title, Detail: detail,
					NodeKey: &nodeKey, IssueID: issueID, IssueTitle: &title,
					CanResume: canResume, ResumeAction: action,
					Metadata: map[string]any{"failure_reason": latest.FailureReason},
					UpdatedAt: node.UpdatedAt,
				})
			case "running", "verification":
				if issueID == nil {
					if age > time.Minute {
						add(AutonomousDiagnosticResponse{
							Code: "state_mismatch", Severity: "error", Title: title,
							Detail: "Node is active but has no materialized issue.",
							NodeKey: &nodeKey, IssueTitle: &title,
							CanResume: true, ResumeAction: "restart_workflow", UpdatedAt: node.UpdatedAt,
						})
					}
					continue
				}
				run, hasRun := workflowByIssue[*issueID]
				if hasRun && run.State == "blocked" {
					add(AutonomousDiagnosticResponse{
						Code: "workflow_blocked", Severity: "warning", Title: title,
						Detail: "Issue workflow is blocked and requires recovery before Project OS can advance.",
						NodeKey: &nodeKey, IssueID: issueID, IssueTitle: &title,
						CanResume: true, ResumeAction: "restart_workflow", UpdatedAt: run.UpdatedAt,
					})
				} else if age > 2*time.Minute &&
					(latest.Status == "" || latest.Status == "failed" || latest.Status == "completed" || latest.Status == "cancelled") {
					add(AutonomousDiagnosticResponse{
						Code: "workflow_stall", Severity: "warning", Title: title,
						Detail: "Node is active but there is no runnable agent task making progress.",
						NodeKey: &nodeKey, IssueID: issueID, IssueTitle: &title,
						CanResume: true, ResumeAction: "restart_workflow", UpdatedAt: node.UpdatedAt,
					})
				}
			case "completed":
				if node.IssueStatus != nil && *node.IssueStatus != "done" && *node.IssueStatus != "cancelled" {
					add(AutonomousDiagnosticResponse{
						Code: "state_mismatch", Severity: "warning", Title: title,
						Detail: "Project node is completed but its issue is still " + *node.IssueStatus + ".",
						NodeKey: &nodeKey, IssueID: issueID, IssueTitle: &title,
						CanResume: true, ResumeAction: "restart_workflow", UpdatedAt: node.UpdatedAt,
					})
				}
			}
		}
	}

	for _, action := range actions {
		if action.Status != "failed" {
			continue
		}
		actionID := action.ID
		detail := action.ActionType + " failed"
		if action.LastError != nil && *action.LastError != "" {
			detail += ": " + *action.LastError
		}
		add(AutonomousDiagnosticResponse{
			Code: "workflow_action_failed", Severity: "error",
			Title: "Workflow action failed", Detail: detail,
			ActionID: &actionID, CanResume: true, ResumeAction: "retry_action",
			UpdatedAt: action.UpdatedAt,
		})
	}

	for _, escalation := range escalations {
		if escalation.Status != "open" && escalation.Status != "acknowledged" {
			continue
		}
		// Approval is intentionally human-owned. Other open escalations are also
		// surfaced even when the node-level diagnostic already exists because
		// this row is the operator's durable evidence/audit handle.
		code := "open_escalation"
		action := "restart_workflow"
		canResume := escalation.Category != "approval_required"
		if escalation.Category == "approval_required" {
			code = "approval_wait"
			action = "resolve_escalation"
		}
		add(AutonomousDiagnosticResponse{
			Code: code, Severity: "warning",
			Title: escalation.Summary,
			Detail: "Open escalation: " + escalation.Category,
			CanResume: canResume, ResumeAction: action,
			UpdatedAt: escalation.OpenedAt,
		})
	}

	// Detect the exact replan ownership leak that leaves an issue in Backlog
	// although no current plan can ever schedule it. The runtime repair should
	// normally retire these within one reconciliation pass; if it cannot, the
	// Control Center still explains the orphan instead of silently looking idle.
	if plan != nil {
		planID, err := util.ParseUUID(plan.ID)
		if err == nil {
			rows, err := h.DB.Query(ctx, `
				SELECT DISTINCT i.id, i.title, i.status, n.node_key, p.revision, i.updated_at
				FROM autonomous_project_plan_node n
				JOIN autonomous_project_plan p ON p.id = n.plan_id
				JOIN issue i ON i.id = n.materialized_issue_id
				WHERE p.workspace_id = $1
				  AND p.project_id = $2
				  AND p.status = 'superseded'
				  AND NOT EXISTS (
				      SELECT 1 FROM autonomous_project_plan_node current_node
				      WHERE current_node.plan_id = $3
				        AND current_node.materialized_issue_id = i.id
				  )
				  AND i.status NOT IN ('done','cancelled')
				ORDER BY i.updated_at DESC
			`, workspaceID, projectID, planID)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var issueID pgtype.UUID
				var issueTitle, issueStatus, nodeKey string
				var revision int64
				var updatedAt time.Time
				if err := rows.Scan(&issueID, &issueTitle, &issueStatus, &nodeKey, &revision, &updatedAt); err != nil {
					rows.Close()
					return nil, err
				}
				issueIDString := uuidToString(issueID)
				nodeKeyCopy := nodeKey
				titleCopy := issueTitle
				add(AutonomousDiagnosticResponse{
					Code: "stale_after_replan", Severity: "error", Title: issueTitle,
					Detail: fmt.Sprintf("Issue is %s but is owned only by superseded plan revision %d; the current plan cannot schedule it.", issueStatus, revision),
					NodeKey: &nodeKeyCopy, IssueID: &issueIDString, IssueTitle: &titleCopy,
					CanResume: true, ResumeAction: "restart_workflow",
					Metadata: map[string]any{"superseded_revision": revision, "issue_status": issueStatus},
					UpdatedAt: updatedAt.UTC().Format(time.RFC3339Nano),
				})
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return nil, err
			}
			rows.Close()
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		rank := func(severity string) int {
			switch severity {
			case "error": return 0
			case "warning": return 1
			default: return 2
			}
		}
		if rank(out[i].Severity) != rank(out[j].Severity) {
			return rank(out[i].Severity) < rank(out[j].Severity)
		}
		return out[i].UpdatedAt > out[j].UpdatedAt
	})
	return out, nil
}

func (h *Handler) requireAutonomousControlAdmin(
	w http.ResponseWriter,
	r *http.Request,
	workspaceID pgtype.UUID,
) (pgtype.UUID, bool) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return pgtype.UUID{}, false
	}
	userUUID, ok := parseUUIDOrBadRequest(w, userID, "user id")
	if !ok {
		return pgtype.UUID{}, false
	}
	member, err := h.Queries.GetMemberByUserAndWorkspace(
		r.Context(),
		db.GetMemberByUserAndWorkspaceParams{
			UserID: userUUID,
			WorkspaceID: workspaceID,
		},
	)
	if err != nil || (member.Role != "owner" && member.Role != "admin") {
		writeError(w, http.StatusForbidden, "workspace owner or admin required")
		return pgtype.UUID{}, false
	}
	return userUUID, true
}


func (h *Handler) UpdateProjectAutonomousBrainConfig(w http.ResponseWriter, r *http.Request) {
	projectID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "project id")
	if !ok { return }
	workspaceID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok { return }
	userUUID, ok := h.requireAutonomousControlAdmin(w, r, workspaceID)
	if !ok { return }
	if _, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{ID: projectID, WorkspaceID: workspaceID}); err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}

	var req struct {
		Enabled       bool   `json:"enabled"`
		RuntimeMode   string `json:"runtime_mode"`
		RuntimeID     string `json:"runtime_id"`
		Model         string `json:"model"`
		ThinkingLevel string `json:"thinking_level"`
		ServiceTier   string `json:"service_tier"`
		LearningMode  string `json:"learning_mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid project brain configuration")
		return
	}
	req.RuntimeMode = strings.ToLower(strings.TrimSpace(req.RuntimeMode))
	req.LearningMode = strings.ToLower(strings.TrimSpace(req.LearningMode))
	if req.RuntimeMode == "" { req.RuntimeMode = "inherit_mika" }
	if req.LearningMode == "" { req.LearningMode = "adaptive" }
	if req.RuntimeMode != "inherit_mika" && req.RuntimeMode != "custom" {
		writeError(w, http.StatusBadRequest, "brain runtime_mode must be inherit_mika or custom")
		return
	}
	if req.LearningMode != "deterministic" && req.LearningMode != "assisted" && req.LearningMode != "adaptive" {
		writeError(w, http.StatusBadRequest, "brain learning_mode must be deterministic, assisted or adaptive")
		return
	}

	var runtimeID pgtype.UUID
	if req.RuntimeMode == "custom" {
		if strings.TrimSpace(req.RuntimeID) == "" {
			writeError(w, http.StatusBadRequest, "custom project brain runtime is required")
			return
		}
		var parsed bool
		runtimeID, parsed = parseUUIDOrBadRequest(w, req.RuntimeID, "runtime_id")
		if !parsed { return }
		var status, visibility string
		var ownerID pgtype.UUID
		if err := h.DB.QueryRow(r.Context(), `
			SELECT status, visibility, owner_id
			FROM agent_runtime
			WHERE id=$1 AND workspace_id=$2
		`, runtimeID, workspaceID).Scan(&status, &visibility, &ownerID); err != nil {
			writeError(w, http.StatusBadRequest, "selected brain runtime is not available in this workspace")
			return
		}
		if status != "online" {
			writeError(w, http.StatusConflict, "selected brain runtime must be online")
			return
		}
		if ownerID.Valid && ownerID != userUUID && visibility != "public" {
			writeError(w, http.StatusForbidden, "selected brain runtime is private to another member")
			return
		}
		model := strings.TrimSpace(req.Model)
		if model != "" {
			if catalog := h.cachedModelCatalog(r.Context(), uuidToString(runtimeID)); catalog != nil && catalog.Supported && len(catalog.Models) > 0 {
				found := false
				for _, candidate := range catalog.Models {
					if candidate.ID == model { found = true; break }
				}
				if !found {
					writeError(w, http.StatusBadRequest, "selected brain model is not available on the selected runtime")
					return
				}
			}
		}
	} else {
		req.RuntimeID, req.Model, req.ThinkingLevel, req.ServiceTier = "", "", "", ""
	}

	_, err := h.DB.Exec(r.Context(), `
		INSERT INTO autonomous_project_brain_config (
			project_id, workspace_id, enabled, runtime_mode, runtime_id, model,
			thinking_level, service_tier, learning_mode, updated_by, updated_at
		)
		VALUES ($1,$2,$3,$4,$5,NULLIF($6,''),NULLIF($7,''),NULLIF($8,''),$9,$10,now())
		ON CONFLICT (project_id) DO UPDATE
		SET enabled=EXCLUDED.enabled,
		    runtime_mode=EXCLUDED.runtime_mode,
		    runtime_id=EXCLUDED.runtime_id,
		    model=EXCLUDED.model,
		    thinking_level=EXCLUDED.thinking_level,
		    service_tier=EXCLUDED.service_tier,
		    learning_mode=EXCLUDED.learning_mode,
		    updated_by=EXCLUDED.updated_by,
		    updated_at=now()
		WHERE autonomous_project_brain_config.workspace_id=EXCLUDED.workspace_id
	`, projectID, workspaceID, req.Enabled, req.RuntimeMode, runtimeID,
		strings.TrimSpace(req.Model), strings.TrimSpace(req.ThinkingLevel),
		strings.TrimSpace(req.ServiceTier), req.LearningMode, userUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update project brain configuration")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": true})
}

func (h *Handler) PauseProjectAutonomous(w http.ResponseWriter, r *http.Request) {
	h.setProjectAutonomousPaused(w, r, true)
}

func (h *Handler) ResumeProjectAutonomous(w http.ResponseWriter, r *http.Request) {
	h.setProjectAutonomousPaused(w, r, false)
}

func (h *Handler) setProjectAutonomousPaused(w http.ResponseWriter, r *http.Request, paused bool) {
	projectID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "project id")
	if !ok {
		return
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	userUUID, ok := h.requireAutonomousControlAdmin(w, r, workspaceID)
	if !ok {
		return
	}
	if _, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{ID: projectID, WorkspaceID: workspaceID}); err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	_, err := h.DB.Exec(r.Context(), `
		INSERT INTO autonomous_project_control (
			project_id, workspace_id, paused, paused_at, paused_by, updated_at
		)
		VALUES ($1, $2, $3, CASE WHEN $3 THEN now() ELSE NULL END,
		        CASE WHEN $3 THEN $4 ELSE NULL END, now())
		ON CONFLICT (project_id) DO UPDATE
		SET paused = EXCLUDED.paused,
		    paused_at = CASE WHEN EXCLUDED.paused THEN now() ELSE NULL END,
		    paused_by = CASE WHEN EXCLUDED.paused THEN EXCLUDED.paused_by ELSE NULL END,
		    updated_at = now()
		WHERE autonomous_project_control.workspace_id = EXCLUDED.workspace_id
	`, projectID, workspaceID, paused, userUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update autonomous project control")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"paused": paused})
}

func (h *Handler) ConfirmProjectAutonomousTeam(w http.ResponseWriter, r *http.Request) {
	projectID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "project id")
	if !ok {
		return
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	userUUID, ok := h.requireAutonomousControlAdmin(w, r, workspaceID)
	if !ok {
		return
	}
	if _, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{
		ID: projectID,
		WorkspaceID: workspaceID,
	}); err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}

	var req struct {
		Assignments []struct {
			Role      string   `json:"role"`
			RuntimeID string   `json:"runtime_id"`
			Model     string   `json:"model"`
			SkillMode string   `json:"skill_mode"`
			SkillIDs  []string `json:"skill_ids"`
		} `json:"assignments"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid autonomous team configuration")
		return
	}

	var draftPlan []byte
	var draftStatus string
	if err := h.DB.QueryRow(r.Context(), `
		SELECT plan, status
		FROM autonomous_project_team_draft
		WHERE workspace_id = $1 AND project_id = $2
	`, workspaceID, projectID).Scan(&draftPlan, &draftStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "autonomous team draft not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load autonomous team draft")
		return
	}
	if draftStatus != "awaiting_configuration" {
		writeError(w, http.StatusConflict, "autonomous team draft is not awaiting configuration")
		return
	}

	var planned struct {
		Roles []struct {
			Role string `json:"role"`
		} `json:"roles"`
	}
	if err := json.Unmarshal(draftPlan, &planned); err != nil || len(planned.Roles) == 0 {
		writeError(w, http.StatusInternalServerError, "autonomous team draft is invalid")
		return
	}

	type storedAssignment struct {
		RuntimeID string   `json:"runtime_id"`
		Model     string   `json:"model,omitempty"`
		SkillMode string   `json:"skill_mode"`
		SkillIDs  []string `json:"skill_ids"`
	}
	normalized := make(map[string]storedAssignment, len(req.Assignments))
	for _, assignment := range req.Assignments {
		role := strings.TrimSpace(assignment.Role)
		if role == "" || strings.TrimSpace(assignment.RuntimeID) == "" {
			writeError(w, http.StatusBadRequest, "every autonomous role requires a runtime")
			return
		}
		if _, duplicate := normalized[role]; duplicate {
			writeError(w, http.StatusBadRequest, "duplicate autonomous role assignment")
			return
		}

		runtimeID, ok := parseUUIDOrBadRequest(w, assignment.RuntimeID, "runtime_id")
		if !ok {
			return
		}
		var runtimeStatus, runtimeVisibility string
		var runtimeOwner pgtype.UUID
		if err := h.DB.QueryRow(r.Context(), `
			SELECT status, visibility, owner_id
			FROM agent_runtime
			WHERE id = $1 AND workspace_id = $2
		`, runtimeID, workspaceID).Scan(&runtimeStatus, &runtimeVisibility, &runtimeOwner); err != nil {
			writeError(w, http.StatusBadRequest, "selected runtime is not available in this workspace")
			return
		}
		if runtimeStatus != "online" {
			writeError(w, http.StatusConflict, "selected runtime must be online")
			return
		}
		if runtimeOwner.Valid && runtimeOwner != userUUID && runtimeVisibility != "public" {
			writeError(w, http.StatusForbidden, "selected runtime is private to another member")
			return
		}

		model := strings.TrimSpace(assignment.Model)
		if model != "" {
			if catalog := h.cachedModelCatalog(r.Context(), uuidToString(runtimeID)); catalog != nil && catalog.Supported && len(catalog.Models) > 0 {
				found := false
				for _, candidate := range catalog.Models {
					if candidate.ID == model {
						found = true
						break
					}
				}
				if !found {
					writeError(w, http.StatusBadRequest, "selected model is not available on the selected runtime")
					return
				}
			}
		}

		skillMode := strings.ToLower(strings.TrimSpace(assignment.SkillMode))
		if skillMode == "" {
			if len(assignment.SkillIDs) > 0 {
				// Backward compatibility with clients that predate skill_mode:
				// a non-empty skill_ids list was an explicit custom selection.
				skillMode = "custom"
			} else {
				skillMode = "inherit"
			}
		}
		if skillMode != "inherit" && skillMode != "custom" {
			writeError(w, http.StatusBadRequest, "skill_mode must be inherit or custom")
			return
		}

		skillIDs := make([]string, 0, len(assignment.SkillIDs))
		seenSkills := make(map[string]struct{}, len(assignment.SkillIDs))
		values := assignment.SkillIDs
		if skillMode == "inherit" {
			values = nil
		}
		for _, value := range values {
			skillID, ok := parseUUIDOrBadRequest(w, value, "skill_id")
			if !ok {
				return
			}
			canonical := uuidToString(skillID)
			if _, duplicate := seenSkills[canonical]; duplicate {
				continue
			}
			seenSkills[canonical] = struct{}{}
			var exists bool
			if err := h.DB.QueryRow(r.Context(), `
				SELECT EXISTS (
					SELECT 1 FROM skill WHERE id = $1 AND workspace_id = $2
				)
			`, skillID, workspaceID).Scan(&exists); err != nil || !exists {
				writeError(w, http.StatusBadRequest, "selected skill is not available in this workspace")
				return
			}
			skillIDs = append(skillIDs, canonical)
		}
		normalized[role] = storedAssignment{
			RuntimeID: uuidToString(runtimeID),
			Model: model,
			SkillMode: skillMode,
			SkillIDs: skillIDs,
		}
	}

	for _, role := range planned.Roles {
		if _, ok := normalized[role.Role]; !ok {
			writeError(w, http.StatusBadRequest, "every planned autonomous role must be configured")
			return
		}
	}
	if len(normalized) != len(planned.Roles) {
		writeError(w, http.StatusBadRequest, "configuration contains a role that is not in the autonomous team plan")
		return
	}

	selectionsJSON, err := json.Marshal(normalized)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode autonomous team configuration")
		return
	}
	tag, err := h.DB.Exec(r.Context(), `
		UPDATE autonomous_project_team_draft
		SET selections = $3,
		    status = 'provisioning',
		    confirmed_at = now(),
		    confirmed_by = $4,
		    updated_at = now()
		WHERE workspace_id = $1
		  AND project_id = $2
		  AND status = 'awaiting_configuration'
	`, workspaceID, projectID, selectionsJSON, userUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to confirm autonomous team configuration")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusConflict, "autonomous team configuration changed; refresh and try again")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"provisioning": true})
}

func (h *Handler) ReplanProjectAutonomous(w http.ResponseWriter, r *http.Request) {
	projectID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "project id")
	if !ok {
		return
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	userUUID, ok := h.requireAutonomousControlAdmin(w, r, workspaceID)
	if !ok {
		return
	}
	if _, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{ID: projectID, WorkspaceID: workspaceID}); err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	_, err := h.DB.Exec(r.Context(), `
		INSERT INTO autonomous_project_control (
			project_id, workspace_id, replan_requested_at, replan_requested_by,
			replan_completed_at, last_error, updated_at
		)
		VALUES ($1, $2, now(), $3, NULL, NULL, now())
		ON CONFLICT (project_id) DO UPDATE
		SET replan_requested_at = now(),
		    replan_requested_by = EXCLUDED.replan_requested_by,
		    replan_completed_at = NULL,
		    last_error = NULL,
		    updated_at = now()
		WHERE autonomous_project_control.workspace_id = EXCLUDED.workspace_id
	`, projectID, workspaceID, userUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to request autonomous team replan")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"requested": true})
}

func (h *Handler) RestartProjectAutonomousWorkflow(w http.ResponseWriter, r *http.Request) {
	projectID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "project id")
	if !ok {
		return
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	if _, ok := h.requireAutonomousControlAdmin(w, r, workspaceID); !ok {
		return
	}
	if _, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{
		ID: projectID,
		WorkspaceID: workspaceID,
	}); err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	if h.AutonomousWorkflowRestart == nil {
		writeError(w, http.StatusServiceUnavailable, "autonomous workflow runtime is unavailable")
		return
	}
	if err := h.AutonomousWorkflowRestart(r.Context(), workspaceID, projectID); err != nil {
		slog.Warn("autonomous workflow restart failed",
			"workspace_id", uuidToString(workspaceID),
			"project_id", uuidToString(projectID),
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, "failed to restart autonomous project workflow")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"restarted": true})
}

func (h *Handler) RetryProjectAutonomousAction(w http.ResponseWriter, r *http.Request) {
	projectID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "project id")
	if !ok {
		return
	}
	actionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "actionId"), "action id")
	if !ok {
		return
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	if _, ok := h.requireAutonomousControlAdmin(w, r, workspaceID); !ok {
		return
	}
	tag, err := h.DB.Exec(r.Context(), `
		UPDATE autonomous_workflow_action a
		SET status = 'pending',
		    attempts = 0,
		    available_at = now(),
		    lease_token = NULL,
		    lease_expires_at = NULL,
		    last_error = NULL,
		    updated_at = now()
		FROM autonomous_workflow_run wr
		WHERE a.id = $1
		  AND a.run_id = wr.id
		  AND wr.project_id = $2
		  AND wr.workspace_id = $3
		  AND a.status = 'failed'
	`, actionID, projectID, workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to retry autonomous action")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "failed autonomous action not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"retried": true})
}

func (h *Handler) ResolveProjectAutonomousEscalation(w http.ResponseWriter, r *http.Request) {
	projectID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "project id")
	if !ok {
		return
	}
	escalationID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "escalationId"), "escalation id")
	if !ok {
		return
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	userID, ok := h.requireAutonomousControlAdmin(w, r, workspaceID)
	if !ok {
		return
	}

	var req struct {
		Decision string `json:"decision"`
		Note     string `json:"note,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid escalation resolution")
		return
	}
	req.Decision = strings.ToLower(strings.TrimSpace(req.Decision))
	if req.Decision != "approved" && req.Decision != "rejected" {
		writeError(w, http.StatusBadRequest, "decision must be approved or rejected")
		return
	}
	resolution, _ := json.Marshal(map[string]any{
		"decision": req.Decision,
		"note": strings.TrimSpace(req.Note),
		"resolved_by": uuidToString(userID),
	})
	tag, err := h.DB.Exec(r.Context(), `
		UPDATE autonomous_project_escalation
		SET status = 'resolved',
		    resolution = $4,
		    resolved_at = now()
		WHERE id = $1
		  AND workspace_id = $2
		  AND project_id = $3
		  AND category = 'approval_required'
		  AND status IN ('open', 'acknowledged')
	`, escalationID, workspaceID, projectID, resolution)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve autonomous escalation")
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "open approval escalation not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"resolved": true, "decision": req.Decision})
}
