package projectorchestration

import (
	"reflect"
	"testing"
)

func TestCapabilitiesCoverIsCanonicalHardSupersetFence(t *testing.T) {
	if !CapabilitiesCover([]string{"backend", "api-design", "database"}, []string{"backend", "api-contract"}) {
		t.Fatal("expected canonical api aliases to satisfy the hard capability fence")
	}
	if CapabilitiesCover([]string{"backend", "database"}, []string{"backend", "api"}) {
		t.Fatal("missing api capability must remain ineligible")
	}
}

func TestCapabilitiesCoverNormalizesPlannerVocabulary(t *testing.T) {
	have := []string{
		"System Design",
		"schema-design",
		"secure code review",
		"A11Y",
		"release-readiness",
	}
	required := []string{
		"software-architecture",
		"data_modeling",
		"security-review",
		"web-accessibility",
		"release-management",
	}
	if !CapabilitiesCover(have, required) {
		t.Fatalf("expected planner vocabulary aliases to match; missing=%v", MissingCapabilities(have, required))
	}
}

func TestMissingCapabilitiesReturnsCanonicalOperatorDiagnostics(t *testing.T) {
	missing := MissingCapabilities(
		[]string{"system-design", "api-design"},
		[]string{"architecture", "api-contract", "data-model"},
	)
	want := []string{"data-modeling"}
	if !reflect.DeepEqual(missing, want) {
		t.Fatalf("MissingCapabilities() = %v, want %v", missing, want)
	}
}
