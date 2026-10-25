// Package projectorchestration owns durable project-level autonomous planning.
//
// LLMs may propose a Plan, but this package validates and persists the plan,
// computes dependency readiness and owns all project-level state transitions.
package projectorchestration

import "time"

const CurrentPlanVersion = 1

type NodeKind string

const (
	NodeResearch      NodeKind = "research"
	NodeProduct       NodeKind = "product"
	NodeArchitecture  NodeKind = "architecture"
	NodeDesign        NodeKind = "design"
	NodeImplementation NodeKind = "implementation"
	NodeMigration     NodeKind = "migration"
	NodeIntegration   NodeKind = "integration"
	NodeReview        NodeKind = "review"
	NodeQA            NodeKind = "qa"
	NodeSecurity      NodeKind = "security"
	NodeRelease       NodeKind = "release"
	NodeDeploy        NodeKind = "deploy"
	NodeObserve       NodeKind = "observe"
	NodeIncident      NodeKind = "incident"
)

type RiskLevel string

const (
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

type DependencyType string

const (
	DependencyHard     DependencyType = "hard"
	DependencySoft     DependencyType = "soft"
	DependencyArtifact DependencyType = "artifact"
)

type AutonomyLevel string

const (
	AutonomyAssisted    AutonomyLevel = "assisted"
	AutonomyDevelopment AutonomyLevel = "development"
	AutonomyDelivery    AutonomyLevel = "delivery"
	AutonomyClosedLoop  AutonomyLevel = "closed_loop"
)

type RuntimePolicy string

const (
	RuntimeManual      RuntimePolicy = "manual"
	RuntimeInheritMika RuntimePolicy = "inherit_mika"
	RuntimeAuto        RuntimePolicy = "auto"
)

type ApprovalPolicy struct {
	DatabaseMigration   bool `json:"database_migration"`
	ProductionDeploy    bool `json:"production_deploy"`
	MajorDependency     bool `json:"major_dependency"`
	CriticalRisk        bool `json:"critical_risk"`
}

type BudgetPolicy struct {
	MaxParallelNodes int   `json:"max_parallel_nodes"`
	MaxTotalAttempts int   `json:"max_total_attempts"`
	TokenLimit       int64 `json:"token_limit,omitempty"`
	RuntimeSeconds   int64 `json:"runtime_seconds_limit,omitempty"`
	CostMicrounits   int64 `json:"cost_microunits_limit,omitempty"`
}

type Policy struct {
	Autonomy       AutonomyLevel  `json:"autonomy"`
	Runtime        RuntimePolicy  `json:"runtime_policy"`
	SkillsAutomatic bool           `json:"skills_automatic"`
	Approvals      ApprovalPolicy `json:"approvals"`
	Budget         BudgetPolicy   `json:"budget"`
}

type Specification struct {
	Summary                   string   `json:"summary"`
	Requirements              []string `json:"requirements"`
	NonFunctionalRequirements []string `json:"non_functional_requirements"`
	Constraints               []string `json:"constraints"`
	DefinitionOfDone          []string `json:"definition_of_done"`
}

type NodeSpec struct {
	Key                  string    `json:"key"`
	Kind                 NodeKind  `json:"kind"`
	Title                string    `json:"title"`
	Description          string    `json:"description"`
	Priority             int       `json:"priority"`
	RequiredRoleFamily   string    `json:"required_role_family,omitempty"`
	RequiredCapabilities []string  `json:"required_capabilities"`
	AcceptanceCriteria   []string  `json:"acceptance_criteria"`
	Risk                 RiskLevel `json:"risk"`
	MaxAttempts          int       `json:"max_attempts"`
}

type EdgeSpec struct {
	From                 string         `json:"from"`
	To                   string         `json:"to"`
	Type                 DependencyType `json:"type"`
	RequiredArtifactType string         `json:"required_artifact_type,omitempty"`
}

type Plan struct {
	Version       int           `json:"version"`
	Goal          string        `json:"goal"`
	Specification Specification `json:"specification"`
	Policy        Policy        `json:"policy"`
	Nodes         []NodeSpec    `json:"nodes"`
	Edges         []EdgeSpec    `json:"edges"`
}

type StoredPlan struct {
	ID             string
	WorkspaceID    string
	ProjectID      string
	Revision       int64
	SourceRevision string
	PlannerName    string
	PlannerModel   string
	Status         string
	Plan           Plan
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type NodeState struct {
	ID                  string
	PlanID              string
	Key                 string
	Kind                NodeKind
	Status              string
	Priority            int
	Risk                RiskLevel
	RequiredRoleFamily  string
	RequiredCapabilities []string
	AcceptanceCriteria  []string
	AssignedRole        string
	AssignedAgentID     string
	MaterializedIssueID string
	Attempt             int
	MaxAttempts         int
	BlockedReason       string
	ReadyAt             *time.Time
	StartedAt           *time.Time
	CompletedAt         *time.Time
}

type ReadyNode struct {
	ID                   string
	Key                  string
	Kind                 NodeKind
	Title                string
	Description          string
	Priority             int
	Risk                 RiskLevel
	RequiredRoleFamily   string
	RequiredCapabilities []string
	AcceptanceCriteria   []string
	MaxAttempts          int
}

type PlannedNode struct {
	ReadyNode
	Status              string
	MaterializedIssueID string
	AssignedRole        string
	AssignedAgentID     string
	Attempt             int
}

type BlockedNode struct {
	PlannedNode
	Category string
	Reason   string
}
