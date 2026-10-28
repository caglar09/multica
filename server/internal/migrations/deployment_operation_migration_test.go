package migrations

import (
	"strings"
	"testing"
)

func TestAutonomousProjectDeploymentOperationMigration(t *testing.T) {
	up := strings.ToLower(readReleaseManifestMigration(t, "605_autonomous_project_deployment_operation_id.up.sql"))
	down := strings.ToLower(readReleaseManifestMigration(t, "605_autonomous_project_deployment_operation_id.down.sql"))
	for _, want := range []string{
		"alter table autonomous_project_deployment",
		"add column if not exists generic_operation_id uuid",
		"add column if not exists node_id uuid",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("605 up migration is missing %q", want)
		}
	}
	if strings.Contains(up, "references ") || strings.Contains(up, "foreign key") {
		t.Fatal("605 migration must keep operation and node correlation application-validated without foreign keys")
	}
	for _, want := range []string{
		"drop column if exists node_id",
		"drop column if exists generic_operation_id",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("605 down migration is missing %q", want)
		}
	}
}
