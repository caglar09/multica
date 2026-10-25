package projectorchestration

import "testing"

func TestClosedLoopImplementationRequiresDeterministicGates(t *testing.T) {
	p := DefaultPolicy()
	p.Autonomy = AutonomyClosedLoop
	reqs := RequiredQualityGates(p, NodeImplementation, RiskHigh)
	got := map[string]bool{}
	for _, req := range reqs {
		got[req.GateType] = req.Deterministic
	}
	if _, ok := got["review"]; !ok {
		t.Fatal("closed loop requires review")
	}
	for _, gate := range []string{"unit_test", "integration_test", "security"} {
		if !got[gate] {
			t.Fatalf("closed loop high-risk implementation requires deterministic %s", gate)
		}
	}
}

func TestAssistedImplementationUsesSemanticReviewOnly(t *testing.T) {
	p := DefaultPolicy()
	p.Autonomy = AutonomyAssisted
	reqs := RequiredQualityGates(p, NodeImplementation, RiskLow)
	if len(reqs) != 1 || reqs[0].GateType != "review" || reqs[0].Deterministic {
		t.Fatalf("unexpected assisted policy: %#v", reqs)
	}
}
