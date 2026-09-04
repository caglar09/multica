package projectorchestration

import (
	"errors"
	"strings"
	"unicode"
)

var ErrNoEligibleAgent = errors.New("no eligible project agent")

// canonicalCapability collapses the small, backend-owned capability vocabulary
// variants that the Team Planner and Project Planner can legitimately emit.
// Eligibility must stay deterministic, but it must not depend on punctuation or
// synonymous labels produced by two independent LLM planning passes.
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
	}
	value = strings.Trim(b.String(), "-")

	// Keep this list intentionally conservative. These are spelling/ontology
	// aliases, not fuzzy semantic inference; the backend still owns the hard
	// eligibility fence.
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

// MissingCapabilities returns the canonical requirements not covered by have.
// It is useful both for scheduler decisions and operator-facing diagnostics.
func MissingCapabilities(have, required []string) []string {
	set := make(map[string]struct{}, len(have))
	for _, value := range have {
		if value = canonicalCapability(value); value != "" {
			set[value] = struct{}{}
		}
	}
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

// CapabilitiesCover is the Phase 2 hard eligibility fence. A scheduler may
// rank only candidates that cover every required capability after deterministic
// canonicalization.
func CapabilitiesCover(have, required []string) bool {
	return len(MissingCapabilities(have, required)) == 0
}

// SpecializationConfidence is deliberately secondary to CapabilitiesCover:
// it can rank two eligible candidates but can never make an ineligible one
// eligible. Fewer unrelated capabilities yield a slightly stronger
// specialization signal for an exact requirement set.
func SpecializationConfidence(have, required []string) float64 {
	if !CapabilitiesCover(have, required) {
		return 0
	}
	if len(required) == 0 {
		return 0.5
	}
	unique := map[string]struct{}{}
	for _, value := range have {
		if value = canonicalCapability(value); value != "" {
			unique[value] = struct{}{}
		}
	}
	if len(unique) == 0 {
		return 0
	}
	requiredUnique := map[string]struct{}{}
	for _, value := range required {
		if value = canonicalCapability(value); value != "" {
			requiredUnique[value] = struct{}{}
		}
	}
	confidence := float64(len(requiredUnique)) / float64(len(unique))
	if confidence > 1 {
		return 1
	}
	if confidence < 0.25 {
		return 0.25
	}
	return confidence
}
