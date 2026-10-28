package workflowruntime

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/projectorchestration"
)

func TestDeploymentRequestDoesNotPersistPresignedURL(t *testing.T) {
	manifest := deploymentTestManifest()
	url := "https://private.example.invalid/release.tar?signature=one-time-secret"
	request := projectorchestration.DeploymentRequest{
		WorkspaceID: pgtype.UUID{Bytes: [16]byte{1}, Valid: true},
		ProjectID:   pgtype.UUID{Bytes: [16]byte{2}, Valid: true},
		PlanID:      pgtype.UUID{Bytes: [16]byte{3}, Valid: true},
		Environment: "production", ReleaseRef: "sha256:" + manifest.ManifestDigest,
		Manifest: manifest, ArtifactDigest: manifest.ArtifactDigest,
		CommitSHA: manifest.CommitSHA, ArtifactURL: url,
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal persisted deployment request: %v", err)
	}
	if strings.Contains(string(encoded), url) || strings.Contains(string(encoded), "one-time-secret") {
		t.Fatal("presigned URL was included in serialized deployment request")
	}
	if strings.Contains(string(encoded), "artifact_storage_key") {
		t.Fatal("provider manifest view exposed the private storage key")
	}
}
