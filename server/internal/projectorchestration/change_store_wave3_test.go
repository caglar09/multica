package projectorchestration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestChangeProposal_CreatesNewSpecRevision(t *testing.T) {
	fixture := newWave3ChangeFixture(t)
	proposal := fixture.proposal("scope-revision", "Expanded project scope")
	result, err := fixture.store.ApplyProjectLeaderProposal(fixture.ctx, fixture.workspaceID, fixture.projectID, proposal, "Project Manager", "test-model")
	if err != nil {
		t.Fatalf("record project leader proposal: %v", err)
	}
	if result.ChangeRequest.ProposedSpecificationRevisionID == "" {
		t.Fatal("proposal did not persist a specification revision")
	}
	if result.ChangeRequest.ProposedSpecificationRevisionID == fixture.initialSpecificationID {
		t.Fatal("proposal reused the current specification revision")
	}

	var revisionID string
	var storedSummary string
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT id::text,specification->>'summary' FROM autonomous_project_specification_revision WHERE id=$1::uuid`, result.ChangeRequest.ProposedSpecificationRevisionID).Scan(&revisionID, &storedSummary); err != nil {
		t.Fatalf("load proposed specification revision: %v", err)
	}
	if revisionID != result.ChangeRequest.ProposedSpecificationRevisionID || storedSummary != "Expanded project scope" {
		t.Fatalf("stored proposal revision = (%s, %q), want (%s, %q)", revisionID, storedSummary, result.ChangeRequest.ProposedSpecificationRevisionID, "Expanded project scope")
	}

	if err := fixture.store.AdvanceChangeRequest(fixture.ctx, mustProjectUUID(result.ChangeRequest.ID), ChangeApproved, ""); err != nil {
		t.Fatalf("approve proposal: %v", err)
	}
	operations, err := CompileProjectLeaderProposal(proposal, fixture.nodes)
	if err != nil {
		t.Fatalf("compile proposal operations: %v", err)
	}
	applied, err := fixture.store.ApplyChangePlanMutation(fixture.ctx, fixture.workspaceID, fixture.projectID, mustProjectUUID(result.ChangeRequest.ID), operations, "Project Manager", "test-model")
	if err != nil {
		t.Fatalf("apply proposal: %v", err)
	}
	if applied.Plan.SpecificationRevisionID != result.ChangeRequest.ProposedSpecificationRevisionID {
		t.Fatalf("applied plan specification revision = %q, want %q", applied.Plan.SpecificationRevisionID, result.ChangeRequest.ProposedSpecificationRevisionID)
	}
	var headRevisionID string
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT specification_revision_id::text FROM autonomous_project_specification_head WHERE workspace_id=$1 AND project_id=$2`, fixture.workspaceID, fixture.projectID).Scan(&headRevisionID); err != nil {
		t.Fatalf("load active specification revision: %v", err)
	}
	if headRevisionID != result.ChangeRequest.ProposedSpecificationRevisionID {
		t.Fatalf("active specification revision = %q, want %q", headRevisionID, result.ChangeRequest.ProposedSpecificationRevisionID)
	}
}

