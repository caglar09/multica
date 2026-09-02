package teamprovision

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestHeuristicPlannerMobileBackendProject(t *testing.T) {
	planner := NewHeuristicPlanner()
	project := db.Project{
		Title: "React Native baby tracker",
		Description: pgtype.Text{String: "Mobile app with backend API and Postgres", Valid: true},
	}
	plan := planner.PlanProject(project)

	if plan.Intent != "backend+mobile" {
		t.Fatalf("intent = %q, want backend+mobile", plan.Intent)
	}
	assertRole(t, plan, RoleMobileEngineer)
	assertRole(t, plan, RoleBackendEngineer)
	assertRole(t, plan, RoleFullstackEngineer)
	assertRole(t, plan, RoleCodeReviewer)
	assertRole(t, plan, RoleQAEngineer)
	assertRole(t, plan, RoleProductManager)
	assertRole(t, plan, RoleSolutionArchitect)
}

func TestHeuristicPlannerRoutesIssueToSpecialist(t *testing.T) {
	planner := NewHeuristicPlanner()
	plan := planner.PlanProject(db.Project{
		Title: "SaaS dashboard",
		Description: pgtype.Text{String: "React frontend with backend API and database", Valid: true},
	})

	backendIssue := db.Issue{Title: "Add billing API endpoint"}
	if got := planner.ImplementationRole(backendIssue, plan); got != RoleBackendEngineer {
		t.Fatalf("backend issue role = %q, want %q", got, RoleBackendEngineer)
	}

	frontendIssue := db.Issue{Title: "Build invoice settings screen"}
	if got := planner.ImplementationRole(frontendIssue, plan); got != RoleFrontendEngineer {
		t.Fatalf("frontend issue role = %q, want %q", got, RoleFrontendEngineer)
	}
}

func TestHeuristicPlannerFallsBackToFullstack(t *testing.T) {
	planner := NewHeuristicPlanner()
	plan := planner.PlanProject(db.Project{Title: "Customer portal MVP"})
	assertRole(t, plan, RoleFullstackEngineer)

	issue := db.Issue{Title: "Implement first MVP slice"}
	if got := planner.ImplementationRole(issue, plan); got != RoleFullstackEngineer {
		t.Fatalf("fallback issue role = %q, want %q", got, RoleFullstackEngineer)
	}
}

func TestDedupeRolesKeepsOneRole(t *testing.T) {
	roles := dedupeRoles([]RoleSpec{
		roleSpec(RoleFullstackEngineer),
		roleSpec(RoleFullstackEngineer),
		roleSpec(RoleCodeReviewer),
	})
	if len(roles) != 2 {
		t.Fatalf("role count = %d, want 2", len(roles))
	}
}

func assertRole(t *testing.T, plan Plan, role string) {
	t.Helper()
	for _, candidate := range plan.Roles {
		if candidate.Role == role {
			return
		}
	}
	t.Fatalf("plan does not contain role %q: %+v", role, plan.Roles)
}
