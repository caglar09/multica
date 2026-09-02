package projectorchestration

import (
	"errors"
	"fmt"
	"strings"
)

var ErrInvalidPlan = errors.New("invalid autonomous project plan")

const DefaultMaxNodes = 100

var validKinds = map[NodeKind]struct{}{
	NodeResearch: {}, NodeProduct: {}, NodeArchitecture: {}, NodeDesign: {},
	NodeImplementation: {}, NodeMigration: {}, NodeIntegration: {}, NodeReview: {},
	NodeQA: {}, NodeSecurity: {}, NodeRelease: {}, NodeDeploy: {}, NodeObserve: {},
	NodeIncident: {},
}

var validRisks = map[RiskLevel]struct{}{
	RiskLow: {}, RiskMedium: {}, RiskHigh: {}, RiskCritical: {},
}

var validDependencies = map[DependencyType]struct{}{
	DependencyHard: {}, DependencySoft: {}, DependencyArtifact: {},
}

func DefaultPolicy() Policy {
	return Policy{
		Autonomy:        AutonomyDevelopment,
		Runtime:         RuntimeInheritMika,
		SkillsAutomatic: true,
		Approvals: ApprovalPolicy{
			DatabaseMigration: true,
			ProductionDeploy:  true,
			MajorDependency:   true,
			CriticalRisk:      true,
		},
		Budget: BudgetPolicy{
			MaxParallelNodes: 4,
			MaxTotalAttempts: 100,
		},
	}
}

