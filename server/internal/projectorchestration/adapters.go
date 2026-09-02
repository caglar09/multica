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
	Revision      string
	Modules       []string
	TestTargets   []string
	APISurfaces   []string
	DataStores    []string
	Dependencies  []string
	Evidence      map[string]any
}

type ChangeImpactRequest struct {
	NodeKey      string
	ChangedFiles []string
	Summary      string
}

type ChangeImpactEvidence struct {
	AffectedModules []string
	AffectedTests   []string
	AffectedAPIs    []string
	AffectedData    []string
	Risk            RiskLevel
	Evidence        map[string]any
}

// DeploymentAdapter is deliberately capability-limited. A provider adapter
// receives a validated release request from the backend; LLMs never receive
// provider credentials and never shell out directly to production.
type DeploymentAdapter interface {
	Deploy(ctx context.Context, request DeploymentRequest) (DeploymentResult, error)
	Rollback(ctx context.Context, request RollbackRequest) (DeploymentResult, error)
}

type DeploymentRequest struct {
	WorkspaceID  pgtype.UUID
	ProjectID    pgtype.UUID
	PlanID       pgtype.UUID
	Environment  string
	ReleaseRef   string
	Policy       Policy
}

type RollbackRequest struct {
	WorkspaceID  pgtype.UUID
	ProjectID    pgtype.UUID
	DeploymentID pgtype.UUID
	Reason       string
}

type DeploymentResult struct {
	Provider    string
	ExternalRef string
	Status      string
	Evidence    map[string]any
}

// ObservationAdapter turns provider telemetry into normalized evidence. It
// cannot create fixes itself; the backend converts detected regressions into
// durable incident/project-plan state.
type ObservationAdapter interface {
	Observe(ctx context.Context, request ObservationRequest) (ObservationResult, error)
}

type ObservationRequest struct {
	WorkspaceID  pgtype.UUID
	ProjectID    pgtype.UUID
	DeploymentID pgtype.UUID
	WindowSeconds int64
}

type ObservationResult struct {
	Healthy     bool
	ErrorRate   float64
	LatencyP95  float64
	Signals     []string
	Evidence    map[string]any
}
