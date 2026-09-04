package projectorchestration

import (
	"errors"
	"strings"
	"unicode"
)

var ErrNoEligibleAgent = errors.New("no eligible project agent")

// canonicalCapability collapses punctuation and conservative ontology aliases
// that the Team Planner and Project Planner can legitimately emit differently.
// This is deterministic normalization only; it is not fuzzy/LLM inference.
func canonicalCapability(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}

	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	value = strings.Trim(b.String(), "-")

	switch value {
	case "system-architecture", "solution-architecture", "software-architecture", "system-design", "architecture-design":
		return "architecture"
	case "api-contract", "api-contracts", "api-design", "rest-api", "http-api", "web-api":
		return "api"
	case "data-model", "data-modeling", "database-design", "schema-design", "schema-modeling":
		return "data-modeling"
	case "code-review", "independent-review", "peer-review", "reviewing":
		return "review"
	case "security-review", "application-security", "app-security", "appsec", "secure-code-review":
		return "security"
	case "quality-assurance", "functional-testing", "functional-test", "validation-testing":
		return "qa"
	case "end-to-end", "end-to-end-testing", "e2e-testing", "e2e-test":
		return "e2e"
	case "a11y", "wcag", "web-accessibility":
		return "accessibility"
	case "next-js", "nextjs-framework":
		return "nextjs"
	case "typescript-js", "ts":
		return "typescript"
	case "release-readiness", "release-management", "release-validation":
		return "release"
	case "ci-cd", "continuous-integration", "continuous-delivery", "continuous-deployment":
		return "cicd"
	}
	return value
}

func canonicalCapabilitySet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = canonicalCapability(value); value != "" {
			set[value] = struct{}{}
		}
	}
	return set
}

// MissingCapabilities returns canonical requirements not explicitly advertised
// by an agent. Missing entries are diagnostics/ranking evidence; they are not a
// hard rejection by themselves because role family is the backend-owned hard
// specialization boundary and the two LLM planners do not share a closed
// capability enum.
func MissingCapabilities(have, required []string) []string {
	set := canonicalCapabilitySet(have)
	missing := make([]string, 0)
	seen := make(map[string]struct{}, len(required))
	for _, value := range required {
		value = canonicalCapability(value)
		if value == "" {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		if _, ok := set[value]; !ok {
			missing = append(missing, value)
		}
	}
	return missing
}

// CapabilitiesCover answers whether a role has usable capability metadata for
// the requested work. Family matching remains the hard scheduler fence. This
// prevents independently generated capability strings (for example QA:
// "quality_assurance" vs node: "accessibility") from deadlocking an otherwise
// correctly staffed autonomous project.
func CapabilitiesCover(have, required []string) bool {
	if len(required) == 0 {
		return true
	}
	return len(canonicalCapabilitySet(have)) > 0
}

// SpecializationConfidence ranks already family-eligible candidates by the
// fraction of explicit requirements they advertise. Missing capability labels
// lower confidence but never override the hard role-family boundary.
func SpecializationConfidence(have, required []string) float64 {
	if len(required) == 0 {
		return 0.5
	}
	haveSet := canonicalCapabilitySet(have)
	if len(haveSet) == 0 {
		return 0
	}
	requiredSet := canonicalCapabilitySet(required)
	if len(requiredSet) == 0 {
		return 0.5
	}
	matched := 0
	for value := range requiredSet {
		if _, ok := haveSet[value]; ok {
			matched++
		}
	}
	if matched == 0 {
		return 0.25
	}
	confidence := float64(matched) / float64(len(requiredSet))
	if confidence < 0.25 {
		return 0.25
	}
	if confidence > 1 {
		return 1
	}
	return confidence
}
