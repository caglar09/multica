package workflowruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/projectorchestration"
)

func TestDeployment_RejectsTimestampRef(t *testing.T) {
	manifest := deploymentTestManifest()
	var calls int
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"provider": "test", "status": "succeeded",
			"evidence": map[string]any{
				"manifest_digest":     manifest.ManifestDigest,
				"artifact_digest":     manifest.ArtifactDigest,
				"deployed_commit_sha": manifest.CommitSHA,
				"artifact_verified":   true,
			},
		})
	}))
	defer server.Close()
	adapter := projectorchestration.NewWebhookDeploymentAdapter(projectorchestration.WebhookAdapterConfig{DeploymentURL: server.URL})
	request := projectorchestration.DeploymentRequest{
		WorkspaceID: pgtype.UUID{Bytes: [16]byte{1}, Valid: true},
		ProjectID:   pgtype.UUID{Bytes: [16]byte{2}, Valid: true},
		PlanID:      pgtype.UUID{Bytes: [16]byte{3}, Valid: true},
		Environment: "production", Manifest: manifest,
		ArtifactDigest: manifest.ArtifactDigest, CommitSHA: manifest.CommitSHA,
		ArtifactURL: "https://private.example.invalid/release.tar?signature=fresh",
		Policy:      projectorchestration.DefaultPolicy(),
	}
	request.ReleaseRef = "release-2026-10-28T12:00:00Z"
	if _, err := adapter.Deploy(context.Background(), request); err == nil {
		t.Fatal("deployment accepted a timestamp-only release reference")
	}
	if calls != 0 {
		t.Fatalf("timestamp-only deployment reached provider %d times", calls)
	}

	request.ReleaseRef = "sha256:" + manifest.ManifestDigest
	if _, err := adapter.Deploy(context.Background(), request); err != nil {
		t.Fatalf("deploy immutable manifest reference: %v", err)
	}
	if received["commit_sha"] != manifest.CommitSHA || received["release_ref"] != request.ReleaseRef {
		t.Fatalf("provider request omitted exact manifest commit/reference: %#v", received)
	}
	manifestPayload, ok := received["manifest"].(map[string]any)
	if !ok || manifestPayload["commit_sha"] != manifest.CommitSHA || received["artifact_digest"] != manifest.ArtifactDigest {
		t.Fatalf("provider request did not include candidate manifest identity: %#v", received)
	}
}

func deploymentTestManifest() projectorchestration.ProviderReleaseManifest {
	return projectorchestration.ProviderReleaseManifest{
		Version:        1,
		ManifestID:     "00000000-0000-4000-8000-000000000031",
		ManifestDigest: strings.Repeat("a", 64), PlanRevision: 2,
		PlanDigest: strings.Repeat("d", 64), CommitSHA: strings.Repeat("b", 40),
		TreeSHA: strings.Repeat("e", 40), Builder: "go_binary",
		Outputs: []string{"bin/server"}, ArtifactDigest: strings.Repeat("c", 64),
		QualityGateRunIDs: []string{
			"00000000-0000-4000-8000-000000000032",
			"00000000-0000-4000-8000-000000000033",
		},
	}
}
