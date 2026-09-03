package workflowruntime

import (
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

func (r *Runtime) enqueueBrainLearning(ctx context.Context, task db.AgentTaskQueue, issue db.Issue) error {
	if r == nil || r.projectStore == nil || !issue.ProjectID.Valid {
		return nil
	}
	cfg, err := r.projectStore.GetBrainConfig(ctx, issue.WorkspaceID, issue.ProjectID)
	if err != nil { return fmt.Errorf("load project brain config: %w", err) }
	if !cfg.Enabled || cfg.LearningMode == "deterministic" {
		return nil
	}

	result := append([]byte(nil), task.Result...)
	const maxEvidenceBytes = 24000
	if len(result) > maxEvidenceBytes {
		result = result[:maxEvidenceBytes]
	}
	evidence := map[string]any{
		"task_id": util.UUIDToString(task.ID),
		"issue_id": util.UUIDToString(issue.ID),
		"agent_id": util.UUIDToString(task.AgentID),
		"task_status": task.Status,
		"result": json.RawMessage(result),
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
			if !ok { break }
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
	sourceID := util.UUIDToString(job.TaskID)
	for _, memory := range memories {
		if err := r.projectStore.RetainMemory(ctx, job.WorkspaceID, job.ProjectID, memory, "brain_learning", sourceID); err != nil {
			_ = r.projectStore.FailBrainLearning(context.Background(), job, err)
			return
		}
	}
	if err := r.projectStore.CompleteBrainLearning(context.Background(), job.ID, result.Provider, result.Model); err != nil {
		slog.Error("project brain learning completion persist failed", "job_id", util.UUIDToString(job.ID), "error", err)
	}
}

func decodeBrainMemories(output string) ([]projectorchestration.MemoryCandidate, error) {
	raw := strings.TrimSpace(output)
	if raw == "" { return nil, errors.New("brain runtime returned empty response") }
	if strings.HasPrefix(raw, "```") {
		lines := strings.Split(raw, "\n")
		if len(lines) >= 3 {
			lines = lines[1:len(lines)-1]
			raw = strings.TrimSpace(strings.Join(lines, "\n"))
		}
	}
	var response struct {
		Memories []projectorchestration.MemoryCandidate `json:"memories"`
	}
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		return nil, fmt.Errorf("decode brain memories: %w", err)
	}
	if len(response.Memories) > 12 { return nil, errors.New("brain runtime returned too many memories") }
	return response.Memories, nil
}
