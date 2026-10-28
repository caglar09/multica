package handler

import (
	"strings"
	"testing"
)

func TestProjectLeaderChatContractRequiresSpecificationForScopeChanges(t *testing.T) {
	for _, requirement := range []string{
		"adds, updates, or cancels planned work changes project scope",
		"MUST include a complete proposed_specification object",
		"preserve unchanged requirement IDs",
		"A proposal that only changes dependencies may omit proposed_specification",
		"goal, summary, requirements",
		"definition_of_done, and acceptance_expectations",
	} {
		if !strings.Contains(projectLeaderChatContract, requirement) {
			t.Errorf("project leader contract is missing %q", requirement)
		}
	}
}
