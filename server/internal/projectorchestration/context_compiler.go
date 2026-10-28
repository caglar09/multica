package projectorchestration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var (
	ErrContextPlanNotFound           = errors.New("active autonomous project plan not found for workflow context")
	ErrAuthoritativeContextTruncated = errors.New("authoritative context truncated")
)

type rankedContextMemory struct {
	ID           string
	CanonicalKey string
	Type         string
	Subject      string
	Content      string
	Authority    string
}

type contextPlanState struct {
	PlanID                 pgtype.UUID
	PlanRevision           int64
	Goal                   string
	Specification          []byte
	Policy                 []byte
	NodeID                 pgtype.UUID
	NodeKey                string
	NodeKind               NodeKind
	NodeTitle              string
	NodeDescription        string
	NodeAcceptanceCriteria string
	RequiredRoleFamily     string
}

func authoritativeSpecificationItems(goal string, raw []byte, planID string) ([]ContextItem, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, fmt.Errorf("%w: specification section is empty", ErrAuthoritativeContextTruncated)
	}

	var specification any
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	if err := decoder.Decode(&specification); err != nil {
		return nil, fmt.Errorf("decode project specification: %w", err)
	}

	goal = strings.TrimSpace(goal)
	summaryParts := make([]string, 0, 3)
	if goal != "" {
		summaryParts = append(summaryParts, "Goal: "+goal)
	}
	if fields, ok := specification.(map[string]any); ok {
		if specGoal, ok := fields["goal"].(string); ok && strings.TrimSpace(specGoal) != "" {
			specGoal = strings.TrimSpace(specGoal)
			if goal == "" {
				summaryParts = append(summaryParts, "Goal: "+specGoal)
				delete(fields, "goal")
			} else if specGoal == goal {
				delete(fields, "goal")
			} else {
				summaryParts = append(summaryParts, "Specification goal: "+specGoal)
			}
		}
		if text, ok := fields["summary"].(string); ok && strings.TrimSpace(text) != "" {
			summaryParts = append(summaryParts, "Summary: "+strings.TrimSpace(text))
			delete(fields, "summary")
		}
	}
	if len(summaryParts) == 0 {
		return nil, fmt.Errorf("%w: specification has no goal or authored summary", ErrAuthoritativeContextTruncated)
	}

	items := []ContextItem{{
		Source: "project_specification", Ref: planID,
		Type: "authoritative_specification_summary", Authority: string(AuthorityAuthoritativeSpec),
		Text: strings.Join(summaryParts, "\n"),
	}}
	if err := appendSpecificationDetails(&items, planID, "", specification, 0); err != nil {
		return nil, err
	}
	return items, nil
}

func appendSpecificationDetails(items *[]ContextItem, planID, path string, value any, depth int) error {
	if depth > 64 {
		return fmt.Errorf("%w: specification hierarchy exceeds 64 levels", ErrAuthoritativeContextTruncated)
	}
	appendItem := func(ref, text string) {
		if strings.TrimSpace(text) == "" {
			return
		}
		*items = append(*items, ContextItem{
			Source: "project_specification", Ref: planID + ":" + ref,
			Type: "authoritative_specification_detail", Authority: string(AuthorityAuthoritativeSpec),
			Text: text,
		})
	}
	displayPath := path
	if displayPath == "" {
		displayPath = "Specification"
	}

	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			if err := appendSpecificationDetails(items, planID, childPath, typed[key], depth+1); err != nil {
				return err
			}
		}
	case []any:
		for index, child := range typed {
			childPath := fmt.Sprintf("%s[%d]", path, index)
			if path == "requirements" {
				if requirement, ok := child.(map[string]any); ok {
					if id, ok := requirement["id"].(string); ok && strings.TrimSpace(id) != "" {
						childPath = "requirements[" + strings.TrimSpace(id) + "]"
					}
				}
				encoded, err := json.Marshal(child)
				if err != nil {
					return fmt.Errorf("encode specification requirement %d: %w", index, err)
				}
				appendItem(childPath, childPath+": "+string(encoded))
				continue
			}
			if err := appendSpecificationDetails(items, planID, childPath, child, depth+1); err != nil {
				return err
			}
		}
	case string:
		appendItem(path, displayPath+": "+typed)
	case json.Number, bool, float64:
		appendItem(path, displayPath+": "+fmt.Sprint(typed))
	case nil:
		return nil
	default:
		return fmt.Errorf("unsupported project specification value at %q: %T", path, value)
	}
	return nil
}

