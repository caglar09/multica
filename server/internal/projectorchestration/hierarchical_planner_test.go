package projectorchestration

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

type hierarchicalPlanExecutor struct {
	output string
	calls  int
	prompt string
}

func (e *hierarchicalPlanExecutor) ExecuteProjectPlan(_ context.Context, _ PlanningInput, _, userPrompt string) (RuntimeExecution, error) {
	e.calls++
	e.prompt = userPrompt
	return RuntimeExecution{Output: e.output, Provider: "test", Model: "test"}, nil
}

func hierarchicalRequirement() ProjectSpecification {
	return ProjectSpecification{
		Goal:    "Deliver a usable catalog",
		Summary: "Users can browse catalog items.",
		Requirements: []SpecificationRequirement{{
			ID: "REQ-1", Text: "Users can browse catalog items", Rationale: "Catalog browsing is the project goal", Source: "brief",
		}},
		DefinitionOfDone: []string{"Implementation and independent verification pass"},
	}
}

func hierarchicalTaskOutput() string {
	return `{"nodes":[
		{"key":"catalog_implementation","is_epic":false,"parent_epic_key":"catalog_epic","requirement_ids":["REQ-1"],"kind":"implementation","title":"Implement catalog browsing","description":"Build catalog browsing for users.","priority":50,"required_capabilities":[],"acceptance_criteria":["Users can browse catalog items"],"risk":"low","max_attempts":3},
		{"key":"catalog_review","is_epic":false,"parent_epic_key":"catalog_epic","requirement_ids":["REQ-1"],"kind":"review","title":"Review catalog browsing","description":"Independently verify the implementation.","priority":40,"required_capabilities":[],"acceptance_criteria":["The implementation meets the catalog acceptance criteria"],"risk":"low","max_attempts":3}
	],"edges":[{"from":"catalog_implementation","to":"catalog_review","type":"hard"}]}`
}

func TestHierarchicalPlanning_EpicDecomposition(t *testing.T) {
	spec := hierarchicalRequirement()
	plan := Plan{
		Version:                 CurrentPlanVersion,
		Goal:                    spec.Goal,
		Specification:           spec.LegacySpecification(),
		SpecificationRevisionID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		Policy:                  DefaultPolicy(),
		Nodes: []NodeSpec{
			{Key: "foundation_epic", IsEpic: true, RequirementIDs: []string{"REQ-1"}, Kind: NodeProduct, Title: "Prepare catalog foundation", Risk: RiskLow, MaxAttempts: 3},
			{Key: "catalog_epic", IsEpic: true, RequirementIDs: []string{"REQ-1"}, Kind: NodeProduct, Title: "Deliver catalog browsing", Risk: RiskLow, MaxAttempts: 3},
		},
		Edges: []EdgeSpec{{From: "foundation_epic", To: "catalog_epic", Type: DependencyHard}},
	}
	executor := &hierarchicalPlanExecutor{output: hierarchicalTaskOutput()}
	planner := NewPlanner(executor, 20)
	expanded, _, err := planner.DecomposeEpic(context.Background(), PlanningInput{ProjectSpecification: &spec}, plan, "catalog_epic")
	if err != nil {
		t.Fatalf("DecomposeEpic() error = %v", err)
	}
	if executor.calls != 1 || !strings.Contains(executor.prompt, "Task nodes only") {
		t.Fatalf("decomposition executor calls/prompt = %d / %q", executor.calls, executor.prompt)
	}
	if len(expanded.Nodes) != 4 {
		t.Fatalf("expanded node count = %d, want 4", len(expanded.Nodes))
	}
	for _, key := range []string{"catalog_implementation", "catalog_review"} {
		idx := planNodeIndex(expanded.Nodes, key)
		if idx < 0 || expanded.Nodes[idx].IsEpic || expanded.Nodes[idx].ParentEpicKey != "catalog_epic" {
			t.Fatalf("Task %q lost its parent Epic metadata: %#v", key, expanded.Nodes[idx])
		}
	}
	if !hasPlanEdge(expanded.Edges, EdgeSpec{From: "foundation_epic", To: "catalog_implementation", Type: DependencyHard}) {
		t.Fatalf("decomposition did not inherit the active Epic's prerequisite: %#v", expanded.Edges)
	}
	if hasPlanEdge(expanded.Edges, EdgeSpec{From: "catalog_epic", To: "catalog_implementation", Type: DependencyHard}) {
		t.Fatal("Epic-to-Task parent relation must not block Task readiness")
	}
	if err := ValidatePlan(expanded, 20); err != nil {
		t.Fatalf("expanded total graph is invalid: %v", err)
	}
}

