package projectorchestration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
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
		BearerToken:   "secret",
		Timeout:       time.Second,
	})
	result, err := adapter.Deploy(context.Background(), DeploymentRequest{
		WorkspaceID: pgtype.UUID{Valid: true},
		ProjectID:   pgtype.UUID{Valid: true},
		PlanID:      pgtype.UUID{Valid: true},
		Environment: "production",
		ReleaseRef:  "rev-1",
		Policy:      DefaultPolicy(),
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
		Timeout:        time.Second,
	})
	result, err := adapter.Observe(context.Background(), ObservationRequest{
		WorkspaceID:   pgtype.UUID{Valid: true},
		ProjectID:     pgtype.UUID{Valid: true},
		DeploymentID:  pgtype.UUID{Valid: true},
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
		Timeout:       time.Second,
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

func TestWebhookQualityGateRunnerRequiresExactCommitEvidence(t *testing.T) {
	const secret = "quality-hook-secret"
	t.Run("verifies identity command exit and signature", func(t *testing.T) {
		request := webhookQualityRequest(t)
		response := signedWebhookQualityResult(t, request, secret, nil)
		called := false
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			if r.Header.Get("Authorization") != "Bearer "+secret {
				t.Errorf("webhook authorization did not use the configured secret")
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			for key, want := range map[string]any{
				"action": "quality_gate", "task_id": util.UUIDToString(request.TaskID),
				"worktree_path": request.WorktreePath, "repo_path": request.RepoPath,
				"commit_sha": request.CommitSHA, "expected_command": "go test -v ./...",
				"expected_image": qualityGoImage, "expected_runner_type": "webhook_oci",
			} {
				if body[key] != want {
					t.Errorf("webhook payload %s = %v, want %v", key, body[key], want)
				}
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(response); err != nil {
				t.Error(err)
			}
		}))
		defer server.Close()

		runner := NewWebhookQualityGateRunner(WebhookAdapterConfig{
			QualityURL: server.URL, BearerToken: secret, Timeout: time.Second,
		})
		result, err := runner.Run(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if !called || !result.Passed || result.RunnerType != "webhook_oci" ||
			result.TaskID != util.UUIDToString(request.TaskID) || result.CommitSHA != request.CommitSHA ||
			result.Evidence["remote_attestation_verified"] != true {
			t.Fatalf("signed quality gate result was not accepted: %+v", result)
		}
		if strings.Contains(result.ArtifactDigest, secret) || strings.Contains(result.Attestation, secret) {
			t.Fatal("webhook secret appeared in persisted evidence")
		}
	})

	for _, test := range []struct {
		name   string
		mutate func(*QualityGateResult)
	}{
		{name: "wrong task", mutate: func(result *QualityGateResult) { result.TaskID = "ffffffff-ffff-ffff-ffff-ffffffffffff" }},
		{name: "wrong SHA", mutate: func(result *QualityGateResult) { result.CommitSHA = strings.Repeat("f", 40) }},
		{name: "wrong command", mutate: func(result *QualityGateResult) { result.ExecutedCommand = "go test ./..." }},
		{name: "nonzero exit", mutate: func(result *QualityGateResult) { result.ExitCode = intPointer(1) }},
		{name: "missing evidence field", mutate: func(result *QualityGateResult) { delete(result.Evidence, "stdout") }},
		{name: "unknown evidence field", mutate: func(result *QualityGateResult) { result.Evidence["unexpected"] = true }},
		{name: "false OCI isolation claim", mutate: func(result *QualityGateResult) { result.Evidence["network_disabled"] = false }},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := webhookQualityRequest(t)
			response := signedWebhookQualityResult(t, request, secret, test.mutate)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(response); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			runner := NewWebhookQualityGateRunner(WebhookAdapterConfig{QualityURL: server.URL, BearerToken: secret, Timeout: time.Second})
			result, err := runner.Run(context.Background(), request)
			if err == nil || result.Passed {
				t.Fatalf("mismatched webhook result passed: %+v, error=%v", result, err)
			}
		})
	}

	t.Run("invalid signature is rejected", func(t *testing.T) {
		request := webhookQualityRequest(t)
		response := signedWebhookQualityResult(t, request, secret, nil)
		response.Attestation = strings.Repeat("0", 64)
		response.Evidence["attestation"] = response.Attestation
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(response); err != nil {
				t.Error(err)
			}
		}))
		defer server.Close()
		runner := NewWebhookQualityGateRunner(WebhookAdapterConfig{QualityURL: server.URL, BearerToken: secret, Timeout: time.Second})
		result, err := runner.Run(context.Background(), request)
		if err == nil || result.Passed {
			t.Fatalf("invalid webhook signature passed: %+v, error=%v", result, err)
		}
	})

	t.Run("missing secret fails before request", func(t *testing.T) {
		request := webhookQualityRequest(t)
		called := false
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
		defer server.Close()
		runner := NewWebhookQualityGateRunner(WebhookAdapterConfig{QualityURL: server.URL, Timeout: time.Second})
		result, err := runner.Run(context.Background(), request)
		if err == nil || result.Passed || called {
			t.Fatalf("missing-secret webhook result = %+v, error=%v, called=%v", result, err, called)
		}
	})

	for _, test := range []struct {
		name   string
		mutate func(map[string]json.RawMessage)
	}{
		{name: "missing required response field", mutate: func(fields map[string]json.RawMessage) { delete(fields, "attestation") }},
		{name: "unknown response field", mutate: func(fields map[string]json.RawMessage) { fields["unexpected"] = json.RawMessage(`true`) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := webhookQualityRequest(t)
			result := signedWebhookQualityResult(t, request, secret, nil)
			fields, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			var response map[string]json.RawMessage
			if err := json.Unmarshal(fields, &response); err != nil {
				t.Fatal(err)
			}
			test.mutate(response)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(response); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			runner := NewWebhookQualityGateRunner(WebhookAdapterConfig{QualityURL: server.URL, BearerToken: secret, Timeout: time.Second})
			got, err := runner.Run(context.Background(), request)
			if err == nil || got.Passed {
				t.Fatalf("malformed webhook response passed: %+v, error=%v", got, err)
			}
		})
	}
}

