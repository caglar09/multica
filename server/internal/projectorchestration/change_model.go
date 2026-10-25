package projectorchestration

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
)

type ChangeRequestType string

const (
	ChangeFeature       ChangeRequestType = "feature"
	ChangeRequirement   ChangeRequestType = "requirement_change"
	ChangeRemoveFeature ChangeRequestType = "remove_feature"
	ChangeArchitecture  ChangeRequestType = "architecture_change"
	ChangePriority      ChangeRequestType = "priority_change"
	ChangeTeam          ChangeRequestType = "team_change"
	ChangePolicy        ChangeRequestType = "policy_change"
	ChangeBug           ChangeRequestType = "bug"
	ChangeQuestion      ChangeRequestType = "question"
)

type ChangeRequestState string

const (
	ChangeReceived         ChangeRequestState = "received"
	ChangeAnalyzing        ChangeRequestState = "analyzing"
	ChangeProposalReady    ChangeRequestState = "proposal_ready"
	ChangeApprovalRequired ChangeRequestState = "approval_required"
	ChangeApproved         ChangeRequestState = "approved"
	ChangeApplying         ChangeRequestState = "applying"
	ChangeApplied          ChangeRequestState = "applied"
	ChangeRejected         ChangeRequestState = "rejected"
	ChangeFailed           ChangeRequestState = "failed"
)

type ChangeRequestSource string

const (
	ChangeSourceProjectDirector ChangeRequestSource = "project_director"
	ChangeSourceMika            ChangeRequestSource = "mika"
	ChangeSourceSystem          ChangeRequestSource = "system"
)

type SpecificationRequirement struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	Rationale string `json:"rationale"`
	Source    string `json:"source"`
}

type ProjectSpecification struct {
	Goal                      string                     `json:"goal"`
	Summary                   string                     `json:"summary"`
	Requirements              []SpecificationRequirement `json:"requirements"`
	NonFunctionalRequirements []string                   `json:"non_functional_requirements"`
	Constraints               []string                   `json:"constraints"`
	NonGoals                  []string                   `json:"non_goals"`
	DefinitionOfDone          []string                   `json:"definition_of_done"`
	AcceptanceExpectations    []string                   `json:"acceptance_expectations"`
}

func ValidateProjectSpecification(spec ProjectSpecification) error {
	if strings.TrimSpace(spec.Goal) == "" {
		return errors.New("project specification goal is required")
	}
	if strings.TrimSpace(spec.Summary) == "" {
		return errors.New("project specification summary is required")
	}
	if len(spec.Requirements) == 0 {
		return errors.New("project specification requires at least one requirement")
	}
	seen := map[string]struct{}{}
	for i, req := range spec.Requirements {
		id := strings.TrimSpace(req.ID)
		if id == "" || strings.TrimSpace(req.Text) == "" || strings.TrimSpace(req.Rationale) == "" || strings.TrimSpace(req.Source) == "" {
			return fmt.Errorf("project specification requirement %d requires id, text, rationale and source", i)
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("duplicate project specification requirement id %q", id)
		}
		seen[id] = struct{}{}
	}
	if len(spec.DefinitionOfDone) == 0 {
		return errors.New("project specification definition_of_done is required")
	}
	return nil
}

func (s ProjectSpecification) LegacySpecification() Specification {
	requirements := make([]string, 0, len(s.Requirements))
	for _, req := range s.Requirements {
		if text := strings.TrimSpace(req.Text); text != "" {
			requirements = append(requirements, text)
		}
	}
	return Specification{
		Summary: s.Summary,
		Requirements: requirements,
		NonFunctionalRequirements: append([]string(nil), s.NonFunctionalRequirements...),
		Constraints: append([]string(nil), s.Constraints...),
		DefinitionOfDone: append([]string(nil), s.DefinitionOfDone...),
	}
}

type PlanMutationOperationType string

const (
	MutationAddNode           PlanMutationOperationType = "ADD_NODE"
	MutationUpdateNode        PlanMutationOperationType = "UPDATE_NODE"
	MutationRemoveNode        PlanMutationOperationType = "REMOVE_NODE"
	MutationAddEdge           PlanMutationOperationType = "ADD_EDGE"
	MutationRemoveEdge        PlanMutationOperationType = "REMOVE_EDGE"
	MutationReplaceDependency PlanMutationOperationType = "REPLACE_DEPENDENCY"
)

type DependencyReplacement struct {
	Old EdgeSpec `json:"old"`
	New EdgeSpec `json:"new"`
}

