package daemon

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestReportTaskResultUnauthorizedCompletionReplaysFromQueue(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	acceptCompletion := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		accept := acceptCompletion
		mu.Unlock()
		if !accept {
			http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	d := New(Config{ServerBaseURL: server.URL, WorkspacesRoot: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.reportTaskResult(context.Background(), "task-1", TaskResult{Status: "completed", Comment: "done"}, d.logger)

	reports, err := d.terminalReports.list()
	if err != nil {
		t.Fatalf("list terminal report queue: %v", err)
	}
	if len(reports) != 1 || reports[0].report.kind != terminalTaskReportComplete {
		t.Fatalf("queued reports = %#v, want one completed report", reports)
	}
	mu.Lock()
	if len(paths) != 1 || paths[0] != "/api/daemon/tasks/task-1/complete" {
		t.Fatalf("terminal callbacks = %v, want one complete callback", paths)
	}
	acceptCompletion = true
	mu.Unlock()

	pending, delivered := d.replayPendingTerminalReports(context.Background())
	if pending != 0 || delivered != 1 {
		t.Fatalf("replay result pending=%d delivered=%d, want 0/1", pending, delivered)
	}
	reports, err = d.terminalReports.list()
	if err != nil {
		t.Fatalf("list terminal report queue after replay: %v", err)
	}
	if len(reports) != 0 {
		t.Fatalf("queued reports after replay = %#v, want none", reports)
	}
}