func TestHierarchicalPlanning_TotalGraphIntegrity(t *testing.T) {
	if !hierarchyMigrationAvailable(t) {
		return
	}
	fixture := newWave3ChangeFixture(t)
	initial := Plan{
		Version:                 CurrentPlanVersion,
		Goal:                    fixture.initialPlan.Plan.Goal,
		Specification:           fixture.initialPlan.Plan.Specification,
		SpecificationRevisionID: fixture.initialSpecificationID,
		Policy:                  DefaultPolicy(),
		Nodes: []NodeSpec{
			{Key: "catalog_epic", IsEpic: true, RequirementIDs: []string{"REQ-1"}, Kind: NodeProduct, Title: "Deliver catalog browsing", Risk: RiskLow, MaxAttempts: 3},
			{Key: "follow_up_epic", IsEpic: true, RequirementIDs: []string{"REQ-1"}, Kind: NodeProduct, Title: "Polish catalog browsing", Risk: RiskLow, MaxAttempts: 3},
		},
		Edges: []EdgeSpec{{From: "catalog_epic", To: "follow_up_epic", Type: DependencyHard}},
	}
	initialStored, err := fixture.store.PersistPlan(fixture.ctx, fixture.workspaceID, fixture.projectID, "wave4-hierarchy-initial", "Project Manager", "test-model", initial)
	if err != nil {
		t.Fatalf("persist compact Epic plan: %v", err)
	}
	if initialStored.Status != "active" || initialStored.PlanDigest == "" {
		t.Fatalf("initial Epic plan lifecycle = (%q, %q), want active with digest", initialStored.Status, initialStored.PlanDigest)
	}
	loaded, err := fixture.store.LoadPlan(fixture.ctx, fixture.workspaceID, fixture.projectID, initialStored.Revision)
	if err != nil {
		t.Fatalf("reload compact Epic plan: %v", err)
	}
	if !loaded.Plan.Nodes[0].IsEpic || loaded.Plan.Nodes[0].ParentEpicKey != "" || loaded.PlanDigest != initialStored.PlanDigest {
		t.Fatalf("reloaded Epic hierarchy/digest = (%#v, %q)", loaded.Plan.Nodes[0], loaded.PlanDigest)
	}
	logicalNodes, err := fixture.store.LoadLogicalPlanNodes(fixture.ctx, fixture.workspaceID, fixture.projectID, initialStored.ID)
	if err != nil {
		t.Fatalf("load logical Epic plan nodes: %v", err)
	}
	if len(logicalNodes) == 0 || !logicalNodes[0].Spec.IsEpic {
		t.Fatalf("logical plan loader lost Epic flag: %#v", logicalNodes)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `DELETE FROM autonomous_project_budget WHERE workspace_id=$1 AND project_id=$2`, fixture.workspaceID, fixture.projectID); err != nil {
		t.Fatalf("remove optional project budget: %v", err)
	}
	ready, err := fixture.store.ListReadyNodes(fixture.ctx, fixture.workspaceID, fixture.projectID, 20)
	if err != nil {
		t.Fatalf("list ready compact Epics: %v", err)
	}
	if len(ready) == 0 || !ready[0].IsEpic {
		t.Fatalf("ready-node projection lost active Epic: %#v", ready)
	}
	expanded, err := mergeEpicTasks(initial, initial.Nodes[0], mustParseTaskNodes(t, hierarchicalTaskOutput()), []EdgeSpec{{From: "catalog_implementation", To: "catalog_review", Type: DependencyHard}}, 20)
	if err != nil {
		t.Fatalf("merge Epic Tasks: %v", err)
	}
	stored, err := fixture.store.PersistEpicExpansion(
		fixture.ctx,
		fixture.workspaceID,
		fixture.projectID,
		wave3UUID(t, initialStored.ID),
		initialStored.PlanDigest,
		"catalog_epic",
		"Project Manager",
		"test-model",
		expanded,
	)
	if err != nil {
		t.Fatalf("persist new immutable Epic expansion revision: %v", err)
	}
	if stored.ID == initialStored.ID || stored.PlanDigest == initialStored.PlanDigest || stored.Status != "active" {
		t.Fatalf("expanded plan lifecycle/digest = (%s, %q, %q), expected new active exact-digest revision", stored.ID, stored.PlanDigest, stored.Status)
	}
	var newDigestMatches, oldDigestMatches, oldApproved bool
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT plan_digest=autonomous_project_plan_digest(id) FROM autonomous_project_plan WHERE id=$1::uuid`, stored.ID).Scan(&newDigestMatches); err != nil {
		t.Fatalf("check expanded exact digest: %v", err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT plan_digest=autonomous_project_plan_digest(id), approved_at IS NOT NULL FROM autonomous_project_plan WHERE id=$1::uuid`, initialStored.ID).Scan(&oldDigestMatches, &oldApproved); err != nil {
		t.Fatalf("check preserved source approval: %v", err)
	}
	if !newDigestMatches || !oldDigestMatches || !oldApproved {
		t.Fatalf("source/new digest or source approval changed: new=%v old=%v approved=%v", newDigestMatches, oldDigestMatches, oldApproved)
	}
	var carriedEpicStatus, successorStatus string
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT status FROM autonomous_project_plan_node WHERE plan_id=$1::uuid AND node_key='catalog_epic'`, stored.ID).Scan(&carriedEpicStatus); err != nil {
		t.Fatalf("read expanded Epic state: %v", err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT status FROM autonomous_project_plan_node WHERE plan_id=$1::uuid AND node_key='follow_up_epic'`, stored.ID).Scan(&successorStatus); err != nil {
		t.Fatalf("read successor Epic state: %v", err)
	}
	if carriedEpicStatus != "running" || successorStatus != "pending" {
		t.Fatalf("Epic state after expansion = (%q, %q), want running parent and pending successor", carriedEpicStatus, successorStatus)
	}
	var edgeCount int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM autonomous_project_plan_edge WHERE plan_id=$1::uuid AND from_node_key='catalog_epic' AND to_node_key='follow_up_epic'`, stored.ID).Scan(&edgeCount); err != nil || edgeCount != 1 {
		t.Fatalf("preserved Epic dependency edge count = %d, err=%v", edgeCount, err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE autonomous_project_plan_node SET status='completed' WHERE plan_id=$1::uuid AND parent_epic_key='catalog_epic'`, stored.ID); err != nil {
		t.Fatalf("complete Epic child Tasks: %v", err)
	}
	if err := fixture.store.RefreshReady(fixture.ctx, fixture.workspaceID, fixture.projectID); err != nil {
		t.Fatalf("reconcile completed Epic and successor readiness: %v", err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT status FROM autonomous_project_plan_node WHERE plan_id=$1::uuid AND node_key='catalog_epic'`, stored.ID).Scan(&carriedEpicStatus); err != nil {
		t.Fatalf("read completed Epic state: %v", err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT status FROM autonomous_project_plan_node WHERE plan_id=$1::uuid AND node_key='follow_up_epic'`, stored.ID).Scan(&successorStatus); err != nil {
		t.Fatalf("read activated successor state: %v", err)
	}
	if carriedEpicStatus != "completed" || successorStatus != "ready" {
		t.Fatalf("after Task completion Epic/successor statuses = (%q, %q), want completed/ready", carriedEpicStatus, successorStatus)
	}
}

