package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const terminalOutboxReplayInterval = 15 * time.Second

// terminalOutbox keeps terminal callbacks until the server accepts them.
// A task result must outlive a daemon restart: an agent can finish while the
// server is restarting or the daemon token is being renewed.
type terminalOutbox struct {
	dir string
	mu  sync.Mutex
}

type terminalOutboxRecord struct {
	Kind                  terminalTaskReportKind `json:"kind"`
	TaskID                string                 `json:"task_id"`
	Output                string                 `json:"output,omitempty"`
	BranchName            string                 `json:"branch_name,omitempty"`
	WorktreeBaseSHA       string                 `json:"worktree_base_sha,omitempty"`
	WorktreeCommitSHA     string                 `json:"worktree_commit_sha,omitempty"`
	WorktreeChangedFiles  []string               `json:"worktree_changed_files,omitempty"`
	ErrorMessage          string                 `json:"error_message,omitempty"`
	SessionID             string                 `json:"session_id,omitempty"`
	WorkDir               string                 `json:"work_dir,omitempty"`
	DurableWorkDir        string                 `json:"durable_work_dir,omitempty"`
	FailureReason         string                 `json:"failure_reason,omitempty"`
	SessionRolloutMissing bool                   `json:"session_rollout_missing,omitempty"`
	RetiredSessionID      string                 `json:"retired_session_id,omitempty"`
}

func newTerminalOutbox(dir string) *terminalOutbox { return &terminalOutbox{dir: dir} }

func (o *terminalOutbox) Put(report terminalTaskReport) error {
	if report.taskID == "" {
		return fmt.Errorf("terminal outbox report has no task id")
	}
	record := terminalOutboxRecord{
		Kind: report.kind, TaskID: report.taskID, Output: report.output, BranchName: report.branchName,
		WorktreeBaseSHA: report.worktreeBaseSHA, WorktreeCommitSHA: report.worktreeCommitSHA,
		WorktreeChangedFiles: report.worktreeChangedFiles, ErrorMessage: report.errorMessage,
		SessionID: report.sessionID, WorkDir: report.workDir, DurableWorkDir: report.durableWorkDir,
		FailureReason: report.failureReason, SessionRolloutMissing: report.sessionRolloutMissing,
		RetiredSessionID: report.retiredSessionID,
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal terminal outbox report: %w", err)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := os.MkdirAll(o.dir, 0o700); err != nil {
		return fmt.Errorf("create terminal outbox: %w", err)
	}
	tmp, err := os.CreateTemp(o.dir, ".pending-*")
	if err != nil {
		return fmt.Errorf("create terminal outbox record: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("secure terminal outbox record: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write terminal outbox record: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close terminal outbox record: %w", err)
	}
	if err := os.Rename(tmpName, o.path(report.taskID)); err != nil {
		return fmt.Errorf("commit terminal outbox record: %w", err)
	}
	return nil
}

func (o *terminalOutbox) Reports() ([]terminalTaskReport, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	entries, err := os.ReadDir(o.dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read terminal outbox: %w", err)
	}
	reports := make([]terminalTaskReport, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(o.dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read terminal outbox record: %w", err)
		}
		var record terminalOutboxRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, fmt.Errorf("decode terminal outbox record: %w", err)
		}
		if record.TaskID == "" || (record.Kind != terminalTaskReportComplete && record.Kind != terminalTaskReportFail) {
			return nil, fmt.Errorf("invalid terminal outbox record %q", entry.Name())
		}
		reports = append(reports, terminalTaskReport{
			kind: record.Kind, taskID: record.TaskID, output: record.Output, branchName: record.BranchName,
			worktreeBaseSHA: record.WorktreeBaseSHA, worktreeCommitSHA: record.WorktreeCommitSHA,
			worktreeChangedFiles: record.WorktreeChangedFiles, errorMessage: record.ErrorMessage,
			sessionID: record.SessionID, workDir: record.WorkDir, durableWorkDir: record.DurableWorkDir,
			failureReason: record.FailureReason, sessionRolloutMissing: record.SessionRolloutMissing,
			retiredSessionID: record.RetiredSessionID,
		})
	}
	sort.Slice(reports, func(i, j int) bool { return reports[i].taskID < reports[j].taskID })
	return reports, nil
}

func (o *terminalOutbox) Delete(taskID string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	err := os.Remove(o.path(taskID))
	if err == nil || os.IsNotExist(err) {
		return nil
	}
	return fmt.Errorf("remove terminal outbox record: %w", err)
}

func (o *terminalOutbox) path(taskID string) string { return filepath.Join(o.dir, taskID+".json") }

func (d *Daemon) terminalOutboxLoop(ctx context.Context) {
	d.replayTerminalOutbox(ctx)
	ticker := time.NewTicker(terminalOutboxReplayInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.replayTerminalOutbox(ctx)
		}
	}
}

func (d *Daemon) replayTerminalOutbox(ctx context.Context) {
	if d.terminalOutbox == nil {
		return
	}
	reports, err := d.terminalOutbox.Reports()
	if err != nil {
		d.logger.Warn("read terminal task outbox failed", "error", err)
		return
	}
	for _, report := range reports {
		if err := d.sendTerminalTaskReport(ctx, report); err != nil {
			d.logger.Debug("replay terminal task report failed", "task", shortID(report.taskID), "error", err)
			continue
		}
		if err := d.terminalOutbox.Delete(report.taskID); err != nil {
			d.logger.Warn("remove replayed terminal task report failed", "task", shortID(report.taskID), "error", err)
		}
	}
}
