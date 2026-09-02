package projectorchestration

import "testing"

func TestEffectivePlanningPolicyCannotExceedServerCeiling(t *testing.T) {
	server := DefaultPolicy()
	server.Autonomy = AutonomyDevelopment
	server.Budget.MaxParallelNodes = 4
	server.Budget.MaxTotalAttempts = 100
	server.Approvals.ProductionDeploy = true

	requested := server
	requested.Autonomy = AutonomyClosedLoop
	requested.Budget.MaxParallelNodes = 12
	requested.Budget.MaxTotalAttempts = 500
	requested.Approvals.ProductionDeploy = false

	got := effectivePlanningPolicy(server, requested)
	if got.Autonomy != AutonomyDevelopment {
		t.Fatalf("autonomy = %q, want %q", got.Autonomy, AutonomyDevelopment)
	}
	if got.Budget.MaxParallelNodes != 4 {
		t.Fatalf("max parallel = %d, want 4", got.Budget.MaxParallelNodes)
	}
	if got.Budget.MaxTotalAttempts != 100 {
		t.Fatalf("max attempts = %d, want 100", got.Budget.MaxTotalAttempts)
	}
	if !got.Approvals.ProductionDeploy {
		t.Fatal("server-required production approval was removed")
	}
}

func TestEffectivePlanningPolicyAllowsStricterProjectPolicy(t *testing.T) {
	server := DefaultPolicy()
	server.Autonomy = AutonomyDelivery
	server.Budget.MaxParallelNodes = 8
	server.Budget.MaxTotalAttempts = 100

	requested := Policy{
		Autonomy: AutonomyAssisted,
		Approvals: ApprovalPolicy{
			MajorDependency: true,
		},
		Budget: BudgetPolicy{
			MaxParallelNodes: 2,
			MaxTotalAttempts: 20,
		},
	}

	got := effectivePlanningPolicy(server, requested)
	if got.Autonomy != AutonomyAssisted {
		t.Fatalf("autonomy = %q, want assisted", got.Autonomy)
	}
	if got.Budget.MaxParallelNodes != 2 || got.Budget.MaxTotalAttempts != 20 {
		t.Fatalf("unexpected project budget: %+v", got.Budget)
	}
	if !got.Approvals.MajorDependency {
		t.Fatal("project-required dependency approval was not preserved")
	}
}
