package workflowruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const handoffSchemaVersion = 1

type handoffArtifact struct {
	Type        string `json:"type"`
	Ref         string `json:"ref"`
	Description string `json:"description"`
}

type handoffTest struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Evidence string `json:"evidence"`
}

type handoffFinding struct {
	ID          string `json:"id"`
	Severity    string `json:"severity"`
	Category    string `json:"category"`
	Description string `json:"description"`
	Evidence    string `json:"evidence"`
	Blocking    bool   `json:"blocking"`
	Lifecycle   string `json:"lifecycle,omitempty"`
}

type handoffBrainReference struct {
	CanonicalKey string `json:"canonical_key"`
	Type         string `json:"type"`
	Subject      string `json:"subject"`
	Content      string `json:"content"`
}

type handoffSource struct {
	NodeID  string `json:"node_id,omitempty"`
	NodeKey string `json:"node_key,omitempty"`
	TaskID  string `json:"task_id,omitempty"`
	AgentID string `json:"agent_id,omitempty"`
}

type implementationHandoffOutput struct {
	Summary      string            `json:"summary"`
	Decisions    []string          `json:"decisions"`
	Artifacts    []handoffArtifact `json:"artifacts"`
	ChangedFiles []string          `json:"changed_files"`
	CommitSHA    string            `json:"commit_sha"`
	Diff         string            `json:"diff"`
	Tests        []handoffTest     `json:"tests"`
	Findings     []handoffFinding  `json:"findings"`
	Risks        []string          `json:"risks"`
	Blockers     []string          `json:"blockers"`
}

type reviewVerdictOutput struct {
	Verdict  string           `json:"verdict"`
	Summary  string           `json:"summary"`
	Findings []handoffFinding `json:"findings"`
}

type durableHandoffEnvelope struct {
	SchemaVersion   int                     `json:"schema_version"`
	Kind            string                  `json:"kind"`
	Source          handoffSource           `json:"source"`
	Summary         string                  `json:"summary"`
	Instructions    string                  `json:"instructions,omitempty"`
	Decisions       []string                `json:"decisions"`
	Artifacts       []handoffArtifact       `json:"artifacts"`
	ChangedFiles    []string                `json:"changed_files"`
	CommitSHA       string                  `json:"commit_sha"`
	Diff            string                  `json:"diff"`
	Tests           []handoffTest           `json:"tests"`
	Findings        []handoffFinding        `json:"findings"`
	Risks           []string                `json:"risks"`
	Blockers        []string                `json:"blockers"`
	BrainReferences []handoffBrainReference `json:"brain_references"`
}

func contractTaskOutput(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", errors.New("task result is empty")
	}
	var wrapper struct {
		Output string `json:"output"`
	}
	if json.Unmarshal(raw, &wrapper) == nil && strings.TrimSpace(wrapper.Output) != "" {
		return strings.TrimSpace(wrapper.Output), nil
	}
	value := strings.TrimSpace(string(raw))
	if strings.HasPrefix(value, "{") && strings.HasSuffix(value, "}") {
		return value, nil
	}
	return "", errors.New("task result does not contain a structured output")
}

func normalizeContractJSONObject(value string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "```") {
		lines := strings.Split(value, "\n")
		if len(lines) < 3 || strings.TrimSpace(lines[len(lines)-1]) != "```" {
			return "", errors.New("structured output has an incomplete markdown fence")
		}
		first := strings.ToLower(strings.TrimSpace(lines[0]))
		if first != "```" && first != "```json" {
			return "", errors.New("structured output uses an unsupported markdown fence")
		}
		value = strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
	}
	if !strings.HasPrefix(value, "{") || !strings.HasSuffix(value, "}") {
		return "", errors.New("structured output must be exactly one JSON object")
	}
	return value, nil
}

func decodeStrictContract(value string, target any) error {
	normalized, err := normalizeContractJSONObject(value)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewBufferString(normalized))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("structured output contains multiple JSON values")
		}
		return err
	}
	return nil
}

