package teamprovision

import (
	"fmt"
	"sort"
	"strings"
)

type TeamDeltaOperationType string

const (
	TeamDeltaKeepAgent   TeamDeltaOperationType = "KEEP_AGENT"
	TeamDeltaAddAgent    TeamDeltaOperationType = "ADD_AGENT"
	TeamDeltaRemoveAgent TeamDeltaOperationType = "REMOVE_AGENT"
	TeamDeltaUpdateRole  TeamDeltaOperationType = "UPDATE_ROLE"
)

type TeamDeltaOperation struct {
	Operation TeamDeltaOperationType `json:"operation"`
	Role      string                 `json:"role"`
	AgentID   string                 `json:"agent_id,omitempty"`
	Before    *RoleSpec              `json:"before,omitempty"`
	After     *RoleSpec              `json:"after,omitempty"`
	Reason    string                 `json:"reason,omitempty"`
}

type TeamDelta struct {
	Operations       []TeamDeltaOperation `json:"operations"`
	RequiresApproval bool                 `json:"requires_approval"`
	AddOnly          bool                 `json:"add_only"`
}

type CapabilityRequirement struct {
	Family       string   `json:"family"`
	Capabilities []string `json:"capabilities,omitempty"`
	Reason       string   `json:"reason,omitempty"`
}

type TeamImpact struct {
	ExistingTeamSufficient bool                    `json:"existing_team_sufficient"`
	Missing                []CapabilityRequirement `json:"missing,omitempty"`
}

// BuildControlledTeamDelta deliberately does not infer removals. Replan Team
// remains add-mostly unless a separate explicit REMOVE_AGENT mutation is approved.
func BuildControlledTeamDelta(current, desired Plan) TeamDelta {
	currentByRole := make(map[string]RoleSpec, len(current.Roles))
	desiredByRole := make(map[string]RoleSpec, len(desired.Roles))
	for _, role := range current.Roles { currentByRole[role.Role] = role }
	for _, role := range desired.Roles { desiredByRole[role.Role] = role }
	roles := make([]string, 0, len(currentByRole)+len(desiredByRole))
	seen := map[string]struct{}{}
	for role := range currentByRole { seen[role] = struct{}{}; roles = append(roles, role) }
	for role := range desiredByRole { if _, ok := seen[role]; !ok { roles = append(roles, role) } }
	sort.Strings(roles)
	delta := TeamDelta{AddOnly: true}
	for _, name := range roles {
		before, existed := currentByRole[name]
		after, desiredExists := desiredByRole[name]
		switch {
		case existed && !desiredExists:
			b := before
			delta.Operations = append(delta.Operations, TeamDeltaOperation{Operation: TeamDeltaKeepAgent, Role: name, Before: &b, After: &b, Reason: "add-only reconciliation retains an existing project agent"})
		case !existed && desiredExists:
			a := after
			delta.Operations = append(delta.Operations, TeamDeltaOperation{Operation: TeamDeltaAddAgent, Role: name, After: &a, Reason: "new capability is not covered by the current project team"})
		case existed && desiredExists && roleMateriallyChanged(before, after):
			b, a := before, after
			delta.Operations = append(delta.Operations, TeamDeltaOperation{Operation: TeamDeltaUpdateRole, Role: name, Before: &b, After: &a, Reason: "existing role metadata/capabilities changed without replacing the agent"})
		default:
			b, a := before, after
			delta.Operations = append(delta.Operations, TeamDeltaOperation{Operation: TeamDeltaKeepAgent, Role: name, Before: &b, After: &a})
		}
	}
	return delta
}

func ValidateExplicitTeamDelta(delta TeamDelta) (TeamDelta, error) {
	seen := map[string]struct{}{}
	for i := range delta.Operations {
		op := &delta.Operations[i]
		op.Role = strings.TrimSpace(op.Role)
		if op.Role == "" { return TeamDelta{}, fmt.Errorf("team delta operation %d has no role", i) }
		identity := string(op.Operation)+"\x00"+op.Role
		if _, exists := seen[identity]; exists { return TeamDelta{}, fmt.Errorf("duplicate team delta operation %s for %s", op.Operation, op.Role) }
		seen[identity] = struct{}{}
		switch op.Operation {
		case TeamDeltaKeepAgent:
			if op.Before == nil { return TeamDelta{}, fmt.Errorf("KEEP_AGENT %s requires before role", op.Role) }
		case TeamDeltaAddAgent:
			if op.After == nil { return TeamDelta{}, fmt.Errorf("ADD_AGENT %s requires after role", op.Role) }
		case TeamDeltaUpdateRole:
			if op.Before == nil || op.After == nil { return TeamDelta{}, fmt.Errorf("UPDATE_ROLE %s requires before and after role", op.Role) }
		case TeamDeltaRemoveAgent:
			if op.Before == nil { return TeamDelta{}, fmt.Errorf("REMOVE_AGENT %s requires before role", op.Role) }
			delta.RequiresApproval, delta.AddOnly = true, false
		default:
			return TeamDelta{}, fmt.Errorf("unsupported team delta operation %q", op.Operation)
		}
	}
	return delta, nil
}

// AnalyzeCapabilityImpact is side-effect free. If it reports sufficient=true,
// a plan mutation must not silently invoke full team reprovisioning.
func AnalyzeCapabilityImpact(current Plan, required []CapabilityRequirement) TeamImpact {
	impact := TeamImpact{ExistingTeamSufficient: true}
	for _, requirement := range required {
		requirement.Family = strings.TrimSpace(strings.ToLower(requirement.Family))
		if requirement.Family == "" { continue }
		covered := false
		for _, role := range current.Roles {
			if strings.ToLower(strings.TrimSpace(role.Family)) == requirement.Family && capabilitiesContain(role.Capabilities, requirement.Capabilities) { covered = true; break }
		}
		if !covered { impact.ExistingTeamSufficient = false; impact.Missing = append(impact.Missing, requirement) }
	}
	return impact
}

func roleMateriallyChanged(a, b RoleSpec) bool {
	return a.Family != b.Family || a.DisplayName != b.DisplayName || a.Description != b.Description || !sameStrings(a.Capabilities, b.Capabilities) || !sameStrings(a.Responsibilities, b.Responsibilities)
}

func capabilitiesContain(have, need []string) bool {
	set := make(map[string]struct{}, len(have))
	for _, item := range have { set[strings.ToLower(strings.TrimSpace(item))] = struct{}{} }
	for _, item := range need { if _, ok := set[strings.ToLower(strings.TrimSpace(item))]; !ok { return false } }
	return true
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) { return false }
	ac, bc := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(ac); sort.Strings(bc)
	for i := range ac { if ac[i] != bc[i] { return false } }
	return true
}
