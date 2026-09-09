package projectorchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	ProjectLeaderOpAddNode              = "add_node"
	ProjectLeaderOpUpdateNotStartedNode = "update_not_started_node"
	ProjectLeaderOpCancelNotStartedNode = "cancel_not_started_node"
	ProjectLeaderOpAddDependency        = "add_dependency"
	ProjectLeaderOpRemoveDependency     = "remove_dependency"
	ProjectLeaderOpReplaceDependency    = "replace_dependency"
)

// ProjectLeaderResponse is the only structured response shape accepted from the
// reasoning-only Project Leader runtime. The model may explain and propose; it
// never mutates Project OS state directly.
type ProjectLeaderResponse struct {
	Message  string                 `json:"message"`
	Proposal *ProjectChangeProposal `json:"proposal,omitempty"`
}

type ProjectChangeProposal struct {
	ProposalID       string                   `json:"proposal_id"`
	Summary          string                   `json:"summary"`
	Rationale        string                   `json:"rationale"`
	BasePlanRevision int64                    `json:"base_plan_revision"`
	Operations       []ProjectChangeOperation `json:"operations"`
}

type ProjectChangeOperation struct {
	Type       string                 `json:"type"`
	NodeID     *uuid.UUID             `json:"node_id,omitempty"`
	NodeRef    string                 `json:"node_ref,omitempty"`
	Node       *NodeSpec              `json:"node,omitempty"`
	Dependency *EdgeSpec              `json:"dependency,omitempty"`
	Replacement *DependencyReplacement `json:"replacement,omitempty"`
	Patch      map[string]any         `json:"patch,omitempty"`
	Reason     string                 `json:"reason"`
}

type ProjectLeaderApplyResult struct {
	ChangeRequest ChangeRequest `json:"change_request"`
	AppliedPlan   *StoredPlan   `json:"applied_plan,omitempty"`
}

// ValidateProjectLeaderResponse validates model output before it is persisted as
// a proposal. Unknown operation types and untyped mutation payloads fail closed.
func ValidateProjectLeaderResponse(response ProjectLeaderResponse) error {
	if strings.TrimSpace(response.Message) == "" {
		return errors.New("project leader response message is required")
	}
	if response.Proposal == nil {
		return nil
	}
	return ValidateProjectChangeProposal(*response.Proposal)
}

func ValidateProjectChangeProposal(proposal ProjectChangeProposal) error {
	if strings.TrimSpace(proposal.ProposalID) == "" {
		return errors.New("project change proposal id is required")
	}
	if strings.TrimSpace(proposal.Summary) == "" || strings.TrimSpace(proposal.Rationale) == "" {
		return errors.New("project change proposal summary and rationale are required")
	}
	if proposal.BasePlanRevision <= 0 {
		return errors.New("project change proposal base_plan_revision must be positive")
	}
	if len(proposal.Operations) == 0 {
		return errors.New("project change proposal requires at least one operation")
	}
	if len(proposal.Operations) > DefaultMaxNodes*3 {
		return errors.New("project change proposal contains too many operations")
	}
	for i, op := range proposal.Operations {
		if strings.TrimSpace(op.Reason) == "" {
			return fmt.Errorf("project change operation %d requires reason", i)
		}
		switch op.Type {
		case ProjectLeaderOpAddNode:
			if op.Node == nil || op.NodeID != nil || len(op.Patch) != 0 || op.Dependency != nil || op.Replacement != nil {
				return fmt.Errorf("project change operation %d add_node has invalid shape", i)
			}
		case ProjectLeaderOpUpdateNotStartedNode:
			if op.NodeID == nil || len(op.Patch) == 0 || op.Node != nil || op.Dependency != nil || op.Replacement != nil {
				return fmt.Errorf("project change operation %d update_not_started_node has invalid shape", i)
			}
			if err := validateProjectLeaderPatch(op.Patch); err != nil {
				return fmt.Errorf("project change operation %d: %w", i, err)
			}
		case ProjectLeaderOpCancelNotStartedNode:
			if op.NodeID == nil || op.Node != nil || len(op.Patch) != 0 || op.Dependency != nil || op.Replacement != nil {
				return fmt.Errorf("project change operation %d cancel_not_started_node has invalid shape", i)
			}
		case ProjectLeaderOpAddDependency, ProjectLeaderOpRemoveDependency:
			if op.Dependency == nil || op.Node != nil || op.NodeID != nil || len(op.Patch) != 0 || op.Replacement != nil {
				return fmt.Errorf("project change operation %d dependency operation has invalid shape", i)
			}
		case ProjectLeaderOpReplaceDependency:
			if op.Replacement == nil || op.Node != nil || op.NodeID != nil || len(op.Patch) != 0 || op.Dependency != nil {
				return fmt.Errorf("project change operation %d replace_dependency has invalid shape", i)
			}
		default:
			return fmt.Errorf("unsupported project change operation type %q", op.Type)
		}
	}
	return nil
}

