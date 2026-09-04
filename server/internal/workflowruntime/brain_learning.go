package workflowruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/projectorchestration"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const brainLearningTimeout = 5 * time.Minute

func shouldEnqueueBrainLearning(task db.AgentTaskQueue) bool {
	raw := bytes.TrimSpace(task.Result)
	if len(raw) == 0 {
		return false
	}
	if output, err := parseImplementationHandoff(task.Result); err == nil {
		return len(output.Decisions) > 0 || len(output.Artifacts) > 0 ||
			len(output.ChangedFiles) > 0 || len(output.Findings) > 0 ||
			len(output.Risks) > 0 || len(output.Blockers) > 0
	}
	if verdict, err := parseReviewVerdict(task.Result); err == nil {
		return verdict.Verdict == "changes_requested" || len(verdict.Findings) > 0
	}
	// Unknown task contracts are allowed into semantic learning only when there
	// is enough evidence to justify an extra control-plane model invocation.
	return len(raw) >= 256
}

func (r *Runtime) enqueueBrainLearning(ctx context.Context, task db.AgentTaskQueue, issue db.Issue) error {
	if r == nil || r.projectStore == nil || !issue.ProjectID.Valid || !shouldEnqueueBrainLearning(task) {
		return nil
	}
	cfg, err := r.projectStore.GetBrainConfig(ctx, issue.WorkspaceID, issue.ProjectID)
	if err != nil {
		return fmt.Errorf("load project brain config: %w", err)
	}
	if !cfg.Enabled || cfg.LearningMode == "deterministic" {
		return nil
	}

	const maxEvidenceBytes = 24000
	evidence := map[string]any{
		"task_id":     util.UUIDToString(task.ID),
		"issue_id":    util.UUIDToString(issue.ID),
		"agent_id":    util.UUIDToString(task.AgentID),
		"task_status": task.Status,
	}
	if len(task.Result) > 0 {
		if len(task.Result) <= maxEvidenceBytes {
			var result any
			if json.Unmarshal(task.Result, &result) == nil {
				evidence["result"] = result
			} else {
				evidence["result_text"] = string(task.Result)
			}
		} else {
			// Keep the evidence envelope valid JSON. Raw JSON must never be byte-sliced:
			// a truncated object would make the durable learning job impossible to encode.
			evidence["result_excerpt"] = string(task.Result[:maxEvidenceBytes])
			evidence["result_truncated"] = true
		}
	}
	return r.projectStore.EnqueueBrainLearning(ctx, issue.WorkspaceID, issue.ProjectID, task.ID, evidence)
}

func (r *Runtime) runBrainWorker(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for {
			job, ok, err := r.projectStore.ClaimBrainLearning(ctx, 6*time.Minute)
			if err != nil {
				slog.Error("project brain learning claim failed", "error", err)
				break
			}
			if !ok {
				break
			}
			r.processBrainLearning(ctx, job)
		}
	}
}

func (r *Runtime) processBrainLearning(parent context.Context, job projectorchestration.BrainLearningJob) {
	ctx, cancel := context.WithTimeout(parent, brainLearningTimeout)
	defer cancel()

	cfg, err := r.projectStore.GetBrainConfig(ctx, job.WorkspaceID, job.ProjectID)
	if err != nil {
		_ = r.projectStore.FailBrainLearning(context.Background(), job, err)
		return
	}
	if !cfg.Enabled || cfg.LearningMode == "deterministic" {
		_ = r.projectStore.CompleteBrainLearning(context.Background(), job.ID, "deterministic", "")
		return
	}

	result, err := r.brainExecutor.Execute(ctx, cfg, job.Evidence)
	if err != nil {
		_ = r.projectStore.FailBrainLearning(context.Background(), job, err)
		return
	}
	memories, err := decodeBrainMemories(result.Output)
	if err != nil {
		_ = r.projectStore.FailBrainLearning(context.Background(), job, err)
		return
	}
	source := projectorchestration.MemorySource{
		SourceType:    "brain_learning",
		SourceID:      util.UUIDToString(job.TaskID),
		CreatedByType: "agent",
		Authority:     projectorchestration.AuthorityAgentInference,
		Evidence:      job.Evidence,
		ObservedAt:    time.Now().UTC(),
	}
	for _, memory := range memories {
		retention, retainErr := r.projectStore.RetainMemoryGoverned(
			ctx, job.WorkspaceID, job.ProjectID, memory, source,
		)
		if retainErr != nil {
			_ = r.projectStore.FailBrainLearning(context.Background(), job, retainErr)
			return
		}
		if impactErr := r.projectStore.AssessMemoryImpact(
			ctx, job.WorkspaceID, job.ProjectID, memory, retention, source,
		); impactErr != nil {
			_ = r.projectStore.FailBrainLearning(context.Background(), job, impactErr)
			return
		}
	}
	if err := r.projectStore.CompleteBrainLearning(context.Background(), job.ID, result.Provider, result.Model); err != nil {
		slog.Error("project brain learning completion persist failed", "job_id", util.UUIDToString(job.ID), "error", err)
	}
}

func decodeBrainMemories(output string) ([]projectorchestration.MemoryCandidate, error) {
	raw := strings.TrimSpace(output)
	if raw == "" {
		return nil, errors.New("brain runtime returned empty response")
	}
	if strings.HasPrefix(raw, "```") {
		lines := strings.Split(raw, "\n")
		if len(lines) >= 3 {
			lines = lines[1 : len(lines)-1]
			raw = strings.TrimSpace(strings.Join(lines, "\n"))
		}
	}
	var response struct {
		Memories []projectorchestration.MemoryCandidate `json:"memories"`
	}
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		return nil, fmt.Errorf("decode brain memories: %w", err)
	}
	if len(response.Memories) > 12 {
		return nil, errors.New("brain runtime returned too many memories")
	}
	return response.Memories, nil
}