type PlanMutationOperation struct {
	Operation           PlanMutationOperationType `json:"operation"`
	TargetLogicalNodeID string                    `json:"target_logical_node_id,omitempty"`
	Node                *NodeSpec                 `json:"node,omitempty"`
	Edge                *EdgeSpec                 `json:"edge,omitempty"`
	Replacement         *DependencyReplacement    `json:"replacement,omitempty"`
	ResetExecution      bool                      `json:"reset_execution,omitempty"`
	Reason              string                    `json:"reason,omitempty"`
}

type LogicalPlanNode struct {
	LogicalNodeID       string   `json:"logical_node_id"`
	Spec                NodeSpec `json:"spec"`
	Status              string   `json:"status"`
	MaterializedIssueID string   `json:"materialized_issue_id,omitempty"`
}

type PlanMutationResult struct {
	Plan            Plan              `json:"plan"`
	Nodes           []LogicalPlanNode `json:"nodes"`
	Removed         []LogicalPlanNode `json:"removed,omitempty"`
	ResetLogicalIDs []string          `json:"reset_logical_ids,omitempty"`
}

func ApplyPlanMutation(current Plan, currentNodes []LogicalPlanNode, operations []PlanMutationOperation) (PlanMutationResult, error) {
	if len(operations) == 0 {
		return PlanMutationResult{}, errors.New("plan mutation requires at least one operation")
	}
	plan := current
	plan.Nodes = append([]NodeSpec(nil), current.Nodes...)
	plan.Edges = append([]EdgeSpec(nil), current.Edges...)
	byID := map[string]LogicalPlanNode{}
	byKey := map[string]string{}
	for _, node := range currentNodes {
		if strings.TrimSpace(node.LogicalNodeID) == "" || strings.TrimSpace(node.Spec.Key) == "" {
			return PlanMutationResult{}, errors.New("current logical nodes require logical id and key")
		}
		byID[node.LogicalNodeID] = node
		byKey[node.Spec.Key] = node.LogicalNodeID
	}
	var removed []LogicalPlanNode
	reset := map[string]struct{}{}
	for i, op := range operations {
		switch op.Operation {
		case MutationAddNode:
			if op.Node == nil || strings.TrimSpace(op.Node.Key) == "" {
				return PlanMutationResult{}, fmt.Errorf("operation %d ADD_NODE requires node", i)
			}
			if _, exists := byKey[op.Node.Key]; exists {
				return PlanMutationResult{}, fmt.Errorf("ADD_NODE key %q already exists", op.Node.Key)
			}
			spec := *op.Node
			id := uuid.NewString()
			plan.Nodes = append(plan.Nodes, spec)
			byID[id] = LogicalPlanNode{LogicalNodeID: id, Spec: spec, Status: "pending"}
			byKey[spec.Key] = id
		case MutationUpdateNode:
			if op.Node == nil || strings.TrimSpace(op.TargetLogicalNodeID) == "" {
				return PlanMutationResult{}, fmt.Errorf("operation %d UPDATE_NODE requires target logical id and node", i)
			}
			prior, ok := byID[op.TargetLogicalNodeID]
			if !ok {
				return PlanMutationResult{}, fmt.Errorf("UPDATE_NODE target %q is unknown", op.TargetLogicalNodeID)
			}
			if other, exists := byKey[op.Node.Key]; exists && other != prior.LogicalNodeID {
				return PlanMutationResult{}, fmt.Errorf("UPDATE_NODE key %q belongs to another logical node", op.Node.Key)
			}
			idx := planNodeIndex(plan.Nodes, prior.Spec.Key)
			if idx < 0 {
				return PlanMutationResult{}, fmt.Errorf("UPDATE_NODE current key %q not found", prior.Spec.Key)
			}
			oldKey := prior.Spec.Key
			prior.Spec = *op.Node
			plan.Nodes[idx] = prior.Spec
			if oldKey != prior.Spec.Key {
				for j := range plan.Edges {
					if plan.Edges[j].From == oldKey { plan.Edges[j].From = prior.Spec.Key }
					if plan.Edges[j].To == oldKey { plan.Edges[j].To = prior.Spec.Key }
				}
				delete(byKey, oldKey)
			}
			byID[prior.LogicalNodeID] = prior
			byKey[prior.Spec.Key] = prior.LogicalNodeID
			if op.ResetExecution { reset[prior.LogicalNodeID] = struct{}{} }
		case MutationRemoveNode:
			prior, ok := byID[op.TargetLogicalNodeID]
			if !ok || strings.TrimSpace(op.TargetLogicalNodeID) == "" {
				return PlanMutationResult{}, fmt.Errorf("REMOVE_NODE target %q is unknown", op.TargetLogicalNodeID)
			}
			idx := planNodeIndex(plan.Nodes, prior.Spec.Key)
			if idx < 0 { return PlanMutationResult{}, fmt.Errorf("REMOVE_NODE current key %q not found", prior.Spec.Key) }
			plan.Nodes = append(plan.Nodes[:idx], plan.Nodes[idx+1:]...)
			edges := plan.Edges[:0]
			for _, edge := range plan.Edges {
				if edge.From != prior.Spec.Key && edge.To != prior.Spec.Key { edges = append(edges, edge) }
			}
			plan.Edges = edges
			removed = append(removed, prior)
			delete(byID, prior.LogicalNodeID)
			delete(byKey, prior.Spec.Key)
		case MutationAddEdge:
			if op.Edge == nil { return PlanMutationResult{}, fmt.Errorf("operation %d ADD_EDGE requires edge", i) }
			if planEdgeIndex(plan.Edges, *op.Edge) >= 0 { return PlanMutationResult{}, fmt.Errorf("ADD_EDGE %s -> %s already exists", op.Edge.From, op.Edge.To) }
			plan.Edges = append(plan.Edges, *op.Edge)
		case MutationRemoveEdge:
			if op.Edge == nil { return PlanMutationResult{}, fmt.Errorf("operation %d REMOVE_EDGE requires edge", i) }
			idx := planEdgeIndex(plan.Edges, *op.Edge)
			if idx < 0 { return PlanMutationResult{}, fmt.Errorf("REMOVE_EDGE %s -> %s not found", op.Edge.From, op.Edge.To) }
			plan.Edges = append(plan.Edges[:idx], plan.Edges[idx+1:]...)
		case MutationReplaceDependency:
			if op.Replacement == nil { return PlanMutationResult{}, fmt.Errorf("operation %d REPLACE_DEPENDENCY requires replacement", i) }
			idx := planEdgeIndex(plan.Edges, op.Replacement.Old)
			if idx < 0 { return PlanMutationResult{}, fmt.Errorf("REPLACE_DEPENDENCY old edge %s -> %s not found", op.Replacement.Old.From, op.Replacement.Old.To) }
			plan.Edges[idx] = op.Replacement.New
		default:
			return PlanMutationResult{}, fmt.Errorf("unsupported plan mutation operation %q", op.Operation)
		}
	}
	plan = HardenPlan(plan)
	plan = EnsureLifecycle(plan)
	plan = HardenPlan(plan)
	if err := ValidatePlan(plan, DefaultMaxNodes); err != nil {
		return PlanMutationResult{}, fmt.Errorf("validate mutated plan: %w", err)
	}
	for _, spec := range plan.Nodes {
		if _, exists := byKey[spec.Key]; exists { continue }
		id := uuid.NewString()
		byID[id] = LogicalPlanNode{LogicalNodeID: id, Spec: spec, Status: "pending"}
		byKey[spec.Key] = id
	}
	nodes := make([]LogicalPlanNode, 0, len(plan.Nodes))
	for _, spec := range plan.Nodes {
		id := byKey[spec.Key]
		node := byID[id]
		node.Spec = spec
		nodes = append(nodes, node)
	}
	resetIDs := make([]string, 0, len(reset))
	for id := range reset { resetIDs = append(resetIDs, id) }
	sort.Strings(resetIDs)
	return PlanMutationResult{Plan: plan, Nodes: nodes, Removed: removed, ResetLogicalIDs: resetIDs}, nil
}

