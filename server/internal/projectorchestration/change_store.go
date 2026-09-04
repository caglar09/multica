package projectorchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
)

type SpecificationRevision struct {
	ID            string               `json:"id"`
	WorkspaceID   string               `json:"workspace_id"`
	ProjectID     string               `json:"project_id"`
	Revision      int64                `json:"revision"`
	SourceKind    string               `json:"source_kind"`
	SourceRef     string               `json:"source_ref,omitempty"`
	Specification ProjectSpecification `json:"specification"`
	CreatedAt     time.Time            `json:"created_at"`
}

type ChangeRequest struct {
	ID                              string              `json:"id"`
	WorkspaceID                     string              `json:"workspace_id"`
	ProjectID                       string              `json:"project_id"`
	RequestKey                      string              `json:"request_key"`
	Type                            ChangeRequestType   `json:"type"`
	State                           ChangeRequestState  `json:"state"`
	Source                          ChangeRequestSource `json:"source"`
	SourceRef                       string              `json:"source_ref,omitempty"`
	RequestText                     string              `json:"request_text"`
	Proposal                        json.RawMessage     `json:"proposal,omitempty"`
	Impact                          *ChangeImpact       `json:"impact,omitempty"`
	BaseSpecificationRevisionID     string              `json:"base_specification_revision_id,omitempty"`
	ProposedSpecificationRevisionID string              `json:"proposed_specification_revision_id,omitempty"`
	BasePlanID                      string              `json:"base_plan_id,omitempty"`
	AppliedPlanID                   string              `json:"applied_plan_id,omitempty"`
	ApprovalEscalationID            string              `json:"approval_escalation_id,omitempty"`
	Error                           string              `json:"error,omitempty"`
	CreatedAt                       time.Time           `json:"created_at"`
	UpdatedAt                       time.Time           `json:"updated_at"`
}

type ChangeRequestEvent struct {
	ID        string             `json:"id"`
	FromState ChangeRequestState `json:"from_state,omitempty"`
	ToState   ChangeRequestState `json:"to_state"`
	ActorType string             `json:"actor_type"`
	ActorRef  string             `json:"actor_ref,omitempty"`
	Details   json.RawMessage    `json:"details"`
	CreatedAt time.Time          `json:"created_at"`
}

