package projectorchestration

import "testing"

func TestRequiresHumanPlanApproval(t *testing.T) {
	base := Plan{Nodes: []NodeSpec{{Risk: RiskLow}}}
	if requiresHumanPlanApproval(base, "planner", "plan:1") {
		t.Fatal("low-risk in-policy plan unexpectedly requires human approval")
	}
	if !requiresHumanPlanApproval(base, "change_request", "plan:1") {
		t.Fatal("scope-change plan must require human approval")
	}
	if !requiresHumanPlanApproval(base, "planner", "change-request:abc") {
		t.Fatal("change-request-derived plan must require human approval")
	}
	highRisk := base
	highRisk.Nodes = []NodeSpec{{Risk: RiskHigh}}
	if !requiresHumanPlanApproval(highRisk, "planner", "plan:1") {
		t.Fatal("high-risk plan must require human approval")
	}
	migration := base
	migration.Nodes = []NodeSpec{{Risk: RiskLow, Kind: NodeMigration}}
	if !requiresHumanPlanApproval(migration, "planner", "plan:1") {
		t.Fatal("database migration must require human approval even if a plan weakens policy")
	}
	deploy := base
	deploy.Nodes = []NodeSpec{{Risk: RiskLow, Kind: NodeDeploy}}
	if !requiresHumanPlanApproval(deploy, "planner", "plan:1") {
		t.Fatal("production deployment must require human approval even if a plan weakens policy")
	}
}