var projectLeaderPatchFields = map[string]struct{}{
	"title": {}, "description": {}, "priority": {}, "required_role_family": {},
	"required_capabilities": {}, "acceptance_criteria": {}, "risk": {}, "max_attempts": {},
}

func validateProjectLeaderPatch(patch map[string]any) error {
	for key := range patch {
		if _, ok := projectLeaderPatchFields[key]; !ok {
			return fmt.Errorf("unsupported node patch field %q", key)
		}
	}
	return nil
}

// CompileProjectLeaderProposal converts the model-facing allowlist into the
// existing Project OS mutation vocabulary. It also enforces the irreversible
// execution boundary: once a node was materialized or execution began, the
// existing node and its dependency topology are immutable. A revision must add
// follow-up work instead of rewriting history.
func CompileProjectLeaderProposal(proposal ProjectChangeProposal, current []LogicalPlanNode) ([]PlanMutationOperation, error) {
	if err := ValidateProjectChangeProposal(proposal); err != nil {
		return nil, err
	}
	byID := make(map[string]LogicalPlanNode, len(current))
	byKey := make(map[string]LogicalPlanNode, len(current))
	for _, node := range current {
		byID[node.LogicalNodeID] = node
		byKey[node.Spec.Key] = node
	}

	ensureMutableID := func(id *uuid.UUID) (LogicalPlanNode, error) {
		if id == nil {
			return LogicalPlanNode{}, errors.New("node id is required")
		}
		node, ok := byID[id.String()]
		if !ok {
			return LogicalPlanNode{}, fmt.Errorf("logical node %s is unknown", id.String())
		}
		if !projectLeaderNodeMutable(node) {
			return LogicalPlanNode{}, fmt.Errorf("logical node %s is immutable after materialization/execution; add follow-up work instead", id.String())
		}
		return node, nil
	}
	ensureMutableKey := func(key string) error {
		node, ok := byKey[strings.TrimSpace(key)]
		if !ok {
			return fmt.Errorf("dependency references unknown node %q", key)
		}
		if !projectLeaderNodeMutable(node) {
			return fmt.Errorf("dependency topology for node %s is immutable after materialization/execution", node.LogicalNodeID)
		}
		return nil
	}

	result := make([]PlanMutationOperation, 0, len(proposal.Operations))
	for _, op := range proposal.Operations {
		switch op.Type {
		case ProjectLeaderOpAddNode:
			node := *op.Node
			result = append(result, PlanMutationOperation{Operation: MutationAddNode, Node: &node, Reason: op.Reason})
		case ProjectLeaderOpUpdateNotStartedNode:
			currentNode, err := ensureMutableID(op.NodeID)
			if err != nil { return nil, err }
			patched, err := patchProjectLeaderNode(currentNode.Spec, op.Patch)
			if err != nil { return nil, err }
			result = append(result, PlanMutationOperation{Operation: MutationUpdateNode, TargetLogicalNodeID: currentNode.LogicalNodeID, Node: &patched, Reason: op.Reason})
		case ProjectLeaderOpCancelNotStartedNode:
			currentNode, err := ensureMutableID(op.NodeID)
			if err != nil { return nil, err }
			result = append(result, PlanMutationOperation{Operation: MutationRemoveNode, TargetLogicalNodeID: currentNode.LogicalNodeID, Reason: op.Reason})
		case ProjectLeaderOpAddDependency:
			// Adding a dependency to an already-started target changes the contract
			// under running work; both endpoints must still be mutable.
			if err := ensureMutableKey(op.Dependency.From); err != nil { return nil, err }
			if err := ensureMutableKey(op.Dependency.To); err != nil { return nil, err }
			edge := *op.Dependency
			result = append(result, PlanMutationOperation{Operation: MutationAddEdge, Edge: &edge, Reason: op.Reason})
		case ProjectLeaderOpRemoveDependency:
			if err := ensureMutableKey(op.Dependency.From); err != nil { return nil, err }
			if err := ensureMutableKey(op.Dependency.To); err != nil { return nil, err }
			edge := *op.Dependency
			result = append(result, PlanMutationOperation{Operation: MutationRemoveEdge, Edge: &edge, Reason: op.Reason})
		case ProjectLeaderOpReplaceDependency:
			for _, edge := range []EdgeSpec{op.Replacement.Old, op.Replacement.New} {
				if err := ensureMutableKey(edge.From); err != nil { return nil, err }
				if err := ensureMutableKey(edge.To); err != nil { return nil, err }
			}
			replacement := *op.Replacement
			result = append(result, PlanMutationOperation{Operation: MutationReplaceDependency, Replacement: &replacement, Reason: op.Reason})
		}
	}
	return result, nil
}

func projectLeaderNodeMutable(node LogicalPlanNode) bool {
	if strings.TrimSpace(node.MaterializedIssueID) != "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(node.Status)) {
	case "", "pending", "ready":
		return true
	default:
		return false
	}
}