func (s *Store) CreateSpecificationRevision(ctx context.Context, workspaceID, projectID pgtype.UUID, sourceKind, sourceRef string, spec ProjectSpecification, activate bool) (SpecificationRevision, error) {
	if s == nil || s.pool == nil { return SpecificationRevision{}, errors.New("project orchestration store is not configured") }
	if !workspaceID.Valid || !projectID.Valid { return SpecificationRevision{}, errors.New("workspace_id and project_id are required") }
	if err := ValidateProjectSpecification(spec); err != nil { return SpecificationRevision{}, err }
	switch sourceKind { case "project_bootstrap", "change_request", "planner", "backfill", "system": default: return SpecificationRevision{}, fmt.Errorf("unsupported specification source %q", sourceKind) }
	payload, err := json.Marshal(spec); if err != nil { return SpecificationRevision{}, err }
	tx, err := s.pool.Begin(ctx); if err != nil { return SpecificationRevision{}, err }; defer tx.Rollback(ctx)
	lockKey := "autonomous-project-specification:"+util.UUIDToString(workspaceID)+":"+util.UUIDToString(projectID)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", lockKey); err != nil { return SpecificationRevision{}, err }
	sourceRef = strings.TrimSpace(sourceRef)
	if sourceRef != "" {
		var id pgtype.UUID; var revision int64; var raw []byte; var created pgtype.Timestamptz
		err := tx.QueryRow(ctx, `SELECT id, revision, specification, created_at FROM autonomous_project_specification_revision WHERE workspace_id=$1 AND project_id=$2 AND source_kind=$3 AND source_ref=$4`, workspaceID, projectID, sourceKind, sourceRef).Scan(&id, &revision, &raw, &created)
		if err == nil {
			var existing ProjectSpecification; if err := json.Unmarshal(raw, &existing); err != nil { return SpecificationRevision{}, err }
			if activate { if err := activateSpecificationTx(ctx, tx, workspaceID, projectID, id, revision); err != nil { return SpecificationRevision{}, err } }
			if err := tx.Commit(ctx); err != nil { return SpecificationRevision{}, err }
			return SpecificationRevision{ID:util.UUIDToString(id), WorkspaceID:util.UUIDToString(workspaceID), ProjectID:util.UUIDToString(projectID), Revision:revision, SourceKind:sourceKind, SourceRef:sourceRef, Specification:existing, CreatedAt:created.Time}, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) { return SpecificationRevision{}, err }
	}
	var revision int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(revision),0)+1 FROM autonomous_project_specification_revision WHERE workspace_id=$1 AND project_id=$2`, workspaceID, projectID).Scan(&revision); err != nil { return SpecificationRevision{}, err }
	var id pgtype.UUID; var created pgtype.Timestamptz
	if err := tx.QueryRow(ctx, `INSERT INTO autonomous_project_specification_revision(workspace_id,project_id,revision,source_kind,source_ref,goal,specification) VALUES($1,$2,$3,$4,NULLIF($5,''),$6,$7) RETURNING id,created_at`, workspaceID, projectID, revision, sourceKind, sourceRef, spec.Goal, payload).Scan(&id,&created); err != nil { return SpecificationRevision{}, err }
	if activate { if err := activateSpecificationTx(ctx, tx, workspaceID, projectID, id, revision); err != nil { return SpecificationRevision{}, err } }
	if err := tx.Commit(ctx); err != nil { return SpecificationRevision{}, err }
	return SpecificationRevision{ID:util.UUIDToString(id), WorkspaceID:util.UUIDToString(workspaceID), ProjectID:util.UUIDToString(projectID), Revision:revision, SourceKind:sourceKind, SourceRef:sourceRef, Specification:spec, CreatedAt:created.Time}, nil
}

func activateSpecificationTx(ctx context.Context, tx pgx.Tx, workspaceID, projectID, revisionID pgtype.UUID, revision int64) error {
	_, err := tx.Exec(ctx, `INSERT INTO autonomous_project_specification_head(workspace_id,project_id,specification_revision_id,revision) VALUES($1,$2,$3,$4) ON CONFLICT(workspace_id,project_id) DO UPDATE SET specification_revision_id=EXCLUDED.specification_revision_id,revision=EXCLUDED.revision,updated_at=now()`, workspaceID, projectID, revisionID, revision)
	return err
}

func (s *Store) LoadLatestSpecification(ctx context.Context, workspaceID, projectID pgtype.UUID) (SpecificationRevision, bool, error) {
	var out SpecificationRevision; var id pgtype.UUID; var sourceRef pgtype.Text; var raw []byte; var created pgtype.Timestamptz
	err := s.pool.QueryRow(ctx, `SELECT r.id,r.revision,r.source_kind,r.source_ref,r.specification,r.created_at FROM autonomous_project_specification_head h JOIN autonomous_project_specification_revision r ON r.id=h.specification_revision_id WHERE h.workspace_id=$1 AND h.project_id=$2`, workspaceID, projectID).Scan(&id,&out.Revision,&out.SourceKind,&sourceRef,&raw,&created)
	if errors.Is(err, pgx.ErrNoRows) { return SpecificationRevision{}, false, nil }; if err != nil { return SpecificationRevision{}, false, err }
	if err := json.Unmarshal(raw,&out.Specification); err != nil { return SpecificationRevision{}, false, err }
	out.ID=util.UUIDToString(id); out.WorkspaceID=util.UUIDToString(workspaceID); out.ProjectID=util.UUIDToString(projectID); out.SourceRef=sourceRef.String; out.CreatedAt=created.Time
	return out,true,nil
}

func (s *Store) ListSpecificationRevisions(ctx context.Context, workspaceID, projectID pgtype.UUID, limit int) ([]SpecificationRevision,error) {
	if limit<=0 || limit>100 { limit=50 }
	rows,err:=s.pool.Query(ctx,`SELECT id,revision,source_kind,source_ref,specification,created_at FROM autonomous_project_specification_revision WHERE workspace_id=$1 AND project_id=$2 ORDER BY revision DESC LIMIT $3`,workspaceID,projectID,limit); if err!=nil{return nil,err}; defer rows.Close()
	out:=[]SpecificationRevision{}
	for rows.Next(){ var item SpecificationRevision; var id pgtype.UUID; var ref pgtype.Text; var raw []byte; var created pgtype.Timestamptz; if err:=rows.Scan(&id,&item.Revision,&item.SourceKind,&ref,&raw,&created);err!=nil{return nil,err}; if err:=json.Unmarshal(raw,&item.Specification);err!=nil{return nil,err}; item.ID=util.UUIDToString(id); item.WorkspaceID=util.UUIDToString(workspaceID); item.ProjectID=util.UUIDToString(projectID); item.SourceRef=ref.String; item.CreatedAt=created.Time; out=append(out,item) }
	return out,rows.Err()
}

func (s *Store) ReceiveChangeRequest(ctx context.Context, workspaceID, projectID pgtype.UUID, requestKey string, requestType ChangeRequestType, source ChangeRequestSource, sourceRef, requestText string) (ChangeRequest,error) {
	if s==nil||s.pool==nil{return ChangeRequest{},errors.New("project orchestration store is not configured")}
	requestKey=strings.TrimSpace(requestKey); requestText=strings.TrimSpace(requestText)
	if requestKey==""||requestText==""||!workspaceID.Valid||!projectID.Valid{return ChangeRequest{},errors.New("workspace_id, project_id, request_key and request_text are required")}
	if !validChangeRequestType(requestType)||!validChangeRequestSource(source){return ChangeRequest{},errors.New("unsupported change request type or source")}
	var id pgtype.UUID
	err:=s.pool.QueryRow(ctx,`INSERT INTO autonomous_project_change_request(workspace_id,project_id,request_key,request_type,source,source_ref,request_text,base_specification_revision_id,base_plan_id) VALUES($1,$2,$3,$4,$5,NULLIF($6,''),$7,(SELECT specification_revision_id FROM autonomous_project_specification_head WHERE workspace_id=$1 AND project_id=$2),(SELECT id FROM autonomous_project_plan WHERE workspace_id=$1 AND project_id=$2 ORDER BY revision DESC LIMIT 1)) ON CONFLICT(workspace_id,project_id,request_key) DO UPDATE SET request_key=EXCLUDED.request_key RETURNING id`,workspaceID,projectID,requestKey,string(requestType),string(source),sourceRef,requestText).Scan(&id)
	if err!=nil{return ChangeRequest{},err}; return s.LoadChangeRequest(ctx,id)
}

func (s *Store) LoadChangeRequest(ctx context.Context,id pgtype.UUID)(ChangeRequest,error){
	var out ChangeRequest; var workspaceID,projectID,baseSpec,proposedSpec,basePlan,appliedPlan,escalation pgtype.UUID; var typ,state,source string; var sourceRef,errorText pgtype.Text; var proposal,impactRaw []byte; var created,updated pgtype.Timestamptz
	err:=s.pool.QueryRow(ctx,`SELECT workspace_id,project_id,request_key,request_type,state,source,source_ref,request_text,proposal,impact,base_specification_revision_id,proposed_specification_revision_id,base_plan_id,applied_plan_id,approval_escalation_id,error,created_at,updated_at FROM autonomous_project_change_request WHERE id=$1`,id).Scan(&workspaceID,&projectID,&out.RequestKey,&typ,&state,&source,&sourceRef,&out.RequestText,&proposal,&impactRaw,&baseSpec,&proposedSpec,&basePlan,&appliedPlan,&escalation,&errorText,&created,&updated)
	if err!=nil{return ChangeRequest{},err}; out.ID=util.UUIDToString(id); out.WorkspaceID=util.UUIDToString(workspaceID); out.ProjectID=util.UUIDToString(projectID); out.Type=ChangeRequestType(typ); out.State=ChangeRequestState(state); out.Source=ChangeRequestSource(source); out.SourceRef=sourceRef.String; out.Proposal=proposal; out.BaseSpecificationRevisionID=util.UUIDToString(baseSpec); out.ProposedSpecificationRevisionID=util.UUIDToString(proposedSpec); out.BasePlanID=util.UUIDToString(basePlan); out.AppliedPlanID=util.UUIDToString(appliedPlan); out.ApprovalEscalationID=util.UUIDToString(escalation); out.Error=errorText.String; out.CreatedAt=created.Time; out.UpdatedAt=updated.Time
	if len(impactRaw)>0{var impact ChangeImpact;if err:=json.Unmarshal(impactRaw,&impact);err!=nil{return ChangeRequest{},err};out.Impact=&impact}
	return out,nil
}

func (s *Store) ListChangeRequests(ctx context.Context,workspaceID,projectID pgtype.UUID,limit int)([]ChangeRequest,error){
	if limit<=0||limit>100{limit=50}; rows,err:=s.pool.Query(ctx,`SELECT id FROM autonomous_project_change_request WHERE workspace_id=$1 AND project_id=$2 ORDER BY created_at DESC LIMIT $3`,workspaceID,projectID,limit);if err!=nil{return nil,err};defer rows.Close();ids:=[]pgtype.UUID{};for rows.Next(){var id pgtype.UUID;if err:=rows.Scan(&id);err!=nil{return nil,err};ids=append(ids,id)};if err:=rows.Err();err!=nil{return nil,err};out:=make([]ChangeRequest,0,len(ids));for _,id:=range ids{item,err:=s.LoadChangeRequest(ctx,id);if err!=nil{return nil,err};out=append(out,item)};return out,nil
}

func (s *Store) ListChangeRequestEvents(ctx context.Context,id pgtype.UUID)([]ChangeRequestEvent,error){
	rows,err:=s.pool.Query(ctx,`SELECT id,from_state,to_state,actor_type,actor_ref,details,created_at FROM autonomous_project_change_request_event WHERE change_request_id=$1 ORDER BY created_at,id`,id);if err!=nil{return nil,err};defer rows.Close();out:=[]ChangeRequestEvent{}
	for rows.Next(){var item ChangeRequestEvent;var eventID pgtype.UUID;var from,actorRef pgtype.Text;var to string;var created pgtype.Timestamptz;if err:=rows.Scan(&eventID,&from,&to,&item.ActorType,&actorRef,&item.Details,&created);err!=nil{return nil,err};item.ID=util.UUIDToString(eventID);item.FromState=ChangeRequestState(from.String);item.ToState=ChangeRequestState(to);item.ActorRef=actorRef.String;item.CreatedAt=created.Time;out=append(out,item)};return out,rows.Err()
}

func (s *Store) AdvanceChangeRequest(ctx context.Context,id pgtype.UUID,to ChangeRequestState,errorText string)error{
	if !id.Valid{return errors.New("change request id is required")};tx,err:=s.pool.Begin(ctx);if err!=nil{return err};defer tx.Rollback(ctx);var from string;if err:=tx.QueryRow(ctx,`SELECT state FROM autonomous_project_change_request WHERE id=$1 FOR UPDATE`,id).Scan(&from);err!=nil{return err};if from==string(to){return tx.Commit(ctx)};if !CanTransitionChangeRequest(ChangeRequestState(from),to){return fmt.Errorf("change request cannot transition from %s to %s",from,to)};if _,err:=tx.Exec(ctx,`UPDATE autonomous_project_change_request SET state=$2,error=NULLIF($3,''),applied_at=CASE WHEN $2='applied' THEN now() ELSE applied_at END,updated_at=now() WHERE id=$1`,id,string(to),errorText);err!=nil{return err};return tx.Commit(ctx)
}

func (s *Store) RecordChangeProposal(ctx context.Context,id pgtype.UUID,proposal any,impact ChangeImpact,proposedSpecification *ProjectSpecification)(ChangeRequest,error){
	cr,err:=s.LoadChangeRequest(ctx,id);if err!=nil{return ChangeRequest{},err};if cr.State==ChangeReceived{if err:=s.AdvanceChangeRequest(ctx,id,ChangeAnalyzing,"");err!=nil{return ChangeRequest{},err};cr.State=ChangeAnalyzing};if cr.State==ChangeProposalReady{if cr.Impact!=nil&&!cr.Impact.AutoApplyAllowed{if err:=s.AdvanceChangeRequest(ctx,id,ChangeApprovalRequired,"");err!=nil{return ChangeRequest{},err};return s.LoadChangeRequest(ctx,id)};return cr,nil};if cr.State==ChangeApprovalRequired{return cr,nil};if cr.State!=ChangeAnalyzing{return ChangeRequest{},fmt.Errorf("change proposal requires analyzing state, got %s",cr.State)}
	proposalJSON,err:=json.Marshal(proposal);if err!=nil{return ChangeRequest{},err};impactJSON,err:=json.Marshal(impact);if err!=nil{return ChangeRequest{},err};proposedID:=""
	if proposedSpecification!=nil{workspaceID,err:=parseProjectUUID(cr.WorkspaceID);if err!=nil{return ChangeRequest{},err};projectID,err:=parseProjectUUID(cr.ProjectID);if err!=nil{return ChangeRequest{},err};revision,err:=s.CreateSpecificationRevision(ctx,workspaceID,projectID,"change_request",cr.ID,*proposedSpecification,false);if err!=nil{return ChangeRequest{},err};proposedID=revision.ID}
	if _,err:=s.pool.Exec(ctx,`UPDATE autonomous_project_change_request SET proposal=$2,impact=$3,proposed_specification_revision_id=COALESCE(NULLIF($4,'')::uuid,proposed_specification_revision_id),state='proposal_ready',updated_at=now() WHERE id=$1 AND state='analyzing'`,id,proposalJSON,impactJSON,proposedID);err!=nil{return ChangeRequest{},err};if !impact.AutoApplyAllowed{if err:=s.AdvanceChangeRequest(ctx,id,ChangeApprovalRequired,"");err!=nil{return ChangeRequest{},err}};return s.LoadChangeRequest(ctx,id)
}

func (s *Store) BindChangeApprovalEscalation(ctx context.Context,id,escalationID pgtype.UUID)error{if !id.Valid||!escalationID.Valid{return errors.New("change request and escalation ids are required")};tag,err:=s.pool.Exec(ctx,`UPDATE autonomous_project_change_request SET approval_escalation_id=$2,updated_at=now() WHERE id=$1 AND state='approval_required'`,id,escalationID);if err!=nil{return err};if tag.RowsAffected()==0{return errors.New("change request is not awaiting approval")};return nil}

func (s *Store) LoadLogicalPlanNodes(ctx context.Context,workspaceID,projectID pgtype.UUID,planID string)([]LogicalPlanNode,error){
	rows,err:=s.pool.Query(ctx,`SELECT logical_node_id,node_key,kind,title,description,priority,COALESCE(required_role_family,''),required_capabilities,acceptance_criteria,risk_level,max_attempts,status,materialized_issue_id FROM autonomous_project_plan_node WHERE workspace_id=$1 AND project_id=$2 AND plan_id=$3::uuid ORDER BY created_at,node_key`,workspaceID,projectID,planID);if err!=nil{return nil,err};defer rows.Close();out:=[]LogicalPlanNode{}
	for rows.Next(){var item LogicalPlanNode;var logicalID,issueID pgtype.UUID;var kind,risk string;var capabilities,criteria []byte;if err:=rows.Scan(&logicalID,&item.Spec.Key,&kind,&item.Spec.Title,&item.Spec.Description,&item.Spec.Priority,&item.Spec.RequiredRoleFamily,&capabilities,&criteria,&risk,&item.Spec.MaxAttempts,&item.Status,&issueID);err!=nil{return nil,err};item.LogicalNodeID=util.UUIDToString(logicalID);item.MaterializedIssueID=util.UUIDToString(issueID);item.Spec.Kind=NodeKind(kind);item.Spec.Risk=RiskLevel(risk);if err:=json.Unmarshal(capabilities,&item.Spec.RequiredCapabilities);err!=nil{return nil,err};if err:=json.Unmarshal(criteria,&item.Spec.AcceptanceCriteria);err!=nil{return nil,err};out=append(out,item)};return out,rows.Err()
}

func (s *Store) loadPlanByID(ctx context.Context,workspaceID,projectID pgtype.UUID,planID string)(StoredPlan,error){var revision int64;if err:=s.pool.QueryRow(ctx,`SELECT revision FROM autonomous_project_plan WHERE id=$1::uuid AND workspace_id=$2 AND project_id=$3`,planID,workspaceID,projectID).Scan(&revision);err!=nil{return StoredPlan{},err};return s.LoadPlan(ctx,workspaceID,projectID,revision)}

func (s *Store) ApplyChangePlanMutation(ctx context.Context,workspaceID,projectID,changeRequestID pgtype.UUID,operations []PlanMutationOperation,plannerName,plannerModel string)(StoredPlan,error){
	cr,err:=s.LoadChangeRequest(ctx,changeRequestID);if err!=nil{return StoredPlan{},err};if cr.WorkspaceID!=util.UUIDToString(workspaceID)||cr.ProjectID!=util.UUIDToString(projectID){return StoredPlan{},errors.New("change request does not belong to workspace/project")};if cr.State==ChangeApplied&&cr.AppliedPlanID!=""{return s.loadPlanByID(ctx,workspaceID,projectID,cr.AppliedPlanID)}
	var mutationBase pgtype.UUID;err=s.pool.QueryRow(ctx,`SELECT base_plan_id FROM autonomous_project_plan_mutation WHERE change_request_id=$1`,changeRequestID).Scan(&mutationBase);var current StoredPlan
	if err==nil&&mutationBase.Valid{current,err=s.loadPlanByID(ctx,workspaceID,projectID,util.UUIDToString(mutationBase));if err!=nil{return StoredPlan{},err}}else if errors.Is(err,pgx.ErrNoRows){var ok bool;current,ok,err=s.LoadLatestPlan(ctx,workspaceID,projectID);if err!=nil{return StoredPlan{},err};if !ok{return StoredPlan{},errors.New("project has no plan to mutate")}}else if err!=nil{return StoredPlan{},err}else{return StoredPlan{},errors.New("change request mutation lost its base plan")}
	nodes,err:=s.LoadLogicalPlanNodes(ctx,workspaceID,projectID,current.ID);if err!=nil{return StoredPlan{},err};impact:=AnalyzeChangeImpact(cr.Type,operations,nodes,current.Plan.Policy);approved:=cr.State==ChangeApproved||cr.State==ChangeApplying;auto:=cr.State==ChangeProposalReady&&impact.AutoApplyAllowed;if !approved&&!auto{return StoredPlan{},fmt.Errorf("change request %s requires approval before plan mutation",cr.ID)}
	result,err:=ApplyPlanMutation(current.Plan,nodes,operations);if err!=nil{return StoredPlan{},err};if cr.ProposedSpecificationRevisionID!=""{var raw []byte;var goal string;if err:=s.pool.QueryRow(ctx,`SELECT goal,specification FROM autonomous_project_specification_revision WHERE id=$1::uuid AND workspace_id=$2 AND project_id=$3`,cr.ProposedSpecificationRevisionID,workspaceID,projectID).Scan(&goal,&raw);err!=nil{return StoredPlan{},err};var spec ProjectSpecification;if err:=json.Unmarshal(raw,&spec);err!=nil{return StoredPlan{},err};result.Plan.Goal=goal;result.Plan.Specification=spec.LegacySpecification()}
	operationsJSON,err:=json.Marshal(operations);if err!=nil{return StoredPlan{},err};var mutationID pgtype.UUID
	if err:=s.pool.QueryRow(ctx,`INSERT INTO autonomous_project_plan_mutation(workspace_id,project_id,change_request_id,base_plan_id,operations,validation_state) VALUES($1,$2,$3,$4::uuid,$5,'validated') ON CONFLICT(change_request_id) DO UPDATE SET operations=CASE WHEN autonomous_project_plan_mutation.validation_state IN('failed','validated') THEN EXCLUDED.operations ELSE autonomous_project_plan_mutation.operations END,validation_state=CASE WHEN autonomous_project_plan_mutation.validation_state='failed' THEN 'validated' ELSE autonomous_project_plan_mutation.validation_state END,validation_error=CASE WHEN autonomous_project_plan_mutation.validation_state='failed' THEN NULL ELSE autonomous_project_plan_mutation.validation_error END RETURNING id`,workspaceID,projectID,changeRequestID,current.ID,operationsJSON).Scan(&mutationID);err!=nil{return StoredPlan{},err}
	if cr.State!=ChangeApplying{if err:=s.AdvanceChangeRequest(ctx,changeRequestID,ChangeApplying,"");err!=nil{return StoredPlan{},err}}
	persisted,err:=s.PersistPlan(ctx,workspaceID,projectID,"change-request:"+cr.ID,plannerName,plannerModel,result.Plan);if err!=nil{_,_=s.pool.Exec(ctx,`UPDATE autonomous_project_plan_mutation SET validation_state='failed',validation_error=$2 WHERE id=$1`,mutationID,err.Error());_=s.AdvanceChangeRequest(ctx,changeRequestID,ChangeFailed,err.Error());return StoredPlan{},err}
	reset:=map[string]struct{}{};for _,id:=range result.ResetLogicalIDs{reset[id]=struct{}{}}
	tx,err:=s.pool.Begin(ctx);if err!=nil{return StoredPlan{},err};defer tx.Rollback(ctx)
	if cr.ProposedSpecificationRevisionID!=""{var revisionID pgtype.UUID;var revision int64;if err:=tx.QueryRow(ctx,`SELECT id,revision FROM autonomous_project_specification_revision WHERE id=$1::uuid AND workspace_id=$2 AND project_id=$3`,cr.ProposedSpecificationRevisionID,workspaceID,projectID).Scan(&revisionID,&revision);err!=nil{return StoredPlan{},err};legacyJSON,err:=json.Marshal(result.Plan.Specification);if err!=nil{return StoredPlan{},err};if _,err:=tx.Exec(ctx,`UPDATE autonomous_project_plan SET specification_revision_id=$2,goal=$3,specification=$4,updated_at=now() WHERE id=$1::uuid`,persisted.ID,revisionID,result.Plan.Goal,legacyJSON);err!=nil{return StoredPlan{},err};if err:=activateSpecificationTx(ctx,tx,workspaceID,projectID,revisionID,revision);err!=nil{return StoredPlan{},err}}
	for _,node:=range result.Nodes{if _,err:=tx.Exec(ctx,`UPDATE autonomous_project_plan_node SET logical_node_id=$3::uuid,updated_at=now() WHERE plan_id=$1::uuid AND node_key=$2`,persisted.ID,node.Spec.Key,node.LogicalNodeID);err!=nil{return StoredPlan{},err};if _,ok:=reset[node.LogicalNodeID];ok{continue};if _,err:=tx.Exec(ctx,`UPDATE autonomous_project_plan_node fresh SET materialized_issue_id=prior.materialized_issue_id,assigned_role=prior.assigned_role,assigned_agent_id=prior.assigned_agent_id,attempt=prior.attempt,status=CASE WHEN prior.status IN('completed','cancelled','running','verification','blocked') THEN prior.status ELSE fresh.status END,ready_at=CASE WHEN prior.status IN('running','verification','blocked') THEN prior.ready_at ELSE fresh.ready_at END,started_at=prior.started_at,completed_at=CASE WHEN prior.status IN('completed','cancelled') THEN prior.completed_at ELSE fresh.completed_at END,blocked_category=CASE WHEN prior.status='blocked' THEN prior.blocked_category ELSE NULL END,blocked_reason=CASE WHEN prior.status='blocked' THEN prior.blocked_reason ELSE NULL END,updated_at=now() FROM autonomous_project_plan_node prior WHERE fresh.plan_id=$1::uuid AND prior.plan_id=$2::uuid AND fresh.logical_node_id=$3::uuid AND prior.logical_node_id=$3::uuid`,persisted.ID,current.ID,node.LogicalNodeID);err!=nil{return StoredPlan{},err}}
	for _,node:=range result.Removed{if _,err:=tx.Exec(ctx,`INSERT INTO autonomous_project_node_retirement(workspace_id,project_id,change_request_id,logical_node_id,prior_plan_node_id,materialized_issue_id,reason) SELECT $1,$2,$3,$4::uuid,n.id,n.materialized_issue_id,$5 FROM autonomous_project_plan_node n WHERE n.plan_id=$6::uuid AND n.logical_node_id=$4::uuid ON CONFLICT(change_request_id,logical_node_id) DO NOTHING`,workspaceID,projectID,changeRequestID,node.LogicalNodeID,"removed by approved plan mutation",current.ID);err!=nil{return StoredPlan{},err}}
	var planUUID pgtype.UUID;if err:=tx.QueryRow(ctx,`SELECT id FROM autonomous_project_plan WHERE id=$1::uuid`,persisted.ID).Scan(&planUUID);err!=nil{return StoredPlan{},err};if err:=refreshReadyTx(ctx,tx,planUUID);err!=nil{return StoredPlan{},err};if _,err:=tx.Exec(ctx,`UPDATE autonomous_project_plan_mutation SET applied_plan_id=$2::uuid,validation_state='applied',applied_at=now() WHERE id=$1`,mutationID,persisted.ID);err!=nil{return StoredPlan{},err};if _,err:=tx.Exec(ctx,`UPDATE autonomous_project_change_request SET applied_plan_id=$2::uuid,state='applied',applied_at=now(),error=NULL,updated_at=now() WHERE id=$1 AND state='applying'`,changeRequestID,persisted.ID);err!=nil{return StoredPlan{},err};if err:=tx.Commit(ctx);err!=nil{return StoredPlan{},err};return persisted,nil
}

type NodeRetirement struct{ID string `json:"id"`;LogicalNodeID string `json:"logical_node_id"`;MaterializedIssueID string `json:"materialized_issue_id,omitempty"`;Reason string `json:"reason"`}

func (s *Store) ClaimNodeRetirements(ctx context.Context,workspaceID,projectID pgtype.UUID,limit int)([]NodeRetirement,error){if limit<=0||limit>50{limit=10};tx,err:=s.pool.Begin(ctx);if err!=nil{return nil,err};defer tx.Rollback(ctx);rows,err:=tx.Query(ctx,`WITH claimed AS(SELECT id FROM autonomous_project_node_retirement WHERE workspace_id=$1 AND project_id=$2 AND status='pending' ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT $3) UPDATE autonomous_project_node_retirement r SET status='claimed',attempt=attempt+1,claimed_at=now(),updated_at=now() FROM claimed c WHERE r.id=c.id RETURNING r.id,r.logical_node_id,r.materialized_issue_id,r.reason`,workspaceID,projectID,limit);if err!=nil{return nil,err};defer rows.Close();out:=[]NodeRetirement{};for rows.Next(){var item NodeRetirement;var id,logicalID,issueID pgtype.UUID;if err:=rows.Scan(&id,&logicalID,&issueID,&item.Reason);err!=nil{return nil,err};item.ID=util.UUIDToString(id);item.LogicalNodeID=util.UUIDToString(logicalID);item.MaterializedIssueID=util.UUIDToString(issueID);out=append(out,item)};if err:=rows.Err();err!=nil{return nil,err};if err:=tx.Commit(ctx);err!=nil{return nil,err};return out,nil}
func (s *Store) CompleteNodeRetirement(ctx context.Context,id pgtype.UUID,succeeded bool,errorText string)error{status:="retired";if !succeeded{status="failed"};_,err:=s.pool.Exec(ctx,`UPDATE autonomous_project_node_retirement SET status=$2,error=NULLIF($3,''),retired_at=CASE WHEN $2='retired' THEN now() ELSE retired_at END,updated_at=now() WHERE id=$1 AND status='claimed'`,id,status,errorText);return err}

func parseProjectUUID(value string)(pgtype.UUID,error){parsed,err:=uuid.Parse(strings.TrimSpace(value));if err!=nil{return pgtype.UUID{},err};return pgtype.UUID{Bytes:[16]byte(parsed),Valid:true},nil}
func validChangeRequestType(v ChangeRequestType)bool{switch v{case ChangeFeature,ChangeRequirement,ChangeRemoveFeature,ChangeArchitecture,ChangePriority,ChangeTeam,ChangePolicy,ChangeBug,ChangeQuestion:return true;default:return false}}
func validChangeRequestSource(v ChangeRequestSource)bool{switch v{case ChangeSourceProjectDirector,ChangeSourceMika,ChangeSourceSystem:return true;default:return false}}
