package teamprovision

import (
	"context"
	"fmt"
	"strings"
)

type RuntimePlanExecution struct {
	Output   string
	Provider string
	Model    string
}

type RuntimePlanExecutor interface {
	ExecuteTeamPlan(
		ctx context.Context,
		input PlanningInput,
		systemPrompt string,
		userPrompt string,
	) (RuntimePlanExecution, error)
}

type RuntimeBackedPlannerConfig struct {
	MaxAgents int
	Required  bool
	Fallback  Planner
}

type RuntimeBackedPlanner struct {
	executor  RuntimePlanExecutor
	maxAgents int
	required  bool
	fallback  Planner
}

func NewRuntimeBackedPlanner(executor RuntimePlanExecutor, cfg RuntimeBackedPlannerConfig) *RuntimeBackedPlanner {
	if cfg.MaxAgents <= 0 {
		cfg.MaxAgents = 12
	}
	if cfg.MaxAgents > 20 {
		cfg.MaxAgents = 20
	}
	if cfg.Fallback == nil {
		cfg.Fallback = NewHeuristicPlanner()
	}
	return &RuntimeBackedPlanner{
		executor: executor,
		maxAgents: cfg.MaxAgents,
		required: cfg.Required,
		fallback: cfg.Fallback,
	}
}

func (p *RuntimeBackedPlanner) Name() string { return "mika_runtime" }

// Model is intentionally empty because the selected model is dynamic: every
// planning call inherits Mika's current runtime/provider/model. The concrete
// provider/model used by a call is stamped onto Plan.PlannerModel instead.
func (p *RuntimeBackedPlanner) Model() string { return "" }

func (p *RuntimeBackedPlanner) MaxAgents() int { return p.maxAgents }

func (p *RuntimeBackedPlanner) Plan(ctx context.Context, input PlanningInput) (Plan, error) {
	if p == nil || p.executor == nil {
		if p != nil && !p.required && p.fallback != nil {
			return p.fallback.Plan(ctx, input)
		}
		return Plan{}, ErrTeamPlannerUnavailable
	}

	systemPrompt := teamPlannerSystemPrompt(p.maxAgents, input.Issue != nil)
	userPrompt, err := teamPlannerUserPrompt(input)
	if err != nil {
		return Plan{}, err
	}

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		prompt := userPrompt
		if attempt == 1 {
			prompt += "\n\nThe previous answer failed validation. Return a corrected COMPLETE JSON object only. Validation error: " + lastErr.Error()
		}

		execution, err := p.executor.ExecuteTeamPlan(ctx, input, systemPrompt, prompt)
		if err != nil {
			lastErr = fmt.Errorf("execute autonomous team planner on Mika runtime: %w", err)
			continue
		}

		raw, err := extractPlannerJSONObject(execution.Output)
		if err != nil {
			lastErr = err
			continue
		}

		// Reuse the same strict semantic validator as the server-side JSON
		// planner. Only the transport/provider changed; backend authority did not.
		validator := &ModelBackedPlanner{maxAgents: p.maxAgents}
		plan, err := validator.parseAndValidate(raw, input)
		if err != nil {
			lastErr = err
			continue
		}
		plan.PlannerName = p.Name()
		plan.PlannerModel = runtimePlannerModelLabel(execution.Provider, execution.Model)
		return plan, nil
	}

	if !p.required && p.fallback != nil {
		return p.fallback.Plan(ctx, input)
	}
	if lastErr == nil {
		lastErr = ErrInvalidTeamPlan
	}
	return Plan{}, lastErr
}

func runtimePlannerModelLabel(provider, model string) string {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	switch {
	case provider != "" && model != "":
		return provider + "/" + model
	case provider != "":
		return provider
	default:
		return model
	}
}

// Runtime CLIs are not JSON-mode APIs. Even with an explicit JSON-only
// instruction some providers may wrap their final answer in a markdown fence.
// Accept that harmless envelope, but never accept surrounding prose: the
// backend still parses and validates exactly one JSON object.
func extractPlannerJSONObject(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", fmt.Errorf("%w: planner returned empty output", ErrInvalidTeamPlan)
	}

	if strings.HasPrefix(value, "```") {
		lines := strings.Split(value, "\n")
		if len(lines) >= 3 && strings.HasPrefix(strings.TrimSpace(lines[0]), "```") &&
			strings.TrimSpace(lines[len(lines)-1]) == "```" {
			value = strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
			if strings.HasPrefix(strings.ToLower(value), "json\n") {
				value = strings.TrimSpace(value[len("json\n"):])
			}
		}
	}

	if !strings.HasPrefix(value, "{") || !strings.HasSuffix(value, "}") {
		return "", fmt.Errorf("%w: runtime planner must return one JSON object with no prose", ErrInvalidTeamPlan)
	}
	return value, nil
}

var _ Planner = (*RuntimeBackedPlanner)(nil)
