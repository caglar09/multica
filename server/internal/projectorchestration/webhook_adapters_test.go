package projectorchestration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestWebhookDeploymentAdapter(t *testing.T) {
	var auth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["action"] != "deploy" {
			t.Fatalf("unexpected action: %v", body["action"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"provider":"test","external_ref":"deploy-1","status":"succeeded","evidence":{"ok":true}}`))
	}))
	defer server.Close()

	adapter := NewWebhookDeploymentAdapter(WebhookAdapterConfig{
		DeploymentURL: server.URL,
		BearerToken: "secret",
		Timeout: time.Second,
	})
	result, err := adapter.Deploy(context.Background(), DeploymentRequest{
		WorkspaceID: pgtype.UUID{Valid: true},
		ProjectID: pgtype.UUID{Valid: true},
		PlanID: pgtype.UUID{Valid: true},
		Environment: "production",
		ReleaseRef: "rev-1",
		Policy: DefaultPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer secret" {
		t.Fatalf("unexpected authorization header: %q", auth)
	}
	if result.Status != "succeeded" || result.ExternalRef != "deploy-1" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestWebhookObservationAdapter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"healthy":false,"error_rate":0.07,"latency_p95":250,"signals":["5xx spike"],"evidence":{"source":"test"}}`))
	}))
	defer server.Close()

	adapter := NewWebhookObservationAdapter(WebhookAdapterConfig{
		ObservationURL: server.URL,
		Timeout: time.Second,
	})
	result, err := adapter.Observe(context.Background(), ObservationRequest{
		WorkspaceID: pgtype.UUID{Valid: true},
		ProjectID: pgtype.UUID{Valid: true},
		DeploymentID: pgtype.UUID{Valid: true},
		WindowSeconds: 300,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Healthy {
		t.Fatal("expected unhealthy observation")
	}
	if result.ErrorRate != 0.07 {
		t.Fatalf("unexpected error rate: %v", result.ErrorRate)
	}
}

func TestWebhookDeploymentAdapterRejectsNonTerminalStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"provider":"test","status":"running"}`))
	}))
	defer server.Close()

	adapter := NewWebhookDeploymentAdapter(WebhookAdapterConfig{DeploymentURL: server.URL})
	_, err := adapter.Deploy(context.Background(), DeploymentRequest{})
	if err == nil {
		t.Fatal("expected non-terminal deployment status to be rejected")
	}
}


func TestWebhookRepositoryAnalyzerSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["action"] != "snapshot" {
			t.Fatalf("unexpected action: %v", body["action"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"revision":"abc123","modules":["server"],"test_targets":["go test ./..."],"api_surfaces":["/api/projects"],"data_stores":["postgres"],"dependencies":["pgx"],"evidence":{"source":"test"}}`))
	}))
	defer server.Close()

	analyzer := NewWebhookRepositoryAnalyzer(WebhookAdapterConfig{
		RepositoryURL: server.URL,
		Timeout: time.Second,
	})
	snapshot, err := analyzer.Snapshot(
		context.Background(),
		pgtype.UUID{Valid: true},
		pgtype.UUID{Valid: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Revision != "abc123" || len(snapshot.Modules) != 1 || snapshot.Modules[0] != "server" {
		t.Fatalf("unexpected repository snapshot: %+v", snapshot)
	}
}

func TestWebhookQualityGateRunner(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["action"] != "quality_gate" || body["gate_type"] != "unit_test" {
			t.Fatalf("unexpected quality payload: %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"passed":true,"evidence":{"command":"go test ./...","exit_code":0}}`))
	}))
	defer server.Close()

	runner := NewWebhookQualityGateRunner(WebhookAdapterConfig{
		QualityURL: server.URL,
		Timeout: time.Second,
	})
	result, err := runner.Run(context.Background(), QualityGateRequest{
		WorkspaceID: pgtype.UUID{Valid: true},
		ProjectID: pgtype.UUID{Valid: true},
		PlanID: pgtype.UUID{Valid: true},
		NodeID: pgtype.UUID{Valid: true},
		IssueID: pgtype.UUID{Valid: true},
		GateType: "unit_test",
		Artifact: map[string]any{"task_id": "task-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed {
		t.Fatalf("quality gate should pass: %+v", result)
	}
}