func TestPlanDigestIncludesHierarchy(t *testing.T) {
	if !hierarchyMigrationAvailable(t) {
		return
	}
	fixture := newWave3ChangeFixture(t)
	baseline := fixture.initialPlan.PlanDigest
	tx, err := fixture.pool.Begin(fixture.ctx)
	if err != nil {
		t.Fatalf("begin hierarchy digest transaction: %v", err)
	}
	defer tx.Rollback(fixture.ctx)
	if _, err := tx.Exec(fixture.ctx, `UPDATE autonomous_project_plan_node SET is_epic=TRUE WHERE plan_id=$1::uuid AND node_key='implementation'`, fixture.initialPlan.ID); err != nil {
		t.Fatalf("set Epic flag in transaction: %v", err)
	}
	var epicDigest string
	if err := tx.QueryRow(fixture.ctx, `SELECT autonomous_project_plan_digest($1::uuid)`, fixture.initialPlan.ID).Scan(&epicDigest); err != nil {
		t.Fatalf("calculate Epic digest: %v", err)
	}
	if epicDigest == baseline {
		t.Fatal("Epic flag was omitted from the plan digest")
	}
	if _, err := tx.Exec(fixture.ctx, `UPDATE autonomous_project_plan_node SET parent_epic_key='implementation' WHERE plan_id=$1::uuid AND node_key='review'`, fixture.initialPlan.ID); err != nil {
		t.Fatalf("set parent Epic key in transaction: %v", err)
	}
	var childDigest string
	if err := tx.QueryRow(fixture.ctx, `SELECT autonomous_project_plan_digest($1::uuid)`, fixture.initialPlan.ID).Scan(&childDigest); err != nil {
		t.Fatalf("calculate child-task digest: %v", err)
	}
	if childDigest == epicDigest {
		t.Fatal("parent Epic key was omitted from the plan digest")
	}
}