func parseImplementationHandoff(raw json.RawMessage) (implementationHandoffOutput, error) {
	text, err := contractTaskOutput(raw)
	if err != nil {
		return implementationHandoffOutput{}, err
	}
	var out implementationHandoffOutput
	if err := decodeStrictContract(text, &out); err != nil {
		return implementationHandoffOutput{}, fmt.Errorf("decode implementation handoff: %w", err)
	}
	if strings.TrimSpace(out.Summary) == "" {
		return implementationHandoffOutput{}, errors.New("implementation handoff summary is required")
	}
	if len(out.Decisions) > 100 || len(out.Artifacts) > 100 || len(out.ChangedFiles) > 500 ||
		len(out.Tests) > 200 || len(out.Findings) > 100 || len(out.Risks) > 100 || len(out.Blockers) > 100 {
		return implementationHandoffOutput{}, errors.New("implementation handoff exceeds collection limits")
	}
	for _, test := range out.Tests {
		switch test.Status {
		case "passed", "failed", "skipped", "not_run":
		default:
			return implementationHandoffOutput{}, fmt.Errorf("unsupported test status %q", test.Status)
		}
	}
	for _, finding := range out.Findings {
		if err := validateFinding(finding); err != nil {
			return implementationHandoffOutput{}, err
		}
	}
	return out, nil
}

func parseReviewVerdict(raw json.RawMessage) (reviewVerdictOutput, error) {
	text, err := contractTaskOutput(raw)
	if err != nil {
		return reviewVerdictOutput{}, err
	}
	var out reviewVerdictOutput
	if err := decodeStrictContract(text, &out); err != nil {
		return reviewVerdictOutput{}, fmt.Errorf("decode review verdict: %w", err)
	}
	if strings.TrimSpace(out.Summary) == "" {
		return reviewVerdictOutput{}, errors.New("review verdict summary is required")
	}
	if len(out.Findings) > 100 {
		return reviewVerdictOutput{}, errors.New("review verdict has too many findings")
	}
	hasBlocking := false
	for _, finding := range out.Findings {
		if err := validateFinding(finding); err != nil {
			return reviewVerdictOutput{}, err
		}
		hasBlocking = hasBlocking || finding.Blocking
	}
	switch out.Verdict {
	case "approved":
		if hasBlocking {
			return reviewVerdictOutput{}, errors.New("approved verdict cannot contain blocking findings")
		}
	case "changes_requested":
		if !hasBlocking {
			return reviewVerdictOutput{}, errors.New("changes_requested verdict requires at least one blocking finding")
		}
	default:
		return reviewVerdictOutput{}, fmt.Errorf("unsupported review verdict %q", out.Verdict)
	}
	return out, nil
}

func validateFinding(finding handoffFinding) error {
	if strings.TrimSpace(finding.ID) == "" || len(finding.ID) > 120 {
		return errors.New("finding id is required and must be <= 120 characters")
	}
	switch finding.Severity {
	case "low", "medium", "high", "critical":
	default:
		return fmt.Errorf("finding %q has unsupported severity %q", finding.ID, finding.Severity)
	}
	if !isLowerSnakeCase(finding.Category, 64) {
		return fmt.Errorf("finding %q category must be lower_snake_case", finding.ID)
	}
	if strings.TrimSpace(finding.Description) == "" {
		return fmt.Errorf("finding %q description is required", finding.ID)
	}
	if strings.TrimSpace(finding.Evidence) == "" {
		return fmt.Errorf("finding %q evidence is required", finding.ID)
	}
	return nil
}

func isLowerSnakeCase(value string, limit int) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > limit {
		return false
	}
	for i, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9' && i > 0) || (r == '_' && i > 0) {
			continue
		}
		return false
	}
	return true
}

func estimateInjectedTokens(value string) int64 {
	runes := utf8.RuneCountInString(value)
	if runes == 0 {
		return 0
	}
	// Provider CLIs do not expose per-prompt-segment token counts. Keep the
	// attribution explicit and marked estimated rather than pretending the
	// aggregate provider input_tokens can identify the Brain slice exactly.
	return int64((runes + 3) / 4)
}