func TestChangeApply_RollsBackOnUserEditConflict(t *testing.T) {
	fixture := newWave3ChangeFixture(t)
	proposal := fixture.proposal("issue-conflict", "Expanded project scope")
	result, err := fixture.store.ApplyProjectLeaderProposal(fixture.ctx, fixture.workspaceID, fixture.projectID, proposal, "Project Manager", "test-model")
	if err != nil {
		t.Fatalf("record project leader proposal: %v", err)
	}

	const userEditedTitle = "User's edit while approval was pending"
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE issue SET title=$2,revision=revision+1,updated_at=now() WHERE id=$1`, fixture.issueID, userEditedTitle); err != nil {
		t.Fatalf("simulate user issue edit: %v", err)
	}
	if err := fixture.store.AdvanceChangeRequest(fixture.ctx, mustProjectUUID(result.ChangeRequest.ID), ChangeApproved, ""); err != nil {
		t.Fatalf("approve proposal: %v", err)
	}
	operations, err := CompileProjectLeaderProposal(proposal, fixture.nodes)
	if err != nil {
		t.Fatalf("compile proposal operations: %v", err)
	}
	_, err = fixture.store.ApplyChangePlanMutation(fixture.ctx, fixture.workspaceID, fixture.projectID, mustProjectUUID(result.ChangeRequest.ID), operations, "Project Manager", "test-model")
	if !errors.Is(err, ErrStaleIssueMutationConflict) {
		t.Fatalf("apply error = %v, want %v", err, ErrStaleIssueMutationConflict)
	}

	var title string
	var revision int64
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT title,revision FROM issue WHERE id=$1`, fixture.issueID).Scan(&title, &revision); err != nil {
		t.Fatalf("reload user-edited issue: %v", err)
	}
	if title != userEditedTitle || revision != 2 {
		t.Fatalf("issue = (%q, revision %d), want (%q, revision 2)", title, revision, userEditedTitle)
	}

	latest, ok, err := fixture.store.LoadLatestPlan(fixture.ctx, fixture.workspaceID, fixture.projectID)
	if err != nil {
		t.Fatalf("load latest plan after conflict: %v", err)
	}
	if !ok || latest.ID != fixture.initialPlan.ID || latest.Revision != fixture.initialPlan.Revision {
		t.Fatalf("latest plan after conflict = (%q, revision %d, found %v), want original plan (%q, revision %d)", latest.ID, latest.Revision, ok, fixture.initialPlan.ID, fixture.initialPlan.Revision)
	}
	if latest.Plan.SpecificationRevisionID != fixture.initialSpecificationID {
		t.Fatalf("plan specification revision after conflict = %q, want %q", latest.Plan.SpecificationRevisionID, fixture.initialSpecificationID)
	}
	var requestState string
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT state FROM autonomous_project_change_request WHERE id=$1`, mustProjectUUID(result.ChangeRequest.ID)).Scan(&requestState); err != nil {
		t.Fatalf("load change request after conflict: %v", err)
	}
	if requestState != string(ChangeApproved) {
		t.Fatalf("change request state after rollback = %q, want %q", requestState, ChangeApproved)
	}
}

func TestProjectLeaderScopeProposalRequiresProposedSpecification(t *testing.T) {
	scopeProposal := ProjectChangeProposal{
		ProposalID:       "scope-without-spec",
		Summary:          "Add follow-up work",
		Rationale:        "The agreed scope expanded",
		BasePlanRevision: 1,
		Operations: []ProjectChangeOperation{{
			Type:   ProjectLeaderOpAddNode,
			Node:   &NodeSpec{Key: "follow-up", Kind: NodeImplementation, Title: "Follow-up work"},
			Reason: "The new scope needs follow-up work",
		}},
	}
	response := ProjectLeaderResponse{Message: "I propose follow-up work", Proposal: &scopeProposal}
	if err := ValidateProjectLeaderResponse(response); err == nil || err.Error() != "scope-changing project leader proposal requires proposed_specification" {
		t.Fatalf("scope response without proposed_specification error = %v", err)
	}

	dependencyProposal := ProjectChangeProposal{
		Operations: []ProjectChangeOperation{{Type: ProjectLeaderOpAddDependency}},
	}
	if err := validateProjectLeaderProposalSpecification(dependencyProposal); err != nil {
		t.Fatalf("dependency-only proposal without proposed_specification: %v", err)
	}
}

type wave3ChangeFixture struct {
	ctx                    context.Context
	pool                   *pgxpool.Pool
	store                  *Store
	workspaceID            pgtype.UUID
	projectID              pgtype.UUID
	issueID                string
	initialSpecificationID string
	initialPlan            StoredPlan
	nodes                  []LogicalPlanNode
}

func newWave3ChangeFixture(t *testing.T) wave3ChangeFixture {
	t.Helper()
	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Skipf("database unavailable: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("database unavailable: %v", err)
	}
	t.Cleanup(pool.Close)

	base := testutil.New(pool, "", "")
	userID := base.User(t, "Wave 3 change store test", "wave3-"+uuid.NewString()+"@example.com")
	workspaceID := base.Workspace(t, "Wave 3 change store test", "wave3-"+uuid.NewString())
	base.Member(t, workspaceID, userID, "owner")
	registerWave3WorkspaceCleanup(t, pool, workspaceID)
	fixture := testutil.New(pool, workspaceID, userID)
	projectID := fixture.Project(t, "Wave 3 change store test")
	issueID := fixture.Issue(t, "Original implementation issue", testutil.Cols{"status": "todo"})

	workspaceUUID := wave3UUID(t, workspaceID)
	projectUUID := wave3UUID(t, projectID)
	store := NewStore(pool)
	spec := ProjectSpecification{
		Goal:    "Deliver the requested project",
		Summary: "Original project scope",
		Requirements: []SpecificationRequirement{{
			ID: "REQ-1", Text: "Implement the requested capability", Rationale: "The project needs this capability", Source: "brief",
		}},
		DefinitionOfDone: []string{"Implementation and review coverage are complete"},
	}
	initialRevision, err := store.CreateSpecificationRevision(ctx, workspaceUUID, projectUUID, "project_bootstrap", "wave3-test", spec, true)
	if err != nil {
		t.Fatalf("create initial specification revision: %v", err)
	}
	plan := Plan{
		Version:                 CurrentPlanVersion,
		Goal:                    spec.Goal,
		Specification:           spec.LegacySpecification(),
		SpecificationRevisionID: initialRevision.ID,
		Policy:                  DefaultPolicy(),
		Nodes: []NodeSpec{
			{Key: "implementation", Kind: NodeImplementation, Title: "Original implementation", Description: "Implement the capability", RequirementIDs: []string{"REQ-1"}, AcceptanceCriteria: []string{"The capability works"}, Risk: RiskLow, MaxAttempts: 3},
			{Key: "review", Kind: NodeReview, Title: "Review implementation", Description: "Verify the capability", RequirementIDs: []string{"REQ-1"}, AcceptanceCriteria: []string{"The capability is verified"}, Risk: RiskLow, MaxAttempts: 3},
		},
		Edges: []EdgeSpec{{From: "implementation", To: "review", Type: DependencyHard}},
	}
	initialPlan, err := store.PersistPlan(ctx, workspaceUUID, projectUUID, "wave3-initial", "Project Manager", "test-model", plan)
	if err != nil {
		t.Fatalf("persist initial plan: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE autonomous_project_plan_node SET materialized_issue_id=$2::uuid,status='ready' WHERE plan_id=$1::uuid AND node_key='implementation'`, initialPlan.ID, issueID); err != nil {
		t.Fatalf("materialize implementation issue: %v", err)
	}
	nodes, err := store.LoadLogicalPlanNodes(ctx, workspaceUUID, projectUUID, initialPlan.ID)
	if err != nil {
		t.Fatalf("load initial plan nodes: %v", err)
	}
	return wave3ChangeFixture{
		ctx: ctx, pool: pool, store: store, workspaceID: workspaceUUID, projectID: projectUUID,
		issueID: issueID, initialSpecificationID: initialRevision.ID, initialPlan: initialPlan, nodes: nodes,
	}
}

