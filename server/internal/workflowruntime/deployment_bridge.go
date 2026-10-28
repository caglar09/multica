package workflowruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	genericdeployment "github.com/multica-ai/multica/server/internal/deployment"
	"github.com/multica-ai/multica/server/internal/projectorchestration"
)

const autonomousProjectDeploymentOperationKind = "autonomous_project_deployment"

type autonomousProjectDeploymentRequest struct {
	Version            int    `json:"version"`
	DeploymentID       string `json:"deployment_id"`
	NodeID             string `json:"node_id"`
	PlanID             string `json:"plan_id"`
	ManifestID         string `json:"manifest_id"`
	ManifestDigest     string `json:"manifest_digest"`
	ArtifactStorageKey string `json:"artifact_storage_key"`
}

// AutonomousDeploymentBridge adapts autonomous project releases to the
// durable generic deployment operation engine. Its persisted requests carry
// immutable manifest references only; provider download URLs are generated
// after each operation dispatch.
type AutonomousDeploymentBridge struct {
	pool          *pgxpool.Pool
	adapter       projectorchestration.DeploymentAdapter
	artifactStore projectorchestration.ReleaseArtifactStore
	repository    *genericdeployment.PostgresRepository
	dispatcher    *genericdeployment.AsyncDispatcher
	provider      *autonomousProjectProvider
	reconciler    *genericdeployment.Reconciler
}

func NewAutonomousDeploymentBridge(
	pool *pgxpool.Pool,
	adapter projectorchestration.DeploymentAdapter,
	artifactStore projectorchestration.ReleaseArtifactStore,
) *AutonomousDeploymentBridge {
	if pool == nil || adapter == nil || artifactStore == nil {
		return nil
	}
	repository := genericdeployment.NewPostgresRepository(pool)
	bridge := &AutonomousDeploymentBridge{
		pool:          pool,
		adapter:       adapter,
		artifactStore: artifactStore,
		repository:    repository,
		dispatcher:    &genericdeployment.AsyncDispatcher{Repository: repository},
	}
	bridge.provider = &autonomousProjectProvider{bridge: bridge}
	bridge.reconciler = &genericdeployment.Reconciler{
		Repository: repository,
		Lister:     autonomousProjectOperationLister{repository: repository},
		Dispatcher: bridge.dispatcher,
		Resolve:    bridge.resolveOperation,
		Trigger:    bridge.triggerOperation,
		Deadline:   24 * time.Hour,
	}
	return bridge
}

// Deploy persists the generic operation before returning control to the
// project workflow. Terminal operation state is projected back by the runtime
// reconciler so a process restart cannot lose deployment completion.
func (b *AutonomousDeploymentBridge) Deploy(
	ctx context.Context,
	request projectorchestration.DeploymentRequest,
) (projectorchestration.DeploymentResult, error) {
	if b == nil || b.pool == nil {
		return projectorchestration.DeploymentResult{}, errors.New("autonomous deployment bridge is not configured")
	}
	manifestID, err := uuid.Parse(request.Manifest.ManifestID)
	if err != nil {
		return projectorchestration.DeploymentResult{}, errors.New("autonomous deployment request has an invalid manifest identity")
	}
	manifestDigest, err := projectorchestration.ParseReleaseReference(request.ReleaseRef)
	if err != nil {
		return projectorchestration.DeploymentResult{}, err
	}
	var deploymentID, nodeID uuid.UUID
	if err := b.pool.QueryRow(ctx, `
		SELECT id, node_id
		FROM autonomous_project_deployment
		WHERE workspace_id = $1 AND project_id = $2 AND plan_id = $3
		  AND environment = $4 AND release_manifest_id = $5 AND release_manifest_digest = $6
		  AND status = 'running' AND generic_operation_id IS NULL
		ORDER BY started_at DESC NULLS LAST, created_at DESC
		LIMIT 1
	`, request.WorkspaceID, request.ProjectID, request.PlanID, request.Environment, manifestID, manifestDigest).Scan(&deploymentID, &nodeID); err != nil {
		return projectorchestration.DeploymentResult{}, fmt.Errorf("load autonomous deployment record correlation: %w", err)
	}
	return b.deployForRecord(ctx, request, deploymentID, nodeID)
}

