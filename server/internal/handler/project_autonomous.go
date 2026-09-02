package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type AutonomousControlResponse struct {
	Paused              bool    `json:"paused"`
	PausedAt            *string `json:"paused_at"`
	ReplanRequestedAt   *string `json:"replan_requested_at"`
	ReplanCompletedAt   *string `json:"replan_completed_at"`
	LastError           *string `json:"last_error"`
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
}

type AutonomousProjectResponse struct {
	Enabled   bool                            `json:"enabled"`
	Control   AutonomousControlResponse       `json:"control"`
	Health    AutonomousProjectHealthResponse `json:"health"`
	Team      *AutonomousTeamResponse         `json:"team"`
	Workflows []AutonomousWorkflowResponse    `json:"workflows"`
	Actions   []AutonomousActionResponse      `json:"actions"`
	Activity  []AutonomousActivityResponse    `json:"activity"`
	Decisions []AutonomousDecisionResponse    `json:"decisions"`
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
		Workflows: []AutonomousWorkflowResponse{},
		Actions:   []AutonomousActionResponse{},
		Activity:  []AutonomousActivityResponse{},
		Decisions: []AutonomousDecisionResponse{},
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

	switch {
	case resp.Control.Paused:
		resp.Health.Status = "paused"
	case resp.Health.Blocked > 0 || totalFailed > 0:
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
			var taskID, agentID, issueID pgtype.UUID
			var agentName, issueTitle, status string
			var createdAt time.Time
			var completedAt pgtype.Timestamptz
			if taskRows.Scan(&taskID, &agentID, &agentName, &issueID, &issueTitle, &status, &createdAt, &completedAt) == nil {
				at := createdAt
				if completedAt.Valid {
					at = completedAt.Time
				}
				taskIDString := uuidToString(taskID)
				agentIDString := uuidToString(agentID)
				issueIDString := uuidToString(issueID)
				sortable = append(sortable, autonomousActivitySortable{
					At: at,
					Item: AutonomousActivityResponse{
						ID: "task:" + taskIDString + ":" + status,
						Type: "task." + status,
						Title: agentName + " · " + status,
						Detail: issueTitle,
						IssueID: &issueIDString,
						AgentID: &agentIDString,
						Metadata: map[string]any{"task_id": taskIDString},
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