func (f wave3ChangeFixture) proposal(id, summary string) ProjectChangeProposal {
	proposedSpec := ProjectSpecification{
		Goal:    "Deliver the requested project",
		Summary: summary,
		Requirements: []SpecificationRequirement{{
			ID: "REQ-1", Text: "Implement the requested capability", Rationale: "The project needs this capability", Source: "brief",
		}},
		DefinitionOfDone: []string{"Implementation and review coverage are complete"},
	}
	return ProjectChangeProposal{
		ProposalID:            id,
		Summary:               summary,
		Rationale:             "The project scope now includes this change",
		BasePlanRevision:      f.initialPlan.Revision,
		ProposedSpecification: &proposedSpec,
		Operations: []ProjectChangeOperation{{
			Type:   ProjectLeaderOpAddNode,
			Node:   &NodeSpec{Key: "follow-up", Kind: NodeImplementation, Title: "Follow-up work", Description: "Complete the additional scope", AcceptanceCriteria: []string{"The follow-up is complete"}, Risk: RiskLow, MaxAttempts: 3},
			Reason: "The revised scope requires follow-up work",
		}},
	}
}

func wave3UUID(t *testing.T, value string) pgtype.UUID {
	t.Helper()
	parsed, err := uuid.Parse(value)
	if err != nil {
		t.Fatalf("parse fixture UUID: %v", err)
	}
	return pgtype.UUID{Bytes: [16]byte(parsed), Valid: true}
}

func registerWave3WorkspaceCleanup(t *testing.T, pool *pgxpool.Pool, workspaceID string) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := pool.Exec(ctx, `DELETE FROM autonomous_project_change_request WHERE workspace_id=$1`, workspaceID); err != nil {
			t.Errorf("clean wave 3 change requests: %v", err)
			return
		}
		rows, err := pool.Query(ctx, `SELECT table_name FROM information_schema.columns WHERE table_schema=current_schema() AND table_name LIKE 'autonomous_project_%' AND column_name='workspace_id'`)
		if err != nil {
			t.Errorf("list project orchestration tables for cleanup: %v", err)
			return
		}
		var tables []string
		for rows.Next() {
			var table string
			if err := rows.Scan(&table); err != nil {
				rows.Close()
				t.Errorf("read project orchestration cleanup table: %v", err)
				return
			}
			tables = append(tables, table)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Errorf("read project orchestration cleanup tables: %v", err)
			return
		}
		rows.Close()
		sort.Strings(tables)
		for _, table := range tables {
			if table == "autonomous_project_change_request" {
				continue
			}
			query := fmt.Sprintf("DELETE FROM %s WHERE workspace_id=$1", pgx.Identifier{table}.Sanitize())
			if _, err := pool.Exec(ctx, query, workspaceID); err != nil {
				t.Errorf("clean %s: %v", table, err)
				return
			}
		}
	})
}
