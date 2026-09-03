package handler

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const projectReportTaskLimit = 500

type ProjectReportSummaryResponse struct {
	TaskCount                 int64   `json:"task_count"`
	CompletedTasks            int64   `json:"completed_tasks"`
	FailedTasks               int64   `json:"failed_tasks"`
	CancelledTasks            int64   `json:"cancelled_tasks"`
	ActiveTasks               int64   `json:"active_tasks"`
	ElapsedSeconds            int64   `json:"elapsed_seconds"`
	TotalRuntimeSeconds       int64   `json:"total_runtime_seconds"`
	AverageRuntimeSeconds     float64 `json:"average_runtime_seconds"`
	P95RuntimeSeconds         float64 `json:"p95_runtime_seconds"`
	AverageQueueSeconds       float64 `json:"average_queue_seconds"`
	InputTokens               int64   `json:"input_tokens"`
	OutputTokens              int64   `json:"output_tokens"`
	CacheReadTokens           int64   `json:"cache_read_tokens"`
	CacheWriteTokens          int64   `json:"cache_write_tokens"`
	TotalTokens               int64   `json:"total_tokens"`
	AuthoritativeCostUsdTicks int64   `json:"authoritative_cost_usd_ticks"`
	UsageRows                 int64   `json:"usage_rows"`
	CostedUsageRows           int64   `json:"costed_usage_rows"`
	UsageAccountedTasks       int64   `json:"usage_accounted_tasks"`
	ExecutionPlaneTokens      int64   `json:"execution_plane_tokens"`
	ExecutionPlaneRuntime     int64   `json:"execution_plane_runtime_seconds"`
	ExecutionPlaneCostTicks   int64   `json:"execution_plane_cost_usd_ticks"`
	ControlPlaneTokens        int64   `json:"control_plane_tokens"`
	ControlPlaneRuntime       int64   `json:"control_plane_runtime_seconds"`
	ControlPlaneCostTicks     int64   `json:"control_plane_cost_usd_ticks"`
	BrainLearningTokens       int64   `json:"brain_learning_tokens"`
	BrainContextTokens        int64   `json:"brain_context_tokens"`
	BrainContextEstimated     bool    `json:"brain_context_estimated"`
	ReviewRejects             int64   `json:"review_rejects"`
	ReviewCycles              int64   `json:"review_cycles"`
	ReviewedIssues            int64   `json:"reviewed_issues"`
	ApprovedIssues            int64   `json:"approved_issues"`
	FirstPassApprovedIssues   int64   `json:"first_pass_approved_issues"`
	FirstPassApprovalRate     float64 `json:"first_pass_approval_rate"`
	BlockedWorkflows          int64   `json:"blocked_workflows"`
	QualityGateTotal          int64   `json:"quality_gate_total"`
	QualityGatePassed         int64   `json:"quality_gate_passed"`
	QualityGatePassRate       float64 `json:"quality_gate_pass_rate"`
}

type ProjectReportTaskResponse struct {
	ID               string  `json:"id"`
	IssueID          *string `json:"issue_id"`
	IssueTitle       *string `json:"issue_title"`
	AgentID          string  `json:"agent_id"`
	AgentName        string  `json:"agent_name"`
	Stage            string  `json:"stage"`
	Plane            string  `json:"plane"`
	Category         string  `json:"category"`
	Status           string  `json:"status"`
	FailureReason    *string `json:"failure_reason"`
	RuntimeID        *string `json:"runtime_id"`
	RuntimeName      *string `json:"runtime_name"`
	RuntimeProvider  *string `json:"runtime_provider"`
	RuntimeMode      *string `json:"runtime_mode"`
	Models           string  `json:"models"`
	CreatedAt        string  `json:"created_at"`
	StartedAt        *string `json:"started_at"`
	CompletedAt      *string `json:"completed_at"`
	QueueSeconds     *int64  `json:"queue_seconds"`
	RuntimeSeconds   *int64  `json:"runtime_seconds"`
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	CostUsdTicks     int64   `json:"cost_usd_ticks"`
	CostComplete     bool    `json:"cost_complete"`
	ReviewRejects    int64   `json:"review_rejects"`
}