// DeployForRecord binds the generic operation to the exact autonomous
// deployment row and plan node created by the workflow runtime.
func (b *AutonomousDeploymentBridge) DeployForRecord(
	ctx context.Context,
	request projectorchestration.DeploymentRequest,
	deploymentID, nodeID uuid.UUID,
) (projectorchestration.DeploymentResult, error) {
	return b.deployForRecord(ctx, request, deploymentID, nodeID)
}

func (b *AutonomousDeploymentBridge) deployForRecord(
	ctx context.Context,
	request projectorchestration.DeploymentRequest,
	deploymentID, nodeID uuid.UUID,
) (projectorchestration.DeploymentResult, error) {
	if b == nil || b.pool == nil || b.dispatcher == nil || b.provider == nil {
		return projectorchestration.DeploymentResult{}, errors.New("autonomous deployment bridge is not configured")
	}
	if deploymentID == uuid.Nil || nodeID == uuid.Nil {
		return projectorchestration.DeploymentResult{}, errors.New("autonomous deployment record correlation is incomplete")
	}
	if !request.WorkspaceID.Valid || !request.ProjectID.Valid || !request.PlanID.Valid {
		return projectorchestration.DeploymentResult{}, errors.New("autonomous deployment request has an invalid UUID")
	}
	workspaceID := uuidFromPG(request.WorkspaceID)
	projectID := uuidFromPG(request.ProjectID)
	planID := uuidFromPG(request.PlanID)
	manifest, err := projectorchestration.LoadPlanReleaseManifest(ctx, b.pool, request.WorkspaceID, request.ProjectID, request.PlanID)
	if err != nil {
		return projectorchestration.DeploymentResult{}, err
	}
	if err := manifest.Validate(); err != nil {
		return projectorchestration.DeploymentResult{}, fmt.Errorf("validate autonomous release manifest: %w", err)
	}
	manifestDigest, err := projectorchestration.ParseReleaseReference(request.ReleaseRef)
	if err != nil || manifestDigest != manifest.ManifestDigest ||
		request.Manifest.ManifestID != manifest.ManifestID ||
		request.Manifest.ManifestDigest != manifest.ManifestDigest ||
		request.ArtifactDigest != manifest.ArtifactDigest ||
		request.CommitSHA != manifest.CommitSHA ||
		request.Environment != manifest.Environment {
		return projectorchestration.DeploymentResult{}, errors.New("autonomous deployment request does not match its persisted release manifest")
	}
	if strings.TrimSpace(request.ArtifactURL) != "" {
		return projectorchestration.DeploymentResult{}, errors.New("autonomous deployment bridge accepts artifact object identity only")
	}
	if err := validateAutonomousDeploymentRecord(ctx, b.pool, request, manifest, deploymentID, nodeID, uuid.Nil); err != nil {
		return projectorchestration.DeploymentResult{}, err
	}

	operationID := uuid.New()
	operationRequest := autonomousProjectDeploymentRequest{
		Version: 1, DeploymentID: deploymentID.String(), NodeID: nodeID.String(),
		PlanID: planID.String(), ManifestID: manifest.ManifestID,
		ManifestDigest: manifest.ManifestDigest, ArtifactStorageKey: manifest.ArtifactStorageKey,
	}
	config, err := json.Marshal(operationRequest)
	if err != nil {
		return projectorchestration.DeploymentResult{}, fmt.Errorf("encode autonomous deployment operation references: %w", err)
	}
	deploymentRequest := genericdeployment.DeploymentRequest{
		WorkspaceID: workspaceID, OperationID: operationID, TargetID: projectID,
		IdempotencyKey: genericdeployment.DeriveIdempotencyKey(projectID, manifest.ManifestDigest, request.Environment, autonomousProjectDeploymentOperationKind, ""),
		Environment:    request.Environment, CommitSHA: manifest.CommitSHA, Config: config,
	}
	encodedRequest, err := json.Marshal(deploymentRequest)
	if err != nil {
		return projectorchestration.DeploymentResult{}, fmt.Errorf("encode generic deployment operation request: %w", err)
	}
	input := genericdeployment.CreateOperationInput{
		ID: operationID, WorkspaceID: workspaceID, TargetID: projectID,
		Action: "deploy", OperationKind: autonomousProjectDeploymentOperationKind,
		IdempotencyKey: deploymentRequest.IdempotencyKey,
		SourceRevision: manifest.ManifestDigest, Environment: request.Environment,
		MaxAttempts: 3, Request: encodedRequest,
	}
	op, _, err := b.dispatcher.Dispatch(ctx, input, deploymentRequest, b.provider)
	if err != nil {
		return projectorchestration.DeploymentResult{}, fmt.Errorf("dispatch generic autonomous deployment operation: %w", err)
	}
	return deploymentResultFromOperation(op), nil
}

