package migrations

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAutonomousProjectReleaseManifestMigrations(t *testing.T) {
	up := readReleaseManifestMigration(t, "601_autonomous_project_release_manifest.up.sql")
	down := readReleaseManifestMigration(t, "601_autonomous_project_release_manifest.down.sql")
	indexUp := readReleaseManifestMigration(t, "602_autonomous_project_release_manifest_digest_index.up.sql")
	indexDown := readReleaseManifestMigration(t, "602_autonomous_project_release_manifest_digest_index.down.sql")
	upLower := strings.ToLower(up)
	downLower := strings.ToLower(down)
	for _, want := range []string{
		"create table if not exists autonomous_project_release_manifest",
		"manifest_id uuid not null",
		"manifest_digest text not null",
		"artifact_storage_key text not null",
		"quality_gate_run_ids uuid[] not null",
		"add column if not exists release_manifest_id uuid",
		"add column if not exists release_manifest_digest text",
		"before update or delete",
		"are immutable",
	} {
		if !strings.Contains(upLower, want) {
			t.Errorf("601 up migration is missing %q", want)
		}
	}
	if strings.Contains(upLower, "references ") || strings.Contains(upLower, "foreign key") {
		t.Fatal("release manifest migration must not add foreign keys")
	}
	for _, want := range []string{
		"drop table if exists autonomous_project_release_manifest",
		"drop column if exists release_manifest_id",
		"drop column if exists release_manifest_digest",
	} {
		if !strings.Contains(downLower, want) {
			t.Errorf("601 down migration is missing %q", want)
		}
	}

	upStatements := brainEmbeddingSQLStatements(indexUp)
	if len(upStatements) != 1 {
		t.Fatalf("602 up migration has %d statements, want one concurrent index statement", len(upStatements))
	}
	indexStatement := strings.ToLower(upStatements[0])
	for _, want := range []string{
		"create unique index concurrently if not exists",
		"autonomous_project_release_manifest_digest_idx",
		"on autonomous_project_release_manifest (manifest_digest)",
	} {
		if !strings.Contains(indexStatement, want) {
			t.Errorf("602 up migration is missing %q", want)
		}
	}
	downStatements := brainEmbeddingSQLStatements(indexDown)
	if len(downStatements) != 1 || !strings.Contains(strings.ToLower(downStatements[0]), "drop index concurrently if exists") {
		t.Fatalf("602 down migration = %#v, want one reversible DROP INDEX CONCURRENTLY statement", downStatements)
	}
}

func TestAutonomousProjectReleaseManifestEnvironmentMigration(t *testing.T) {
	up := strings.ToLower(readReleaseManifestMigration(t, "606_autonomous_project_release_manifest_environment.up.sql"))
	down := strings.ToLower(readReleaseManifestMigration(t, "606_autonomous_project_release_manifest_environment.down.sql"))

	for _, want := range []string{
		"drop constraint if exists autonomous_project_release_manifest_environment_check",
		"add constraint autonomous_project_release_manifest_environment_check",
		"check (environment in ('staging', 'production'))",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("606 up migration is missing %q", want)
		}
	}
	if strings.Contains(up, "check (environment = 'production')") {
		t.Error("606 up migration still restricts manifests to production")
	}

	guardPositions := []int{
		strings.Index(down, "if exists"),
		strings.Index(down, "from autonomous_project_release_manifest"),
		strings.Index(down, "where environment = 'staging'"),
		strings.Index(down, "raise exception"),
	}
	for i, position := range guardPositions {
		if position < 0 {
			t.Fatalf("606 down migration is missing staging rollback guard component %d", i+1)
		}
	}
	dropConstraint := strings.Index(down, "drop constraint")
	if dropConstraint < 0 {
		t.Fatal("606 down migration does not replace the environment constraint")
	}
	for _, position := range guardPositions {
		if position > dropConstraint {
			t.Fatal("606 down migration changes the constraint before checking for staging rows")
		}
	}
	if !strings.Contains(down, "cannot restore production-only manifest environment constraint while staging manifests exist") {
		t.Error("606 down migration does not explain why staging rows block rollback")
	}
	if !strings.Contains(down, "check (environment = 'production')") {
		t.Error("606 down migration does not restore the production-only environment constraint")
	}
}

func readReleaseManifestMigration(t *testing.T, name string) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve release manifest migration test path")
	}
	path := filepath.Join(filepath.Dir(source), "..", "..", "migrations", name)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration %s: %v", name, err)
	}
	return string(contents)
}
