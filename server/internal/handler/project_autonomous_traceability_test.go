package handler

import (
	"context"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/projectorchestration"
)

func TestProjectPlanPersistsRichTraceabilityOnReload(t *testing.T) {
	if testPool == nil || testHandler == nil {
		t.Skip("database not available")
	}
	project := createProjectPermissionTestProject(t, "autonomous plan traceability project")
	ctx := context.Background()
	var specificationRevisionID pgtype.UUID
	if err := testPool.QueryRow(ctx, `
		INSERT INTO autonomous_project_specification_revision (
			workspace_id, project_id, revision, source_kind, goal, specification
		) VALUES ($1, $2, 1, 'planner', 'Trace specification requirements', '{}'::jsonb)
		RETURNING id
	`, testWorkspaceID, project.ID).Scan(&specificationRevisionID); err != nil {
		t.Fatalf("insert specification revision: %v", err)
	}
	workspaceID, projectID := parseUUID(testWorkspaceID), parseUUID(project.ID)
	store := projectorchestration.NewStore(testPool)
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		if err := store.CleanupProject(cleanupCtx, workspaceID, projectID); err != nil {
			t.Errorf("clean project orchestration rows: %v", err)
		}
		for _, statement := range []string{
			`DELETE FROM autonomous_project_specification_head WHERE workspace_id=$1 AND project_id=$2`,
			`DELETE FROM autonomous_project_specification_revision WHERE workspace_id=$1 AND project_id=$2`,
			`DELETE FROM autonomous_project_control WHERE workspace_id=$1 AND project_id=$2`,
		} {
			if _, err := testPool.Exec(cleanupCtx, statement, workspaceID, projectID); err != nil {
				t.Errorf("clean specification and control rows: %v", err)
			}
		}
	})

	specification := projectorchestration.Specification{
		Summary:                   "Trace a rich requirement through implementation and review.",
		Requirements:              []string{"Persist the full requirement and its stable ID."},
		NonFunctionalRequirements: []string{"Reloaded data remains exact."},
		Constraints:               []string{"Keep the requirement linked to the active specification revision."},
		DefinitionOfDone:          []string{"Implementation and independent review carry the requirement ID."},
		RequirementDetails: []projectorchestration.SpecificationRequirement{{
			ID: "REQ-TRACE-1", Text: "Persist the full requirement and its stable ID.",
			Rationale: "Stable IDs preserve auditability.", Source: "user-approved specification",
		}},
		NonGoals:               []string{"Do not infer requirement coverage."},
		AcceptanceExpectations: []string{"Both mapped nodes retain REQ-TRACE-1 after reload."},
	}
	plan := projectorchestration.Plan{
		Version:                 projectorchestration.CurrentPlanVersion,
		Goal:                    "Trace specification requirements",
		Specification:           specification,
		SpecificationRevisionID: specificationRevisionID.String(),
		Policy:                  projectorchestration.DefaultPolicy(),
		Nodes: []projectorchestration.NodeSpec{
			{Key: "arch", Kind: projectorchestration.NodeArchitecture, Title: "Architecture", Risk: projectorchestration.RiskLow, MaxAttempts: 2},
			{Key: "implement", Kind: projectorchestration.NodeImplementation, Title: "Implement", RequirementIDs: []string{"REQ-TRACE-1"}, AcceptanceCriteria: []string{"Persist the full requirement and its stable ID."}, Risk: projectorchestration.RiskLow, MaxAttempts: 2},
			{Key: "review", Kind: projectorchestration.NodeReview, Title: "Review", RequirementIDs: []string{"REQ-TRACE-1"}, AcceptanceCriteria: []string{"Independently verify the persisted requirement mapping."}, Risk: projectorchestration.RiskLow, MaxAttempts: 2},
		},
		Edges: []projectorchestration.EdgeSpec{
			{From: "arch", To: "implement", Type: projectorchestration.DependencyHard},
			{From: "implement", To: "review", Type: projectorchestration.DependencyHard},
		},
	}
	stored, err := store.PersistPlan(ctx, workspaceID, projectID, "traceability-test", "test-planner", "", plan)
	if err != nil {
		t.Fatalf("persist rich project plan: %v", err)
	}
	var storedDigest, computedDigest string
	if err := testPool.QueryRow(ctx, `
		SELECT plan_digest, autonomous_project_plan_digest(id)
		FROM autonomous_project_plan
		WHERE id=$1::uuid
	`, stored.ID).Scan(&storedDigest, &computedDigest); err != nil {
		t.Fatalf("read persisted plan digest: %v", err)
	}
	if storedDigest != computedDigest {
		t.Fatalf("persisted plan digest = %q, want canonical digest %q", storedDigest, computedDigest)
	}
	reloaded, err := store.LoadPlan(ctx, workspaceID, projectID, stored.Revision)
	if err != nil {
		t.Fatalf("reload project plan: %v", err)
	}
	if reloaded.Plan.SpecificationRevisionID != plan.SpecificationRevisionID ||
		!reflect.DeepEqual(reloaded.Plan.Specification, specification) {
		t.Fatalf("reloaded specification = %+v, revision=%q; want exact rich data and revision %q", reloaded.Plan.Specification, reloaded.Plan.SpecificationRevisionID, plan.SpecificationRevisionID)
	}
	byKey := make(map[string]projectorchestration.NodeSpec, len(reloaded.Plan.Nodes))
	for _, node := range reloaded.Plan.Nodes {
		byKey[node.Key] = node
	}
	for _, key := range []string{"implement", "review"} {
		if got := byKey[key].RequirementIDs; !reflect.DeepEqual(got, []string{"REQ-TRACE-1"}) {
			t.Errorf("reloaded %s requirement IDs = %v, want explicit REQ-TRACE-1 mapping", key, got)
		}
	}
	if got := byKey["arch"].RequirementIDs; len(got) != 0 {
		t.Errorf("reloaded architecture node inferred requirement IDs %v", got)
	}
}
