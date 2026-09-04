package projectorchestration

import (
	"reflect"
	"testing"
)

func TestCapabilitiesCoverUsesFamilyAsHardFenceAndCapabilityAsMetadata(t *testing.T) {
	if !CapabilitiesCover([]string{"architecture", "system_design"}, []string{"api-contract", "data-modeling"}) {
		t.Fatal("family-eligible role with capability metadata should remain schedulable")
	}
	if CapabilitiesCover(nil, []string{"api"}) {
		t.Fatal("role with no capability metadata should remain ineligible for capability-requiring work")
	}
	if !CapabilitiesCover(nil, nil) {
		t.Fatal("work without capability requirements should remain eligible")
	}
}

func TestCapabilitiesNormalizePlannerVocabulary(t *testing.T) {
	have := []string{"System Design", "schema-design", "secure code review", "A11Y", "release-readiness"}
	required := []string{"software-architecture", "data_modeling", "security-review", "web-accessibility", "release-management"}
	if missing := MissingCapabilities(have, required); len(missing) != 0 {
		t.Fatalf("expected planner vocabulary aliases to normalize; missing=%v", missing)
	}
}

func TestMissingCapabilitiesReturnsCanonicalOperatorDiagnostics(t *testing.T) {
	missing := MissingCapabilities(
		[]string{"system-design", "api-design"},
		[]string{"architecture", "api-contract", "data-model", "accessibility"},
	)
	want := []string{"data-modeling", "accessibility"}
	if !reflect.DeepEqual(missing, want) {
		t.Fatalf("MissingCapabilities() = %v, want %v", missing, want)
	}
}

func TestSpecializationConfidenceRanksExplicitCoverage(t *testing.T) {
	strong := SpecializationConfidence([]string{"architecture", "api", "data-modeling"}, []string{"architecture", "api"})
	weak := SpecializationConfidence([]string{"architecture"}, []string{"architecture", "api"})
	if strong <= weak {
		t.Fatalf("expected explicit capability coverage to rank higher: strong=%v weak=%v", strong, weak)
	}
}
