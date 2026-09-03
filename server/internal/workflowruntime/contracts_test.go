package workflowruntime

import (
	"encoding/json"
	"strings"
	"testing"
)

func wrappedOutput(value string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"output": value})
	return raw
}

func TestStructuredReviewVerdictRejectsFreeText(t *testing.T) {
	if _, err := parseReviewVerdict(wrappedOutput("Looks good to me.")); err == nil {
		t.Fatal("expected free-text review output to be rejected")
	}
}

func TestStructuredReviewVerdictRequiresBlockingFindingForChanges(t *testing.T) {
	raw := `{"verdict":"changes_requested","summary":"needs work","findings":[{"id":"F-1","severity":"medium","category":"correctness","description":"wrong result","evidence":"test fails","blocking":false}]}`
	if _, err := parseReviewVerdict(wrappedOutput(raw)); err == nil {
		t.Fatal("expected changes_requested without blocking finding to fail")
	}
}

func TestStructuredReviewVerdictApprovedRejectsBlockingFinding(t *testing.T) {
	raw := `{"verdict":"approved","summary":"ok","findings":[{"id":"F-1","severity":"low","category":"quality","description":"minor","evidence":"line 10","blocking":true}]}`
	if _, err := parseReviewVerdict(wrappedOutput(raw)); err == nil {
		t.Fatal("expected approved verdict with blocking finding to fail")
	}
}

func TestImplementationHandoffContract(t *testing.T) {
	raw := `{"summary":"implemented","decisions":["kept API stable"],"artifacts":[{"type":"diff","ref":"git","description":"implementation"}],"changed_files":["server/a.go"],"commit_sha":"abc1234","diff":"server/a.go changed","tests":[{"name":"go test ./...","status":"passed","evidence":"ok"}],"findings":[],"risks":[],"blockers":[]}`
	out, err := parseImplementationHandoff(wrappedOutput(raw))
	if err != nil {
		t.Fatalf("parse implementation handoff: %v", err)
	}
	if out.Summary != "implemented" || len(out.Tests) != 1 {
		t.Fatalf("unexpected handoff: %#v", out)
	}
}

func TestWorkflowDefinitionProjectsInProgressStatus(t *testing.T) {
	def := definition()
	actions := def.States["in_progress"].OnEnter
	if len(actions) < 2 || actions[0].Type != "set_issue_status" || actions[0].Params["status"] != "in_progress" {
		t.Fatalf("in_progress must project backend-owned status before dispatch: %#v", actions)
	}
}

func TestWorkflowDefinitionUsesStructuredVerdictContract(t *testing.T) {
	def := definition()
	if def.Version < 2 {
		t.Fatalf("workflow version = %d, want >= 2", def.Version)
	}
	review := def.States["in_review"].OnEnter
	joined := ""
	for _, action := range review {
		joined += action.Params["instructions"]
	}
	if !strings.Contains(joined, "\"verdict\"") {
		t.Fatalf("review instructions do not require a verdict JSON contract: %q", joined)
	}
	if strings.Contains(strings.ToLower(joined), "move the issue to") {
		t.Fatalf("review instructions still use issue status as verdict signalling: %q", joined)
	}
}