func patchProjectLeaderNode(node NodeSpec, patch map[string]any) (NodeSpec, error) {
	if err := validateProjectLeaderPatch(patch); err != nil { return NodeSpec{}, err }
	raw, err := json.Marshal(node)
	if err != nil { return NodeSpec{}, err }
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil { return NodeSpec{}, err }
	for key, value := range patch { object[key] = value }
	raw, err = json.Marshal(object)
	if err != nil { return NodeSpec{}, err }
	var out NodeSpec
	if err := json.Unmarshal(raw, &out); err != nil { return NodeSpec{}, fmt.Errorf("decode patched node: %w", err) }
	return out, nil
}

// ApplyProjectLeaderProposal is the deterministic apply boundary used by chat
// orchestration. It fences the proposal to the exact plan revision the model
// observed, persists it through the existing change-request ledger, and either
// applies an allowed closed-loop change or leaves a durable approval request.
func (s *Store) ApplyProjectLeaderProposal(ctx context.Context, workspaceID, projectID pgtype.UUID, proposal ProjectChangeProposal, plannerName, plannerModel string) (ProjectLeaderApplyResult, error) {
	if s == nil || s.pool == nil { return ProjectLeaderApplyResult{}, errors.New("project orchestration store is not configured") }
	if !workspaceID.Valid || !projectID.Valid { return ProjectLeaderApplyResult{}, errors.New("workspace_id and project_id are required") }
	if err := ValidateProjectChangeProposal(proposal); err != nil { return ProjectLeaderApplyResult{}, err }

	current, ok, err := s.LoadLatestPlan(ctx, workspaceID, projectID)
	if err != nil { return ProjectLeaderApplyResult{}, err }
	if !ok { return ProjectLeaderApplyResult{}, errors.New("project has no plan to mutate") }
	if current.Revision != proposal.BasePlanRevision {
		return ProjectLeaderApplyResult{}, fmt.Errorf("stale project leader proposal: base revision %d, current revision %d", proposal.BasePlanRevision, current.Revision)
	}
	nodes, err := s.LoadLogicalPlanNodes(ctx, workspaceID, projectID, current.ID)
	if err != nil { return ProjectLeaderApplyResult{}, err }
	operations, err := CompileProjectLeaderProposal(proposal, nodes)
	if err != nil { return ProjectLeaderApplyResult{}, err }

	proposalID := strings.TrimSpace(proposal.ProposalID)
	requestKey := "project-leader:" + proposalID
	requestText := strings.TrimSpace(proposal.Summary) + "\n\n" + strings.TrimSpace(proposal.Rationale)
	cr, err := s.ReceiveChangeRequest(ctx, workspaceID, projectID, requestKey, ChangeRequirement, ChangeSourceProjectDirector, proposalID, requestText)
	if err != nil { return ProjectLeaderApplyResult{}, err }
	if cr.State == ChangeApplied && cr.AppliedPlanID != "" {
		plan, err := s.loadPlanByID(ctx, workspaceID, projectID, cr.AppliedPlanID)
		if err != nil { return ProjectLeaderApplyResult{}, err }
		return ProjectLeaderApplyResult{ChangeRequest: cr, AppliedPlan: &plan}, nil
	}

	impact := AnalyzeChangeImpact(cr.Type, operations, nodes, current.Plan.Policy)
	cr, err = s.RecordChangeProposal(ctx, mustProjectUUID(cr.ID), proposal, impact, nil)
	if err != nil { return ProjectLeaderApplyResult{}, err }
	if cr.State == ChangeApprovalRequired {
		return ProjectLeaderApplyResult{ChangeRequest: cr}, nil
	}
	if cr.State != ChangeProposalReady && cr.State != ChangeApproved && cr.State != ChangeApplying {
		return ProjectLeaderApplyResult{}, fmt.Errorf("project leader proposal cannot apply from change request state %s", cr.State)
	}
	plan, err := s.ApplyChangePlanMutation(ctx, workspaceID, projectID, mustProjectUUID(cr.ID), operations, plannerName, plannerModel)
	if err != nil { return ProjectLeaderApplyResult{}, err }
	cr, err = s.LoadChangeRequest(ctx, mustProjectUUID(cr.ID))
	if err != nil { return ProjectLeaderApplyResult{}, err }
	return ProjectLeaderApplyResult{ChangeRequest: cr, AppliedPlan: &plan}, nil
}

func mustProjectUUID(value string) pgtype.UUID {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil { return pgtype.UUID{} }
	return pgtype.UUID{Bytes: [16]byte(parsed), Valid: true}
}

// StableProjectLeaderOperations is useful in audit/UI projections: it returns a
// deterministic list without changing proposal semantics.
func StableProjectLeaderOperations(ops []ProjectChangeOperation) []ProjectChangeOperation {
	out := append([]ProjectChangeOperation(nil), ops...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Type == out[j].Type { return out[i].NodeRef < out[j].NodeRef }
		return out[i].Type < out[j].Type
	})
	return out
}
