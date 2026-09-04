package projectorchestration

import "unicode/utf8"

const (
	ContextSchemaVersion = 2
	DefaultContextBudget = 2800
)

var DefaultContextSectionBudgets = map[string]int{
	"specification":        450,
	"policy":               250,
	"brain":                700,
	"predecessor_artifacts": 450,
	"structured_handoffs":  400,
	"unresolved_findings":  300,
	"repository_facts":     250,
}

type ContextItem struct {
	Source    string `json:"source"`
	Ref       string `json:"ref,omitempty"`
	Type      string `json:"type,omitempty"`
	Authority string `json:"authority,omitempty"`
	Text      string `json:"text"`
}

type ContextSection struct {
	Name        string        `json:"name"`
	TokenBudget int           `json:"token_budget"`
	UsedTokens  int           `json:"used_tokens"`
	Omitted     int           `json:"omitted"`
	Items       []ContextItem `json:"items"`
}

type ContextPackage struct {
	SchemaVersion      int              `json:"schema_version"`
	TotalTokenBudget   int              `json:"total_token_budget"`
	UsedTokens         int              `json:"used_tokens"`
	RoleFamily         string           `json:"role_family,omitempty"`
	NodeKind           NodeKind         `json:"node_kind,omitempty"`
	NodeKey            string           `json:"node_key,omitempty"`
	PlanRevision       int64            `json:"plan_revision,omitempty"`
	BrainRevision      int64            `json:"brain_revision,omitempty"`
	RepositoryRevision string           `json:"repository_revision,omitempty"`
	Sections           []ContextSection `json:"sections"`
}

func EstimateContextTokens(value string) int {
	runes := utf8.RuneCountInString(value)
	if runes == 0 {
		return 0
	}
	return (runes + 3) / 4
}

func ClipContextText(value string, maxTokens int) string {
	if maxTokens <= 0 || value == "" {
		return ""
	}
	runes := []rune(value)
	maxRunes := maxTokens * 4
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes])
}

// NewBoundedContext deterministically packs already-ranked source items into
// fixed section budgets. It never borrows between sections: one noisy source
// cannot starve specification, policy or findings of their reserved context.
func NewBoundedContext(
	roleFamily string,
	nodeKind NodeKind,
	nodeKey string,
	planRevision, brainRevision int64,
	repositoryRevision string,
	items map[string][]ContextItem,
) ContextPackage {
	order := []string{
		"specification", "policy", "brain", "predecessor_artifacts",
		"structured_handoffs", "unresolved_findings", "repository_facts",
	}
	pkg := ContextPackage{
		SchemaVersion: ContextSchemaVersion,
		TotalTokenBudget: DefaultContextBudget,
		RoleFamily: roleFamily,
		NodeKind: nodeKind,
		NodeKey: nodeKey,
		PlanRevision: planRevision,
		BrainRevision: brainRevision,
		RepositoryRevision: repositoryRevision,
		Sections: make([]ContextSection, 0, len(order)),
	}
	for _, name := range order {
		budget := DefaultContextSectionBudgets[name]
		section := ContextSection{Name: name, TokenBudget: budget, Items: []ContextItem{}}
		for _, item := range items[name] {
			remaining := budget - section.UsedTokens
			if remaining <= 0 {
				section.Omitted++
				continue
			}
			needed := EstimateContextTokens(item.Text)
			if needed <= 0 {
				continue
			}
			if needed > remaining {
				item.Text = ClipContextText(item.Text, remaining)
				needed = EstimateContextTokens(item.Text)
				section.Omitted++
			}
			if needed <= 0 {
				continue
			}
			section.Items = append(section.Items, item)
			section.UsedTokens += needed
		}
		pkg.UsedTokens += section.UsedTokens
		pkg.Sections = append(pkg.Sections, section)
	}
	if pkg.UsedTokens > pkg.TotalTokenBudget {
		// This is an invariant guard, not a normal path: section budgets sum to
		// TotalTokenBudget. Keeping the total explicit makes future budget edits
		// fail visibly in tests instead of silently expanding prompts.
		pkg.UsedTokens = pkg.TotalTokenBudget
	}
	return pkg
}
