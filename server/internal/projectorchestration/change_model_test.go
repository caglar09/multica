package projectorchestration

import "testing"

func TestApplyPlanMutationPreservesHierarchyFields(t *testing.T) {
	epic := changeTestNode("epic", "Catalog Epic")
	epic.IsEpic = true
	child := changeTestNode("catalog_task", "Catalog Task")
	child.ParentEpicKey = epic.Key
	plan := changeTestPlan(epic, child)
	logical := []LogicalPlanNode{
		{LogicalNodeID: "11111111-1111-1111-1111-111111111111", Spec: epic, Status: "completed"},
		{LogicalNodeID: "22222222-2222-2222-2222-222222222222", Spec: child, Status: "pending"},
	}
	updated := child
	updated.Title = "Updated catalog Task"
	result, err := ApplyPlanMutation(plan, logical, []PlanMutationOperation{{
		Operation: MutationUpdateNode, TargetLogicalNodeID: logical[1].LogicalNodeID, Node: &updated,
	}})
	if err != nil {
		t.Fatalf("ApplyPlanMutation() error = %v", err)
	}
	for _, node := range result.Plan.Nodes {
		if node.Key == epic.Key && !node.IsEpic {
			t.Fatal("Epic flag was lost while cloning plan nodes")
		}
		if node.Key == child.Key && node.ParentEpicKey != epic.Key {
			t.Fatalf("Task parent Epic = %q, want %q", node.ParentEpicKey, epic.Key)
		}
	}
}

func changeTestNode(key, title string) NodeSpec {
	return NodeSpec{Key: key, Kind: NodeProduct, Title: title, Description: title, Priority: 1, RequiredCapabilities: []string{"planning"}, RequirementIDs: []string{"REQ-CHANGE-1"}, AcceptanceCriteria: []string{"accepted"}, Risk: RiskLow, MaxAttempts: 3}
}
func changeTestPlan(nodes ...NodeSpec) Plan {
	specification := ProjectSpecification{
		Goal:    "Ship a changeable project",
		Summary: "A project that can evolve safely",
		Requirements: []SpecificationRequirement{{
			ID: "REQ-CHANGE-1", Text: "Preserve logical work identity", Rationale: "Changes must retain traceability", Source: "brief",
		}},
		DefinitionOfDone: []string{"Approved changes are traceable"},
	}
	plan := Plan{
		Version:       CurrentPlanVersion,
		Goal:          specification.Goal,
		Specification: specification.LegacySpecification(),
		Policy:        DefaultPolicy(),
		Nodes:         append([]NodeSpec(nil), nodes...),
	}
	var epicKey string
	for _, node := range plan.Nodes {
		if epicKey == "" && node.IsEpic {
			epicKey = node.Key
		}
	}
	implementation := NodeSpec{Key: "change_test_implementation", Kind: NodeImplementation, Title: "Implement change test requirement", RequirementIDs: []string{"REQ-CHANGE-1"}, AcceptanceCriteria: []string{"The requirement is implemented"}, Risk: RiskLow, MaxAttempts: 3}
	verification := NodeSpec{Key: "change_test_review", Kind: NodeReview, Title: "Review change test requirement", RequirementIDs: []string{"REQ-CHANGE-1"}, AcceptanceCriteria: []string{"The requirement is independently verified"}, Risk: RiskLow, MaxAttempts: 3}
	if epicKey != "" {
		implementation.ParentEpicKey = epicKey
		verification.ParentEpicKey = epicKey
	}
	plan.Nodes = append(plan.Nodes, implementation, verification)
	return plan
}

func TestApplyPlanMutationRenamePreservesLogicalIdentityAndEdges(t *testing.T) {
	first := changeTestNode("discover", "Discover")
	second := changeTestNode("design", "Design")
	plan := changeTestPlan(first, second)
	plan.Edges = []EdgeSpec{
		{From: "discover", To: "design", Type: DependencyHard},
		{From: "change_test_implementation", To: "change_test_review", Type: DependencyHard},
	}
	nodes := []LogicalPlanNode{{LogicalNodeID: "11111111-1111-1111-1111-111111111111", Spec: first, Status: "completed"}, {LogicalNodeID: "22222222-2222-2222-2222-222222222222", Spec: second, Status: "pending"}}
	renamed := first
	renamed.Key = "discovery"
	renamed.Title = "Product discovery"
	result, err := ApplyPlanMutation(plan, nodes, []PlanMutationOperation{{Operation: MutationUpdateNode, TargetLogicalNodeID: nodes[0].LogicalNodeID, Node: &renamed}})
	if err != nil {
		t.Fatalf("ApplyPlanMutation() error = %v", err)
	}
	found := false
	for _, node := range result.Nodes {
		if node.Spec.Key == "discovery" {
			found = true
			if node.LogicalNodeID != nodes[0].LogicalNodeID {
				t.Fatalf("rename changed logical id: got %s want %s", node.LogicalNodeID, nodes[0].LogicalNodeID)
			}
			if node.Status != "completed" {
				t.Fatalf("rename lost execution state: %s", node.Status)
			}
		}
	}
	if !found {
		t.Fatal("renamed node not found")
	}
	wantEdges := []EdgeSpec{
		{From: "discovery", To: "design", Type: DependencyHard},
		{From: "change_test_implementation", To: "change_test_review", Type: DependencyHard},
	}
	if len(result.Plan.Edges) != len(wantEdges) {
		t.Fatalf("mutation edges = %#v, want %#v", result.Plan.Edges, wantEdges)
	}
	for _, want := range wantEdges {
		found := false
		for _, edge := range result.Plan.Edges {
			if edge == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("mutation edges = %#v, want edge %#v", result.Plan.Edges, want)
		}
	}
}

