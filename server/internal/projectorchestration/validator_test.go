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
