package projectorchestration

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestReleaseManifestSupportsStagingAndProduction(t *testing.T) {
	staging := releaseManifestEnvironmentTestValue("staging")
	production := releaseManifestEnvironmentTestValue("production")

	sealedStaging, err := SealReleaseManifest(staging)
	if err != nil {
		t.Fatalf("seal staging manifest: %v", err)
	}
	sealedProduction, err := SealReleaseManifest(production)
	if err != nil {
		t.Fatalf("seal production manifest: %v", err)
	}
	for name, manifest := range map[string]ReleaseManifest{
		"staging":    sealedStaging,
		"production": sealedProduction,
	} {
		if err := manifest.Validate(); err != nil {
			t.Errorf("validate %s manifest: %v", name, err)
		}
	}
	if sealedStaging.ManifestDigest == sealedProduction.ManifestDigest {
		t.Fatal("manifest digest did not change when the environment changed")
	}
	invalidEnvironment := releaseManifestEnvironmentTestValue("preview")
	if _, err := SealReleaseManifest(invalidEnvironment); err == nil {
		t.Fatal("accepted a release manifest for an unsupported environment")
	}
}

func TestReleaseManifestRequiresContentAddressedStorageKey(t *testing.T) {
	manifest := releaseManifestEnvironmentTestValue("production")
	manifest.ArtifactStorageKey = "public/releases/latest.tar"
	if _, err := SealReleaseManifest(manifest); err == nil {
		t.Fatal("accepted a storage key that is not bound to the artifact digest")
	}
}

func releaseManifestEnvironmentTestValue(environment string) ReleaseManifest {
	artifactDigest := strings.Repeat("a", 64)
	return ReleaseManifest{
		Version:            releaseManifestVersion,
		ManifestID:         "00000000-0000-4000-8000-000000000021",
		WorkspaceID:        "00000000-0000-4000-8000-000000000001",
		ProjectID:          "00000000-0000-4000-8000-000000000002",
		PlanID:             "00000000-0000-4000-8000-000000000003",
		ChangeSetID:        "00000000-0000-4000-8000-000000000004",
		PlanRevision:       7,
		PlanDigest:         strings.Repeat("b", 64),
		Environment:        environment,
		CommitSHA:          strings.Repeat("c", 40),
		TreeSHA:            strings.Repeat("d", 40),
		Builder:            "go_binary",
		Outputs:            []string{"bin/server"},
		ArtifactDigest:     artifactDigest,
		ArtifactStorageKey: "autonomous-project/releases/sha256/" + artifactDigest + ".tar",
		QualityGateRunIDs:  []string{"00000000-0000-4000-8000-000000000005", "00000000-0000-4000-8000-000000000006", "00000000-0000-4000-8000-000000000007"},
	}
}

func TestReleaseManifest_BindsRealCommitSHA(t *testing.T) {
	repo, commitSHA := newMergeQueueTestRepo(t)
	treeSHA := mergeTestGit(t, repo, "rev-parse", commitSHA+"^{tree}")
	packageBytes := []byte("release package bytes")
	artifactHash := sha256.Sum256(packageBytes)
	manifest := ReleaseManifest{
		Version:      releaseManifestVersion,
		ManifestID:   "00000000-0000-4000-8000-000000000021",
		WorkspaceID:  "00000000-0000-4000-8000-000000000001",
		ProjectID:    "00000000-0000-4000-8000-000000000002",
		PlanID:       "00000000-0000-4000-8000-000000000003",
		ChangeSetID:  "00000000-0000-4000-8000-000000000004",
		PlanRevision: 7, PlanDigest: strings.Repeat("a", 64), Environment: "production",
		CommitSHA: commitSHA, TreeSHA: treeSHA, Builder: "go_binary", Outputs: []string{"bin/server"},
		ArtifactDigest:     hex.EncodeToString(artifactHash[:]),
		ArtifactStorageKey: "autonomous-project/releases/sha256/" + hex.EncodeToString(artifactHash[:]) + ".tar",
		QualityGateRunIDs: []string{
			"00000000-0000-4000-8000-000000000005",
			"00000000-0000-4000-8000-000000000006",
			"00000000-0000-4000-8000-000000000007",
		},
	}
	sealed, err := SealReleaseManifest(manifest)
	if err != nil {
		t.Fatalf("seal release manifest: %v", err)
	}
	if sealed.CommitSHA != commitSHA || sealed.TreeSHA != treeSHA {
		t.Fatalf("manifest candidate = (%s, %s), want real Git commit/tree (%s, %s)", sealed.CommitSHA, sealed.TreeSHA, commitSHA, treeSHA)
	}
	if err := sealed.Validate(); err != nil {
		t.Fatalf("validate sealed manifest: %v", err)
	}
	if _, err := ParseReleaseReference("sha256:" + sealed.ManifestDigest); err != nil {
		t.Fatalf("parse digest release reference: %v", err)
	}
	if _, err := ParseReleaseReference("release-2026-10-28T12:00:00Z"); err == nil {
		t.Fatal("accepted a timestamp-only deployment reference")
	}

	mutations := map[string]func(*ReleaseManifest){
		"commit":        func(value *ReleaseManifest) { value.CommitSHA = strings.Repeat("f", len(value.CommitSHA)) },
		"tree":          func(value *ReleaseManifest) { value.TreeSHA = strings.Repeat("f", len(value.TreeSHA)) },
		"artifact":      func(value *ReleaseManifest) { value.ArtifactDigest = strings.Repeat("f", 64) },
		"plan revision": func(value *ReleaseManifest) { value.PlanRevision++ },
		"quality run":   func(value *ReleaseManifest) { value.QualityGateRunIDs[0] = "00000000-0000-4000-8000-000000000007" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := sealed
			changed.QualityGateRunIDs = append([]string(nil), sealed.QualityGateRunIDs...)
			mutate(&changed)
			if err := changed.Validate(); err == nil {
				t.Fatal("accepted manifest contents changed without resealing")
			}
		})
	}
	changedCommit := manifest
	changedCommit.CommitSHA = strings.Repeat("f", len(commitSHA))
	changedCommit, err = SealReleaseManifest(changedCommit)
	if err != nil {
		t.Fatalf("seal different candidate manifest: %v", err)
	}
	if changedCommit.ManifestDigest == sealed.ManifestDigest {
		t.Fatal("manifest digest did not change when the tested Git commit changed")
	}
}
