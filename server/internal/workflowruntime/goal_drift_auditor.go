package workflowruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/multica-ai/multica/server/internal/projectorchestration"
)

type goalReviewRequirement struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type goalReviewContext struct {
	Goal         string                  `json:"goal"`
	NonGoals     []string                `json:"non_goals"`
	Requirements []goalReviewRequirement `json:"requirements"`
}

// canonicalGoalReviewContext copies only the authoritative goal, non-goals,
// and requirement identities from the current specification.
func canonicalGoalReviewContext(spec projectorchestration.ProjectSpecification) (goalReviewContext, error) {
	if err := projectorchestration.ValidateProjectSpecification(spec); err != nil {
		return goalReviewContext{}, fmt.Errorf("validate canonical review specification: %w", err)
	}
	context := goalReviewContext{
		Goal:         spec.Goal,
		NonGoals:     append([]string{}, spec.NonGoals...),
		Requirements: make([]goalReviewRequirement, 0, len(spec.Requirements)),
	}
	for _, requirement := range spec.Requirements {
		context.Requirements = append(context.Requirements, goalReviewRequirement{
			ID:   requirement.ID,
			Text: requirement.Text,
		})
	}
	return context, nil
}

func (context goalReviewContext) promptText() (string, error) {
	encoded, err := json.Marshal(context)
	if err != nil {
		return "", fmt.Errorf("encode canonical goal review context: %w", err)
	}
	return string(encoded), nil
}

func (context goalReviewContext) requirementIDs() []string {
	ids := make([]string, 0, len(context.Requirements))
	for _, requirement := range context.Requirements {
		ids = append(ids, requirement.ID)
	}
	return ids
}

func validateGoalDriftReview(out reviewVerdictOutput, expectedIDs []string) error {
	if err := validateOptionalGoalAlignment(out); err != nil {
		return err
	}
	if out.GoalAlignmentScore == nil {
		return errors.New("goal alignment score is required for autonomous project review")
	}
	if len(expectedIDs) == 0 || len(expectedIDs) > 500 {
		return errors.New("canonical review specification requires between 1 and 500 requirement IDs")
	}
	expected := make(map[string]struct{}, len(expectedIDs))
	for _, id := range expectedIDs {
		if strings.TrimSpace(id) == "" || id != strings.TrimSpace(id) || len(id) > 120 {
			return errors.New("canonical specification contains a non-canonical requirement ID")
		}
		if _, exists := expected[id]; exists {
			return fmt.Errorf("canonical specification repeats requirement ID %q", id)
		}
		expected[id] = struct{}{}
	}
	if len(out.RequirementAttestations) != len(expected) {
		return fmt.Errorf("review must attest to all %d canonical requirements", len(expected))
	}
	for _, id := range out.RequirementAttestations {
		if _, exists := expected[id]; !exists {
			return fmt.Errorf("review attests to unknown requirement ID %q", id)
		}
	}
	return nil
}

// goalDriftAuditCheckpoints returns every quarter milestone crossed between
// two completed-node counts. Invalid or decreasing progress produces no work.
func goalDriftAuditCheckpoints(previousCompleted, currentCompleted, total int) []int {
	if total <= 0 || previousCompleted < 0 || currentCompleted < previousCompleted || currentCompleted > total {
		return nil
	}
	checkpoints := []int{25, 50, 75, 100}
	crossed := make([]int, 0, len(checkpoints))
	for _, percent := range checkpoints {
		threshold := quarterProgressThreshold(total, percent)
		if previousCompleted < threshold && currentCompleted >= threshold {
			crossed = append(crossed, percent)
		}
	}
	return crossed
}

func quarterProgressThreshold(total, percent int) int {
	whole := total / 100
	remainder := total % 100
	return whole*percent + (remainder*percent+99)/100
}
