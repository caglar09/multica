package projectorchestration

import "errors"

var ErrQualityGateUnavailable = errors.New("required deterministic quality gate runner is unavailable")

// QualityRequirement is derived entirely from backend-owned policy. Models may
// propose QA nodes, but they cannot weaken these minimum gates.
type QualityRequirement struct {
	GateType      string
	Deterministic bool
}

func autonomyRank(level AutonomyLevel) int {
	switch level {
	case AutonomyAssisted:
		return 1
	case AutonomyDevelopment:
		return 2
	case AutonomyDelivery:
		return 3
	case AutonomyClosedLoop:
		return 4
	default:
		return 0
	}
}

// RequiredQualityGates is the backend-owned minimum quality policy.
//
// assisted: structured semantic review for code changes.
// development: review + deterministic unit/test execution.
// delivery: review + deterministic tests + integration.
// closed_loop: delivery gates + security for high-risk code and observation/
// deploy verification through their dedicated deterministic adapters.
func RequiredQualityGates(policy Policy, kind NodeKind, risk RiskLevel) []QualityRequirement {
	rank := autonomyRank(policy.Autonomy)
	out := make([]QualityRequirement, 0, 5)
	add := func(gate string, deterministic bool) {
		for _, existing := range out {
			if existing.GateType == gate {
				return
			}
		}
		out = append(out, QualityRequirement{GateType: gate, Deterministic: deterministic})
	}

	switch kind {
	case NodeImplementation, NodeMigration, NodeIntegration:
		add("review", false)
		if rank >= 2 {
			add("unit_test", true)
		}
		if rank >= 3 {
			add("integration_test", true)
		}
		if rank >= 4 && (risk == RiskHigh || risk == RiskCritical) {
			add("security", true)
		}
	case NodeQA:
		if rank >= 2 {
			add("acceptance", true)
		}
	case NodeSecurity:
		if rank >= 3 {
			add("security", true)
		}
	case NodeDeploy:
		if rank >= 3 {
			add("acceptance", true)
		}
	case NodeObserve:
		if rank >= 4 {
			add("performance", true)
		}
	}
	return out
}
