package projectorchestration

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBoundedContextHonorsHardAndSectionBudgets(t *testing.T) {
	totalSections := 0
	for name, budget := range DefaultContextSectionBudgets {
		if budget <= 0 {
			t.Fatalf("section %q has invalid budget %d", name, budget)
		}
		totalSections += budget
	}
	if totalSections != DefaultContextBudget {
		t.Fatalf("section budgets sum to %d, hard budget is %d", totalSections, DefaultContextBudget)
	}

	items := map[string][]ContextItem{}
	for name := range DefaultContextSectionBudgets {
		items[name] = []ContextItem{{Source: name, Text: strings.Repeat("x", DefaultContextBudget*8)}}
	}
	pkg := NewBoundedContext("qa", NodeQA, "qa-1", 7, 11, "repo-sha", items)
	if pkg.TotalTokenBudget != DefaultContextBudget {
		t.Fatalf("total budget = %d, want %d", pkg.TotalTokenBudget, DefaultContextBudget)
	}
	if pkg.UsedTokens > pkg.TotalTokenBudget {
		t.Fatalf("context used %d tokens above hard budget %d", pkg.UsedTokens, pkg.TotalTokenBudget)
	}
	for _, section := range pkg.Sections {
		if section.UsedTokens > section.TokenBudget {
			t.Fatalf("section %q used %d tokens above budget %d", section.Name, section.UsedTokens, section.TokenBudget)
		}
	}
	if pkg.RoleFamily != "qa" || pkg.NodeKind != NodeQA || pkg.PlanRevision != 7 || pkg.BrainRevision != 11 {
		t.Fatalf("context identity metadata was not preserved: %#v", pkg)
	}
}

func TestSemanticMemoryFingerprintCompactsEquivalentEvidence(t *testing.T) {
	a := MemoryCandidate{
		Type:    "fact",
		Subject: "Stripe support",
		Content: json.RawMessage(`{"country":"TR","supported":false}`),
	}
	b := MemoryCandidate{
		Type:    "FACT",
		Subject: "  Stripe support  ",
		Content: json.RawMessage(`{ "country" : "TR", "supported" : false }`),
	}
	if semanticMemoryFingerprint(a) != semanticMemoryFingerprint(b) {
		t.Fatal("semantically equivalent normalized evidence should share a fingerprint")
	}
}

func TestBrainImpactClassification(t *testing.T) {
	tests := []struct {
		name      string
		candidate MemoryCandidate
		retention MemoryRetention
		want      BrainImpactClassification
	}{
		{
			name:      "low value fact is advisory",
			candidate: MemoryCandidate{Type: "fact", Importance: 0.20},
			want:      BrainImpactNone,
		},
		{
			name:      "repository constraint can require node review",
			candidate: MemoryCandidate{Type: "repository_fact", Importance: 0.75},
			want:      BrainImpactMinor,
		},
		{
			name:      "important requirement requires replan proposal",
			candidate: MemoryCandidate{Type: "requirement", Importance: 0.90},
			want:      BrainImpactMajor,
		},
		{
			name:      "contradiction is major regardless of type",
			candidate: MemoryCandidate{Type: "lesson", Importance: 0.10},
			retention: MemoryRetention{Conflict: true, GovernanceState: "conflicted"},
			want:      BrainImpactMajor,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyBrainImpact(tt.candidate, tt.retention); got != tt.want {
				t.Fatalf("classification = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAuthorityOrderingKeepsUserAndSpecAboveInference(t *testing.T) {
	if authorityRank(AuthorityUserDecision) <= authorityRank(AuthorityAuthoritativeSpec) {
		t.Fatal("user decision must outrank authoritative specification")
	}
	if authorityRank(AuthorityAuthoritativeSpec) <= authorityRank(AuthorityDeterministicObservation) {
		t.Fatal("authoritative specification must outrank deterministic observation")
	}
	if authorityRank(AuthorityDeterministicObservation) <= authorityRank(AuthorityAgentInference) {
		t.Fatal("deterministic repository observation must outrank agent inference")
	}
}
