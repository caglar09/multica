package projectorchestration

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestAutonomousProjectPlanExecutablePredicateGatesProjectionAndBinding(t *testing.T) {
	testCases := []struct {
		name   string
		mutate func(t *testing.T, fixture wave3ChangeFixture)
	}{
		{
			name: "blocked status",
			mutate: func(t *testing.T, fixture wave3ChangeFixture) {
				if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE autonomous_project_plan SET status='blocked' WHERE id=$1::uuid`, fixture.initialPlan.ID); err != nil {
					t.Fatalf("block plan: %v", err)
				}
			},
		},
		{
			name: "missing approval",
			mutate: func(t *testing.T, fixture wave3ChangeFixture) {
				if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE autonomous_project_plan SET approved_at=NULL, approved_by=NULL WHERE id=$1::uuid`, fixture.initialPlan.ID); err != nil {
					t.Fatalf("clear plan approval: %v", err)
				}
			},
		},
		{
			name: "stale digest",
			mutate: func(t *testing.T, fixture wave3ChangeFixture) {
				if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE autonomous_project_plan_node SET description=COALESCE(description,'') || ' changed' WHERE plan_id=$1::uuid AND node_key='implementation'`, fixture.initialPlan.ID); err != nil {
					t.Fatalf("change plan after approval: %v", err)
				}
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newWave3ChangeFixtureWithApprovedSpecification(t)
			var executable bool
			if err := fixture.pool.QueryRow(fixture.ctx, `SELECT autonomous_project_plan_executable($1::uuid)`, fixture.initialPlan.ID).Scan(&executable); err != nil {
				t.Fatalf("check approved plan executable predicate: %v", err)
			}
			if !executable {
				t.Fatal("fresh approved plan did not satisfy the executable predicate")
			}

			testCase.mutate(t, fixture)
			if err := fixture.pool.QueryRow(fixture.ctx, `SELECT autonomous_project_plan_executable($1::uuid)`, fixture.initialPlan.ID).Scan(&executable); err != nil {
				t.Fatalf("check invalidated plan executable predicate: %v", err)
			}
			if executable {
				t.Fatal("blocked, unapproved, or stale plan satisfied the executable predicate")
			}

			planned, err := fixture.store.ListPlanNodes(fixture.ctx, fixture.workspaceID, fixture.projectID, 20)
			if err != nil {
				t.Fatalf("list plan nodes for non-executable plan: %v", err)
			}
			if len(planned) != 0 {
				t.Fatalf("planned nodes for non-executable plan = %d, want 0", len(planned))
			}
			ready, err := fixture.store.ListReadyNodes(fixture.ctx, fixture.workspaceID, fixture.projectID, 20)
			if err != nil {
				t.Fatalf("list ready nodes for non-executable plan: %v", err)
			}
			if len(ready) != 0 {
				t.Fatalf("ready nodes for non-executable plan = %d, want 0", len(ready))
			}
			issueID := wave3UUID(t, fixture.issueID)
			if err := fixture.store.BindNodeIssue(
				fixture.ctx, fixture.workspaceID, fixture.projectID, "implementation", issueID, "", pgtype.UUID{},
			); err == nil {
				t.Fatal("node issue binding succeeded for non-executable plan")
			}
		})
	}
}