type ProjectReportAgentResponse struct {
	AgentID               string  `json:"agent_id"`
	AgentName             string  `json:"agent_name"`
	TaskCount             int64   `json:"task_count"`
	CompletedTasks        int64   `json:"completed_tasks"`
	FailedTasks           int64   `json:"failed_tasks"`
	TotalRuntimeSeconds   int64   `json:"total_runtime_seconds"`
	AverageRuntimeSeconds float64 `json:"average_runtime_seconds"`
	TotalTokens           int64   `json:"total_tokens"`
	CostUsdTicks          int64   `json:"cost_usd_ticks"`
}

type ProjectReportRuntimeResponse struct {
	Provider            string `json:"provider"`
	RuntimeName         string `json:"runtime_name"`
	RuntimeMode         string `json:"runtime_mode"`
	TaskCount           int64  `json:"task_count"`
	FailedTasks         int64  `json:"failed_tasks"`
	TotalRuntimeSeconds int64  `json:"total_runtime_seconds"`
	TotalTokens         int64  `json:"total_tokens"`
	CostUsdTicks        int64  `json:"cost_usd_ticks"`
}

type ProjectReportDayResponse struct {
	Date                string `json:"date"`
	TaskCount           int64  `json:"task_count"`
	CompletedTasks      int64  `json:"completed_tasks"`
	FailedTasks         int64  `json:"failed_tasks"`
	TotalRuntimeSeconds int64  `json:"total_runtime_seconds"`
	TotalTokens         int64  `json:"total_tokens"`
}

type ProjectReportUsageBucketResponse struct {
	Plane               string `json:"plane"`
	Category            string `json:"category"`
	TaskCount           int64  `json:"task_count"`
	CostCompleteTasks   int64  `json:"cost_complete_tasks"`
	InputTokens         int64  `json:"input_tokens"`
	OutputTokens        int64  `json:"output_tokens"`
	CacheReadTokens     int64  `json:"cache_read_tokens"`
	CacheWriteTokens    int64  `json:"cache_write_tokens"`
	TotalTokens         int64  `json:"total_tokens"`
	RuntimeSeconds      int64  `json:"runtime_seconds"`
	CostUsdTicks        int64  `json:"cost_usd_ticks"`
	BrainContextTokens  int64  `json:"brain_context_tokens"`
	BrainContextEstimated bool `json:"brain_context_estimated"`
}

type ProjectReportResponse struct {
	GeneratedAt   string                         `json:"generated_at"`
	TaskLimit     int                            `json:"task_limit"`
	TaskTruncated bool                           `json:"task_truncated"`
	Summary       ProjectReportSummaryResponse   `json:"summary"`
	Tasks         []ProjectReportTaskResponse    `json:"tasks"`
	Agents        []ProjectReportAgentResponse   `json:"agents"`
	Runtimes      []ProjectReportRuntimeResponse `json:"runtimes"`
	Daily         []ProjectReportDayResponse     `json:"daily"`
	Usage         []ProjectReportUsageBucketResponse `json:"usage"`
}

func projectReportInt64Ptr(value pgtype.Int8) *int64 {
	if !value.Valid {
		return nil
	}
	out := value.Int64
	return &out
}

