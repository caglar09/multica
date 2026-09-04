package projectorchestration

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgtype"
)

var ErrAdapterNotConfigured = errors.New("autonomous project adapter is not configured")

// RepositoryAnalyzer is the deterministic integration seam for repository
// indexing/change-impact implementations. An analyzer returns evidence only;
// it cannot mutate plans, issues or workflow state.
type RepositoryAnalyzer interface {
	Snapshot(ctx context.Context, workspaceID, projectID pgtype.UUID) (RepositorySnapshot, error)
	Impact(ctx context.Context, workspaceID, projectID pgtype.UUID, request ChangeImpactRequest) (ChangeImpactEvidence, error)
}

type RepositorySnapshot struct {
	Revision     string         `json:"revision"`
	Modules      []string       `json:"modules"`
	TestTargets  []string       `json:"test_targets"`
	APISurfaces  []string       `json:"api_surfaces"`
	DataStores   []string       `json:"data_stores"`
	Dependencies []string       `json:"dependencies"`
	Evidence     map[string]any `json:"evidence"`
}

type ChangeImpactRequest struct {
	NodeKey      string   `json:"node_key"`
	ChangedFiles []string `json:"changed_files"`
	Summary      string   `json:"summary"`
}

type ChangeImpactEvidence struct {
	AffectedModules []string       `json:"affected_modules"`
	AffectedTests   []string       `json:"affected_tests"`
	AffectedAPIs    []string       `json:"affected_apis"`
	AffectedData    []string       `json:"affected_data"`
	Risk            RiskLevel      `json:"risk"`
	Evidence        map[string]any `json:"evidence"`
}

type QualityGateRequest struct {
	WorkspaceID pgtype.UUID   `json:"workspace_id"`
	ProjectID   pgtype.UUID   `json:"project_id"`
	PlanID      pgtype.UUID   `json:"plan_id"`
	NodeID      pgtype.UUID   `json:"node_id"`
	IssueID     pgtype.UUID   `json:"issue_id"`
	GateType    string        `json:"gate_type"`
	Artifact    map[string]any `json:"artifact"`
}

type QualityGateResult struct {
	Passed   bool           `json:"passed"`
	Evidence map[string]any `json:"evidence"`
	Error    string         `json:"error"`
}

// QualityGateRunner is the deterministic verifier boundary for build/lint/test/
// security/etc. The runner returns evidence only; backend state transitions
// remain deterministic and durable.
type QualityGateRunner interface {
	Run(ctx context.Context, request QualityGateRequest) (QualityGateResult, error)
}

// DeploymentAdapter is deliberately capability-limited. A provider adapter
// receives a validated release request from the backend; LLMs never receive
// provider credentials and never shell out directly to production.
type DeploymentAdapter interface {
	Deploy(ctx context.Context, request DeploymentRequest) (DeploymentResult, error)
	Rollback(ctx context.Context, request RollbackRequest) (DeploymentResult, error)
}

type DeploymentRequest struct {
	WorkspaceID pgtype.UUID `json:"workspace_id"`
	ProjectID   pgtype.UUID `json:"project_id"`
	PlanID      pgtype.UUID `json:"plan_id"`
	Environment string      `json:"environment"`
	ReleaseRef  string      `json:"release_ref"`
	Policy      Policy      `json:"policy"`
}

type RollbackRequest struct {
	WorkspaceID  pgtype.UUID `json:"workspace_id"`
	ProjectID    pgtype.UUID `json:"project_id"`
	DeploymentID pgtype.UUID `json:"deployment_id"`
	Reason       string      `json:"reason"`
}

type DeploymentResult struct {
	Provider    string         `json:"provider"`
	ExternalRef string         `json:"external_ref"`
	Status      string         `json:"status"`
	Evidence    map[string]any `json:"evidence"`
}

// ObservationAdapter turns provider telemetry into normalized evidence. It
// cannot create fixes itself; the backend converts detected regressions into
// durable incident/project-plan state.
type ObservationAdapter interface {
	Observe(ctx context.Context, request ObservationRequest) (ObservationResult, error)
}

type ObservationRequest struct {
	WorkspaceID   pgtype.UUID `json:"workspace_id"`
	ProjectID     pgtype.UUID `json:"project_id"`
	DeploymentID  pgtype.UUID `json:"deployment_id"`
	WindowSeconds int64       `json:"window_seconds"`
}

type ObservationResult struct {
	Healthy    bool           `json:"healthy"`
	ErrorRate  float64        `json:"error_rate"`
	LatencyP95 float64        `json:"latency_p95"`
	Signals    []string       `json:"signals"`
	Evidence   map[string]any `json:"evidence"`
}