func planNodeIndex(nodes []NodeSpec, key string) int {
	for i := range nodes { if nodes[i].Key == key { return i } }
	return -1
}

func planEdgeIndex(edges []EdgeSpec, target EdgeSpec) int {
	for i := range edges {
		if edges[i].From == target.From && edges[i].To == target.To && edges[i].Type == target.Type && edges[i].RequiredArtifactType == target.RequiredArtifactType { return i }
	}
	return -1
}

type ChangeImpactLevel string

const (
	ImpactLow ChangeImpactLevel = "LOW"
	ImpactMedium ChangeImpactLevel = "MEDIUM"
	ImpactHigh ChangeImpactLevel = "HIGH"
	ImpactDestructive ChangeImpactLevel = "DESTRUCTIVE"
)

type ChangeImpact struct {
	Level ChangeImpactLevel `json:"level"`
	RequiresApproval bool `json:"requires_approval"`
	AutoApplyAllowed bool `json:"auto_apply_allowed"`
	RunningLogicalNodeIDs []string `json:"running_logical_node_ids,omitempty"`
	CompletedLogicalNodeIDs []string `json:"completed_logical_node_ids,omitempty"`
	ArtifactImpact bool `json:"artifact_impact"`
	TeamImpact bool `json:"team_impact"`
	ArchitectureImpact bool `json:"architecture_impact"`
	Reasons []string `json:"reasons"`
}