func (h *Handler) GetProjectReport(w http.ResponseWriter, r *http.Request) {
	projectID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "project id")
	if !ok {
		return
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	if _, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{
		ID: projectID, WorkspaceID: workspaceID,
	}); err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}

	resp := ProjectReportResponse{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
		TaskLimit:   projectReportTaskLimit,
		Tasks:       []ProjectReportTaskResponse{},
		Agents:      []ProjectReportAgentResponse{},
		Runtimes:    []ProjectReportRuntimeResponse{},
		Daily:       []ProjectReportDayResponse{},
		Usage:       []ProjectReportUsageBucketResponse{},
	}

	const projectTasksCTE = `
		WITH project_tasks AS (
			SELECT atq.id
			FROM agent_task_queue atq
			JOIN agent a ON a.id = atq.agent_id
			LEFT JOIN issue i ON i.id = atq.issue_id
			LEFT JOIN autonomous_project_usage_accounting apu
			  ON apu.task_id = atq.id AND apu.workspace_id = $1 AND apu.project_id = $2
			WHERE a.workspace_id = $1
			  AND (i.project_id = $2 OR apu.task_id IS NOT NULL)
		)
	`

	if err := h.DB.QueryRow(r.Context(), projectTasksCTE+`
		SELECT
			COUNT(*)::bigint,
			COUNT(*) FILTER (WHERE atq.status = 'completed')::bigint,
			COUNT(*) FILTER (WHERE atq.status = 'failed')::bigint,
			COUNT(*) FILTER (WHERE atq.status = 'cancelled')::bigint,
			COUNT(*) FILTER (WHERE atq.status NOT IN ('completed','failed','cancelled'))::bigint,
			COALESCE(CASE WHEN COUNT(*) = 0 THEN 0 ELSE
				EXTRACT(EPOCH FROM (
					CASE WHEN COUNT(*) FILTER (WHERE atq.status NOT IN ('completed','failed','cancelled')) > 0
						THEN now()
						ELSE COALESCE(MAX(atq.completed_at), MAX(atq.created_at))
					END - MIN(atq.created_at)
				))::bigint END, 0)::bigint,
			COALESCE(SUM(EXTRACT(EPOCH FROM (atq.completed_at - atq.started_at)))
				FILTER (WHERE atq.started_at IS NOT NULL AND atq.completed_at IS NOT NULL), 0)::bigint,
			COALESCE(AVG(EXTRACT(EPOCH FROM (atq.completed_at - atq.started_at)))
				FILTER (WHERE atq.started_at IS NOT NULL AND atq.completed_at IS NOT NULL), 0)::double precision,
			COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (
				ORDER BY EXTRACT(EPOCH FROM (atq.completed_at - atq.started_at))
			) FILTER (WHERE atq.started_at IS NOT NULL AND atq.completed_at IS NOT NULL), 0)::double precision,
			COALESCE(AVG(EXTRACT(EPOCH FROM (atq.started_at - atq.created_at)))
				FILTER (WHERE atq.started_at IS NOT NULL), 0)::double precision
		FROM project_tasks pt
		JOIN agent_task_queue atq ON atq.id = pt.id
	`, workspaceID, projectID).Scan(
		&resp.Summary.TaskCount,
		&resp.Summary.CompletedTasks,
		&resp.Summary.FailedTasks,
		&resp.Summary.CancelledTasks,
		&resp.Summary.ActiveTasks,
		&resp.Summary.ElapsedSeconds,
		&resp.Summary.TotalRuntimeSeconds,
		&resp.Summary.AverageRuntimeSeconds,
		&resp.Summary.P95RuntimeSeconds,
		&resp.Summary.AverageQueueSeconds,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load project report task summary")
		return
	}

	if err := h.DB.QueryRow(r.Context(), projectTasksCTE+`
		SELECT
			COALESCE(SUM(tu.input_tokens), 0)::bigint,
			COALESCE(SUM(tu.output_tokens), 0)::bigint,
			COALESCE(SUM(tu.cache_read_tokens), 0)::bigint,
			COALESCE(SUM(tu.cache_write_tokens), 0)::bigint,
			COALESCE(SUM(tu.cost_usd_ticks), 0)::bigint,
			COUNT(tu.id)::bigint,
			COUNT(tu.cost_usd_ticks)::bigint
		FROM project_tasks pt
		LEFT JOIN task_usage tu ON tu.task_id = pt.id
	`, workspaceID, projectID).Scan(
		&resp.Summary.InputTokens,
		&resp.Summary.OutputTokens,
		&resp.Summary.CacheReadTokens,
		&resp.Summary.CacheWriteTokens,
		&resp.Summary.AuthoritativeCostUsdTicks,
		&resp.Summary.UsageRows,
		&resp.Summary.CostedUsageRows,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load project report usage summary")
		return
	}
	resp.Summary.TotalTokens = resp.Summary.InputTokens + resp.Summary.OutputTokens + resp.Summary.CacheReadTokens + resp.Summary.CacheWriteTokens

	usageBucketRows, err := h.DB.Query(r.Context(), `
		SELECT
			plane,
			category,
			COUNT(*)::bigint,
			COUNT(*) FILTER (WHERE cost_complete)::bigint,
			COALESCE(SUM(input_tokens), 0)::bigint,
			COALESCE(SUM(output_tokens), 0)::bigint,
			COALESCE(SUM(cache_read_tokens), 0)::bigint,
			COALESCE(SUM(cache_write_tokens), 0)::bigint,
			COALESCE(SUM(tokens), 0)::bigint,
			COALESCE(SUM(runtime_seconds), 0)::bigint,
			COALESCE(SUM(cost_usd_ticks), 0)::bigint,
			COALESCE(SUM(brain_context_tokens), 0)::bigint,
			COALESCE(BOOL_OR(brain_context_estimated), FALSE)
		FROM autonomous_project_usage_accounting
		WHERE workspace_id = $1 AND project_id = $2
		GROUP BY plane, category
		ORDER BY plane, category
	`, workspaceID, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load project report usage attribution")
		return
	}
	for usageBucketRows.Next() {
		var item ProjectReportUsageBucketResponse
		if err := usageBucketRows.Scan(
			&item.Plane, &item.Category, &item.TaskCount, &item.CostCompleteTasks,
			&item.InputTokens, &item.OutputTokens, &item.CacheReadTokens, &item.CacheWriteTokens,
			&item.TotalTokens, &item.RuntimeSeconds, &item.CostUsdTicks,
			&item.BrainContextTokens, &item.BrainContextEstimated,
		); err != nil {
			usageBucketRows.Close()
			writeError(w, http.StatusInternalServerError, "failed to decode project report usage attribution")
			return
		}
		resp.Summary.UsageAccountedTasks += item.TaskCount
		resp.Summary.BrainContextTokens += item.BrainContextTokens
		resp.Summary.BrainContextEstimated = resp.Summary.BrainContextEstimated || item.BrainContextEstimated
		if item.Category == "brain_learning" {
			resp.Summary.BrainLearningTokens += item.TotalTokens
		}
		switch item.Plane {
		case "execution":
			resp.Summary.ExecutionPlaneTokens += item.TotalTokens
			resp.Summary.ExecutionPlaneRuntime += item.RuntimeSeconds
			resp.Summary.ExecutionPlaneCostTicks += item.CostUsdTicks
		case "control":
			resp.Summary.ControlPlaneTokens += item.TotalTokens
			resp.Summary.ControlPlaneRuntime += item.RuntimeSeconds
			resp.Summary.ControlPlaneCostTicks += item.CostUsdTicks
		}
		resp.Usage = append(resp.Usage, item)
	}
	if err := usageBucketRows.Err(); err != nil {
		usageBucketRows.Close()
		writeError(w, http.StatusInternalServerError, "failed to load project report usage attribution")
		return
	}
	usageBucketRows.Close()

	if err := h.DB.QueryRow(r.Context(), `
		WITH per_run AS (
			SELECT wr.id, wr.state, wr.review_cycles,
				COUNT(pe.event_id) FILTER (WHERE pe.event_type LIKE 'review.%')::bigint AS review_events,
				COUNT(pe.event_id) FILTER (WHERE pe.event_type = 'review.completed')::bigint AS approvals,
				COUNT(pe.event_id) FILTER (WHERE pe.event_type = 'review.changes_requested')::bigint AS rejects
			FROM autonomous_workflow_run wr
			LEFT JOIN autonomous_workflow_processed_event pe ON pe.run_id = wr.id
			WHERE wr.workspace_id = $1 AND wr.project_id = $2
			GROUP BY wr.id, wr.state, wr.review_cycles
		)
		SELECT
			COALESCE(SUM(rejects), 0)::bigint,
			COALESCE(SUM(review_cycles), 0)::bigint,
			COUNT(*) FILTER (WHERE review_events > 0)::bigint,
			COUNT(*) FILTER (WHERE approvals > 0)::bigint,
			COUNT(*) FILTER (WHERE approvals > 0 AND rejects = 0)::bigint,
			COUNT(*) FILTER (WHERE state = 'blocked')::bigint
		FROM per_run
	`, workspaceID, projectID).Scan(
		&resp.Summary.ReviewRejects,
		&resp.Summary.ReviewCycles,
		&resp.Summary.ReviewedIssues,
		&resp.Summary.ApprovedIssues,
		&resp.Summary.FirstPassApprovedIssues,
		&resp.Summary.BlockedWorkflows,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load project report review summary")
		return
	}
	if resp.Summary.ApprovedIssues > 0 {
		resp.Summary.FirstPassApprovalRate = float64(resp.Summary.FirstPassApprovedIssues) / float64(resp.Summary.ApprovedIssues)
	}

	if err := h.DB.QueryRow(r.Context(), `
		SELECT
			COUNT(*) FILTER (WHERE required)::bigint,
			COUNT(*) FILTER (WHERE required AND status = 'passed')::bigint
		FROM autonomous_project_quality_gate_run
		WHERE workspace_id = $1 AND project_id = $2
	`, workspaceID, projectID).Scan(
		&resp.Summary.QualityGateTotal,
		&resp.Summary.QualityGatePassed,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load project report quality summary")
		return
	}
	if resp.Summary.QualityGateTotal > 0 {
		resp.Summary.QualityGatePassRate = float64(resp.Summary.QualityGatePassed) / float64(resp.Summary.QualityGateTotal)
	}

	taskRows, err := h.DB.Query(r.Context(), projectTasksCTE+`
		, usage_by_task AS (
			SELECT tu.task_id,
				COALESCE(SUM(tu.input_tokens), 0)::bigint AS input_tokens,
				COALESCE(SUM(tu.output_tokens), 0)::bigint AS output_tokens,
				COALESCE(SUM(tu.cache_read_tokens), 0)::bigint AS cache_read_tokens,
				COALESCE(SUM(tu.cache_write_tokens), 0)::bigint AS cache_write_tokens,
				COALESCE(SUM(tu.cost_usd_ticks), 0)::bigint AS cost_usd_ticks,
				COUNT(*)::bigint AS usage_rows,
				COUNT(tu.cost_usd_ticks)::bigint AS cost_rows,
				STRING_AGG(DISTINCT NULLIF(tu.model, ''), ', ') AS models
			FROM task_usage tu
			JOIN project_tasks pt ON pt.id = tu.task_id
			GROUP BY tu.task_id
		), workflow_roles AS (
			SELECT DISTINCT ON (issue_id)
				issue_id, owner_agent_id, reviewer_agent_id
			FROM autonomous_workflow_run
			WHERE workspace_id = $1 AND project_id = $2
			ORDER BY issue_id, updated_at DESC
		), review_rejects AS (
			SELECT wr.issue_id, COUNT(*)::bigint AS rejects
			FROM autonomous_workflow_processed_event pe
			JOIN autonomous_workflow_run wr ON wr.id = pe.run_id
			WHERE wr.workspace_id = $1 AND wr.project_id = $2
			  AND pe.event_type = 'review.changes_requested'
			GROUP BY wr.issue_id
		)
		SELECT
			atq.id, atq.issue_id, i.title, atq.agent_id, a.name,
			CASE
				WHEN atq.issue_id IS NULL THEN 'control_plane'
				WHEN wr.reviewer_agent_id = atq.agent_id THEN 'review'
				WHEN wr.owner_agent_id = atq.agent_id THEN 'implementation'
				ELSE 'project'
			END AS stage,
			COALESCE(apu.plane, CASE WHEN atq.issue_id IS NULL THEN 'control' ELSE 'execution' END) AS plane,
			COALESCE(apu.category,
				CASE
					WHEN atq.issue_id IS NULL THEN 'unattributed_control'
					WHEN wr.reviewer_agent_id = atq.agent_id THEN 'review'
					ELSE 'execution'
				END
			) AS category,
			atq.status, atq.failure_reason, atq.runtime_id,
			COALESCE(NULLIF(rt.custom_name, ''), NULLIF(rt.name, '')) AS runtime_name,
			rt.provider, rt.runtime_mode,
			COALESCE(u.models, ''), atq.created_at, atq.started_at, atq.completed_at,
			CASE WHEN atq.started_at IS NOT NULL
				THEN GREATEST(EXTRACT(EPOCH FROM (atq.started_at - atq.created_at))::bigint, 0)
			END AS queue_seconds,
			CASE WHEN atq.started_at IS NOT NULL AND atq.completed_at IS NOT NULL
				THEN GREATEST(EXTRACT(EPOCH FROM (atq.completed_at - atq.started_at))::bigint, 0)
			END AS runtime_seconds,
			COALESCE(u.input_tokens, 0)::bigint,
			COALESCE(u.output_tokens, 0)::bigint,
			COALESCE(u.cache_read_tokens, 0)::bigint,
			COALESCE(u.cache_write_tokens, 0)::bigint,
			COALESCE(u.cost_usd_ticks, 0)::bigint,
			COALESCE(u.usage_rows, 0)::bigint,
			COALESCE(u.cost_rows, 0)::bigint,
			COALESCE(rr.rejects, 0)::bigint
		FROM project_tasks pt
		JOIN agent_task_queue atq ON atq.id = pt.id
		JOIN agent a ON a.id = atq.agent_id
		LEFT JOIN issue i ON i.id = atq.issue_id
		LEFT JOIN agent_runtime rt ON rt.id = atq.runtime_id
		LEFT JOIN usage_by_task u ON u.task_id = atq.id
		LEFT JOIN workflow_roles wr ON wr.issue_id = atq.issue_id
		LEFT JOIN review_rejects rr ON rr.issue_id = atq.issue_id
		LEFT JOIN autonomous_project_usage_accounting apu
		  ON apu.task_id = atq.id AND apu.workspace_id = $1 AND apu.project_id = $2
		ORDER BY COALESCE(atq.completed_at, atq.started_at, atq.created_at) DESC
		LIMIT $3
	`, workspaceID, projectID, projectReportTaskLimit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load project report task log")
		return
	}
	for taskRows.Next() {
		var item ProjectReportTaskResponse
		var taskID, issueID, agentID, runtimeID pgtype.UUID
		var issueTitle, failureReason, runtimeName, runtimeProvider, runtimeMode pgtype.Text
		var createdAt time.Time
		var startedAt, completedAt pgtype.Timestamptz
		var queueSeconds, runtimeSeconds pgtype.Int8
		var usageRows, costRows int64
		if err := taskRows.Scan(
			&taskID, &issueID, &issueTitle, &agentID, &item.AgentName,
			&item.Stage, &item.Plane, &item.Category, &item.Status, &failureReason, &runtimeID,
			&runtimeName, &runtimeProvider, &runtimeMode,
			&item.Models, &createdAt, &startedAt, &completedAt,
			&queueSeconds, &runtimeSeconds,
			&item.InputTokens, &item.OutputTokens, &item.CacheReadTokens, &item.CacheWriteTokens,
			&item.CostUsdTicks, &usageRows, &costRows, &item.ReviewRejects,
		); err != nil {
			taskRows.Close()
			writeError(w, http.StatusInternalServerError, "failed to decode project report task log")
			return
		}
		item.ID = uuidToString(taskID)
		item.IssueID = nullableUUIDString(issueID)
		item.IssueTitle = nullableTextString(issueTitle)
		item.AgentID = uuidToString(agentID)
		item.FailureReason = nullableTextString(failureReason)
		item.RuntimeID = nullableUUIDString(runtimeID)
		item.RuntimeName = nullableTextString(runtimeName)
		item.RuntimeProvider = nullableTextString(runtimeProvider)
		item.RuntimeMode = nullableTextString(runtimeMode)
		item.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
		item.StartedAt = nullableTimestampString(startedAt)
		item.CompletedAt = nullableTimestampString(completedAt)
		item.QueueSeconds = projectReportInt64Ptr(queueSeconds)
		item.RuntimeSeconds = projectReportInt64Ptr(runtimeSeconds)
		item.TotalTokens = item.InputTokens + item.OutputTokens + item.CacheReadTokens + item.CacheWriteTokens
		item.CostComplete = usageRows > 0 && costRows == usageRows
		resp.Tasks = append(resp.Tasks, item)
	}
	if err := taskRows.Err(); err != nil {
		taskRows.Close()
		writeError(w, http.StatusInternalServerError, "failed to load project report task log")
		return
	}
	taskRows.Close()
	resp.TaskTruncated = resp.Summary.TaskCount > int64(len(resp.Tasks))

	agentRows, err := h.DB.Query(r.Context(), projectTasksCTE+`
		, usage_by_task AS (
			SELECT tu.task_id,
				COALESCE(SUM(tu.input_tokens + tu.output_tokens + tu.cache_read_tokens + tu.cache_write_tokens), 0)::bigint AS tokens,
				COALESCE(SUM(tu.cost_usd_ticks), 0)::bigint AS cost_usd_ticks
			FROM task_usage tu
			JOIN project_tasks pt ON pt.id = tu.task_id
			GROUP BY tu.task_id
		)
		SELECT atq.agent_id, a.name,
			COUNT(*)::bigint,
			COUNT(*) FILTER (WHERE atq.status = 'completed')::bigint,
			COUNT(*) FILTER (WHERE atq.status = 'failed')::bigint,
			COALESCE(SUM(EXTRACT(EPOCH FROM (atq.completed_at - atq.started_at)))
				FILTER (WHERE atq.started_at IS NOT NULL AND atq.completed_at IS NOT NULL), 0)::bigint,
			COALESCE(AVG(EXTRACT(EPOCH FROM (atq.completed_at - atq.started_at)))
				FILTER (WHERE atq.started_at IS NOT NULL AND atq.completed_at IS NOT NULL), 0)::double precision,
			COALESCE(SUM(u.tokens), 0)::bigint,
			COALESCE(SUM(u.cost_usd_ticks), 0)::bigint
		FROM project_tasks pt
		JOIN agent_task_queue atq ON atq.id = pt.id
		JOIN agent a ON a.id = atq.agent_id
		LEFT JOIN usage_by_task u ON u.task_id = atq.id
		GROUP BY atq.agent_id, a.name
		ORDER BY COUNT(*) DESC, a.name ASC
	`, workspaceID, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load project report agent metrics")
		return
	}
	for agentRows.Next() {
		var item ProjectReportAgentResponse
		var agentID pgtype.UUID
		if err := agentRows.Scan(
			&agentID, &item.AgentName, &item.TaskCount, &item.CompletedTasks, &item.FailedTasks,
			&item.TotalRuntimeSeconds, &item.AverageRuntimeSeconds, &item.TotalTokens, &item.CostUsdTicks,
		); err != nil {
			agentRows.Close()
			writeError(w, http.StatusInternalServerError, "failed to decode project report agent metrics")
			return
		}
		item.AgentID = uuidToString(agentID)
		resp.Agents = append(resp.Agents, item)
	}
	if err := agentRows.Err(); err != nil {
		agentRows.Close()
		writeError(w, http.StatusInternalServerError, "failed to load project report agent metrics")
		return
	}
	agentRows.Close()

	runtimeRows, err := h.DB.Query(r.Context(), projectTasksCTE+`
		, usage_by_task AS (
			SELECT tu.task_id,
				COALESCE(SUM(tu.input_tokens + tu.output_tokens + tu.cache_read_tokens + tu.cache_write_tokens), 0)::bigint AS tokens,
				COALESCE(SUM(tu.cost_usd_ticks), 0)::bigint AS cost_usd_ticks
			FROM task_usage tu
			JOIN project_tasks pt ON pt.id = tu.task_id
			GROUP BY tu.task_id
		)
		SELECT
			COALESCE(NULLIF(rt.provider, ''), 'unassigned') AS provider,
			COALESCE(NULLIF(rt.custom_name, ''), NULLIF(rt.name, ''), 'No runtime') AS runtime_name,
			COALESCE(NULLIF(rt.runtime_mode, ''), 'unknown') AS runtime_mode,
			COUNT(*)::bigint,
			COUNT(*) FILTER (WHERE atq.status = 'failed')::bigint,
			COALESCE(SUM(EXTRACT(EPOCH FROM (atq.completed_at - atq.started_at)))
				FILTER (WHERE atq.started_at IS NOT NULL AND atq.completed_at IS NOT NULL), 0)::bigint,
			COALESCE(SUM(u.tokens), 0)::bigint,
			COALESCE(SUM(u.cost_usd_ticks), 0)::bigint
		FROM project_tasks pt
		JOIN agent_task_queue atq ON atq.id = pt.id
		LEFT JOIN agent_runtime rt ON rt.id = atq.runtime_id
		LEFT JOIN usage_by_task u ON u.task_id = atq.id
		GROUP BY 1, 2, 3
		ORDER BY COUNT(*) DESC, 1, 2
	`, workspaceID, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load project report runtime metrics")
		return
	}
	for runtimeRows.Next() {
		var item ProjectReportRuntimeResponse
		if err := runtimeRows.Scan(
			&item.Provider, &item.RuntimeName, &item.RuntimeMode, &item.TaskCount, &item.FailedTasks,
			&item.TotalRuntimeSeconds, &item.TotalTokens, &item.CostUsdTicks,
		); err != nil {
			runtimeRows.Close()
			writeError(w, http.StatusInternalServerError, "failed to decode project report runtime metrics")
			return
		}
		resp.Runtimes = append(resp.Runtimes, item)
	}
	if err := runtimeRows.Err(); err != nil {
		runtimeRows.Close()
		writeError(w, http.StatusInternalServerError, "failed to load project report runtime metrics")
		return
	}
	runtimeRows.Close()

	dayRows, err := h.DB.Query(r.Context(), projectTasksCTE+`
		, usage_by_task AS (
			SELECT tu.task_id,
				COALESCE(SUM(tu.input_tokens + tu.output_tokens + tu.cache_read_tokens + tu.cache_write_tokens), 0)::bigint AS tokens
			FROM task_usage tu
			JOIN project_tasks pt ON pt.id = tu.task_id
			GROUP BY tu.task_id
		)
		SELECT
			DATE(COALESCE(atq.completed_at, atq.started_at, atq.created_at)) AS day,
			COUNT(*)::bigint,
			COUNT(*) FILTER (WHERE atq.status = 'completed')::bigint,
			COUNT(*) FILTER (WHERE atq.status = 'failed')::bigint,
			COALESCE(SUM(EXTRACT(EPOCH FROM (atq.completed_at - atq.started_at)))
				FILTER (WHERE atq.started_at IS NOT NULL AND atq.completed_at IS NOT NULL), 0)::bigint,
			COALESCE(SUM(u.tokens), 0)::bigint
		FROM project_tasks pt
		JOIN agent_task_queue atq ON atq.id = pt.id
		LEFT JOIN usage_by_task u ON u.task_id = atq.id
		GROUP BY 1
		ORDER BY 1 DESC
		LIMIT 60
	`, workspaceID, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load project report daily metrics")
		return
	}
	for dayRows.Next() {
		var item ProjectReportDayResponse
		var day pgtype.Date
		if err := dayRows.Scan(
			&day, &item.TaskCount, &item.CompletedTasks, &item.FailedTasks,
			&item.TotalRuntimeSeconds, &item.TotalTokens,
		); err != nil {
			dayRows.Close()
			writeError(w, http.StatusInternalServerError, "failed to decode project report daily metrics")
			return
		}
		if day.Valid {
			item.Date = day.Time.Format("2006-01-02")
		}
		resp.Daily = append(resp.Daily, item)
	}
	if err := dayRows.Err(); err != nil {
		dayRows.Close()
		writeError(w, http.StatusInternalServerError, "failed to load project report daily metrics")
		return
	}
	dayRows.Close()

	writeJSON(w, http.StatusOK, resp)
}
