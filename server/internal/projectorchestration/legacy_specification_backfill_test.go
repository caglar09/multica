package projectorchestration

import (
	"os"
	"strings"
	"testing"
)

func TestAutonomousProjectLegacySpecificationBackfill(t *testing.T) {
	upSQL, err := os.ReadFile("../../migrations/617_autonomous_project_legacy_specification_policy.up.sql")
	if err != nil {
		t.Fatalf("read legacy specification backfill migration: %v", err)
	}
	downSQL, err := os.ReadFile("../../migrations/617_autonomous_project_legacy_specification_policy.down.sql")
	if err != nil {
		t.Fatalf("read legacy specification backfill down migration: %v", err)
	}
	if !strings.Contains(string(downSQL), "SET LOCAL multica.approval_purge = 'on'") ||
		!strings.Contains(string(downSQL), "source_endpoint = 'migration:617_autonomous_project_legacy_specification_policy'") {
		t.Fatal("down migration must use transaction-local purge and delete only its own events")
	}

	unapproved := newWave3ChangeFixture(t)
	registerPlanApprovalLedgerCleanup(t, unapproved)
	assertLegacySpecificationBackfill(t, unapproved, string(upSQL), false, false)

	approved := newWave3ChangeFixture(t)
	registerPlanApprovalLedgerCleanup(t, approved)
	insertExecutionSpecificationApproval(t, approved, "local", "approved")
	assertLegacySpecificationBackfill(t, approved, string(upSQL), true, false)

	imported := newWave3ChangeFixture(t)
	registerPlanApprovalLedgerCleanup(t, imported)
	insertExecutionSpecificationApproval(t, imported, "imported", "approved")
	assertLegacySpecificationBackfill(t, imported, string(upSQL), false, true)

	t.Log("LEGACY_SPECIFICATION_BACKFILL_GATE_PASS")
}

func assertLegacySpecificationBackfill(t *testing.T, fixture wave3ChangeFixture, migration string, hasLocalApproval, hasImportedApproval bool) {
	t.Helper()

	tx, err := fixture.pool.Begin(fixture.ctx)
	if err != nil {
		t.Fatalf("begin legacy specification backfill test: %v", err)
	}
	defer tx.Rollback(fixture.ctx)

	for i := 0; i < 2; i++ {
		if _, err := tx.Exec(fixture.ctx, migration); err != nil {
			t.Fatalf("run legacy specification backfill migration: %v", err)
		}
	}

	var eventCount int
	if err := tx.QueryRow(fixture.ctx, `
		SELECT count(*)
		FROM autonomous_project_approval_event
		WHERE workspace_id=$1::uuid AND project_id=$2::uuid
		  AND policy_ref='legacy_unapproved'
	`, fixture.workspaceID, fixture.projectID).Scan(&eventCount); err != nil {
		t.Fatalf("count legacy policy events: %v", err)
	}
	if hasLocalApproval {
		if eventCount != 0 {
			t.Fatalf("legacy policy events for an approved specification = %d, want 0", eventCount)
		}
	} else if eventCount != 1 {
		t.Fatalf("legacy policy events for an unapproved specification = %d, want 1 after two runs", eventCount)
	}

	var planStatus, nodeStatus string
	var materializedIssueID string
	if err := tx.QueryRow(fixture.ctx, `
		SELECT p.status,n.status,n.materialized_issue_id::text
		FROM autonomous_project_plan p
		JOIN autonomous_project_plan_node n ON n.plan_id=p.id AND n.node_key='implementation'
		WHERE p.id=$1::uuid
	`, fixture.initialPlan.ID).Scan(&planStatus, &nodeStatus, &materializedIssueID); err != nil {
		t.Fatalf("read plan and issue state after backfill: %v", err)
	}
	if planStatus != "active" || nodeStatus != "ready" || materializedIssueID != fixture.issueID {
		t.Fatalf("backfill changed existing work: plan=%s node=%s issue=%s", planStatus, nodeStatus, materializedIssueID)
	}

	var executable bool
	if err := tx.QueryRow(fixture.ctx, `SELECT autonomous_project_plan_executable($1::uuid)`, fixture.initialPlan.ID).Scan(&executable); err != nil {
		t.Fatalf("read plan execution gate after backfill: %v", err)
	}
	if executable != hasLocalApproval {
		t.Fatalf("plan executable = %t with local approval %t", executable, hasLocalApproval)
	}
	if !hasLocalApproval {
		var decision, actorType, policyRef, digest string
		if err := tx.QueryRow(fixture.ctx, `
			SELECT decision,actor_type,policy_ref,subject_digest
			FROM autonomous_project_approval_event
			WHERE workspace_id=$1::uuid AND project_id=$2::uuid AND policy_ref='legacy_unapproved'
		`, fixture.workspaceID, fixture.projectID).Scan(&decision, &actorType, &policyRef, &digest); err != nil {
			t.Fatalf("read legacy policy event: %v", err)
		}
		var expectedDigest string
		if err := tx.QueryRow(fixture.ctx, `SELECT autonomous_project_specification_digest($1::uuid)`, fixture.initialSpecificationID).Scan(&expectedDigest); err != nil {
			t.Fatalf("read specification digest: %v", err)
		}
		if decision != "rejected" || actorType != "system" || policyRef != "legacy_unapproved" || digest != expectedDigest {
			t.Fatalf("legacy event = %s/%s/%s/%s, want rejected/system/legacy_unapproved/%s", decision, actorType, policyRef, digest, expectedDigest)
		}
	}
	var importedCount int
	if err := tx.QueryRow(fixture.ctx, `
		SELECT count(*)
		FROM autonomous_project_approval_event
		WHERE workspace_id=$1::uuid AND project_id=$2::uuid
		  AND subject_type='specification_revision' AND origin='imported'
	`, fixture.workspaceID, fixture.projectID).Scan(&importedCount); err != nil {
		t.Fatalf("count imported specification approvals after backfill: %v", err)
	}
	wantImportedCount := 0
	if hasImportedApproval {
		wantImportedCount = 1
	}
	if importedCount != wantImportedCount {
		t.Fatalf("imported specification approvals = %d, want %d", importedCount, wantImportedCount)
	}
}