func ValidatePlan(plan Plan, maxNodes int) error {
	if plan.Version != CurrentPlanVersion {
		return fmt.Errorf("%w: unsupported version %d", ErrInvalidPlan, plan.Version)
	}
	if strings.TrimSpace(plan.Goal) == "" {
		return fmt.Errorf("%w: goal is required", ErrInvalidPlan)
	}
	if strings.TrimSpace(plan.Specification.Summary) == "" {
		return fmt.Errorf("%w: specification.summary is required", ErrInvalidPlan)
	}
	if len(plan.Specification.Requirements) == 0 {
		return fmt.Errorf("%w: at least one requirement is required", ErrInvalidPlan)
	}
	if len(plan.Specification.DefinitionOfDone) == 0 {
		return fmt.Errorf("%w: definition_of_done is required", ErrInvalidPlan)
	}
	if maxNodes <= 0 {
		maxNodes = DefaultMaxNodes
	}
	if len(plan.Nodes) == 0 || len(plan.Nodes) > maxNodes {
		return fmt.Errorf("%w: node count must be between 1 and %d", ErrInvalidPlan, maxNodes)
	}
	if err := validatePolicy(plan.Policy); err != nil {
		return err
	}

	nodes := make(map[string]NodeSpec, len(plan.Nodes))
	for i, node := range plan.Nodes {
		node.Key = strings.TrimSpace(node.Key)
		if node.Key == "" {
			return fmt.Errorf("%w: nodes[%d].key is required", ErrInvalidPlan, i)
		}
		if _, exists := nodes[node.Key]; exists {
			return fmt.Errorf("%w: duplicate node key %q", ErrInvalidPlan, node.Key)
		}
		if _, ok := validKinds[node.Kind]; !ok {
			return fmt.Errorf("%w: node %q has unsupported kind %q", ErrInvalidPlan, node.Key, node.Kind)
		}
		if strings.TrimSpace(node.Title) == "" {
			return fmt.Errorf("%w: node %q title is required", ErrInvalidPlan, node.Key)
		}
		if _, ok := validRisks[node.Risk]; !ok {
			return fmt.Errorf("%w: node %q has unsupported risk %q", ErrInvalidPlan, node.Key, node.Risk)
		}
		if node.MaxAttempts <= 0 || node.MaxAttempts > 20 {
			return fmt.Errorf("%w: node %q max_attempts must be between 1 and 20", ErrInvalidPlan, node.Key)
		}
		if requiresAcceptance(node.Kind) && len(node.AcceptanceCriteria) == 0 {
			return fmt.Errorf("%w: node %q requires acceptance criteria", ErrInvalidPlan, node.Key)
		}
		nodes[node.Key] = node
	}

	seenEdges := make(map[string]struct{}, len(plan.Edges))
	adj := make(map[string][]string, len(nodes))
	indegree := make(map[string]int, len(nodes))
	for key := range nodes {
		indegree[key] = 0
	}
	for i, edge := range plan.Edges {
		edge.From = strings.TrimSpace(edge.From)
		edge.To = strings.TrimSpace(edge.To)
		if edge.From == "" || edge.To == "" || edge.From == edge.To {
			return fmt.Errorf("%w: edges[%d] has invalid endpoints", ErrInvalidPlan, i)
		}
		if _, ok := nodes[edge.From]; !ok {
			return fmt.Errorf("%w: edge source %q is unknown", ErrInvalidPlan, edge.From)
		}
		if _, ok := nodes[edge.To]; !ok {
			return fmt.Errorf("%w: edge target %q is unknown", ErrInvalidPlan, edge.To)
		}
		if _, ok := validDependencies[edge.Type]; !ok {
			return fmt.Errorf("%w: edge %s -> %s has invalid type %q", ErrInvalidPlan, edge.From, edge.To, edge.Type)
		}
		identity := edge.From + "\x00" + edge.To + "\x00" + string(edge.Type)
		if _, exists := seenEdges[identity]; exists {
			return fmt.Errorf("%w: duplicate edge %s -> %s", ErrInvalidPlan, edge.From, edge.To)
		}
		seenEdges[identity] = struct{}{}
		if edge.Type == DependencyHard || edge.Type == DependencyArtifact {
			adj[edge.From] = append(adj[edge.From], edge.To)
			indegree[edge.To]++
		}
	}

	queue := make([]string, 0, len(nodes))
	for key, degree := range indegree {
		if degree == 0 {
			queue = append(queue, key)
		}
	}
	visited := 0
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		visited++
		for _, next := range adj[key] {
			indegree[next]--
			if indegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	if visited != len(nodes) {
		return fmt.Errorf("%w: hard dependency graph contains a cycle", ErrInvalidPlan)
	}

	if err := validateLifecycle(plan); err != nil {
		return err
	}
	return nil
}

func validatePolicy(policy Policy) error {
	switch policy.Autonomy {
	case AutonomyAssisted, AutonomyDevelopment, AutonomyDelivery, AutonomyClosedLoop:
	default:
		return fmt.Errorf("%w: invalid autonomy level %q", ErrInvalidPlan, policy.Autonomy)
	}
	switch policy.Runtime {
	case RuntimeManual, RuntimeInheritMika, RuntimeAuto:
	default:
		return fmt.Errorf("%w: invalid runtime policy %q", ErrInvalidPlan, policy.Runtime)
	}
	if policy.Budget.MaxParallelNodes <= 0 || policy.Budget.MaxParallelNodes > 64 {
		return fmt.Errorf("%w: max_parallel_nodes must be between 1 and 64", ErrInvalidPlan)
	}
	if policy.Budget.MaxTotalAttempts <= 0 {
		return fmt.Errorf("%w: max_total_attempts must be positive", ErrInvalidPlan)
	}
	return nil
}

func validateLifecycle(plan Plan) error {
	hasImplementation := false
	hasReview := false
	for _, node := range plan.Nodes {
		switch node.Kind {
		case NodeImplementation, NodeMigration:
			hasImplementation = true
		case NodeReview:
			hasReview = true
		case NodeDeploy:
			if plan.Policy.Autonomy == AutonomyAssisted || plan.Policy.Autonomy == AutonomyDevelopment {
				return fmt.Errorf("%w: deploy node requires delivery or closed_loop autonomy", ErrInvalidPlan)
			}
		case NodeObserve, NodeIncident:
			if plan.Policy.Autonomy != AutonomyClosedLoop {
				return fmt.Errorf("%w: %s node requires closed_loop autonomy", ErrInvalidPlan, node.Kind)
			}
		}
	}
	if hasImplementation && !hasReview {
		return fmt.Errorf("%w: implementation plans require an independent review node", ErrInvalidPlan)
	}
	return nil
}

func requiresAcceptance(kind NodeKind) bool {
	switch kind {
	case NodeResearch, NodeProduct, NodeArchitecture, NodeDesign, NodeObserve:
		return false
	default:
		return true
	}
}
