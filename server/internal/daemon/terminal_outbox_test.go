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

func TestReportTaskResultUnauthorizedCompletionReplaysFromOutbox(t *testing.T) {
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

	reports, err := d.terminalOutbox.Reports()
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 || reports[0].kind != terminalTaskReportComplete {
		t.Fatalf("outbox reports = %#v, want one completed report", reports)
	}
	mu.Lock()
	if len(paths) != 1 || paths[0] != "/api/daemon/tasks/task-1/complete" {
		t.Fatalf("terminal callbacks = %v, want one complete callback", paths)
	}
	acceptCompletion = true
	mu.Unlock()

	d.replayTerminalOutbox(context.Background())
	reports, err = d.terminalOutbox.Reports()
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 0 {
		t.Fatalf("outbox reports after replay = %#v, want none", reports)
	}
}