func workflowRunUUID(run workflow.Run) pgtype.UUID {
	if strings.TrimSpace(run.ID) == "" {
		return pgtype.UUID{}
	}
	id, err := util.ParseUUID(run.ID)
	if err != nil {
		return pgtype.UUID{}
	}
	return id
}

func (r *Runtime) projectNodeSource(ctx context.Context, issue db.Issue) (string, string, error) {
	if !issue.ProjectID.Valid {
		return "", "", nil
	}
	var nodeID pgtype.UUID
	var nodeKey string
	err := r.pool.QueryRow(ctx, `
		SELECT id, node_key
		FROM autonomous_project_plan_node
		WHERE workspace_id = $1 AND project_id = $2 AND materialized_issue_id = $3
		ORDER BY updated_at DESC
		LIMIT 1
	`, issue.WorkspaceID, issue.ProjectID, issue.ID).Scan(&nodeID, &nodeKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	return util.UUIDToString(nodeID), nodeKey, nil
}

func (r *Runtime) assignmentBrainReferences(ctx context.Context, issue db.Issue) ([]handoffBrainReference, int64, bool, error) {
	if r.projectStore == nil || !issue.ProjectID.Valid {
		return []handoffBrainReference{}, 0, false, nil
	}
	memories, err := r.projectStore.RecallMemories(ctx, issue.WorkspaceID, issue.ProjectID, issue.Title, 12, 12000)
	if err != nil {
		return nil, 0, false, err
	}
	refs := make([]handoffBrainReference, 0, len(memories))
	for _, memory := range memories {
		refs = append(refs, handoffBrainReference{
			CanonicalKey: memory.CanonicalKey,
			Type:         memory.Type,
			Subject:      memory.Subject,
			Content:      memory.Content,
		})
	}
	if len(refs) == 0 {
		return refs, 0, false, nil
	}
	raw, err := json.Marshal(refs)
	if err != nil {
		return nil, 0, false, err
	}
	return refs, estimateInjectedTokens(string(raw)), true, nil
}

func (r *Runtime) latestImplementationHandoff(ctx context.Context, issue db.Issue) (durableHandoffEnvelope, bool, error) {
	var raw []byte
	err := r.pool.QueryRow(ctx, `
		SELECT envelope
		FROM autonomous_project_handoff
		WHERE workspace_id = $1 AND project_id = $2 AND issue_id = $3
		  AND handoff_kind = 'implementation_result'
		ORDER BY created_at DESC
		LIMIT 1
	`, issue.WorkspaceID, issue.ProjectID, issue.ID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return durableHandoffEnvelope{}, false, nil
	}
	if err != nil {
		return durableHandoffEnvelope{}, false, err
	}
	var out durableHandoffEnvelope
	if err := json.Unmarshal(raw, &out); err != nil {
		return durableHandoffEnvelope{}, false, err
	}
	return out, true, nil
}

func (r *Runtime) unresolvedReviewFindings(ctx context.Context, issue db.Issue) ([]handoffFinding, handoffSource, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT f.finding_key, f.severity, f.category, f.description, f.evidence, f.blocking,
		       v.review_task_id, v.reviewer_agent_id
		FROM autonomous_project_review_finding f
		JOIN autonomous_project_review_verdict v ON v.id = f.verdict_id
		WHERE f.workspace_id = $1 AND f.project_id = $2 AND f.issue_id = $3
		  AND f.lifecycle_status = 'open'
		ORDER BY f.blocking DESC, f.created_at ASC, f.finding_key ASC
	`, issue.WorkspaceID, issue.ProjectID, issue.ID)
	if err != nil {
		return nil, handoffSource{}, err
	}
	defer rows.Close()
	findings := []handoffFinding{}
	source := handoffSource{}
	for rows.Next() {
		var finding handoffFinding
		var reviewTaskID, reviewerAgentID pgtype.UUID
		if err := rows.Scan(
			&finding.ID, &finding.Severity, &finding.Category, &finding.Description,
			&finding.Evidence, &finding.Blocking, &reviewTaskID, &reviewerAgentID,
		); err != nil {
			return nil, handoffSource{}, err
		}
		finding.Lifecycle = "open"
		findings = append(findings, finding)
		source.TaskID = util.UUIDToString(reviewTaskID)
		source.AgentID = util.UUIDToString(reviewerAgentID)
	}
	return findings, source, rows.Err()
}

func (r *Runtime) prepareAssignmentHandoff(
	ctx context.Context,
	run workflow.Run,
	pending workflow.PendingAction,
	issue db.Issue,
	selector string,
	targetAgentID pgtype.UUID,
) (durableHandoffEnvelope, error) {
	actionID, err := util.ParseUUID(pending.ID)
	if err != nil {
		return durableHandoffEnvelope{}, err
	}
	var existingRaw []byte
	err = r.pool.QueryRow(ctx, `
		SELECT envelope
		FROM autonomous_project_handoff
		WHERE workflow_action_id = $1
	`, actionID).Scan(&existingRaw)
	if err == nil {
		var existing durableHandoffEnvelope
		if err := json.Unmarshal(existingRaw, &existing); err != nil {
			return durableHandoffEnvelope{}, err
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return durableHandoffEnvelope{}, err
	}

	nodeID, nodeKey, err := r.projectNodeSource(ctx, issue)
	if err != nil {
		return durableHandoffEnvelope{}, err
	}
	instructions := strings.TrimSpace(pending.Action.Params["instructions"])
	if instructions == "" {
		instructions = strings.TrimSpace(pending.Action.Params["handoff"])
	}
	envelope := durableHandoffEnvelope{
		SchemaVersion: handoffSchemaVersion,
		Kind:          "implementation_assignment",
		Source:        handoffSource{NodeID: nodeID, NodeKey: nodeKey},
		Summary:       "Autonomous implementation assignment",
		Instructions:  instructions,
		Decisions:     []string{},
		Artifacts:     []handoffArtifact{},
		ChangedFiles:  []string{},
		Tests:         []handoffTest{},
		Findings:      []handoffFinding{},
		Risks:         []string{},
		Blockers:      []string{},
	}

	latest, hasLatest, err := r.latestImplementationHandoff(ctx, issue)
	if err != nil {
		return durableHandoffEnvelope{}, err
	}
	if selector == "reviewer" {
		envelope.Kind = "review_assignment"
		envelope.Summary = "Review the latest implementation handoff"
		if hasLatest {
			envelope.Source = latest.Source
			envelope.Summary = latest.Summary
			envelope.Decisions = latest.Decisions
			envelope.Artifacts = latest.Artifacts
			envelope.ChangedFiles = latest.ChangedFiles
			envelope.CommitSHA = latest.CommitSHA
			envelope.Diff = latest.Diff
			envelope.Tests = latest.Tests
			envelope.Findings = latest.Findings
			envelope.Risks = latest.Risks
			envelope.Blockers = latest.Blockers
		}
	} else {
		findings, reviewSource, err := r.unresolvedReviewFindings(ctx, issue)
		if err != nil {
			return durableHandoffEnvelope{}, err
		}
		if len(findings) > 0 {
			envelope.Kind = "review_to_implementation"
			envelope.Summary = fmt.Sprintf("Resolve %d unresolved review finding(s)", len(findings))
			envelope.Findings = findings
			reviewSource.NodeID = nodeID
			reviewSource.NodeKey = nodeKey
			envelope.Source = reviewSource
			if hasLatest {
				envelope.Artifacts = latest.Artifacts
				envelope.ChangedFiles = latest.ChangedFiles
				envelope.CommitSHA = latest.CommitSHA
				envelope.Tests = latest.Tests
			}
		}
	}

	brainRefs, brainTokens, brainEstimated, err := r.assignmentBrainReferences(ctx, issue)
	if err != nil {
		return durableHandoffEnvelope{}, err
	}
	envelope.BrainReferences = brainRefs
	raw, err := json.Marshal(envelope)
	if err != nil {
		return durableHandoffEnvelope{}, err
	}
	runID := workflowRunUUID(run)
	_, err = r.pool.Exec(ctx, `
		INSERT INTO autonomous_project_handoff (
			workspace_id, project_id, issue_id, workflow_run_id, workflow_action_id,
			source_node_id, source_node_key, source_task_id, source_agent_id,
			target_agent_id, handoff_kind, schema_version, summary, envelope,
			brain_context_tokens, brain_context_estimated
		)
		VALUES (
			$1,$2,$3,$4,$5,
			NULLIF($6,'')::uuid,NULLIF($7,''),NULLIF($8,'')::uuid,NULLIF($9,'')::uuid,
			$10,$11,$12,$13,$14,$15,$16
		)
		ON CONFLICT (workflow_action_id) DO NOTHING
	`, issue.WorkspaceID, issue.ProjectID, issue.ID, nullableUUID(runID), actionID,
		nodeID, nodeKey, envelope.Source.TaskID, envelope.Source.AgentID,
		targetAgentID, envelope.Kind, handoffSchemaVersion, envelope.Summary, raw,
		brainTokens, brainEstimated)
	if err != nil {
		return durableHandoffEnvelope{}, err
	}
	return envelope, nil
}

func (r *Runtime) bindAssignmentHandoffTask(ctx context.Context, actionID string, taskID pgtype.UUID) error {
	id, err := util.ParseUUID(actionID)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `
		UPDATE autonomous_project_handoff
		SET target_task_id = $2
		WHERE workflow_action_id = $1
		  AND (target_task_id IS NULL OR target_task_id = $2)
	`, id, taskID)
	return err
}

func (r *Runtime) brainContextUsageForTask(ctx context.Context, taskID pgtype.UUID) (int64, bool, error) {
	var tokens int64
	var estimated bool
	err := r.pool.QueryRow(ctx, `
		SELECT brain_context_tokens, brain_context_estimated
		FROM autonomous_project_handoff
		WHERE target_task_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, taskID).Scan(&tokens, &estimated)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return tokens, estimated, err
}

