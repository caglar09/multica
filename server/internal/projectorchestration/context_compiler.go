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

var ErrContextPlanNotFound = errors.New("active autonomous project plan not found for workflow context")

type rankedContextMemory struct {
	ID           string
	CanonicalKey string
	Type         string
	Subject      string
	Content      string
	Authority    string
}

type contextPlanState struct {
	PlanID       pgtype.UUID
	PlanRevision int64
	Goal         string
	Specification []byte
	Policy       []byte
	NodeID       pgtype.UUID
	NodeKey      string
	NodeKind     NodeKind
	NodeTitle    string
	NodeDescription string
	RequiredRoleFamily string
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
		"specification": {},
		"policy": {},
		"brain": {},
		"predecessor_artifacts": {},
		"structured_handoffs": {},
		"unresolved_findings": {},
		"repository_facts": {},
	}
	items["specification"] = append(items["specification"], ContextItem{
		Source: "project_specification", Ref: util.UUIDToString(plan.PlanID), Type: "authoritative_specification",
		Authority: string(AuthorityAuthoritativeSpec),
		Text: fmt.Sprintf("Goal: %s\nSpecification: %s", plan.Goal, strings.TrimSpace(string(plan.Specification))),
	})
	items["policy"] = append(items["policy"], ContextItem{
		Source: "project_policy", Ref: util.UUIDToString(plan.PlanID), Type: "policy",
		Authority: string(AuthoritySystemDerived), Text: strings.TrimSpace(string(plan.Policy)),
	})

	query := strings.TrimSpace(plan.NodeTitle)
	if query == "" {
		query = strings.TrimSpace(issue.Title)
	}
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
	if pkg.UsedTokens > pkg.TotalTokenBudget {
		return ContextPackage{}, fmt.Errorf("compiled context exceeds hard token budget: %d > %d", pkg.UsedTokens, pkg.TotalTokenBudget)
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
	var nodeKey, nodeKind, nodeTitle, nodeDescription, requiredFamily pgtype.Text
	err := tx.QueryRow(ctx, `
		SELECT p.id,p.revision,p.goal,p.specification,p.policy,
		       n.id,n.node_key,n.kind,n.title,n.description,n.required_role_family
		FROM autonomous_project_plan p
		LEFT JOIN autonomous_project_plan_node n
		  ON n.plan_id=p.id AND n.materialized_issue_id=$3
		WHERE p.workspace_id=$1 AND p.project_id=$2
		  AND p.status IN ('active','blocked')
		ORDER BY p.revision DESC
		LIMIT 1
	`, issue.WorkspaceID, issue.ProjectID, issue.ID).Scan(
		&out.PlanID, &out.PlanRevision, &out.Goal, &specification, &policy,
		&nodeID, &nodeKey, &nodeKind, &nodeTitle, &nodeDescription, &requiredFamily,
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
	if nodeKey.Valid { out.NodeKey = strings.TrimSpace(nodeKey.String) }
	if nodeKind.Valid { out.NodeKind = NodeKind(strings.TrimSpace(nodeKind.String)) }
	if nodeTitle.Valid { out.NodeTitle = strings.TrimSpace(nodeTitle.String) }
	if nodeDescription.Valid { out.NodeDescription = strings.TrimSpace(nodeDescription.String) }
	if requiredFamily.Valid { out.RequiredRoleFamily = strings.TrimSpace(requiredFamily.String) }
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
			SELECT COALESCE(NULLIF(merged_sha,''),NULLIF(base_sha,''),'')
			FROM autonomous_project_change_set
			WHERE workspace_id=$1 AND project_id=$2
			ORDER BY (merged_at IS NOT NULL) DESC, merged_at DESC NULLS LAST, created_at DESC
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
	load := func(search string) ([]rankedContextMemory, error) {
		rows, err := tx.Query(ctx, `
			WITH q AS (
				SELECT CASE WHEN btrim($3)='' THEN NULL ELSE websearch_to_tsquery('simple'::regconfig,$3) END AS tsq
			)
			SELECT b.id,COALESCE(b.canonical_key,''),b.entry_type,b.subject,
			       left(b.content::text,6000),b.authority
			FROM autonomous_project_brain_entry b
			CROSS JOIN q
			WHERE b.workspace_id=$1 AND b.project_id=$2
			  AND b.status='active' AND b.superseded_by IS NULL
			  AND b.governance_state='current'
			  AND (b.expires_at IS NULL OR b.expires_at>now())
			  AND ($5='' OR b.repository_revision IS NULL OR b.repository_revision=$5)
			  AND (q.tsq IS NULL OR to_tsvector('simple'::regconfig,
			      COALESCE(b.subject,'') || ' ' || COALESCE(b.canonical_key,'') || ' ' || COALESCE(b.content::text,'')) @@ q.tsq)
			ORDER BY (
			  CASE WHEN q.tsq IS NULL THEN 0 ELSE ts_rank_cd(to_tsvector('simple'::regconfig,
			    COALESCE(b.subject,'') || ' ' || COALESCE(b.canonical_key,'') || ' ' || COALESCE(b.content::text,'')),q.tsq)*4.0 END
			  + b.importance*1.5 + LEAST(b.confirmation_count,5)*0.10 + COALESCE(b.confidence,0)*0.50
			  + CASE b.authority WHEN 'user_decision' THEN 2.0 WHEN 'authoritative_spec' THEN 1.8
			    WHEN 'deterministic_observation' THEN 1.6 WHEN 'trusted_external' THEN 1.3
			    WHEN 'system_derived' THEN 1.0 ELSE 0.6 END
			  + CASE WHEN $4 IN ('qa','review') AND b.entry_type IN ('requirement','risk','lesson') THEN 0.8
			    WHEN $4='security' AND b.entry_type IN ('risk','constraint','architecture_decision') THEN 0.8
			    WHEN $4 IN ('frontend','design') AND b.entry_type IN ('product_decision','requirement','architecture_decision') THEN 0.8
			    WHEN $4 IN ('backend','implementation','architecture') AND b.entry_type IN ('architecture_decision','repository_fact','constraint','dependency') THEN 0.8
			    WHEN $4='product' AND b.entry_type IN ('requirement','product_decision','constraint') THEN 0.8 ELSE 0 END
			  + (1.0/(1.0+GREATEST(EXTRACT(EPOCH FROM (now()-b.observed_at)),0)/2592000.0))
			) DESC,b.brain_revision DESC,b.id
			LIMIT 60
		`, workspaceID, projectID, strings.TrimSpace(search), strings.ToLower(strings.TrimSpace(role)), strings.TrimSpace(repositoryRevision))
		if err != nil { return nil, err }
		defer rows.Close()
		out := []rankedContextMemory{}
		seen := map[string]struct{}{}
		for rows.Next() {
			var id pgtype.UUID
			var m rankedContextMemory
			if err := rows.Scan(&id,&m.CanonicalKey,&m.Type,&m.Subject,&m.Content,&m.Authority); err != nil { return nil, err }
			m.ID = util.UUIDToString(id)
			identity := m.Type + "\x00" + m.CanonicalKey
			if m.CanonicalKey == "" { identity = m.Type + "\x00" + strings.ToLower(strings.TrimSpace(m.Subject)) }
			if _, ok := seen[identity]; ok { continue }
			seen[identity] = struct{}{}
			out = append(out,m)
		}
		return out, rows.Err()
	}
	memories, err := load(query)
	if err != nil { return nil, err }
	if len(memories)==0 && strings.TrimSpace(query)!="" { return load("") }
	return memories,nil
}

func contextPredecessorArtifacts(ctx context.Context, tx pgx.Tx, issue db.Issue, plan contextPlanState) ([]ContextItem,error) {
	if plan.NodeKey=="" { return []ContextItem{},nil }
	rows,err:=tx.Query(ctx,`
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
	`,plan.PlanID,plan.NodeKey)
	if err!=nil{return nil,err}
	defer rows.Close()
	out:=[]ContextItem{}
	for rows.Next(){
		var id pgtype.UUID
		var nodeKey,typ,name,content string
		if err:=rows.Scan(&id,&nodeKey,&typ,&name,&content);err!=nil{return nil,err}
		out=append(out,ContextItem{Source:"predecessor_artifact",Ref:util.UUIDToString(id),Type:typ,Authority:string(AuthoritySystemDerived),Text:nodeKey+" / "+name+"\n"+content})
	}
	return out,rows.Err()
}

func contextStructuredHandoffs(ctx context.Context, tx pgx.Tx, issue db.Issue, plan contextPlanState, actionID pgtype.UUID) ([]ContextItem,error){
	rows,err:=tx.Query(ctx,`
		SELECT h.id,h.handoff_kind,h.summary,left((h.envelope-'context_package'-'brain_references')::text,6000)
		FROM autonomous_project_handoff h
		WHERE h.workspace_id=$1 AND h.project_id=$2
		  AND (h.workflow_action_id IS NULL OR h.workflow_action_id<>$4)
		  AND (h.issue_id=$3 OR h.issue_id IN (
		    SELECT pred.materialized_issue_id
		    FROM autonomous_project_plan_edge e
		    JOIN autonomous_project_plan_node pred ON pred.plan_id=e.plan_id AND pred.node_key=e.from_node_key
		    WHERE e.plan_id=$5 AND e.to_node_key=$6 AND pred.materialized_issue_id IS NOT NULL
		  ))
		ORDER BY h.created_at DESC
		LIMIT 12
	`,issue.WorkspaceID,issue.ProjectID,issue.ID,actionID,plan.PlanID,plan.NodeKey)
	if err!=nil{return nil,err}
	defer rows.Close()
	out:=[]ContextItem{}
	for rows.Next(){
		var id pgtype.UUID
		var kind,summary,envelope string
		if err:=rows.Scan(&id,&kind,&summary,&envelope);err!=nil{return nil,err}
		out=append(out,ContextItem{Source:"structured_handoff",Ref:util.UUIDToString(id),Type:kind,Authority:string(AuthoritySystemDerived),Text:strings.TrimSpace(summary+"\n"+envelope)})
	}
	return out,rows.Err()
}

func contextOpenFindings(ctx context.Context,tx pgx.Tx,issue db.Issue)([]ContextItem,error){
	rows,err:=tx.Query(ctx,`
		SELECT f.id,f.finding_key,f.severity,f.category,f.description,f.evidence
		FROM autonomous_project_review_finding f
		WHERE f.workspace_id=$1 AND f.project_id=$2 AND f.issue_id=$3 AND f.lifecycle_status='open'
		ORDER BY f.blocking DESC,f.created_at,f.finding_key
		LIMIT 40
	`,issue.WorkspaceID,issue.ProjectID,issue.ID)
	if err!=nil{return nil,err}
	defer rows.Close()
	out:=[]ContextItem{}
	for rows.Next(){
		var id pgtype.UUID
		var key,severity,category,description,evidence string
		if err:=rows.Scan(&id,&key,&severity,&category,&description,&evidence);err!=nil{return nil,err}
		out=append(out,ContextItem{Source:"review_finding",Ref:util.UUIDToString(id),Type:category,Authority:string(AuthoritySystemDerived),Text:fmt.Sprintf("%s [%s/%s] %s\nEvidence: %s",key,severity,category,description,evidence)})
	}
	return out,rows.Err()
}

func ensureContextBrainSnapshot(ctx context.Context,tx pgx.Tx,issue db.Issue,plan contextPlanState,brainRevision int64)(pgtype.UUID,error){
	var id pgtype.UUID
	err:=tx.QueryRow(ctx,`
		SELECT id FROM autonomous_project_brain_snapshot
		WHERE workspace_id=$1 AND project_id=$2 AND plan_id=$3 AND plan_revision=$4 AND brain_revision=$5
		ORDER BY created_at DESC LIMIT 1
	`,issue.WorkspaceID,issue.ProjectID,plan.PlanID,plan.PlanRevision,brainRevision).Scan(&id)
	if err==nil{return id,nil}
	if !errors.Is(err,pgx.ErrNoRows){return pgtype.UUID{},err}
	var entryIDs []byte
	if err:=tx.QueryRow(ctx,`
		SELECT COALESCE(jsonb_agg(id ORDER BY brain_revision,id),'[]'::jsonb)
		FROM autonomous_project_brain_entry
		WHERE workspace_id=$1 AND project_id=$2 AND status='active' AND superseded_by IS NULL AND governance_state='current'
		  AND (expires_at IS NULL OR expires_at>now())
	`,issue.WorkspaceID,issue.ProjectID).Scan(&entryIDs);err!=nil{return pgtype.UUID{},err}
	err=tx.QueryRow(ctx,`
		INSERT INTO autonomous_project_brain_snapshot(workspace_id,project_id,plan_id,plan_revision,brain_revision,entry_ids)
		VALUES($1,$2,$3,$4,$5,$6) RETURNING id
	`,issue.WorkspaceID,issue.ProjectID,plan.PlanID,plan.PlanRevision,brainRevision,entryIDs).Scan(&id)
	return id,err
}

func attachContextToHandoff(ctx context.Context,tx pgx.Tx,actionID pgtype.UUID,pkg ContextPackage)error{
	raw,err:=json.Marshal(pkg)
	if err!=nil{return err}
	brainTokens:=0
	for _,section:=range pkg.Sections{
		if section.Name=="brain" || section.Name=="repository_facts"{brainTokens+=section.UsedTokens}
	}
	_,err=tx.Exec(ctx,`
		UPDATE autonomous_project_handoff
		SET envelope=jsonb_set(envelope,'{context_package}',$2::jsonb,true),
		    brain_context_tokens=$3,
		    brain_context_estimated=($3>0)
		WHERE workflow_action_id=$1
	`,actionID,raw,brainTokens)
	return err
}

func nullableContextUUID(value pgtype.UUID) any {
	if value.Valid { return value }
	return nil
}
