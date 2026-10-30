package workflowruntime

import (
	"errors"
	"github.com/multica-ai/multica/server/internal/projectorchestration"
	"testing"
)

func TestConductorDoesNotResumeExhaustedNode(t *testing.T) {
	f := newAutonomousDeploymentBridgeFixture(t)
	for _, exhausted := range []bool{true, false} {
		attempt := 0
		if exhausted {
			attempt = 3
		}
		if _, err := f.pool.Exec(f.ctx, `UPDATE autonomous_project_plan_node SET status='blocked',blocked_category='technical_failure',attempt=$2,max_attempts=3 WHERE id=$1`, f.nodeID, attempt); err != nil {
			t.Fatal(err)
		}
		for tick := 0; tick < 3; tick++ {
			resumed, err := f.runtime(nil).projectStore.ResumeBlockedNode(f.ctx, f.workspaceID, f.projectID, "deploy")
			if err != nil {
				t.Fatal(err)
			}
			if exhausted && resumed {
				t.Fatal("exhausted node resumed: blocked/ready oscillation")
			}
			if !exhausted && tick == 0 && !resumed {
				t.Fatal("node with remaining attempts did not resume")
			}
		}
	}
}

func TestConductorExhaustedClaimReportsBudget(t *testing.T) {
	f := newAutonomousDeploymentBridgeFixture(t)
	if _, err := f.pool.Exec(f.ctx, `UPDATE autonomous_project_plan_node SET status='ready',attempt=max_attempts WHERE id=$1`, f.nodeID); err != nil {
		t.Fatal(err)
	}
	err := f.runtime(nil).projectStore.ClaimReadyNode(f.ctx, f.workspaceID, f.projectID, "deploy")
	if !errors.Is(err, projectorchestration.ErrBudgetExceeded) {
		t.Fatalf("claim exhausted node = %v, want budget error", err)
	}
}
