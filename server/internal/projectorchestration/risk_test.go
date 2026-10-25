package projectorchestration

import "testing"

func TestAssessImpactRaisesAuthImplementationRisk(t *testing.T) {
	policy := DefaultPolicy()
	got := AssessImpact(NodeSpec{
		Kind: NodeImplementation,
		Title: "Implement OAuth token rotation",
		Risk: RiskMedium,
	}, policy)
	if got.Level != RiskHigh {
		t.Fatalf("level = %q, want high", got.Level)
	}
	if len(got.RequiredGates) == 0 {
		t.Fatal("expected security gate")
	}
}

func TestAssessImpactRequiresMigrationApproval(t *testing.T) {
	got := AssessImpact(NodeSpec{
		Kind: NodeMigration,
		Title: "Migrate users",
		Risk: RiskMedium,
	}, DefaultPolicy())
	if !got.Approval {
		t.Fatal("expected migration approval")
	}
}
