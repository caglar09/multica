package projectorchestration

import (
	"testing"

	"github.com/google/uuid"
)

func TestApprovalDigestSQLFunctions(t *testing.T) {
	fixture := newWave3ChangeFixture(t)
	tx, err := fixture.pool.Begin(fixture.ctx)
	if err != nil {
		t.Fatalf("begin digest test transaction: %v", err)
	}
	defer tx.Rollback(fixture.ctx)

	baseRequirement := `[{"id":"REQ-1","text":"Create the first capability","rationale":"User need A","source":"brief"},{"id":"REQ-2","text":"Create the second capability","rationale":"User need B","source":"brief"}]`
	reversedRequirements := `[{"id":"REQ-2","text":"Create the second capability","rationale":"User need B","source":"brief"},{"id":"REQ-1","text":"Create the first capability","rationale":"User need A","source":"brief"}]`
	changedTextRequirements := `[{"id":"REQ-1","text":"Create the first feature","rationale":"User need A","source":"brief"},{"id":"REQ-2","text":"Create the second capability","rationale":"User need B","source":"brief"}]`
	changedContentRequirements := `[{"id":"REQ-1","text":"Create the first capability","rationale":"Different reason","source":"brief"},{"id":"REQ-2","text":"Create the second capability","rationale":"User need B","source":"brief"}]`
	insertRevision := `INSERT INTO autonomous_project_specification_revision
		(id, workspace_id, project_id, revision, source_kind, source_ref, goal, specification)
		VALUES ($1,$2,$3,$4,'system',$5,'Deliver the approved project',
		jsonb_build_object('summary','Approved scope','requirements',$6::jsonb,'constraints','[]'::jsonb))`
	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	requirementSets := []string{baseRequirement, baseRequirement, reversedRequirements, changedTextRequirements}
	for i, specRequirements := range requirementSets {
		if _, err := tx.Exec(fixture.ctx, insertRevision, ids[i], fixture.workspaceID, fixture.projectID, 901+i, uuid.NewString(), specRequirements); err != nil {
			t.Fatalf("insert specification digest fixture %d: %v", i, err)
		}
	}
	// Add a same-text requirement with a changed rationale to cover non-text content.
	contentID := uuid.New()
	if _, err := tx.Exec(fixture.ctx, insertRevision, contentID, fixture.workspaceID, fixture.projectID, 905, uuid.NewString(), changedContentRequirements); err != nil {
		t.Fatalf("insert changed-content specification fixture: %v", err)
	}
	specDigest := func(id uuid.UUID) string {
		t.Helper()
		var digest string
		if err := tx.QueryRow(fixture.ctx, `SELECT autonomous_project_specification_digest($1)`, id).Scan(&digest); err != nil {
			t.Fatalf("calculate specification digest: %v", err)
		}
		return digest
	}
	baseDigest := specDigest(ids[0])
	if sameDigest := specDigest(ids[1]); sameDigest != baseDigest {
		t.Fatalf("same specification content produced different digests: %s != %s", baseDigest, sameDigest)
	}
	if orderedDigest := specDigest(ids[2]); orderedDigest == baseDigest {
		t.Fatal("reordering requirements did not change the specification digest")
	}
	if textDigest := specDigest(ids[3]); textDigest == baseDigest {
		t.Fatal("changing requirement text did not change the specification digest")
	}
	if contentDigest := specDigest(contentID); contentDigest == baseDigest {
		t.Fatal("changing requirement rationale did not change the specification digest")
	}
	if len(baseDigest) != 64 {
		t.Fatalf("specification digest length = %d, want 64 hex characters", len(baseDigest))
	}

	projectID := uuid.New()
	teamPlan := `{"roles":[{"role":"builder"}]}`
	teamSelections := `{"builder":{"runtime_id":"runtime-a","model":"model-a","skill_mode":"inherit","skill_ids":[]}}`
	if _, err := tx.Exec(fixture.ctx, `INSERT INTO autonomous_project_team_draft
		(project_id, workspace_id, plan, planner_name, selections)
		VALUES ($1,$2,$3::jsonb,'digest-test',$4::jsonb)`, projectID, fixture.workspaceID, teamPlan, teamSelections); err != nil {
		t.Fatalf("insert team selection digest fixture: %v", err)
	}
	teamDigest := func() string {
		t.Helper()
		var digest string
		if err := tx.QueryRow(fixture.ctx, `SELECT autonomous_project_team_selection_digest($1)`, projectID).Scan(&digest); err != nil {
			t.Fatalf("calculate team selection digest: %v", err)
		}
		return digest
	}
	firstTeamDigest := teamDigest()
	if sameTeamDigest := teamDigest(); sameTeamDigest != firstTeamDigest {
		t.Fatalf("same team selection produced different digests: %s != %s", firstTeamDigest, sameTeamDigest)
	}
	if _, err := tx.Exec(fixture.ctx, `UPDATE autonomous_project_team_draft SET selections=$2::jsonb WHERE project_id=$1`, projectID,
		`{"builder":{"runtime_id":"runtime-a","model":"model-b","skill_mode":"inherit","skill_ids":[]}}`); err != nil {
		t.Fatalf("change team selection fixture: %v", err)
	}
	if changedTeamDigest := teamDigest(); changedTeamDigest == firstTeamDigest {
		t.Fatal("changing the selected model did not change the team selection digest")
	}

	changeRequestID := uuid.New()
	if _, err := tx.Exec(fixture.ctx, `INSERT INTO autonomous_project_change_request
		(id, workspace_id, project_id, request_key, request_type, state, source, request_text,
		 proposal, impact, base_specification_revision_id, proposed_specification_revision_id, base_plan_id)
		VALUES ($1,$2,$3,$4,'feature','proposal_ready','project_director','Add the requested capability',
		'{"summary":"Add feature"}'::jsonb,'{"risk":"low"}'::jsonb,$5,$6,$7)`,
		changeRequestID, fixture.workspaceID, fixture.projectID, uuid.NewString(), ids[0], ids[1], fixture.initialPlan.ID); err != nil {
		t.Fatalf("insert change proposal digest fixture: %v", err)
	}
	proposalDigest := func() string {
		t.Helper()
		var digest string
		if err := tx.QueryRow(fixture.ctx, `SELECT autonomous_project_change_proposal_digest($1)`, changeRequestID).Scan(&digest); err != nil {
			t.Fatalf("calculate change proposal digest: %v", err)
		}
		return digest
	}
	firstProposalDigest := proposalDigest()
	if sameProposalDigest := proposalDigest(); sameProposalDigest != firstProposalDigest {
		t.Fatalf("same change proposal produced different digests: %s != %s", firstProposalDigest, sameProposalDigest)
	}
	if _, err := tx.Exec(fixture.ctx, `UPDATE autonomous_project_change_request SET impact='{"risk":"medium"}'::jsonb WHERE id=$1`, changeRequestID); err != nil {
		t.Fatalf("change proposal impact fixture: %v", err)
	}
	if changedProposalDigest := proposalDigest(); changedProposalDigest == firstProposalDigest {
		t.Fatal("changing proposal impact did not change the change proposal digest")
	}
	afterImpactDigest := proposalDigest()
	if _, err := tx.Exec(fixture.ctx, `UPDATE autonomous_project_change_request SET proposed_specification_revision_id=$2 WHERE id=$1`, changeRequestID, ids[2]); err != nil {
		t.Fatalf("change proposed specification fixture: %v", err)
	}
	if changedSpecDigest := proposalDigest(); changedSpecDigest == afterImpactDigest {
		t.Fatal("changing proposed specification content did not change the change proposal digest")
	}

	t.Log("APPROVAL_DIGESTS_GATE_PASS")
}
