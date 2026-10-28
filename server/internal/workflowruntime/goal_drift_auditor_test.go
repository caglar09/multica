package workflowruntime

import (
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/projectorchestration"
)

func TestReviewerPrompt_ContainsCanonicalGoal(t *testing.T) {
	spec := projectorchestration.ProjectSpecification{
		Goal:    "Deliver a reliable issue workflow for small teams.",
		Summary: "Issue workflow",
		Requirements: []projectorchestration.SpecificationRequirement{
			{ID: "REQ-ISSUE-CREATE", Text: "Users can create issues.", Rationale: "Track work.", Source: "brief"},
			{ID: "REQ-ISSUE-REVIEW", Text: "Reviewers can reject incomplete work.", Rationale: "Protect quality.", Source: "brief"},
		},
		NonGoals:         []string{"Do not add a public marketplace."},
		DefinitionOfDone: []string{"Review contract is validated."},
	}

	context, err := canonicalGoalReviewContext(spec)
	if err != nil {
		t.Fatalf("build canonical reviewer context: %v", err)
	}
	promptText, err := context.promptText()
	if err != nil {
		t.Fatalf("render canonical reviewer context: %v", err)
	}
	for _, required := range []string{
		spec.Goal,
		spec.NonGoals[0],
		spec.Requirements[0].ID,
		spec.Requirements[1].ID,
	} {
		if !strings.Contains(promptText, required) {
			t.Errorf("canonical reviewer context omits %q: %s", required, promptText)
		}
	}
	if got := context.requirementIDs(); len(got) != 2 || got[0] != spec.Requirements[0].ID || got[1] != spec.Requirements[1].ID {
		t.Fatalf("requirement IDs = %#v, want canonical source order", got)
	}
}

func TestValidateGoalDriftReviewRequiresExactRequirements(t *testing.T) {
	score := 90
	out := reviewVerdictOutput{
		Verdict:                 "approved",
		Summary:                 "aligned",
		GoalAlignmentScore:      &score,
		GoalAlignmentRationale:  "all requirements are covered",
		RequirementAttestations: []string{"REQ-A", "REQ-B"},
	}
	if err := validateGoalDriftReview(out, []string{"REQ-A", "REQ-B"}); err != nil {
		t.Fatalf("validate exact attestations: %v", err)
	}

	out.RequirementAttestations = []string{"REQ-A", "REQ-OLD"}
	if err := validateGoalDriftReview(out, []string{"REQ-A", "REQ-B"}); err == nil {
		t.Fatal("review with an unknown or missing requirement ID was accepted")
	}
}

func TestGoalDriftReviewVerdictLowScoreRequestsChanges(t *testing.T) {
	result := projectorchestration.GoalDriftAuditResult{
		Verdict:                 "approved",
		Summary:                 "Most planned work is complete.",
		GoalAlignmentScore:      79,
		GoalAlignmentRationale:  "The latest work underdelivers on the core reliability goal.",
		RequirementAttestations: []string{"REQ-A"},
	}
	out, err := goalDriftReviewVerdict(result)
	if err != nil {
		t.Fatalf("goalDriftReviewVerdict() error = %v", err)
	}
	if out.Verdict != "changes_requested" || len(out.Findings) != 1 || !out.Findings[0].Blocking {
		t.Fatalf("low score verdict = %+v, want blocking changes_requested", out)
	}
	if err := validateGoalDriftReview(out, []string{"REQ-A"}); err != nil {
		t.Fatalf("validate low score review: %v", err)
	}
}

func TestGoalDriftAuditAtQuarterProgress(t *testing.T) {
	tests := []struct {
		name     string
		previous int
		current  int
		total    int
		want     []int
	}{
		{name: "first quarter", previous: 0, current: 2, total: 8, want: []int{25}},
		{name: "second quarter", previous: 2, current: 4, total: 8, want: []int{50}},
		{name: "third quarter", previous: 4, current: 6, total: 8, want: []int{75}},
		{name: "complete", previous: 6, current: 8, total: 8, want: []int{100}},
		{name: "small plan crosses multiple milestones", previous: 2, current: 3, total: 3, want: []int{75, 100}},
		{name: "unchanged progress", previous: 2, current: 2, total: 8},
		{name: "decreasing progress", previous: 3, current: 2, total: 8},
		{name: "unknown total", previous: 0, current: 1, total: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := goalDriftAuditCheckpoints(test.previous, test.current, test.total)
			if len(got) != len(test.want) {
				t.Fatalf("checkpoints = %#v, want %#v", got, test.want)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Fatalf("checkpoints = %#v, want %#v", got, test.want)
				}
			}
		})
	}
}