func TestHierarchicalPlanning_ExactDigestApproval(t *testing.T) {
	if !hierarchyMigrationAvailable(t) {
		return
	}
	fixture := newWave3ChangeFixture(t)
	initial := Plan{
		Version:                 CurrentPlanVersion,
		Goal:                    fixture.initialPlan.Plan.Goal,
		Specification:           fixture.initialPlan.Plan.Specification,
		SpecificationRevisionID: fixture.initialSpecificationID,
		Policy:                  DefaultPolicy(),
		Nodes: []NodeSpec{{
			Key: "catalog_epic", IsEpic: true, RequirementIDs: []string{"REQ-1"},
			Kind: NodeProduct, Title: "Deliver catalog browsing", Risk: RiskLow, MaxAttempts: 3,
		}},
	}
	stored, err := fixture.store.PersistPlan(fixture.ctx, fixture.workspaceID, fixture.projectID, "wave4-hierarchy-approval", "Project Manager", "test-model", initial)
	if err != nil {
		t.Fatalf("persist exact-digest Epic plan: %v", err)
	}
	expanded, err := mergeEpicTasks(initial, initial.Nodes[0], mustParseTaskNodes(t, hierarchicalTaskOutput()), []EdgeSpec{{From: "catalog_implementation", To: "catalog_review", Type: DependencyHard}}, 20)
	if err != nil {
		t.Fatalf("prepare exact-digest expansion: %v", err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE autonomous_project_plan_node SET is_epic=FALSE WHERE plan_id=$1::uuid AND node_key='catalog_epic'`, stored.ID); err != nil {
		t.Fatalf("tamper with approved Epic flag for stale-digest check: %v", err)
	}
	_, err = fixture.store.PersistEpicExpansion(
		fixture.ctx,
		fixture.workspaceID,
		fixture.projectID,
		wave3UUID(t, stored.ID),
		stored.PlanDigest,
		"catalog_epic",
		"Project Manager",
		"test-model",
		expanded,
	)
	if !errors.Is(err, ErrEpicExpansionStale) {
		t.Fatalf("PersistEpicExpansion() error = %v, want ErrEpicExpansionStale", err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE autonomous_project_plan_node SET is_epic=TRUE WHERE plan_id=$1::uuid AND node_key='catalog_epic'`, stored.ID); err != nil {
		t.Fatalf("restore Epic flag after stale-digest check: %v", err)
	}
	var revisionCount int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM autonomous_project_plan WHERE workspace_id=$1 AND project_id=$2`, fixture.workspaceID, fixture.projectID).Scan(&revisionCount); err != nil {
		t.Fatalf("count plan revisions after rejected expansion: %v", err)
	}
	if revisionCount != 2 {
		t.Fatalf("stale digest created a plan revision: count=%d, want 2", revisionCount)
	}
}

func mustParseTaskNodes(t *testing.T, raw string) []NodeSpec {
	t.Helper()
	plan, err := ParsePlan(raw)
	if err != nil {
		t.Fatalf("parse test Task output: %v", err)
	}
	return plan.Nodes
}

func hasPlanEdge(edges []EdgeSpec, target EdgeSpec) bool {
	for _, edge := range edges {
		if edge == target {
			return true
		}
	}
	return false
}

func hierarchyMigrationAvailable(t *testing.T) bool {
	t.Helper()
	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Skipf("database unavailable: %v", err)
		return false
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("database unavailable: %v", err)
		return false
	}
	var found bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='autonomous_project_plan_node' AND column_name='is_epic')`).Scan(&found); err != nil {
		t.Skipf("cannot inspect plan hierarchy schema: %v", err)
		return false
	}
	if !found {
		t.Skip("migration 608 has not been applied to the test database")
		return false
	}
	return true
}