// Rollback remains delegated to the existing adapter. Staging, approval, and
// automated rollback policy remain owned by the deployment-safety workflow.
func (b *AutonomousDeploymentBridge) Rollback(
	ctx context.Context,
	request projectorchestration.RollbackRequest,
) (projectorchestration.DeploymentResult, error) {
	if b == nil || b.adapter == nil {
		return projectorchestration.DeploymentResult{}, errors.New("autonomous deployment bridge is not configured")
	}
	return b.adapter.Rollback(ctx, request)
}

// SmokeTest delegates the staging gate through the same provider adapter used
// for deployment, while keeping its result outside the generic operation DTO.
func (b *AutonomousDeploymentBridge) SmokeTest(
	ctx context.Context,
	request projectorchestration.StagingSmokeRequest,
) (projectorchestration.StagingSmokeResult, error) {
	if b == nil || b.adapter == nil {
		return projectorchestration.StagingSmokeResult{}, errors.New("autonomous deployment bridge is not configured")
	}
	smokeAdapter, ok := b.adapter.(projectorchestration.StagingSmokeAdapter)
	if !ok {
		return projectorchestration.StagingSmokeResult{}, errors.New("deployment provider does not support the staging smoke gate")
	}
	return smokeAdapter.SmokeTest(ctx, request)
}

// ReconcileOperations retries only autonomous project operations. It leaves
// unrelated user-created deployment operations to the generic deployment
// scheduler that owns them.
func (b *AutonomousDeploymentBridge) ReconcileOperations(ctx context.Context) (int, error) {
	if b == nil || b.reconciler == nil {
		return 0, errors.New("autonomous deployment bridge is not configured")
	}
	return b.reconciler.Reconcile(ctx, "")
}

func (b *AutonomousDeploymentBridge) resolveOperation(
	_ context.Context,
	op genericdeployment.Operation,
) (genericdeployment.Provider, genericdeployment.OperationLookup, error) {
	if b == nil || b.provider == nil {
		return nil, genericdeployment.OperationLookup{}, errors.New("autonomous deployment provider is not configured")
	}
	if op.ProviderOperationID == nil || strings.TrimSpace(*op.ProviderOperationID) == "" {
		return nil, genericdeployment.OperationLookup{}, errors.New("autonomous deployment operation has no provider operation id")
	}
	return b.provider, genericdeployment.OperationLookup{
		WorkspaceID: op.WorkspaceID, OperationID: op.ID,
		ProviderOperationID: *op.ProviderOperationID,
		ExternalTarget:      genericdeployment.RemoteTarget{ExternalID: op.TargetID.String()},
	}, nil
}