func (r *Runtime) persistImplementationHandoff(
	ctx context.Context,
	run workflow.Run,
	task db.AgentTaskQueue,
	issue db.Issue,
	output implementationHandoffOutput,
) error {
	nodeID, nodeKey, err := r.projectNodeSource(ctx, issue)
	if err != nil {
		return err
	}
	brainRefs := []handoffBrainReference{}
	var assignmentRaw []byte
	err = r.pool.QueryRow(ctx, `
		SELECT envelope
		FROM autonomous_project_handoff
		WHERE target_task_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, task.ID).Scan(&assignmentRaw)
	if err == nil {
		var assignment durableHandoffEnvelope
		if json.Unmarshal(assignmentRaw, &assignment) == nil {
			brainRefs = assignment.BrainReferences
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	envelope := durableHandoffEnvelope{
		SchemaVersion:   handoffSchemaVersion,
		Kind:            "implementation_result",
		Source:          handoffSource{NodeID: nodeID, NodeKey: nodeKey, TaskID: util.UUIDToString(task.ID), AgentID: util.UUIDToString(task.AgentID)},
		Summary:         output.Summary,
		Decisions:       output.Decisions,
		Artifacts:       output.Artifacts,
		ChangedFiles:    output.ChangedFiles,
		CommitSHA:       output.CommitSHA,
		Diff:            output.Diff,
		Tests:           output.Tests,
		Findings:        output.Findings,
		Risks:           output.Risks,
		Blockers:        output.Blockers,
		BrainReferences: brainRefs,
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO autonomous_project_handoff (
			workspace_id, project_id, issue_id, workflow_run_id,
			source_node_id, source_node_key, source_task_id, source_agent_id,
			handoff_kind, schema_version, summary, envelope
		)
		VALUES (
			$1,$2,$3,$4,
			NULLIF($5,'')::uuid,NULLIF($6,''),$7,$8,
			'implementation_result',$9,$10,$11
		)
		ON CONFLICT DO NOTHING
	`, issue.WorkspaceID, issue.ProjectID, issue.ID, nullableUUID(workflowRunUUID(run)),
		nodeID, nodeKey, task.ID, task.AgentID, handoffSchemaVersion, envelope.Summary, raw)
	return err
}

