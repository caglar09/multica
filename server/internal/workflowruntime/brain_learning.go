package workflowruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

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
			evidence["result_excerpt"] = string(task.Result[:maxEvidenceBytes])
			evidence["result_truncated"] = true
		}
	}
	return r.enqueueControlPlaneJob(
		ctx,
		issue.WorkspaceID,
		issue.ProjectID,
		controlPlaneJobBrainLearning,
		"task:"+util.UUIDToString(task.ID),
		brainLearningJobPayload{TaskID: util.UUIDToString(task.ID), Evidence: evidence},
		40,
		3,
	)
}

// runBrainWorker is retained as the startup hook used by Runtime registration.
// Phase 5 expands it into the generic durable control-plane worker pool.
func (r *Runtime) runBrainWorker(ctx context.Context) {
	r.runControlPlaneWorkers(ctx)
	<-ctx.Done()
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
