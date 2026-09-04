package projectorchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// DirectContextCompilation is the durable receipt for a Project OS stage task
// that is dispatched directly by the scheduler rather than by a durable
// autonomous_workflow_action. workflow_action_id intentionally remains NULL for
// these receipts; task_id is bound after normal task admission succeeds.
type DirectContextCompilation struct {
	ID      pgtype.UUID
	Package ContextPackage
}

// CompileDirectProjectContext applies the same bounded, governed context policy
// used by issue-workflow actions to Project OS stages that are dispatched
// directly (research/product/architecture/design/review/qa/security/release,
// etc.). It is fail-closed and persists a context compilation receipt before
// task admission.
func CompileDirectProjectContext(
	ctx context.Context,
	tx pgx.Tx,
	issue db.Issue,
	targetAgentID pgtype.UUID,
	handoffKind string,
) (DirectContextCompilation, error) {
	if tx == nil || !issue.ProjectID.Valid {
		return DirectContextCompilation{}, errors.New("direct project context compiler requires transaction and project")
	}

	plan, err := loadContextPlanState(ctx, tx, issue)
	if err != nil {
		return DirectContextCompilation{}, err
	}
	roleFamily := contextRoleFamily(handoffKind, plan.RequiredRoleFamily, plan.NodeKind)
	// Direct review/incident stages do not carry the issue-workflow handoff kind
	// that normally disambiguates the role. Keep the package role-aware even when
	// the planner omitted required_role_family.
	if strings.TrimSpace(plan.RequiredRoleFamily) == "" {
		switch plan.NodeKind {
		case NodeReview:
			roleFamily = "review"
		case NodeIncident:
			roleFamily = "sre"
		}
	}

	repositoryRevision, err := currentContextRepositoryRevision(ctx, tx, issue.WorkspaceID, issue.ProjectID)
	if err != nil {
		return DirectContextCompilation{}, err
	}
	if err := refreshContextMemoryFreshness(ctx, tx, issue.WorkspaceID, issue.ProjectID, repositoryRevision); err != nil {
		return DirectContextCompilation{}, err
	}

	brainRevision := int64(0)
	err = tx.QueryRow(ctx, `
		SELECT revision
		FROM autonomous_project_brain_state
		WHERE project_id=$1 AND workspace_id=$2
	`, issue.ProjectID, issue.WorkspaceID).Scan(&brainRevision)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return DirectContextCompilation{}, err
	}

	items := map[string][]ContextItem{
		"specification":         {},
		"policy":                {},
		"brain":                 {},
		"predecessor_artifacts": {},
		"structured_handoffs":   {},
		"unresolved_findings":   {},
		"repository_facts":      {},
	}
	items["specification"] = append(items["specification"], ContextItem{
		Source:    "project_specification",
		Ref:       util.UUIDToString(plan.PlanID),
		Type:      "authoritative_specification",
		Authority: string(AuthorityAuthoritativeSpec),
		Text:      fmt.Sprintf("Goal: %s\nSpecification: %s", plan.Goal, strings.TrimSpace(string(plan.Specification))),
	})
	items["policy"] = append(items["policy"], ContextItem{
		Source:    "project_policy",
		Ref:       util.UUIDToString(plan.PlanID),
		Type:      "policy",
		Authority: string(AuthoritySystemDerived),
		Text:      strings.TrimSpace(string(plan.Policy)),
	})

	query := strings.TrimSpace(plan.NodeTitle)
	if query == "" {
		query = strings.TrimSpace(issue.Title)
	}
	memories, err := recallContextMemories(
		ctx, tx, issue.WorkspaceID, issue.ProjectID, query, roleFamily, repositoryRevision,
	)
	if err != nil {
		return DirectContextCompilation{}, err
	}
	for _, memory := range memories {
		section := "brain"
		if memory.Type == "repository_fact" {
			section = "repository_facts"
		}
		items[section] = append(items[section], ContextItem{
			Source:    "brain",
			Ref:       memory.ID,
			Type:      memory.Type,
			Authority: memory.Authority,
			Text:      strings.TrimSpace(memory.Subject + "\n" + memory.Content),
		})
	}

	artifacts, err := contextPredecessorArtifacts(ctx, tx, issue, plan)
	if err != nil {
		return DirectContextCompilation{}, err
	}
	items["predecessor_artifacts"] = append(items["predecessor_artifacts"], artifacts...)

	// Use a valid sentinel that cannot be a real generated action UUID so the
	// shared handoff query includes all existing handoffs for direct stage work.
	sentinelActionID := pgtype.UUID{Valid: true}
	handoffs, err := contextStructuredHandoffs(ctx, tx, issue, plan, sentinelActionID)
	if err != nil {
		return DirectContextCompilation{}, err
	}
	items["structured_handoffs"] = append(items["structured_handoffs"], handoffs...)

	findings, err := contextOpenFindings(ctx, tx, issue)
	if err != nil {
		return DirectContextCompilation{}, err
	}
	items["unresolved_findings"] = append(items["unresolved_findings"], findings...)

	pkg := NewBoundedContext(
		roleFamily,
		plan.NodeKind,
		plan.NodeKey,
		plan.PlanRevision,
		brainRevision,
		repositoryRevision,
		items,
	)
	if pkg.UsedTokens > pkg.TotalTokenBudget {
		return DirectContextCompilation{}, fmt.Errorf(
			"compiled direct context exceeds hard token budget: %d > %d",
			pkg.UsedTokens, pkg.TotalTokenBudget,
		)
	}

	snapshotID, err := ensureContextBrainSnapshot(ctx, tx, issue, plan, brainRevision)
	if err != nil {
		return DirectContextCompilation{}, err
	}
	rawPackage, err := json.Marshal(pkg)
	if err != nil {
		return DirectContextCompilation{}, err
	}
	sectionUsage := make(map[string]int, len(pkg.Sections))
	for _, section := range pkg.Sections {
		sectionUsage[section.Name] = section.UsedTokens
	}
	rawUsage, err := json.Marshal(sectionUsage)
	if err != nil {
		return DirectContextCompilation{}, err
	}

	var compilationID pgtype.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO autonomous_project_context_compilation (
			workspace_id,project_id,plan_id,node_id,issue_id,
			role_family,total_token_budget,used_tokens,section_usage,context_package,
			brain_snapshot_id
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		RETURNING id
	`, issue.WorkspaceID, issue.ProjectID, plan.PlanID, nullableContextUUID(plan.NodeID), issue.ID,
		roleFamily, pkg.TotalTokenBudget, pkg.UsedTokens, rawUsage, rawPackage,
		nullableContextUUID(snapshotID)).Scan(&compilationID)
	if err != nil {
		return DirectContextCompilation{}, fmt.Errorf("persist direct project context: %w", err)
	}

	_ = targetAgentID // reserved for future skill/tool-aware context policy.
	return DirectContextCompilation{ID: compilationID, Package: pkg}, nil
}

// BindDirectContextTask associates a direct Project OS compilation receipt with
// the admitted task. A single task may only own one context compilation.
func BindDirectContextTask(ctx context.Context, tx pgx.Tx, compilationID, taskID pgtype.UUID) error {
	if tx == nil || !compilationID.Valid || !taskID.Valid {
		return nil
	}
	_, err := tx.Exec(ctx, `
		UPDATE autonomous_project_context_compilation
		SET task_id=$2
		WHERE id=$1 AND workflow_action_id IS NULL AND (task_id IS NULL OR task_id=$2)
	`, compilationID, taskID)
	return err
}