func validateAuthoritativeContext(pkg ContextPackage) error {
	seen := map[string]bool{}
	for _, section := range pkg.Sections {
		if section.Name != "specification" && section.Name != "policy" {
			continue
		}
		seen[section.Name] = true
		if section.Omitted > 0 {
			return fmt.Errorf("%w: %s section was truncated (omitted items > 0)", ErrAuthoritativeContextTruncated, section.Name)
		}
		if len(section.Items) == 0 || section.UsedTokens == 0 {
			return fmt.Errorf("%w: required %s section has no retained content", ErrAuthoritativeContextTruncated, section.Name)
		}
	}
	for _, name := range []string{"specification", "policy"} {
		if !seen[name] {
			return fmt.Errorf("%w: required %s section is missing", ErrAuthoritativeContextTruncated, name)
		}
	}
	return nil
}

func validateContextTokenBudget(pkg ContextPackage) error {
	if pkg.TotalTokenBudget < 0 || pkg.UsedTokens < 0 || pkg.UsedTokens > pkg.TotalTokenBudget {
		return fmt.Errorf("context exceeds hard token budget: used %d, total %d", pkg.UsedTokens, pkg.TotalTokenBudget)
	}
	return nil
}

// CompileWorkflowContext is the Phase 3 fail-closed context compiler used by
// project-bound autonomous workflow dispatch. It runs in the caller's
// transaction, so the durable context receipt and the handoff envelope always
// describe the same Brain/plan snapshot before a task can be admitted.
func CompileWorkflowContext(
	ctx context.Context,
	tx pgx.Tx,
	issue db.Issue,
	targetAgentID pgtype.UUID,
	workflowActionID pgtype.UUID,
	handoffKind string,
) (ContextPackage, error) {
	if tx == nil || !issue.ProjectID.Valid || !workflowActionID.Valid {
		return ContextPackage{}, errors.New("project workflow context compiler requires transaction, project, and workflow action")
	}

	// A reclaimed workflow action must reuse the exact package that was compiled
	// on its first admission attempt. New Brain revisions must not silently
	// change an already-durable action's prompt.
	var existingRaw []byte
	err := tx.QueryRow(ctx, `
		SELECT context_package
		FROM autonomous_project_context_compilation
		WHERE workflow_action_id=$1
	`, workflowActionID).Scan(&existingRaw)
	if err == nil {
		var existing ContextPackage
		if err := json.Unmarshal(existingRaw, &existing); err != nil {
			return ContextPackage{}, fmt.Errorf("decode persisted workflow context: %w", err)
		}
		if err := validateAuthoritativeContext(existing); err != nil {
			return ContextPackage{}, fmt.Errorf("validate persisted workflow context: %w", err)
		}
		if err := validateContextTokenBudget(existing); err != nil {
			return ContextPackage{}, fmt.Errorf("validate persisted workflow context: %w", err)
		}
		if err := attachContextToHandoff(ctx, tx, workflowActionID, existing); err != nil {
			return ContextPackage{}, err
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ContextPackage{}, err
	}

	plan, err := loadContextPlanState(ctx, tx, issue)
	if err != nil {
		return ContextPackage{}, err
	}
	specificationItems, err := authoritativeSpecificationItems(plan.Goal, plan.Specification, util.UUIDToString(plan.PlanID))
	if err != nil {
		return ContextPackage{}, err
	}
	policy := strings.TrimSpace(string(plan.Policy))
	if policy == "" || policy == "null" {
		return ContextPackage{}, fmt.Errorf("%w: policy section is empty", ErrAuthoritativeContextTruncated)
	}
	roleFamily := contextRoleFamily(handoffKind, plan.RequiredRoleFamily, plan.NodeKind)
	repositoryRevision, err := currentContextRepositoryRevision(ctx, tx, issue.WorkspaceID, issue.ProjectID)
	if err != nil {
		return ContextPackage{}, err
	}
	if err := refreshContextMemoryFreshness(ctx, tx, issue.WorkspaceID, issue.ProjectID, repositoryRevision); err != nil {
		return ContextPackage{}, err
	}

	brainRevision := int64(0)
	err = tx.QueryRow(ctx, `
		SELECT revision FROM autonomous_project_brain_state
		WHERE project_id=$1 AND workspace_id=$2
	`, issue.ProjectID, issue.WorkspaceID).Scan(&brainRevision)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ContextPackage{}, err
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
	items["specification"] = append(items["specification"], specificationItems...)
	items["policy"] = append(items["policy"], ContextItem{
		Source: "project_policy", Ref: util.UUIDToString(plan.PlanID), Type: "policy",
		Authority: string(AuthoritySystemDerived), Text: policy,
	})

	query := buildContextMemoryQuery(
		plan.NodeTitle,
		issue.Title,
		plan.NodeDescription,
		optionalContextText(issue.Description),
		[]byte(plan.NodeAcceptanceCriteria),
		issue.AcceptanceCriteria,
	)
	memories, err := recallContextMemories(ctx, tx, issue.WorkspaceID, issue.ProjectID, query, roleFamily, repositoryRevision)
	if err != nil {
		return ContextPackage{}, err
	}
	for _, memory := range memories {
		section := "brain"
		if memory.Type == "repository_fact" {
			section = "repository_facts"
		}
		items[section] = append(items[section], ContextItem{
			Source: "brain", Ref: memory.ID, Type: memory.Type, Authority: memory.Authority,
			Text: strings.TrimSpace(memory.Subject + "\n" + memory.Content),
		})
	}

	artifacts, err := contextPredecessorArtifacts(ctx, tx, issue, plan)
	if err != nil {
		return ContextPackage{}, err
	}
	items["predecessor_artifacts"] = append(items["predecessor_artifacts"], artifacts...)

	handoffs, err := contextStructuredHandoffs(ctx, tx, issue, plan, workflowActionID)
	if err != nil {
		return ContextPackage{}, err
	}
	items["structured_handoffs"] = append(items["structured_handoffs"], handoffs...)

	findings, err := contextOpenFindings(ctx, tx, issue)
	if err != nil {
		return ContextPackage{}, err
	}
	items["unresolved_findings"] = append(items["unresolved_findings"], findings...)

	pkg := NewBoundedContext(
		roleFamily, plan.NodeKind, plan.NodeKey, plan.PlanRevision, brainRevision,
		repositoryRevision, items,
	)
	if err := validateAuthoritativeContext(pkg); err != nil {
		return ContextPackage{}, err
	}
	if err := validateContextTokenBudget(pkg); err != nil {
		return ContextPackage{}, err
	}

	snapshotID, err := ensureContextBrainSnapshot(ctx, tx, issue, plan, brainRevision)
	if err != nil {
		return ContextPackage{}, err
	}
	rawPackage, err := json.Marshal(pkg)
	if err != nil {
		return ContextPackage{}, err
	}
	sectionUsage := map[string]int{}
	for _, section := range pkg.Sections {
		sectionUsage[section.Name] = section.UsedTokens
	}
	rawUsage, err := json.Marshal(sectionUsage)
	if err != nil {
		return ContextPackage{}, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO autonomous_project_context_compilation (
			workspace_id,project_id,plan_id,node_id,issue_id,workflow_action_id,
			role_family,total_token_budget,used_tokens,section_usage,context_package,
			brain_snapshot_id
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (workflow_action_id) DO NOTHING
	`, issue.WorkspaceID, issue.ProjectID, plan.PlanID, nullableContextUUID(plan.NodeID), issue.ID,
		workflowActionID, roleFamily, pkg.TotalTokenBudget, pkg.UsedTokens, rawUsage, rawPackage,
		nullableContextUUID(snapshotID))
	if err != nil {
		return ContextPackage{}, fmt.Errorf("persist compiled workflow context: %w", err)
	}
	if err := attachContextToHandoff(ctx, tx, workflowActionID, pkg); err != nil {
		return ContextPackage{}, err
	}
	_ = targetAgentID // retained in API for future skill/tool-aware context policy.
	return pkg, nil
}

func BindWorkflowContextTask(ctx context.Context, tx pgx.Tx, workflowActionID, taskID pgtype.UUID) error {
	if tx == nil || !workflowActionID.Valid || !taskID.Valid {
		return nil
	}
	_, err := tx.Exec(ctx, `
		UPDATE autonomous_project_context_compilation
		SET task_id=$2
		WHERE workflow_action_id=$1 AND (task_id IS NULL OR task_id=$2)
	`, workflowActionID, taskID)
	return err
}

func loadContextPlanState(ctx context.Context, tx pgx.Tx, issue db.Issue) (contextPlanState, error) {
	var out contextPlanState
	var specification, policy []byte
	var nodeID pgtype.UUID
	var nodeKey, nodeKind, nodeTitle, nodeDescription, requiredFamily, nodeAcceptanceCriteria pgtype.Text
	err := tx.QueryRow(ctx, `
		SELECT p.id,p.revision,p.goal,p.specification,p.policy,
		       n.id,n.node_key,n.kind,n.title,n.description,n.required_role_family,
	       n.acceptance_criteria::text
		FROM autonomous_project_plan p
		LEFT JOIN autonomous_project_plan_node n
		  ON n.plan_id=p.id AND n.materialized_issue_id=$3::uuid
		WHERE p.workspace_id=$1 AND p.project_id=$2
		  AND p.status IN ('active','blocked')
		ORDER BY p.revision DESC
		LIMIT 1
	`, issue.WorkspaceID, issue.ProjectID, issue.ID).Scan(
		&out.PlanID, &out.PlanRevision, &out.Goal, &specification, &policy,
		&nodeID, &nodeKey, &nodeKind, &nodeTitle, &nodeDescription, &requiredFamily, &nodeAcceptanceCriteria,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return contextPlanState{}, ErrContextPlanNotFound
	}
	if err != nil {
		return contextPlanState{}, err
	}
	out.Specification = append([]byte(nil), specification...)
	out.Policy = append([]byte(nil), policy...)
	out.NodeID = nodeID
	if nodeKey.Valid {
		out.NodeKey = strings.TrimSpace(nodeKey.String)
	}
	if nodeKind.Valid {
		out.NodeKind = NodeKind(strings.TrimSpace(nodeKind.String))
	}
	if nodeTitle.Valid {
		out.NodeTitle = strings.TrimSpace(nodeTitle.String)
	}
	if nodeDescription.Valid {
		out.NodeDescription = strings.TrimSpace(nodeDescription.String)
	}
	if nodeAcceptanceCriteria.Valid {
		out.NodeAcceptanceCriteria = strings.TrimSpace(nodeAcceptanceCriteria.String)
	}
	if requiredFamily.Valid {
		out.RequiredRoleFamily = strings.TrimSpace(requiredFamily.String)
	}
	return out, nil
}

func contextRoleFamily(handoffKind, required string, kind NodeKind) string {
	if strings.EqualFold(strings.TrimSpace(handoffKind), "review_assignment") {
		return "review"
	}
	if required = strings.ToLower(strings.TrimSpace(required)); required != "" {
		return required
	}
	switch kind {
	case NodeQA:
		return "qa"
	case NodeSecurity:
		return "security"
	case NodeArchitecture:
		return "architecture"
	case NodeDesign:
		return "design"
	case NodeProduct, NodeResearch:
		return "product"
	case NodeRelease, NodeDeploy, NodeObserve:
		return "devops"
	default:
		return "implementation"
	}
}

func currentContextRepositoryRevision(ctx context.Context, tx pgx.Tx, workspaceID, projectID pgtype.UUID) (string, error) {
	var revision string
	err := tx.QueryRow(ctx, `
		SELECT COALESCE((
			SELECT NULLIF(commit_sha,'')
			FROM autonomous_project_change_set
			WHERE workspace_id=$1 AND project_id=$2
			  AND merge_status IN ('ready','changes_requested','queued','merging','merged')
			ORDER BY created_at DESC, id DESC
			LIMIT 1
		),'')
	`, workspaceID, projectID).Scan(&revision)
	return strings.TrimSpace(revision), err
}

func refreshContextMemoryFreshness(ctx context.Context, tx pgx.Tx, workspaceID, projectID pgtype.UUID, repositoryRevision string) error {
	if _, err := tx.Exec(ctx, `
		UPDATE autonomous_project_brain_entry
		SET governance_state='stale'
		WHERE workspace_id=$1 AND project_id=$2
		  AND status='active' AND superseded_by IS NULL
		  AND governance_state='current'
		  AND expires_at IS NOT NULL AND expires_at <= now()
	`, workspaceID, projectID); err != nil {
		return err
	}
	if strings.TrimSpace(repositoryRevision) == "" {
		return nil
	}
	_, err := tx.Exec(ctx, `
		UPDATE autonomous_project_brain_entry
		SET governance_state=CASE
		    WHEN repository_revision=$3 AND (expires_at IS NULL OR expires_at>now()) THEN 'current'
		    ELSE 'stale' END
		WHERE workspace_id=$1 AND project_id=$2
		  AND entry_type='repository_fact'
		  AND status='active' AND superseded_by IS NULL
		  AND governance_state IN ('current','stale')
	`, workspaceID, projectID, repositoryRevision)
	return err
}

func recallContextMemories(ctx context.Context, tx pgx.Tx, workspaceID, projectID pgtype.UUID, query, role, repositoryRevision string) ([]rankedContextMemory, error) {
	if strings.TrimSpace(query) == "" {
		return []rankedContextMemory{}, nil
	}
	embedder, err := memoryEmbedderFromEnvironment()
	if err != nil {
		return nil, err
	}
	return recallContextMemoriesWithEmbedder(ctx, tx, workspaceID, projectID, query, role, repositoryRevision, embedder)
}

func recallContextMemoriesWithEmbedder(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, projectID pgtype.UUID,
	query, role, repositoryRevision string,
	embedder MemoryEmbedder,
) ([]rankedContextMemory, error) {
	memories, err := queryHybridBrainMemories(ctx, tx, workspaceID, projectID, RecallOptions{
		Query: query, RoleFamily: role, Limit: 8, MaxTokens: 16000,
		CurrentRepositoryRevision: repositoryRevision,
	}, embedder)
	if err != nil {
		return nil, err
	}
	out := make([]rankedContextMemory, 0, len(memories))
	seen := map[string]struct{}{}
	for _, memory := range memories {
		if len(out) >= 8 {
			break
		}
		identity := memory.Type + "\x00" + normalizeBrainKey(memory.CanonicalKey)
		if memory.CanonicalKey == "" {
			identity = memory.Type + "\x00" + strings.ToLower(strings.TrimSpace(memory.Subject))
		}
		if _, ok := seen[identity]; ok {
			continue
		}
		seen[identity] = struct{}{}
		out = append(out, rankedContextMemory{
			ID: memory.ID, CanonicalKey: memory.CanonicalKey, Type: memory.Type,
			Subject: memory.Subject, Content: memory.Content, Authority: string(memory.Authority),
		})
	}
	return out, nil
}

func buildContextMemoryQuery(nodeTitle, issueTitle, nodeDescription, issueDescription string, acceptanceCriteria ...[]byte) string {
	parts := make([]string, 0, 5)
	seen := map[string]struct{}{}
	appendPart := func(text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		key := strings.ToLower(text)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		parts = append(parts, text)
	}
	appendPart(nodeTitle)
	appendPart(issueTitle)
	appendPart(nodeDescription)
	appendPart(issueDescription)
	for _, rawCriteria := range acceptanceCriteria {
		for _, criterion := range acceptanceCriteriaText(rawCriteria) {
			appendPart(criterion)
		}
	}
	return strings.Join(parts, "\n")
}

func optionalContextText(value pgtype.Text) string {
	if !value.Valid {
		return ""
	}
	return strings.TrimSpace(value.String)
}

func acceptanceCriteriaText(raw []byte) []string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return []string{strings.TrimSpace(string(raw))}
	}
	var values []string
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case string:
			if text := strings.TrimSpace(typed); text != "" {
				values = append(values, text)
			}
		case []any:
			for _, item := range typed {
				visit(item)
			}
		case map[string]any:
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				visit(typed[key])
			}
		}
	}
	visit(value)
	return values
}

func contextPredecessorArtifacts(ctx context.Context, tx pgx.Tx, issue db.Issue, plan contextPlanState) ([]ContextItem, error) {
	if plan.NodeKey == "" {
		return []ContextItem{}, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT ON (pred.node_key,a.artifact_type)
		       a.id,pred.node_key,a.artifact_type,a.name,left(a.content::text,6000)
		FROM autonomous_project_plan_edge e
		JOIN autonomous_project_plan_node pred ON pred.plan_id=e.plan_id AND pred.node_key=e.from_node_key
		JOIN autonomous_project_artifact a ON a.plan_id=e.plan_id AND a.node_id=pred.id
		WHERE e.plan_id=$1 AND e.to_node_key=$2
		  AND a.status='active' AND a.valid=TRUE AND a.artifact_revision=pred.spec_revision
		  AND (e.dependency_type<>'artifact' OR e.required_artifact_type=a.artifact_type)
		ORDER BY pred.node_key,a.artifact_type,a.created_at DESC,a.id DESC
		LIMIT 24
	`, plan.PlanID, plan.NodeKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ContextItem{}
	for rows.Next() {
		var id pgtype.UUID
		var nodeKey, typ, name, content string
		if err := rows.Scan(&id, &nodeKey, &typ, &name, &content); err != nil {
			return nil, err
		}
		out = append(out, ContextItem{Source: "predecessor_artifact", Ref: util.UUIDToString(id), Type: typ, Authority: string(AuthoritySystemDerived), Text: nodeKey + " / " + name + "\n" + content})
	}
	return out, rows.Err()
}

func contextStructuredHandoffs(ctx context.Context, tx pgx.Tx, issue db.Issue, plan contextPlanState, actionID pgtype.UUID) ([]ContextItem, error) {
	rows, err := tx.Query(ctx, `
		SELECT h.id,h.handoff_kind,h.summary,left((h.envelope-'context_package'-'brain_references')::text,6000)
		FROM autonomous_project_handoff h
		WHERE h.workspace_id=$1 AND h.project_id=$2
		  AND (h.workflow_action_id IS NULL OR h.workflow_action_id<>$4)
		  AND (h.issue_id=$3::uuid OR h.issue_id IN (
		    SELECT pred.materialized_issue_id
		    FROM autonomous_project_plan_edge e
		    JOIN autonomous_project_plan_node pred ON pred.plan_id=e.plan_id AND pred.node_key=e.from_node_key
		    WHERE e.plan_id=$5 AND e.to_node_key=$6 AND pred.materialized_issue_id IS NOT NULL
		  ))
		ORDER BY h.created_at DESC
		LIMIT 12
	`, issue.WorkspaceID, issue.ProjectID, issue.ID, actionID, plan.PlanID, plan.NodeKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ContextItem{}
	for rows.Next() {
		var id pgtype.UUID
		var kind, summary, envelope string
		if err := rows.Scan(&id, &kind, &summary, &envelope); err != nil {
			return nil, err
		}
		out = append(out, ContextItem{Source: "structured_handoff", Ref: util.UUIDToString(id), Type: kind, Authority: string(AuthoritySystemDerived), Text: strings.TrimSpace(summary + "\n" + envelope)})
	}
	return out, rows.Err()
}

func contextOpenFindings(ctx context.Context, tx pgx.Tx, issue db.Issue) ([]ContextItem, error) {
	rows, err := tx.Query(ctx, `
		SELECT f.id,f.finding_key,f.severity,f.category,f.description,f.evidence
		FROM autonomous_project_review_finding f
		WHERE f.workspace_id=$1 AND f.project_id=$2 AND f.issue_id=$3 AND f.lifecycle_status='open'
		ORDER BY f.blocking DESC,f.created_at,f.finding_key
		LIMIT 40
	`, issue.WorkspaceID, issue.ProjectID, issue.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ContextItem{}
	for rows.Next() {
		var id pgtype.UUID
		var key, severity, category, description, evidence string
		if err := rows.Scan(&id, &key, &severity, &category, &description, &evidence); err != nil {
			return nil, err
		}
		out = append(out, ContextItem{Source: "review_finding", Ref: util.UUIDToString(id), Type: category, Authority: string(AuthoritySystemDerived), Text: fmt.Sprintf("%s [%s/%s] %s\nEvidence: %s", key, severity, category, description, evidence)})
	}
	return out, rows.Err()
}

func ensureContextBrainSnapshot(ctx context.Context, tx pgx.Tx, issue db.Issue, plan contextPlanState, brainRevision int64) (pgtype.UUID, error) {
	var id pgtype.UUID
	err := tx.QueryRow(ctx, `
		SELECT id FROM autonomous_project_brain_snapshot
		WHERE workspace_id=$1 AND project_id=$2 AND plan_id=$3 AND plan_revision=$4 AND brain_revision=$5
		ORDER BY created_at DESC LIMIT 1
	`, issue.WorkspaceID, issue.ProjectID, plan.PlanID, plan.PlanRevision, brainRevision).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, err
	}
	var entryIDs []byte
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(jsonb_agg(id ORDER BY brain_revision,id),'[]'::jsonb)
		FROM autonomous_project_brain_entry
		WHERE workspace_id=$1 AND project_id=$2 AND status='active' AND superseded_by IS NULL AND governance_state='current'
		  AND (expires_at IS NULL OR expires_at>now())
	`, issue.WorkspaceID, issue.ProjectID).Scan(&entryIDs); err != nil {
		return pgtype.UUID{}, err
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO autonomous_project_brain_snapshot(workspace_id,project_id,plan_id,plan_revision,brain_revision,entry_ids)
		VALUES($1,$2,$3,$4,$5,$6) RETURNING id
	`, issue.WorkspaceID, issue.ProjectID, plan.PlanID, plan.PlanRevision, brainRevision, entryIDs).Scan(&id)
	return id, err
}

func attachContextToHandoff(ctx context.Context, tx pgx.Tx, actionID pgtype.UUID, pkg ContextPackage) error {
	raw, err := json.Marshal(pkg)
	if err != nil {
		return err
	}
	brainTokens := 0
	for _, section := range pkg.Sections {
		if section.Name == "brain" || section.Name == "repository_facts" {
			brainTokens += section.UsedTokens
		}
	}
	_, err = tx.Exec(ctx, `
		UPDATE autonomous_project_handoff
		SET envelope=jsonb_set(envelope,'{context_package}',$2::jsonb,true),
		    brain_context_tokens=$3::bigint,
		    brain_context_estimated=($3::bigint>0)
		WHERE workflow_action_id=$1
	`, actionID, raw, brainTokens)
	return err
}

func nullableContextUUID(value pgtype.UUID) any {
	if value.Valid {
		return value
	}
	return nil
}
