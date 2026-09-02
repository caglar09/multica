package projectorchestration

import "strings"

type ImpactProfile struct {
	Level         RiskLevel
	BlastRadius   []string
	RequiredGates []string
	Approval      bool
	Reasons       []string
}

// AssessImpact converts structured plan metadata into deterministic safeguards.
// The model proposes risk/capabilities; the backend only raises safeguards, it
// never lowers the risk declared by the plan.
func AssessImpact(node NodeSpec, policy Policy) ImpactProfile {
	out := ImpactProfile{Level: node.Risk}
	raise := func(level RiskLevel, reason string) {
		if riskRank(level) > riskRank(out.Level) {
			out.Level = level
		}
		out.Reasons = append(out.Reasons, reason)
	}
	addGate := func(gate string) {
		for _, existing := range out.RequiredGates {
			if existing == gate {
				return
			}
		}
		out.RequiredGates = append(out.RequiredGates, gate)
	}

	text := strings.ToLower(node.Title + " " + node.Description + " " +
		strings.Join(node.RequiredCapabilities, " "))
	switch node.Kind {
	case NodeMigration:
		raise(RiskHigh, "database/schema migration")
		out.BlastRadius = append(out.BlastRadius, "database")
		addGate("migration")
		addGate("integration_test")
		if policy.Approvals.DatabaseMigration {
			out.Approval = true
		}
	case NodeDeploy:
		raise(RiskHigh, "deployment changes runtime environment")
		out.BlastRadius = append(out.BlastRadius, "deployment")
		addGate("acceptance")
		if policy.Approvals.ProductionDeploy {
			out.Approval = true
		}
	case NodeSecurity:
		addGate("security")
	case NodeIntegration:
		addGate("integration_test")
	case NodeQA:
		addGate("acceptance")
	case NodeReview:
		addGate("review")
	}

	if containsAny(text, "auth", "authentication", "authorization", "oauth", "token", "secret", "credential") {
		raise(RiskHigh, "authentication/credential surface")
		out.BlastRadius = append(out.BlastRadius, "security")
		addGate("security")
	}
	if containsAny(text, "payment", "billing", "stripe", "checkout") {
		raise(RiskHigh, "payment/billing surface")
		out.BlastRadius = append(out.BlastRadius, "payments")
		addGate("security")
		addGate("integration_test")
	}
	if containsAny(text, "public api", "breaking", "compatibility") {
		raise(RiskHigh, "public API compatibility")
		out.BlastRadius = append(out.BlastRadius, "api")
		addGate("api_compatibility")
	}
	if out.Level == RiskCritical && policy.Approvals.CriticalRisk {
		out.Approval = true
	}
	return out
}

func riskRank(level RiskLevel) int {
	switch level {
	case RiskCritical:
		return 4
	case RiskHigh:
		return 3
	case RiskMedium:
		return 2
	case RiskLow:
		return 1
	default:
		return 0
	}
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}
