package projectorchestration

import (
	"errors"
	"strings"
)

var ErrNoEligibleAgent = errors.New("no eligible project agent")

// CapabilitiesCover is the Phase 2 hard eligibility fence. A scheduler may
// rank only candidates that reach this function's true branch.
func CapabilitiesCover(have, required []string) bool {
	if len(required) == 0 {
		return true
	}
	set := make(map[string]struct{}, len(have))
	for _, value := range have {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			set[value] = struct{}{}
		}
	}
	for _, value := range required {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, ok := set[value]; !ok {
			return false
		}
	}
	return true
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
		if value = strings.ToLower(strings.TrimSpace(value)); value != "" {
			unique[value] = struct{}{}
		}
	}
	if len(unique) == 0 {
		return 0
	}
	confidence := float64(len(required)) / float64(len(unique))
	if confidence > 1 {
		return 1
	}
	if confidence < 0.25 {
		return 0.25
	}
	return confidence
}
