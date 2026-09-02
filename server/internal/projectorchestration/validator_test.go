package projectorchestration

import (
	"errors"
	"testing"
)

func validPlan() Plan {
	return Plan{
		Version: CurrentPlanVersion,
		Goal: "Ship authentication",
		Specification: Specification{
			Summary: "Users can register and sign in.",
			Requirements: []string{"Registration", "Login"},
			DefinitionOfDone: []string{"Acceptance tests pass"},
		},
		Policy: DefaultPolicy(),
		Nodes: []NodeSpec{
			{Key: "arch", Kind: NodeArchitecture, Title: "Auth architecture", Risk: RiskMedium, MaxAttempts: 2},
			{Key: "impl", Kind: NodeImplementation, Title: "Implement user flow", Risk: RiskHigh, MaxAttempts: 3, AcceptanceCriteria: []string{"Login works"}},
			{Key: "review", Kind: NodeReview, Title: "Independent review", Risk: RiskMedium, MaxAttempts: 3, AcceptanceCriteria: []string{"No blocking findings"}},
		},
		Edges: []EdgeSpec{
			{From: "arch", To: "impl", Type: DependencyHard},
			{From: "impl", To: "review", Type: DependencyHard},
		},
	}
}

func TestValidatePlanAcceptsAcyclicLifecycle(t *testing.T) {
	if err := ValidatePlan(validPlan(), 20); err != nil {
		t.Fatalf("ValidatePlan() error = %v", err)
	}
}

func TestValidatePlanRejectsCycle(t *testing.T) {
	plan := validPlan()
	plan.Edges = append(plan.Edges, EdgeSpec{From: "review", To: "arch", Type: DependencyHard})
	if err := ValidatePlan(plan, 20); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("ValidatePlan() error = %v, want ErrInvalidPlan", err)
	}
}

func TestValidatePlanRejectsImplementationWithoutReview(t *testing.T) {
	plan := validPlan()
	plan.Nodes = plan.Nodes[:2]
	plan.Edges = plan.Edges[:1]
	if err := ValidatePlan(plan, 20); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("ValidatePlan() error = %v, want ErrInvalidPlan", err)
	}
}

func TestValidatePlanRequiresClosedLoopForObserve(t *testing.T) {
	plan := validPlan()
	plan.Nodes = append(plan.Nodes, NodeSpec{
		Key: "observe", Kind: NodeObserve, Title: "Observe release", Risk: RiskLow, MaxAttempts: 2,
	})
	plan.Edges = append(plan.Edges, EdgeSpec{From: "review", To: "observe", Type: DependencyHard})
	if err := ValidatePlan(plan, 20); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("ValidatePlan() error = %v, want ErrInvalidPlan", err)
	}
}


func TestEnsureLifecycleAddsSecurityAndReviewForAuthImplementation(t *testing.T) {
	plan := Plan{
		Version: CurrentPlanVersion,
		Goal: "Ship authenticated application",
		Specification: Specification{
			Summary: "Authenticated application is usable.",
			Requirements: []string{"Authentication"},
			DefinitionOfDone: []string{"Security and review gates pass"},
		},
		Policy: DefaultPolicy(),
		Nodes: []NodeSpec{
			{
				Key: "integrate_application",
				Kind: NodeImplementation,
				Title: "Integrate application authentication",
				Description: "Connect authentication and session token handling.",
				Risk: RiskMedium,
				MaxAttempts: 3,
				AcceptanceCriteria: []string{"Authentication works end to end"},
			},
		},
	}
	plan = HardenPlan(plan)
	plan = EnsureLifecycle(plan)
	plan = HardenPlan(plan)

	if err := ValidatePlan(plan, 20); err != nil {
		t.Fatalf("ValidatePlan(EnsureLifecycle()) error = %v", err)
	}

	byKind := map[NodeKind]int{}
	for _, node := range plan.Nodes {
		byKind[node.Kind]++
	}
	if byKind[NodeSecurity] == 0 {
		t.Fatal("expected synthesized downstream security gate")
	}
	if byKind[NodeReview] == 0 {
		t.Fatal("expected synthesized downstream independent review")
	}
}

func TestEnsureLifecycleAddsMigrationIntegrationAndReview(t *testing.T) {
	plan := Plan{
		Version: CurrentPlanVersion,
		Goal: "Apply schema migration safely",
		Specification: Specification{
			Summary: "Schema is migrated without regressions.",
			Requirements: []string{"Schema migration"},
			DefinitionOfDone: []string{"Migration and integration checks pass"},
		},
		Policy: DefaultPolicy(),
		Nodes: []NodeSpec{
			{
				Key: "migrate_schema",
				Kind: NodeMigration,
				Title: "Migrate SQLite schema",
				Description: "Add required schema changes.",
				Risk: RiskMedium,
				MaxAttempts: 3,
				AcceptanceCriteria: []string{"Migration is reversible and data remains valid"},
			},
		},
	}
	plan = HardenPlan(plan)
	plan = EnsureLifecycle(plan)
	plan = HardenPlan(plan)

	if err := ValidatePlan(plan, 30); err != nil {
		t.Fatalf("ValidatePlan(EnsureLifecycle()) error = %v", err)
	}

	var integration, migrationReview bool
	for _, node := range plan.Nodes {
		if node.Kind == NodeIntegration {
			integration = true
		}
		if node.Kind == NodeReview && node.Key == "migrate_schema__independent_review" {
			migrationReview = true
		}
	}
	if !integration {
		t.Fatal("expected synthesized migration integration gate")
	}
	if !migrationReview {
		t.Fatal("expected synthesized migration review gate")
	}
}

func TestEnsureLifecycleIsIdempotent(t *testing.T) {
	plan := validPlan()
	plan.Nodes[1].Title = "Implement authentication token flow"
	plan = HardenPlan(plan)
	once := EnsureLifecycle(plan)
	twice := EnsureLifecycle(once)
	if len(twice.Nodes) != len(once.Nodes) {
		t.Fatalf("EnsureLifecycle duplicated nodes: once=%d twice=%d", len(once.Nodes), len(twice.Nodes))
	}
	if len(twice.Edges) != len(once.Edges) {
		t.Fatalf("EnsureLifecycle duplicated edges: once=%d twice=%d", len(once.Edges), len(twice.Edges))
	}
}