func TestApplyPlanMutationRemoveAddCreatesNewLogicalIdentity(t *testing.T) {
	old := changeTestNode("feature", "Old feature")
	oldID := "11111111-1111-1111-1111-111111111111"
	replacement := changeTestNode("feature_v2", "Replacement feature")
	result, err := ApplyPlanMutation(changeTestPlan(old), []LogicalPlanNode{{LogicalNodeID: oldID, Spec: old, Status: "completed"}}, []PlanMutationOperation{{Operation: MutationRemoveNode, TargetLogicalNodeID: oldID}, {Operation: MutationAddNode, Node: &replacement}})
	if err != nil {
		t.Fatalf("ApplyPlanMutation() error = %v", err)
	}
	if len(result.Removed) != 1 || result.Removed[0].LogicalNodeID != oldID {
		t.Fatalf("removed mismatch: %#v", result.Removed)
	}
	for _, node := range result.Nodes {
		if node.Spec.Key == replacement.Key {
			if node.LogicalNodeID == "" || node.LogicalNodeID == oldID {
				t.Fatalf("replacement must receive new identity: %q", node.LogicalNodeID)
			}
			return
		}
	}
	t.Fatal("replacement node not found")
}

func TestAnalyzeChangeImpactRunningCancellationRequiresApproval(t *testing.T) {
	node := changeTestNode("feature", "Feature")
	logical := LogicalPlanNode{LogicalNodeID: "11111111-1111-1111-1111-111111111111", Spec: node, Status: "running"}
	policy := DefaultPolicy()
	policy.Autonomy = AutonomyClosedLoop
	impact := AnalyzeChangeImpact(ChangeRemoveFeature, []PlanMutationOperation{{Operation: MutationRemoveNode, TargetLogicalNodeID: logical.LogicalNodeID}}, []LogicalPlanNode{logical}, policy)
	if impact.Level != ImpactHigh || !impact.RequiresApproval || impact.AutoApplyAllowed {
		t.Fatalf("unexpected impact: %#v", impact)
	}
}
func TestAnalyzeChangeImpactCompletedProductionInvalidationIsDestructive(t *testing.T) {
	node := changeTestNode("deploy", "Deploy")
	node.Kind = NodeDeploy
	logical := LogicalPlanNode{LogicalNodeID: "11111111-1111-1111-1111-111111111111", Spec: node, Status: "completed"}
	impact := AnalyzeChangeImpact(ChangeFeature, []PlanMutationOperation{{Operation: MutationRemoveNode, TargetLogicalNodeID: logical.LogicalNodeID}}, []LogicalPlanNode{logical}, DefaultPolicy())
	if impact.Level != ImpactDestructive || !impact.RequiresApproval {
		t.Fatalf("unexpected impact: %#v", impact)
	}
}
func TestAnalyzeChangeImpactLowClosedLoopCanAutoApply(t *testing.T) {
	node := changeTestNode("feature", "Feature")
	logical := LogicalPlanNode{LogicalNodeID: "11111111-1111-1111-1111-111111111111", Spec: node, Status: "pending"}
	updated := node
	updated.Title = "Feature wording"
	policy := DefaultPolicy()
	policy.Autonomy = AutonomyClosedLoop
	impact := AnalyzeChangeImpact(ChangeFeature, []PlanMutationOperation{{Operation: MutationUpdateNode, TargetLogicalNodeID: logical.LogicalNodeID, Node: &updated}}, []LogicalPlanNode{logical}, policy)
	if impact.Level != ImpactLow || impact.RequiresApproval || !impact.AutoApplyAllowed {
		t.Fatalf("unexpected impact: %#v", impact)
	}
}
func TestChangeRequestStateMachine(t *testing.T) {
	allowed := [][2]ChangeRequestState{{ChangeReceived, ChangeAnalyzing}, {ChangeAnalyzing, ChangeProposalReady}, {ChangeProposalReady, ChangeApprovalRequired}, {ChangeApprovalRequired, ChangeApproved}, {ChangeApproved, ChangeApplying}, {ChangeApplying, ChangeApplied}}
	for _, pair := range allowed {
		if !CanTransitionChangeRequest(pair[0], pair[1]) {
			t.Fatalf("expected transition %s -> %s", pair[0], pair[1])
		}
	}
	if CanTransitionChangeRequest(ChangeReceived, ChangeApplied) {
		t.Fatal("received -> applied must not bypass analysis")
	}
}
