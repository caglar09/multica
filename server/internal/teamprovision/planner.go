package teamprovision

import (
	"regexp"
	"sort"
	"strings"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const PlannerVersion = 1

const (
	RoleProductManager    = "product_manager"
	RoleSolutionArchitect = "solution_architect"
	RoleBackendEngineer   = "backend_engineer"
	RoleFrontendEngineer  = "frontend_engineer"
	RoleMobileEngineer    = "mobile_engineer"
	RoleFullstackEngineer = "fullstack_engineer"
	RoleCodeReviewer      = "code_reviewer"
	RoleQAEngineer        = "qa_engineer"
)

type RoleSpec struct {
	Role         string `json:"role"`
	DisplayName  string `json:"display_name"`
	Description  string `json:"description"`
	Instructions string `json:"instructions"`
}

type Plan struct {
	Version            int        `json:"version"`
	Intent             string     `json:"intent"`
	ImplementationRole string     `json:"implementation_role"`
	Roles              []RoleSpec `json:"roles"`
}

// Planner is deliberately deterministic. Team creation is infrastructure, not a
// free-running LLM loop: the same project text must converge to the same role
// set after retries/restarts. A future model-backed planner can implement the
// same contract, snapshot its Plan, and keep provisioning deterministic.
type Planner interface {
	PlanProject(project db.Project) Plan
	ImplementationRole(issue db.Issue, plan Plan) string
}

type HeuristicPlanner struct{}

func NewHeuristicPlanner() HeuristicPlanner { return HeuristicPlanner{} }

var nonWord = regexp.MustCompile(`[^a-z0-9+#.]+`)

func normalizedText(parts ...string) string {
	return nonWord.ReplaceAllString(strings.ToLower(strings.Join(parts, " ")), " ")
}

func containsAny(text string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

func (HeuristicPlanner) PlanProject(project db.Project) Plan {
	description := ""
	if project.Description.Valid {
		description = project.Description.String
	}
	text := normalizedText(project.Title, description)

	mobile := containsAny(text,
		"react native", "react-native", "flutter", "android", "ios", "mobile", "mobil",
	)
	frontend := containsAny(text,
		"frontend", "front end", "react", "next.js", "nextjs", "vue", "angular", "web app", "dashboard",
	)
	backend := containsAny(text,
		"backend", "back end", "api", "server", "database", "postgres", "dotnet", ".net", "go ", "golang", "node",
	)

	intentParts := make([]string, 0, 3)
	if mobile {
		intentParts = append(intentParts, "mobile")
	}
	if frontend {
		intentParts = append(intentParts, "web")
	}
	if backend {
		intentParts = append(intentParts, "backend")
	}
	if len(intentParts) == 0 {
		intentParts = append(intentParts, "fullstack")
	}
	sort.Strings(intentParts)

	roles := []RoleSpec{
		roleSpec(RoleProductManager),
		roleSpec(RoleSolutionArchitect),
	}
	engineerDomains := 0
	if mobile {
		roles = append(roles, roleSpec(RoleMobileEngineer))
		engineerDomains++
	}
	if frontend {
		roles = append(roles, roleSpec(RoleFrontendEngineer))
		engineerDomains++
	}
	if backend {
		roles = append(roles, roleSpec(RoleBackendEngineer))
		engineerDomains++
	}
	if engineerDomains == 0 {
		roles = append(roles, roleSpec(RoleFullstackEngineer))
	} else if engineerDomains > 1 {
		// Cross-cutting issues such as auth, release plumbing or shared contracts
		// should not be forced onto one specialist merely because the project
		// spans several technical surfaces.
		roles = append(roles, roleSpec(RoleFullstackEngineer))
	}
	roles = append(roles, roleSpec(RoleCodeReviewer), roleSpec(RoleQAEngineer))

	implementation := RoleFullstackEngineer
	switch {
	case engineerDomains > 1:
		implementation = RoleFullstackEngineer
	case mobile:
		implementation = RoleMobileEngineer
	case backend:
		implementation = RoleBackendEngineer
	case frontend:
		implementation = RoleFrontendEngineer
	}

	return Plan{
		Version:            PlannerVersion,
		Intent:             strings.Join(intentParts, "+"),
		ImplementationRole: implementation,
		Roles:              dedupeRoles(roles),
	}
}

func (HeuristicPlanner) ImplementationRole(issue db.Issue, plan Plan) string {
	description := ""
	if issue.Description.Valid {
		description = issue.Description.String
	}
	text := normalizedText(issue.Title, description)

	if containsAny(text, "android", "ios", "react native", "react-native", "flutter", "mobile", "mobil") {
		return preferRole(plan, RoleMobileEngineer)
	}
	if containsAny(text, "api", "backend", "back end", "database", "migration", "postgres", "server", "endpoint") {
		return preferRole(plan, RoleBackendEngineer)
	}
	if containsAny(text, "frontend", "front end", "react", "next.js", "nextjs", "vue", "angular", "ui", "screen", "page") {
		return preferRole(plan, RoleFrontendEngineer)
	}
	return preferRole(plan, plan.ImplementationRole)
}

func preferRole(plan Plan, requested string) string {
	for _, role := range plan.Roles {
		if role.Role == requested {
			return requested
		}
	}
	for _, fallback := range []string{
		RoleFullstackEngineer,
		RoleBackendEngineer,
		RoleFrontendEngineer,
		RoleMobileEngineer,
	} {
		for _, role := range plan.Roles {
			if role.Role == fallback {
				return fallback
			}
		}
	}
	return requested
}

func dedupeRoles(in []RoleSpec) []RoleSpec {
	seen := make(map[string]struct{}, len(in))
	out := make([]RoleSpec, 0, len(in))
	for _, role := range in {
		if _, ok := seen[role.Role]; ok {
			continue
		}
		seen[role.Role] = struct{}{}
		out = append(out, role)
	}
	return out
}

func roleSpec(role string) RoleSpec {
	switch role {
	case RoleProductManager:
		return RoleSpec{
			Role: role, DisplayName: "Product Manager",
			Description: "Owns product scope, acceptance criteria, priorities and delivery clarity.",
			Instructions: "Act as the project's Product Manager. Turn goals into concrete scope, acceptance criteria and ordered issues. Keep decisions and scope explicit. Do not implement production code unless a task explicitly asks you to.",
		}
	case RoleSolutionArchitect:
		return RoleSpec{
			Role: role, DisplayName: "Solution Architect",
			Description: "Owns architecture boundaries, interfaces and technical decisions.",
			Instructions: "Act as the project's Solution Architect. Define pragmatic architecture, interfaces, constraints and ADR-worthy decisions. Prefer the smallest design that satisfies the project requirements and existing repository conventions.",
		}
	case RoleBackendEngineer:
		return RoleSpec{
			Role: role, DisplayName: "Backend Engineer",
			Description: "Implements backend services, APIs, persistence and server-side behavior.",
			Instructions: "Act as the project's Backend Engineer. Implement assigned backend work against acceptance criteria, repository conventions and tests. Keep changes scoped. Finish the assigned task normally; workflow routing is owned by the server.",
		}
	case RoleFrontendEngineer:
		return RoleSpec{
			Role: role, DisplayName: "Frontend Engineer",
			Description: "Implements web UI, state, integration and client behavior.",
			Instructions: "Act as the project's Frontend Engineer. Implement assigned UI/client work against acceptance criteria, existing design patterns and tests. Finish the assigned task normally; workflow routing is owned by the server.",
		}
	case RoleMobileEngineer:
		return RoleSpec{
			Role: role, DisplayName: "Mobile Engineer",
			Description: "Implements React Native, Flutter, Android or iOS application work.",
			Instructions: "Act as the project's Mobile Engineer. Implement assigned mobile work against acceptance criteria, platform constraints, repository conventions and tests. Finish the assigned task normally; workflow routing is owned by the server.",
		}
	case RoleFullstackEngineer:
		return RoleSpec{
			Role: role, DisplayName: "Full-stack Engineer",
			Description: "Implements end-to-end product slices across client and server.",
			Instructions: "Act as the project's Full-stack Engineer. Implement assigned product work end to end, respecting architecture, acceptance criteria and tests. Keep boundaries clean and changes scoped. Workflow routing is owned by the server.",
		}
	case RoleCodeReviewer:
		return RoleSpec{
			Role: role, DisplayName: "Code Reviewer",
			Description: "Independent implementation reviewer and quality gate.",
			Instructions: "Act as an independent Code Reviewer. Review implementation against acceptance criteria, architecture, correctness, security and tests. If changes are required, explain them clearly and move the issue to In Progress. If approved, leave it In Review and finish your review task normally. Never review by merely trusting the implementation summary.",
		}
	case RoleQAEngineer:
		return RoleSpec{
			Role: role, DisplayName: "QA Engineer",
			Description: "Owns verification strategy, regression coverage and release confidence.",
			Instructions: "Act as the project's QA Engineer. Verify behavior against acceptance criteria, identify regressions and improve automated test coverage where appropriate. Report reproducible failures with evidence.",
		}
	default:
		return RoleSpec{Role: role, DisplayName: role, Description: role, Instructions: "Perform the assigned project role and follow the issue acceptance criteria."}
	}
}
