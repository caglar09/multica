package teamprovision

import (
	"context"
	"errors"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type fakeRuntimePlanExecutor struct {
	result RuntimePlanExecution
	err    error
	calls  int
}

func (f *fakeRuntimePlanExecutor) ExecuteTeamPlan(
	ctx context.Context,
	input PlanningInput,
	systemPrompt string,
	userPrompt string,
) (RuntimePlanExecution, error) {
	f.calls++
	return f.result, f.err
}

func TestRuntimeBackedPlannerUsesRuntimeProviderMetadata(t *testing.T) {
	exec := &fakeRuntimePlanExecutor{result: RuntimePlanExecution{
		Provider: "codex",
		Model:    "gpt-5.6-luna",
		Output: `{
			"summary":"Small web delivery team",
			"intent":"web",
			"roles":[
				{"role":"fullstack_engineer","family":"fullstack","display_name":"Fullstack Engineer","capabilities":["web"],"responsibilities":["Deliver the app"],"reason":"Small MVP"},
				{"role":"code_reviewer","family":"review","display_name":"Code Reviewer","capabilities":["code_review"],"responsibilities":["Review changes"],"reason":"Independent quality gate"}
			],
			"route_role":""
		}`,
	}}
	planner := NewRuntimeBackedPlanner(exec, RuntimeBackedPlannerConfig{Required: true, MaxAgents: 8})
	plan, err := planner.Plan(context.Background(), PlanningInput{Project: db.Project{Title: "Todo web app"}})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.PlannerName != "mika_runtime" {
		t.Fatalf("PlannerName = %q", plan.PlannerName)
	}
	if plan.PlannerModel != "codex/gpt-5.6-luna" {
		t.Fatalf("PlannerModel = %q", plan.PlannerModel)
	}
	if exec.calls != 1 {
		t.Fatalf("executor calls = %d, want 1", exec.calls)
	}
}

func TestRuntimeBackedPlannerAcceptsMarkdownJSONFence(t *testing.T) {
	exec := &fakeRuntimePlanExecutor{result: RuntimePlanExecution{
		Provider: "antigravity",
		Output: "```json\n{\n\"summary\":\"Minimal team\",\"intent\":\"web\",\"roles\":[{\"role\":\"fullstack_engineer\",\"family\":\"fullstack\",\"display_name\":\"Fullstack Engineer\",\"capabilities\":[\"web\"],\"responsibilities\":[],\"reason\":\"MVP\"},{\"role\":\"reviewer\",\"family\":\"review\",\"display_name\":\"Reviewer\",\"capabilities\":[\"code_review\"],\"responsibilities\":[],\"reason\":\"Independent review\"}],\"route_role\":\"\"}\n```",
	}}
	planner := NewRuntimeBackedPlanner(exec, RuntimeBackedPlannerConfig{Required: true})
	if _, err := planner.Plan(context.Background(), PlanningInput{Project: db.Project{Title: "Web app"}}); err != nil {
		t.Fatalf("Plan() fenced JSON error = %v", err)
	}
}


func TestRuntimeBackedPlannerFailsClosedWhenRuntimeUnavailable(t *testing.T) {
	runtimeErr := errors.New("runtime unavailable")
	exec := &fakeRuntimePlanExecutor{err: runtimeErr}
	planner := NewRuntimeBackedPlanner(exec, RuntimeBackedPlannerConfig{
		Required:  true,
		MaxAgents: 8,
	})

	_, err := planner.Plan(context.Background(), PlanningInput{
		Project: db.Project{Title: "Todo web app"},
	})
	if !errors.Is(err, runtimeErr) {
		t.Fatalf("Plan() error = %v, want wrapped runtime error", err)
	}
	if exec.calls != 2 {
		t.Fatalf("executor calls = %d, want 2 repair attempts", exec.calls)
	}
}

func TestRuntimeBackedPlannerCanFallbackWhenRuntimeUnavailable(t *testing.T) {
	exec := &fakeRuntimePlanExecutor{err: errors.New("runtime unavailable")}
	planner := NewRuntimeBackedPlanner(exec, RuntimeBackedPlannerConfig{
		Required:  false,
		MaxAgents: 8,
	})

	plan, err := planner.Plan(context.Background(), PlanningInput{
		Project: db.Project{Title: "Todo web app"},
	})
	if err != nil {
		t.Fatalf("Plan() fallback error = %v", err)
	}
	if plan.PlannerName != "heuristic" {
		t.Fatalf("PlannerName = %q, want heuristic fallback", plan.PlannerName)
	}
	if exec.calls != 2 {
		t.Fatalf("executor calls = %d, want 2 runtime attempts before fallback", exec.calls)
	}
}