func AnalyzeChangeImpact(kind ChangeRequestType, operations []PlanMutationOperation, current []LogicalPlanNode, policy Policy) ChangeImpact {
	impact := ChangeImpact{Level: ImpactLow}
	byID := make(map[string]LogicalPlanNode, len(current))
	for _, node := range current { byID[node.LogicalNodeID] = node }
	raise := func(level ChangeImpactLevel, reason string) {
		if impactRank(level) > impactRank(impact.Level) { impact.Level = level }
		if strings.TrimSpace(reason) != "" { impact.Reasons = append(impact.Reasons, reason) }
	}
	switch kind {
	case ChangeArchitecture:
		impact.ArchitectureImpact, impact.RequiresApproval = true, true
		raise(ImpactHigh, "architecture changes require explicit approval")
	case ChangePolicy:
		impact.RequiresApproval = true
		raise(ImpactHigh, "project policy changes require explicit approval")
	case ChangeTeam:
		impact.TeamImpact = true
		raise(ImpactMedium, "team composition is a separate controlled mutation")
	case ChangeRemoveFeature:
		raise(ImpactMedium, "feature removal can retire existing work")
	}
	for _, op := range operations {
		if op.Operation == MutationAddNode && op.Node != nil {
			if architectureOrProductionNode(op.Node.Kind) {
				impact.RequiresApproval = true
				impact.ArchitectureImpact = op.Node.Kind == NodeArchitecture || op.Node.Kind == NodeMigration
				raise(ImpactHigh, "architecture, database or production lifecycle work requires approval")
			} else { raise(ImpactMedium, "plan gains executable work") }
		}
		node, ok := byID[op.TargetLogicalNodeID]
		if !ok { continue }
		if (node.Status == "running" || node.Status == "verification") && (op.Operation == MutationRemoveNode || op.ResetExecution) {
			impact.RunningLogicalNodeIDs = append(impact.RunningLogicalNodeIDs, node.LogicalNodeID)
			impact.RequiresApproval = true
			raise(ImpactHigh, "change cancels or resets running work")
		}
		if node.Status == "completed" && (op.Operation == MutationRemoveNode || op.ResetExecution) {
			impact.CompletedLogicalNodeIDs = append(impact.CompletedLogicalNodeIDs, node.LogicalNodeID)
			impact.ArtifactImpact, impact.RequiresApproval = true, true
			if architectureOrProductionNode(node.Spec.Kind) { raise(ImpactDestructive, "change invalidates completed architecture/database/production work") } else { raise(ImpactHigh, "change invalidates completed work and its artifacts") }
		}
		if architectureOrProductionNode(node.Spec.Kind) && (op.Operation == MutationRemoveNode || op.Operation == MutationUpdateNode) {
			impact.RequiresApproval, impact.ArchitectureImpact = true, true
			raise(ImpactHigh, "architecture/database/production node is being changed")
		}
	}
	impact.AutoApplyAllowed = impact.Level == ImpactLow && !impact.RequiresApproval && policy.Autonomy == AutonomyClosedLoop
	if len(impact.Reasons) == 0 { impact.Reasons = []string{"localized change has no running/completed/architecture policy impact"} }
	return impact
}

func architectureOrProductionNode(kind NodeKind) bool {
	switch kind { case NodeArchitecture, NodeMigration, NodeDeploy, NodeRelease: return true; default: return false }
}

func impactRank(level ChangeImpactLevel) int {
	switch level { case ImpactLow: return 1; case ImpactMedium: return 2; case ImpactHigh: return 3; case ImpactDestructive: return 4; default: return 0 }
}

func CanTransitionChangeRequest(from, to ChangeRequestState) bool {
	allowed := map[ChangeRequestState]map[ChangeRequestState]struct{}{
		ChangeReceived: {ChangeAnalyzing: {}, ChangeRejected: {}, ChangeFailed: {}},
		ChangeAnalyzing: {ChangeProposalReady: {}, ChangeApprovalRequired: {}, ChangeRejected: {}, ChangeFailed: {}},
		ChangeProposalReady: {ChangeApproved: {}, ChangeApplying: {}, ChangeApprovalRequired: {}, ChangeRejected: {}, ChangeFailed: {}},
		ChangeApprovalRequired: {ChangeApproved: {}, ChangeRejected: {}, ChangeFailed: {}},
		ChangeApproved: {ChangeApplying: {}, ChangeRejected: {}, ChangeFailed: {}},
		ChangeApplying: {ChangeApplied: {}, ChangeFailed: {}},
		ChangeFailed: {ChangeAnalyzing: {}, ChangeRejected: {}},
	}
	_, ok := allowed[from][to]
	return ok
}
