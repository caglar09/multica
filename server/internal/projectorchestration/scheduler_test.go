package projectorchestration

import "testing"

func TestCapabilitiesCoverIsHardSupersetFence(t *testing.T) {
	if !CapabilitiesCover([]string{"backend","api","database"}, []string{"backend","api"}) {
		t.Fatal("expected complete capability set to be eligible")
	}
	if CapabilitiesCover([]string{"backend","database"}, []string{"backend","api"}) {
		t.Fatal("missing api capability must be ineligible")
	}
}