func (r *Runtime) persistReviewVerdict(
	ctx context.Context,
	run workflow.Run,
	task db.AgentTaskQueue,
	issue db.Issue,
	verdict reviewVerdictOutput,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var existing string
	err = tx.QueryRow(ctx, `
		SELECT verdict FROM autonomous_project_review_verdict WHERE review_task_id = $1
	`, task.ID).Scan(&existing)
	if err == nil {
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	lifecycle := "superseded"
	if verdict.Verdict == "approved" {
		lifecycle = "resolved"
	}
	_, err = tx.Exec(ctx, `
		UPDATE autonomous_project_review_finding
		SET lifecycle_status = $4,
		    resolved_at = CASE WHEN $4 = 'resolved' THEN now() ELSE resolved_at END,
		    updated_at = now()
		WHERE workspace_id = $1 AND project_id = $2 AND issue_id = $3
		  AND lifecycle_status = 'open'
	`, issue.WorkspaceID, issue.ProjectID, issue.ID, lifecycle)
	if err != nil {
		return err
	}

	artifact, err := json.Marshal(verdict)
	if err != nil {
		return err
	}
	var verdictID pgtype.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO autonomous_project_review_verdict (
			workspace_id, project_id, issue_id, workflow_run_id,
			review_task_id, reviewer_agent_id, verdict, summary, artifact
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING id
	`, issue.WorkspaceID, issue.ProjectID, issue.ID, nullableUUID(workflowRunUUID(run)),
		task.ID, task.AgentID, verdict.Verdict, verdict.Summary, artifact).Scan(&verdictID)
	if err != nil {
		return err
	}

	findingLifecycle := "resolved"
	if verdict.Verdict == "changes_requested" {
		findingLifecycle = "open"
	}
	for _, finding := range verdict.Findings {
		_, err = tx.Exec(ctx, `
			INSERT INTO autonomous_project_review_finding (
				verdict_id, workspace_id, project_id, issue_id, review_task_id,
				finding_key, severity, category, description, evidence, blocking, lifecycle_status,
				resolved_at
			)
			VALUES (
				$1,$2,$3,$4,$5,
				$6,$7,$8,$9,$10,$11,$12,
				CASE WHEN $12 = 'resolved' THEN now() ELSE NULL END
			)
		`, verdictID, issue.WorkspaceID, issue.ProjectID, issue.ID, task.ID,
			finding.ID, finding.Severity, finding.Category, finding.Description,
			finding.Evidence, finding.Blocking, findingLifecycle)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *Runtime) recordContractViolation(
	ctx context.Context,
	task db.AgentTaskQueue,
	issue db.Issue,
	contract string,
	cause error,
) error {
	if !issue.ProjectID.Valid {
		return nil
	}
	contextJSON, _ := json.Marshal(map[string]any{
		"task_id":   util.UUIDToString(task.ID),
		"issue_id":  util.UUIDToString(issue.ID),
		"agent_id":  util.UUIDToString(task.AgentID),
		"contract":  contract,
		"error":     cause.Error(),
	})
	_, err := r.pool.Exec(ctx, `
		INSERT INTO autonomous_project_escalation (
			workspace_id, project_id, category, severity, summary, context
		)
		SELECT $1,$2,'contract_violation','high',$3,$4
		WHERE NOT EXISTS (
			SELECT 1 FROM autonomous_project_escalation
			WHERE workspace_id=$1 AND project_id=$2
			  AND category='contract_violation'
			  AND context ->> 'task_id' = $5
		)
	`, issue.WorkspaceID, issue.ProjectID,
		"Agent output violated "+contract+" contract", contextJSON, util.UUIDToString(task.ID))
	return err
}