func webhookQualityRequest(t *testing.T) QualityGateRequest {
	t.Helper()
	worktree := t.TempDir()
	gitTestCommand(t, worktree, "init")
	gitTestCommand(t, worktree, "config", "user.name", "Quality Runner Test")
	gitTestCommand(t, worktree, "config", "user.email", "quality-runner@example.invalid")
	if err := os.WriteFile(filepath.Join(worktree, "go.mod"), []byte("module webhook.test\n\ngo 1.26.9\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTestCommand(t, worktree, "add", "go.mod")
	gitTestCommand(t, worktree, "commit", "-m", "webhook test fixture")
	return QualityGateRequest{
		WorkspaceID: pgtype.UUID{Bytes: [16]byte{1}, Valid: true},
		ProjectID:   pgtype.UUID{Bytes: [16]byte{2}, Valid: true},
		PlanID:      pgtype.UUID{Bytes: [16]byte{3}, Valid: true},
		NodeID:      pgtype.UUID{Bytes: [16]byte{4}, Valid: true},
		IssueID:     pgtype.UUID{Bytes: [16]byte{5}, Valid: true},
		TaskID:      pgtype.UUID{Bytes: [16]byte{6}, Valid: true},
		GateType:    "unit_test", WorktreePath: worktree,
		RepoPath:  filepath.Dir(worktree),
		CommitSHA: gitTestCommand(t, worktree, "rev-parse", "HEAD"),
		Artifact:  map[string]any{"summary": "test"},
	}
}

func signedWebhookQualityResult(t *testing.T, request QualityGateRequest, secret string, mutate func(*QualityGateResult)) QualityGateResult {
	t.Helper()
	command, err := detectDaemonQualityCommand(request.WorktreePath, request.GateType, request.CommitSHA)
	if err != nil {
		t.Fatal(err)
	}
	result := QualityGateResult{
		Passed: true, RunnerType: "webhook_oci", TaskID: util.UUIDToString(request.TaskID),
		GateType: request.GateType, ExecutedCommand: command.display,
		ExitCode: intPointer(0), WorktreePath: request.WorktreePath, RepoPath: request.RepoPath,
		CommitSHA: request.CommitSHA, DurationMs: 125, Stdout: "--- PASS: TestQuality (0.00s)\n",
		TestCount: 1, FailedCount: 0,
	}
	result.Evidence = map[string]any{
		"runner_type": result.RunnerType, "task_id": result.TaskID, "gate_type": result.GateType,
		"executed_command": result.ExecutedCommand, "exit_code": *result.ExitCode,
		"worktree_path": result.WorktreePath, "repo_path": result.RepoPath,
		"commit_sha": result.CommitSHA, "container_image": command.image,
		"container_image_id":     "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"source_snapshot_digest": strings.Repeat("b", 64), "network_disabled": true,
		"host_environment_inherited": false, "task_worktree_mounted": false,
		"source_snapshot": "git_archive_of_recorded_commit",
		"resource_limits": map[string]string{"memory": qualityOCIMemoryLimit, "cpus": qualityOCICPUQuota, "pids": qualityOCIPIDLimit, "tmpfs": qualityOCITmpLimit},
		"stdout":          result.Stdout, "stderr": result.Stderr, "duration_ms": result.DurationMs,
		"output_truncated": result.OutputTruncated, "test_count": result.TestCount, "failed_count": result.FailedCount,
	}
	if mutate != nil {
		mutate(&result)
	}
	result.ArtifactDigest = qualityResultDigest(result)
	result.Evidence["artifact_digest"] = result.ArtifactDigest
	payload, err := qualityGateAttestationPayloadFor(request, command, result)
	if err != nil {
		t.Fatal(err)
	}
	result.Attestation = signQualityGateAttestation(secret, payload)
	result.Evidence["attestation"] = result.Attestation
	return result
}

func intPointer(value int) *int { return &value }