func (b *AutonomousDeploymentBridge) triggerOperation(
	ctx context.Context,
	op genericdeployment.Operation,
) (genericdeployment.Provider, genericdeployment.DeploymentRequest, error) {
	if b == nil || b.provider == nil {
		return nil, genericdeployment.DeploymentRequest{}, errors.New("autonomous deployment provider is not configured")
	}
	if op.OperationKind != autonomousProjectDeploymentOperationKind || op.Action != "deploy" {
		return nil, genericdeployment.DeploymentRequest{}, errors.New("operation is not an autonomous project deployment")
	}
	var request genericdeployment.DeploymentRequest
	decoder := json.NewDecoder(strings.NewReader(string(op.Request)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return nil, genericdeployment.DeploymentRequest{}, fmt.Errorf("decode persisted autonomous deployment request: %w", err)
	}
	if request.OperationID != op.ID || request.WorkspaceID != op.WorkspaceID || request.TargetID != op.TargetID || request.IdempotencyKey != op.IdempotencyKey {
		return nil, genericdeployment.DeploymentRequest{}, errors.New("persisted autonomous deployment request identity does not match its operation")
	}
	refs, err := decodeAutonomousDeploymentRequest(request.Config)
	if err != nil {
		return nil, genericdeployment.DeploymentRequest{}, err
	}
	if op.SourceRevision != refs.ManifestDigest || op.Environment != request.Environment {
		return nil, genericdeployment.DeploymentRequest{}, errors.New("generic operation source revision or environment does not match its release manifest reference")
	}
	planID, err := uuid.Parse(refs.PlanID)
	if err != nil {
		return nil, genericdeployment.DeploymentRequest{}, errors.New("autonomous deployment operation has an invalid plan UUID")
	}
	deploymentID, err := uuid.Parse(refs.DeploymentID)
	if err != nil {
		return nil, genericdeployment.DeploymentRequest{}, errors.New("autonomous deployment operation has an invalid deployment UUID")
	}
	nodeID, err := uuid.Parse(refs.NodeID)
	if err != nil {
		return nil, genericdeployment.DeploymentRequest{}, errors.New("autonomous deployment operation has an invalid node UUID")
	}
	manifest, err := projectorchestration.LoadPlanReleaseManifest(ctx, b.pool,
		pgUUID(request.WorkspaceID), pgUUID(request.TargetID), pgUUID(planID))
	if err != nil {
		return nil, genericdeployment.DeploymentRequest{}, err
	}
	if err := validateAutonomousDeploymentRecord(ctx, b.pool, projectorchestration.DeploymentRequest{
		WorkspaceID: pgUUID(request.WorkspaceID), ProjectID: pgUUID(request.TargetID),
		PlanID: pgUUID(planID), Environment: request.Environment,
	}, manifest, deploymentID, nodeID, op.ID); err != nil {
		return nil, genericdeployment.DeploymentRequest{}, err
	}
	return b.provider, request, nil
}

type autonomousProjectOperationLister struct {
	repository *genericdeployment.PostgresRepository
}

func (l autonomousProjectOperationLister) ListNonTerminalOperations(ctx context.Context, workspaceID string) ([]genericdeployment.Operation, error) {
	if l.repository == nil {
		return nil, errors.New("autonomous deployment operation repository is not configured")
	}
	operations, err := l.repository.ListNonTerminalOperations(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	out := make([]genericdeployment.Operation, 0, len(operations))
	for _, operation := range operations {
		if operation.OperationKind == autonomousProjectDeploymentOperationKind {
			out = append(out, operation)
		}
	}
	return out, nil
}

type autonomousProjectProvider struct {
	bridge *AutonomousDeploymentBridge
}

func (p *autonomousProjectProvider) Kind() string { return "autonomous-project" }

func (p *autonomousProjectProvider) Capabilities() genericdeployment.CapabilitySet {
	return genericdeployment.CapabilitySet{Trigger: true}
}

func (*autonomousProjectProvider) ValidateConnection(context.Context, genericdeployment.ConnectionCredentials) (genericdeployment.ConnectionInfo, error) {
	return genericdeployment.ConnectionInfo{}, genericdeployment.ErrCapabilityUnsupported
}

func (*autonomousProjectProvider) ListTargets(context.Context, genericdeployment.ProviderConnection, genericdeployment.ConnectionCredentials) ([]genericdeployment.RemoteTarget, error) {
	return nil, genericdeployment.ErrCapabilityUnsupported
}

func (*autonomousProjectProvider) GetTarget(context.Context, genericdeployment.ProviderConnection, genericdeployment.ConnectionCredentials, string) (genericdeployment.RemoteTarget, error) {
	return genericdeployment.RemoteTarget{}, genericdeployment.ErrCapabilityUnsupported
}

func (*autonomousProjectProvider) CreateTarget(context.Context, genericdeployment.ProviderConnection, genericdeployment.ConnectionCredentials, genericdeployment.CreateTargetRequest) (genericdeployment.RemoteTarget, error) {
	return genericdeployment.RemoteTarget{}, genericdeployment.ErrCapabilityUnsupported
}

func (p *autonomousProjectProvider) TriggerDeployment(ctx context.Context, request genericdeployment.DeploymentRequest) (genericdeployment.RemoteOperation, error) {
	if p == nil || p.bridge == nil {
		return genericdeployment.RemoteOperation{}, errors.New("autonomous project provider is not configured")
	}
	config, err := decodeAutonomousDeploymentRequest(request.Config)
	if err != nil {
		return genericdeployment.RemoteOperation{}, &genericdeployment.FinalProviderError{Err: err}
	}
	workspaceID := pgUUID(request.WorkspaceID)
	projectID := pgUUID(request.TargetID)
	planUUID, err := uuid.Parse(config.PlanID)
	if err != nil {
		return genericdeployment.RemoteOperation{}, &genericdeployment.FinalProviderError{Err: errors.New("autonomous deployment request has an invalid plan identity")}
	}
	planID := pgUUID(planUUID)
	manifest, err := projectorchestration.LoadPlanReleaseManifest(ctx, p.bridge.pool, workspaceID, projectID, planID)
	if err != nil {
		return genericdeployment.RemoteOperation{}, &genericdeployment.FinalProviderError{Err: err}
	}
	if manifest.ManifestID != config.ManifestID || manifest.ManifestDigest != config.ManifestDigest ||
		manifest.ArtifactStorageKey != config.ArtifactStorageKey || manifest.CommitSHA != request.CommitSHA ||
		manifest.Environment != request.Environment {
		return genericdeployment.RemoteOperation{}, &genericdeployment.FinalProviderError{Err: errors.New("autonomous deployment operation references a different immutable release manifest")}
	}
	deploymentID, err := uuid.Parse(config.DeploymentID)
	if err != nil {
		return genericdeployment.RemoteOperation{}, &genericdeployment.FinalProviderError{Err: errors.New("autonomous deployment operation has an invalid deployment UUID")}
	}
	nodeID, err := uuid.Parse(config.NodeID)
	if err != nil {
		return genericdeployment.RemoteOperation{}, &genericdeployment.FinalProviderError{Err: errors.New("autonomous deployment operation has an invalid node UUID")}
	}
	if err := validateAutonomousDeploymentRecord(ctx, p.bridge.pool, projectorchestration.DeploymentRequest{
		WorkspaceID: workspaceID, ProjectID: projectID, PlanID: planID, Environment: request.Environment,
	}, manifest, deploymentID, nodeID, request.OperationID); err != nil {
		return genericdeployment.RemoteOperation{}, &genericdeployment.FinalProviderError{Err: err}
	}
	if err := projectorchestration.VerifyStoredReleaseArtifact(ctx, p.bridge.artifactStore, manifest.ArtifactStorageKey, manifest.ArtifactDigest); err != nil {
		return genericdeployment.RemoteOperation{}, &genericdeployment.FinalProviderError{Err: err}
	}
	var policyJSON []byte
	if err := p.bridge.pool.QueryRow(ctx, `
		SELECT policy
		FROM autonomous_project_plan
		WHERE id=$1 AND workspace_id=$2 AND project_id=$3
	`, planID, workspaceID, projectID).Scan(&policyJSON); err != nil {
		return genericdeployment.RemoteOperation{}, &genericdeployment.FinalProviderError{Err: fmt.Errorf("load autonomous deployment policy: %w", err)}
	}
	var policy projectorchestration.Policy
	if err := json.Unmarshal(policyJSON, &policy); err != nil {
		return genericdeployment.RemoteOperation{}, &genericdeployment.FinalProviderError{Err: fmt.Errorf("decode autonomous deployment policy: %w", err)}
	}
	artifactURL, err := p.bridge.artifactStore.PresignGet(ctx, manifest.ArtifactStorageKey, 10*time.Minute)
	if err != nil || strings.TrimSpace(artifactURL) == "" {
		if err == nil {
			err = errors.New("artifact storage returned an empty presigned URL")
		}
		return genericdeployment.RemoteOperation{}, &genericdeployment.FinalProviderError{Err: fmt.Errorf("create fresh release artifact retrieval URL: %w", err)}
	}
	projectRequest := projectorchestration.DeploymentRequest{
		WorkspaceID: workspaceID, ProjectID: projectID, PlanID: planID,
		Environment: request.Environment, ReleaseRef: "sha256:" + manifest.ManifestDigest,
		Manifest: manifest.ProviderView(), ArtifactDigest: manifest.ArtifactDigest,
		CommitSHA: manifest.CommitSHA, ArtifactURL: artifactURL, Policy: policy,
	}
	result, err := p.bridge.adapter.Deploy(ctx, projectRequest)
	if err != nil {
		safeErr := &redactedProviderError{message: strings.ReplaceAll(err.Error(), artifactURL, "[redacted release URL]"), cause: err}
		var netErr net.Error
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.As(err, &netErr) {
			return genericdeployment.RemoteOperation{}, &genericdeployment.RetryableProviderError{Err: safeErr}
		}
		return genericdeployment.RemoteOperation{}, &genericdeployment.FinalProviderError{Err: safeErr}
	}
	status := genericdeployment.StatusFailed
	if result.Status == "succeeded" {
		if err := projectorchestration.ValidateReleaseAcknowledgement(result.Evidence, manifest); err != nil {
			return genericdeployment.RemoteOperation{}, &genericdeployment.FinalProviderError{Err: fmt.Errorf("deployment provider acknowledgement is invalid: %w", err)}
		}
		status = genericdeployment.StatusSucceeded
	}
	evidence := projectorchestration.ReleaseAcknowledgementEvidence(result.Evidence)
	evidence["provider"] = result.Provider
	evidence["external_ref"] = result.ExternalRef
	encodedEvidence, err := json.Marshal(evidence)
	if err != nil {
		return genericdeployment.RemoteOperation{}, &genericdeployment.FinalProviderError{Err: fmt.Errorf("encode autonomous deployment evidence: %w", err)}
	}
	providerOperationID := strings.TrimSpace(result.ExternalRef)
	if providerOperationID == "" {
		providerOperationID = request.OperationID.String()
	}
	return genericdeployment.RemoteOperation{
		ProviderOperationID: providerOperationID,
		ExternalStatus:      result.Status,
		NormalizedStatus:    status,
		Evidence:            encodedEvidence,
	}, nil
}

func (*autonomousProjectProvider) GetOperation(context.Context, genericdeployment.OperationLookup) (genericdeployment.RemoteOperation, error) {
	return genericdeployment.RemoteOperation{}, &genericdeployment.FinalProviderError{Err: errors.New("autonomous project deployment adapter reports terminal state during dispatch")}
}

func (*autonomousProjectProvider) CancelOperation(context.Context, genericdeployment.CancelRequest) (genericdeployment.RemoteOperation, error) {
	return genericdeployment.RemoteOperation{}, genericdeployment.ErrCapabilityUnsupported
}

func (*autonomousProjectProvider) ParseWebhook(context.Context, genericdeployment.WebhookRequest) (genericdeployment.ProviderEvent, error) {
	return genericdeployment.ProviderEvent{}, genericdeployment.ErrCapabilityUnsupported
}

type redactedProviderError struct {
	message string
	cause   error
}

func (e *redactedProviderError) Error() string { return e.message }
func (e *redactedProviderError) Unwrap() error { return e.cause }

func deploymentResultFromOperation(operation genericdeployment.Operation) projectorchestration.DeploymentResult {
	evidence := make(map[string]any)
	if len(operation.Evidence) > 0 {
		_ = json.Unmarshal(operation.Evidence, &evidence)
	}
	evidence["generic_operation_id"] = operation.ID.String()
	status := "pending"
	if operation.Status == genericdeployment.StatusReady || operation.Status == genericdeployment.StatusSucceeded {
		status = "succeeded"
	} else if operation.Status == genericdeployment.StatusFailed || operation.Status == genericdeployment.StatusCanceled || operation.Status == genericdeployment.StatusCancelled {
		status = "failed"
	}
	provider := "generic"
	if value, ok := evidence["provider"].(string); ok && strings.TrimSpace(value) != "" {
		provider = value
	}
	externalRef, _ := evidence["external_ref"].(string)
	return projectorchestration.DeploymentResult{Provider: provider, ExternalRef: externalRef, Status: status, Evidence: evidence}
}

func validateAutonomousDeploymentRecord(
	ctx context.Context,
	pool *pgxpool.Pool,
	request projectorchestration.DeploymentRequest,
	manifest projectorchestration.ReleaseManifest,
	deploymentID, nodeID, operationID uuid.UUID,
) error {
	if pool == nil || deploymentID == uuid.Nil || nodeID == uuid.Nil ||
		!request.WorkspaceID.Valid || !request.ProjectID.Valid || !request.PlanID.Valid {
		return errors.New("autonomous deployment correlation identity is incomplete")
	}
	var workspaceID, projectID, planID, storedNodeID, manifestID pgtype.UUID
	var environment, manifestDigest, status string
	var genericOperationID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		SELECT workspace_id, project_id, plan_id, node_id, environment,
		       release_manifest_id, release_manifest_digest, status, generic_operation_id
		FROM autonomous_project_deployment
		WHERE id = $1
	`, pgUUID(deploymentID)).Scan(
		&workspaceID, &projectID, &planID, &storedNodeID, &environment,
		&manifestID, &manifestDigest, &status, &genericOperationID,
	); err != nil {
		return fmt.Errorf("load autonomous deployment operation correlation: %w", err)
	}
	if uuidFromPG(workspaceID) != uuidFromPG(request.WorkspaceID) ||
		uuidFromPG(projectID) != uuidFromPG(request.ProjectID) ||
		uuidFromPG(planID) != uuidFromPG(request.PlanID) ||
		uuidFromPG(storedNodeID) != nodeID || environment != request.Environment || status != "running" ||
		uuidFromPG(manifestID).String() != manifest.ManifestID || manifestDigest != manifest.ManifestDigest ||
		manifest.WorkspaceID != uuidFromPG(workspaceID).String() ||
		manifest.ProjectID != uuidFromPG(projectID).String() ||
		manifest.PlanID != uuidFromPG(planID).String() || manifest.Environment != environment {
		return errors.New("autonomous deployment record does not match operation project, plan, node, environment, or manifest identity")
	}
	if genericOperationID.Valid && (operationID == uuid.Nil || uuidFromPG(genericOperationID) != operationID) {
		return errors.New("autonomous deployment record references a different generic operation")
	}
	return nil
}

func pgUUID(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: id != uuid.Nil} }

func uuidFromPG(value pgtype.UUID) uuid.UUID {
	if !value.Valid {
		return uuid.Nil
	}
	return uuid.UUID(value.Bytes)
}

func decodeAutonomousDeploymentRequest(encoded []byte) (autonomousProjectDeploymentRequest, error) {
	var request autonomousProjectDeploymentRequest
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return autonomousProjectDeploymentRequest{}, fmt.Errorf("decode autonomous deployment operation references: %w", err)
	}
	if request.Version != 1 || request.DeploymentID == "" || request.NodeID == "" || request.PlanID == "" || request.ManifestID == "" || request.ManifestDigest == "" || request.ArtifactStorageKey == "" {
		return autonomousProjectDeploymentRequest{}, errors.New("autonomous deployment operation references are incomplete")
	}
	for name, value := range map[string]string{
		"deployment": request.DeploymentID, "node": request.NodeID, "plan": request.PlanID, "manifest": request.ManifestID,
	} {
		if _, err := uuid.Parse(value); err != nil {
			return autonomousProjectDeploymentRequest{}, fmt.Errorf("autonomous deployment operation has an invalid %s UUID", name)
		}
	}
	if _, err := projectorchestration.ParseReleaseReference("sha256:" + request.ManifestDigest); err != nil {
		return autonomousProjectDeploymentRequest{}, err
	}
	if strings.Contains(request.ArtifactStorageKey, "://") || strings.Contains(request.ArtifactStorageKey, "?") {
		return autonomousProjectDeploymentRequest{}, errors.New("autonomous deployment operation must store an object key, not a URL")
	}
	return request, nil
}
