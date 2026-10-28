package workflowruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	genericdeployment "github.com/multica-ai/multica/server/internal/deployment"
	"github.com/multica-ai/multica/server/internal/projectorchestration"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestAutonomousDeployment_UsesGenericEngine(t *testing.T) {
	fixture := newAutonomousDeploymentBridgeFixture(t)
	provider := newDeploymentBridgeWebhook(t, fixture.manifest)
	bridge := NewAutonomousDeploymentBridge(fixture.pool,
		projectorchestration.NewWebhookDeploymentAdapter(projectorchestration.WebhookAdapterConfig{DeploymentURL: provider.server.URL}),
		fixture.artifactStore)
	if bridge == nil {
		t.Fatal("deployment bridge was not configured")
	}

	deploymentID := fixture.insertDeployment(t, pgtype.UUID{})
	result, err := bridge.Deploy(fixture.ctx, fixture.deploymentRequest())
	if err != nil {
		t.Fatalf("dispatch autonomous deployment: %v", err)
	}
	operationID := parseGenericOperationID(t, result)
	if _, err := fixture.pool.Exec(fixture.ctx, `
		UPDATE autonomous_project_deployment SET generic_operation_id=$2 WHERE id=$1
	`, deploymentID, operationID); err != nil {
		t.Fatalf("persist generic operation correlation: %v", err)
	}

	operation := waitDeploymentOperation(t, fixture, operationID)
	if operation.Status != "succeeded" {
		t.Fatalf("generic operation status = %s, want succeeded", operation.Status)
	}
	assertDeploymentRequestHasNoURL(t, operation.Request)
	provider.assertCalledWithFreshURL(t, fixture.artifactStore)

	runtime := fixture.runtime(bridge)
	if err := runtime.reconcileAutonomousDeploymentResults(fixture.ctx); err != nil {
		t.Fatalf("project generic deployment terminal state: %v", err)
	}
	assertAutonomousDeploymentCompleted(t, fixture, deploymentID)
}

func TestAutonomousDeployment_RecoversAfterRestart(t *testing.T) {
	fixture := newAutonomousDeploymentBridgeFixture(t)
	provider := newDeploymentBridgeWebhook(t, fixture.manifest)
	adapter := projectorchestration.NewWebhookDeploymentAdapter(projectorchestration.WebhookAdapterConfig{DeploymentURL: provider.server.URL})
	deploymentID := fixture.insertDeployment(t, pgtype.UUID{})
	operationID := uuid.New()
	config, err := json.Marshal(d2TestOperationReferences{
		Version: 1, DeploymentID: d2UUIDText(deploymentID), NodeID: d2UUIDText(fixture.nodeID),
		PlanID: fixture.planID.String(), ManifestID: fixture.manifest.ManifestID,
		ManifestDigest: fixture.manifest.ManifestDigest, ArtifactStorageKey: fixture.manifest.ArtifactStorageKey,
	})
	if err != nil {
		t.Fatalf("encode persisted release references: %v", err)
	}
	request := genericdeployment.DeploymentRequest{
		WorkspaceID: d2UUID(fixture.workspaceID), OperationID: operationID, TargetID: d2UUID(fixture.projectID),
		IdempotencyKey: "restart-" + operationID.String(), Environment: fixture.manifest.Environment,
		CommitSHA: fixture.manifest.CommitSHA, Config: config,
	}
	encodedRequest, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("encode persisted generic request: %v", err)
	}
	repository := genericdeployment.NewPostgresRepository(fixture.pool)
	operation, created, err := repository.CreateOperation(fixture.ctx, genericdeployment.CreateOperationInput{
		ID: operationID, WorkspaceID: d2UUID(fixture.workspaceID), TargetID: d2UUID(fixture.projectID),
		Action: "deploy", OperationKind: "autonomous_project_deployment",
		IdempotencyKey: request.IdempotencyKey, SourceRevision: fixture.manifest.ManifestDigest,
		Environment: fixture.manifest.Environment, MaxAttempts: 3, Request: encodedRequest,
	})
	if err != nil {
		t.Fatalf("persist pre-restart generic operation: %v", err)
	}
	if !created || operation.Status != genericdeployment.StatusPending {
		t.Fatalf("pre-restart operation = status %s created=%v, want pending and created", operation.Status, created)
	}
	assertDeploymentRequestHasNoURL(t, operation.Request)
	var correlated bool
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT generic_operation_id IS NOT NULL FROM autonomous_project_deployment WHERE id=$1
	`, deploymentID).Scan(&correlated); err != nil {
		t.Fatalf("check pre-restart deployment correlation: %v", err)
	}
	if correlated {
		t.Fatal("pre-restart deployment already has a generic operation correlation; want to exercise recovery")
	}

	// A newly constructed bridge models process restart. It reads the stored
	// request, generates a fresh download URL, and dispatches the pending op.
	bridge := NewAutonomousDeploymentBridge(fixture.pool, adapter, fixture.artifactStore)
	if _, err := bridge.ReconcileOperations(fixture.ctx); err != nil {
		t.Fatalf("reconcile generic operation after restart: %v", err)
	}
	current := waitDeploymentOperation(t, fixture, d2PGUUID(operationID))
	if current.Status != "succeeded" {
		t.Fatalf("recovered generic operation status = %s, want succeeded", current.Status)
	}
	assertDeploymentRequestHasNoURL(t, current.Request)
	provider.assertCalledWithFreshURL(t, fixture.artifactStore)

	// The restarted runtime projects the durable terminal state into its own
	// deployment and plan-node records.
	runtime := fixture.runtime(bridge)
	if err := runtime.reconcileAutonomousDeploymentResults(fixture.ctx); err != nil {
		t.Fatalf("project recovered deployment terminal state: %v", err)
	}
	assertAutonomousDeploymentCompleted(t, fixture, deploymentID)
}

func TestAutonomousDeployment_RejectsMismatchedOperationCorrelation(t *testing.T) {
	t.Run("transient status query error is returned", func(t *testing.T) {
		boom := errors.New("temporary database query failure")
		item := autonomousDeploymentTerminal{
			deploymentID: pgtype.UUID{Bytes: [16]byte(uuid.New()), Valid: true},
			workspaceID:  pgtype.UUID{Bytes: [16]byte(uuid.New()), Valid: true},
			operationID:  pgtype.UUID{Bytes: [16]byte(uuid.New()), Valid: true},
		}
		stillRunning, err := autonomousDeploymentStillRunning(context.Background(), erroringDeploymentStatusQueryer{err: boom}, item)
		if stillRunning || !errors.Is(err, boom) {
			t.Fatalf("status query result = (%v, %v), want false and original database error", stillRunning, err)
		}
	})

	cases := []struct {
		name   string
		mutate func(*testing.T, *autonomousDeploymentBridgeFixture, uuid.UUID)
	}{
		{
			name: "cross project target",
			mutate: func(t *testing.T, fixture *autonomousDeploymentBridgeFixture, operationID uuid.UUID) {
				if _, err := fixture.pool.Exec(fixture.ctx, "UPDATE deployment_operation SET target_id=$2 WHERE id=$1", operationID, uuid.New()); err != nil {
					t.Fatalf("mutate operation target: %v", err)
				}
			},
		},
		{
			name: "cross environment",
			mutate: func(t *testing.T, fixture *autonomousDeploymentBridgeFixture, operationID uuid.UUID) {
				if _, err := fixture.pool.Exec(fixture.ctx, "UPDATE deployment_operation SET environment='staging' WHERE id=$1", operationID); err != nil {
					t.Fatalf("mutate operation environment: %v", err)
				}
			},
		},
		{
			name: "malformed request identity",
			mutate: func(t *testing.T, fixture *autonomousDeploymentBridgeFixture, operationID uuid.UUID) {
				if _, err := fixture.pool.Exec(fixture.ctx, `
					UPDATE deployment_operation SET request='{"operation_id":"not-a-uuid"}'::jsonb WHERE id=$1
				`, operationID); err != nil {
					t.Fatalf("malform operation request: %v", err)
				}
			},
		},
		{
			name: "wrong operation UUID in request",
			mutate: func(t *testing.T, fixture *autonomousDeploymentBridgeFixture, operationID uuid.UUID) {
				if _, err := fixture.pool.Exec(fixture.ctx, `
					UPDATE deployment_operation
					SET request=jsonb_set(request, '{operation_id}', to_jsonb($2::text))
					WHERE id=$1
				`, operationID, uuid.NewString()); err != nil {
					t.Fatalf("mutate request operation UUID: %v", err)
				}
			},
		},
		{
			name: "wrong idempotency key in request",
			mutate: func(t *testing.T, fixture *autonomousDeploymentBridgeFixture, operationID uuid.UUID) {
				if _, err := fixture.pool.Exec(fixture.ctx, `
					UPDATE deployment_operation
					SET request=jsonb_set(request, '{idempotency_key}', to_jsonb($2::text))
					WHERE id=$1
				`, operationID, "different-idempotency-key"); err != nil {
					t.Fatalf("mutate request idempotency key: %v", err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newAutonomousDeploymentBridgeFixture(t)
			deploymentID := fixture.insertDeployment(t, pgtype.UUID{})
			operationID := insertSucceededAutonomousOperation(t, fixture, deploymentID)
			tc.mutate(t, fixture, operationID)
			runtime := fixture.runtime(nil)
			if err := runtime.reconcileAutonomousDeploymentResults(fixture.ctx); err == nil {
				t.Fatal("terminal projection accepted mismatched generic operation correlation")
			}
			assertDeploymentCorrelationRemainsPending(t, fixture, deploymentID)
		})
	}
}

func insertSucceededAutonomousOperation(t *testing.T, fixture *autonomousDeploymentBridgeFixture, deploymentID pgtype.UUID) uuid.UUID {
	t.Helper()
	operationID := uuid.New()
	config, err := json.Marshal(d2TestOperationReferences{
		Version: 1, DeploymentID: d2UUIDText(deploymentID), NodeID: d2UUIDText(fixture.nodeID),
		PlanID: fixture.planID.String(), ManifestID: fixture.manifest.ManifestID,
		ManifestDigest: fixture.manifest.ManifestDigest, ArtifactStorageKey: fixture.manifest.ArtifactStorageKey,
	})
	if err != nil {
		t.Fatalf("encode operation correlation fixture: %v", err)
	}
	request := genericdeployment.DeploymentRequest{
		WorkspaceID: d2UUID(fixture.workspaceID), OperationID: operationID, TargetID: d2UUID(fixture.projectID),
		IdempotencyKey: "correlation-" + operationID.String(), Environment: fixture.manifest.Environment,
		CommitSHA: fixture.manifest.CommitSHA, Config: config,
	}
	encodedRequest, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("encode generic operation fixture: %v", err)
	}
	repository := genericdeployment.NewPostgresRepository(fixture.pool)
	operation, created, err := repository.CreateOperation(fixture.ctx, genericdeployment.CreateOperationInput{
		ID: operationID, WorkspaceID: d2UUID(fixture.workspaceID), TargetID: d2UUID(fixture.projectID),
		Action: "deploy", OperationKind: "autonomous_project_deployment",
		IdempotencyKey: request.IdempotencyKey, SourceRevision: fixture.manifest.ManifestDigest,
		Environment: fixture.manifest.Environment, MaxAttempts: 3, Request: encodedRequest,
	})
	if err != nil || !created {
		t.Fatalf("create generic operation fixture: created=%v err=%v", created, err)
	}
	operation.Status = genericdeployment.StatusSucceeded
	operation.ProviderOperationID = d2StringPointer("provider-correlation-test")
	operation.Evidence, err = json.Marshal(map[string]any{
		"manifest_digest":     fixture.manifest.ManifestDigest,
		"artifact_digest":     fixture.manifest.ArtifactDigest,
		"deployed_commit_sha": fixture.manifest.CommitSHA,
		"artifact_verified":   true,
		"provider":            "test-provider",
		"external_ref":        "provider-correlation-test",
	})
	if err != nil {
		t.Fatalf("encode terminal operation evidence: %v", err)
	}
	if _, err := repository.UpdateOperation(fixture.ctx, operation, operation.Revision); err != nil {
		t.Fatalf("mark generic operation succeeded: %v", err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE autonomous_project_deployment SET generic_operation_id=$2 WHERE id=$1`, deploymentID, operationID); err != nil {
		t.Fatalf("link operation fixture to project deployment: %v", err)
	}
	return operationID
}

func assertDeploymentCorrelationRemainsPending(t *testing.T, fixture *autonomousDeploymentBridgeFixture, deploymentID pgtype.UUID) {
	t.Helper()
	var deploymentStatus, nodeStatus string
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT d.status, n.status
		FROM autonomous_project_deployment d
		JOIN autonomous_project_plan_node n ON n.id=d.node_id
		WHERE d.id=$1
	`, deploymentID).Scan(&deploymentStatus, &nodeStatus); err != nil {
		t.Fatalf("read deployment after rejected correlation: %v", err)
	}
	if deploymentStatus != "running" || nodeStatus != "running" {
		t.Fatalf("mismatched operation changed deployment/node to %s/%s", deploymentStatus, nodeStatus)
	}
	var artifactCount int
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT count(*) FROM autonomous_project_artifact WHERE plan_id=$1 AND node_id=$2 AND artifact_type='deployment_record'
	`, fixture.planID, fixture.nodeID).Scan(&artifactCount); err != nil {
		t.Fatalf("count artifacts after rejected correlation: %v", err)
	}
	if artifactCount != 0 {
		t.Fatalf("mismatched operation created %d deployment artifacts", artifactCount)
	}
}

type erroringDeploymentStatusQueryer struct{ err error }

func (q erroringDeploymentStatusQueryer) QueryRow(context.Context, string, ...any) pgx.Row {
	return errorDeploymentStatusRow{err: q.err}
}

type errorDeploymentStatusRow struct{ err error }

func (r errorDeploymentStatusRow) Scan(...any) error { return r.err }

type d2TestOperationReferences struct {
	Version            int    `json:"version"`
	DeploymentID       string `json:"deployment_id"`
	NodeID             string `json:"node_id"`
	PlanID             string `json:"plan_id"`
	ManifestID         string `json:"manifest_id"`
	ManifestDigest     string `json:"manifest_digest"`
	ArtifactStorageKey string `json:"artifact_storage_key"`
}

type autonomousDeploymentBridgeFixture struct {
	ctx             context.Context
	pool            *pgxpool.Pool
	workspaceID     pgtype.UUID
	workspaceIDText string
	ownerID         string
	projectID       pgtype.UUID
	projectIDText   string
	planID          pgtype.UUID
	nodeID          pgtype.UUID
	manifest        projectorchestration.ReleaseManifest
	artifactStore   *deploymentBridgeArtifactStore
	artifactDigest  string
}

func newAutonomousDeploymentBridgeFixture(t *testing.T) *autonomousDeploymentBridgeFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Skipf("database unavailable: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("database unavailable: %v", err)
	}
	t.Cleanup(pool.Close)

	base := testutil.New(pool, "", "")
	userID := base.User(t, "Autonomous deployment bridge test", "bridge-"+uuid.NewString()+"@example.invalid")
	workspaceIDText := base.Workspace(t, "Autonomous deployment bridge test", "bridge-"+uuid.NewString())
	base.Member(t, workspaceIDText, userID, "owner")
	projectFixture := testutil.New(pool, workspaceIDText, userID)
	projectIDText := projectFixture.Project(t, "Autonomous deployment bridge test")
	fixture := &autonomousDeploymentBridgeFixture{
		ctx: ctx, pool: pool, workspaceID: d2ParseUUID(t, workspaceIDText), workspaceIDText: workspaceIDText,
		ownerID: userID, projectID: d2ParseUUID(t, projectIDText), projectIDText: projectIDText,
		planID: pgtype.UUID{Bytes: [16]byte(uuid.New()), Valid: true},
		nodeID: pgtype.UUID{Bytes: [16]byte(uuid.New()), Valid: true},
	}

	policy, err := json.Marshal(projectorchestration.DefaultPolicy())
	if err != nil {
		t.Fatalf("encode deployment test policy: %v", err)
	}
	planDigest := strings.Repeat("0", 64)
	if _, err := pool.Exec(ctx, `
		INSERT INTO autonomous_project_plan (
			id, workspace_id, project_id, revision, source_revision, planner_name, goal,
			policy, plan_digest, status
		) VALUES ($1,$2,$3,1,$4,'test','Restart-safe deployment bridge test',$5,$6,'active')
	`, fixture.planID, fixture.workspaceID, fixture.projectID, "bridge-"+uuid.NewString(), policy, planDigest); err != nil {
		t.Fatalf("insert deployment plan fixture: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO autonomous_project_plan_node (
			id, plan_id, workspace_id, project_id, node_key, kind, title, status, attempt, max_attempts
		) VALUES ($1,$2,$3,$4,'deploy','deploy','Deploy release','running',1,3)
	`, fixture.nodeID, fixture.planID, fixture.workspaceID, fixture.projectID); err != nil {
		t.Fatalf("insert deployment plan node fixture: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE autonomous_project_plan
		SET plan_digest=autonomous_project_plan_digest(id), approved_at=now(), approved_by='bridge test'
		WHERE id=$1
	`, fixture.planID); err != nil {
		t.Fatalf("approve deployment test plan fixture: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO autonomous_project_budget (workspace_id, project_id, max_total_attempts)
		VALUES ($1,$2,100)
	`, fixture.workspaceID, fixture.projectID); err != nil {
		t.Fatalf("insert deployment budget fixture: %v", err)
	}
	var persistedPlanDigest string
	if err := pool.QueryRow(ctx, "SELECT plan_digest FROM autonomous_project_plan WHERE id=$1", fixture.planID).Scan(&persistedPlanDigest); err != nil {
		t.Fatalf("read persisted deployment plan digest: %v", err)
	}
	fixture.artifactStore = &deploymentBridgeArtifactStore{content: []byte("deterministic package bytes")}
	artifactHash := sha256.Sum256(fixture.artifactStore.content)
	fixture.artifactDigest = hex.EncodeToString(artifactHash[:])
	manifest, err := projectorchestration.SealReleaseManifest(projectorchestration.ReleaseManifest{
		Version: 1, ManifestID: uuid.NewString(), WorkspaceID: d2UUIDText(fixture.workspaceID),
		ProjectID: d2UUIDText(fixture.projectID), PlanID: d2UUIDText(fixture.planID),
		ChangeSetID: uuid.NewString(), PlanRevision: 1, PlanDigest: persistedPlanDigest,
		Environment: "production", CommitSHA: strings.Repeat("c", 40), TreeSHA: strings.Repeat("d", 40),
		Builder: "go_binary", Outputs: []string{"bin/server"}, ArtifactDigest: fixture.artifactDigest,
		ArtifactStorageKey: "autonomous-project/releases/sha256/" + fixture.artifactDigest + ".tar",
		QualityGateRunIDs:  []string{uuid.NewString(), uuid.NewString(), uuid.NewString()},
	})
	if err != nil {
		t.Fatalf("seal deployment test manifest: %v", err)
	}
	fixture.manifest = manifest
	encodedManifest, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("encode deployment test manifest: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO autonomous_project_release_manifest (
			manifest_id, manifest_digest, workspace_id, project_id, plan_id, change_set_id,
			plan_revision, plan_digest, environment, commit_sha, tree_sha, builder, outputs,
			artifact_digest, artifact_storage_key, quality_gate_run_ids, manifest_json
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
	`, d2ParseUUID(t, manifest.ManifestID), manifest.ManifestDigest, fixture.workspaceID,
		fixture.projectID, fixture.planID, d2ParseUUID(t, manifest.ChangeSetID), manifest.PlanRevision,
		manifest.PlanDigest, manifest.Environment, manifest.CommitSHA, manifest.TreeSHA, manifest.Builder,
		manifest.Outputs, manifest.ArtifactDigest, manifest.ArtifactStorageKey,
		[]pgtype.UUID{d2ParseUUID(t, manifest.QualityGateRunIDs[0]), d2ParseUUID(t, manifest.QualityGateRunIDs[1]), d2ParseUUID(t, manifest.QualityGateRunIDs[2])}, encodedManifest); err != nil {
		t.Fatalf("persist immutable deployment test manifest: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE autonomous_project_plan SET release_manifest_id=$2, release_manifest_digest=$3 WHERE id=$1
	`, fixture.planID, d2ParseUUID(t, manifest.ManifestID), manifest.ManifestDigest); err != nil {
		t.Fatalf("pin deployment test manifest to plan: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM deployment_operation WHERE workspace_id=$1 AND target_id=$2`, fixture.workspaceID, fixture.projectID)
		if err := projectorchestration.NewStore(pool).CleanupProject(context.Background(), fixture.workspaceID, fixture.projectID); err != nil {
			t.Errorf("clean deployment bridge fixture project: %v", err)
		}
	})
	return fixture
}

func (f *autonomousDeploymentBridgeFixture) deploymentRequest() projectorchestration.DeploymentRequest {
	return projectorchestration.DeploymentRequest{
		WorkspaceID: f.workspaceID, ProjectID: f.projectID, PlanID: f.planID,
		Environment: f.manifest.Environment, ReleaseRef: "sha256:" + f.manifest.ManifestDigest,
		Manifest: f.manifest.ProviderView(), ArtifactDigest: f.manifest.ArtifactDigest,
		CommitSHA: f.manifest.CommitSHA,
	}
}

func (f *autonomousDeploymentBridgeFixture) insertDeployment(t *testing.T, operationID pgtype.UUID) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	err := f.pool.QueryRow(f.ctx, `
		INSERT INTO autonomous_project_deployment (
			workspace_id, project_id, plan_id, node_id, environment, provider, status,
			policy_snapshot, release_manifest_id, release_manifest_digest, generic_operation_id, started_at
		) VALUES ($1,$2,$3,$4,$5,'generic','running','{}'::jsonb,$6,$7,NULLIF($8,'00000000-0000-0000-0000-000000000000'::uuid),now())
		RETURNING id
	`, f.workspaceID, f.projectID, f.planID, f.nodeID, f.manifest.Environment,
		d2ParseUUID(t, f.manifest.ManifestID), f.manifest.ManifestDigest, operationID).Scan(&id)
	if err != nil {
		t.Fatalf("insert autonomous deployment fixture: %v", err)
	}
	return id
}

func (f *autonomousDeploymentBridgeFixture) runtime(bridge *AutonomousDeploymentBridge) *Runtime {
	return &Runtime{pool: f.pool, projectStore: projectorchestration.NewStore(f.pool), deploymentBridge: bridge}
}

func TestCleanupProjectDeletesReleaseManifest(t *testing.T) {
	t.Parallel()
	fixture := newAutonomousDeploymentBridgeFixture(t)
	if _, err := fixture.pool.Exec(fixture.ctx, `
		DELETE FROM autonomous_project_release_manifest WHERE manifest_id=$1
	`, d2ParseUUID(t, fixture.manifest.ManifestID)); err == nil {
		t.Fatal("direct release manifest deletion bypassed the cleanup boundary")
	}
	store := projectorchestration.NewStore(fixture.pool)
	if err := store.CleanupProject(fixture.ctx, fixture.workspaceID, fixture.projectID); err != nil {
		t.Fatalf("clean project deployment data: %v", err)
	}
	assertReleaseManifestDeleted(t, fixture)
}

func TestCleanupWorkspaceDeletesReleaseManifest(t *testing.T) {
	t.Parallel()
	fixture := newAutonomousDeploymentBridgeFixture(t)
	store := projectorchestration.NewStore(fixture.pool)
	if err := store.CleanupWorkspace(fixture.ctx, fixture.workspaceID); err != nil {
		t.Fatalf("clean workspace deployment data: %v", err)
	}
	assertReleaseManifestDeleted(t, fixture)
}

func assertReleaseManifestDeleted(t *testing.T, fixture *autonomousDeploymentBridgeFixture) {
	t.Helper()
	var count int
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT count(*) FROM autonomous_project_release_manifest
		WHERE workspace_id=$1 AND project_id=$2
	`, fixture.workspaceID, fixture.projectID).Scan(&count); err != nil {
		t.Fatalf("count remaining release manifests: %v", err)
	}
	if count != 0 {
		t.Fatalf("remaining release manifests = %d, want 0", count)
	}
}

type deploymentBridgeArtifactStore struct {
	mu      sync.Mutex
	content []byte
	urls    []string
}

func (s *deploymentBridgeArtifactStore) Upload(context.Context, string, []byte, string, string) (string, error) {
	return "", fmt.Errorf("unexpected artifact upload during deployment")
}
func (s *deploymentBridgeArtifactStore) GetReader(_ context.Context, key string) (io.ReadCloser, error) {
	if !strings.HasSuffix(key, ".tar") {
		return nil, fmt.Errorf("unexpected artifact key %q", key)
	}
	return io.NopCloser(bytes.NewReader(s.content)), nil
}
func (s *deploymentBridgeArtifactStore) PresignGet(_ context.Context, key string, ttl time.Duration) (string, error) {
	if ttl <= 0 || !strings.HasSuffix(key, ".tar") {
		return "", fmt.Errorf("invalid release retrieval request")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	url := fmt.Sprintf("https://private.example.invalid/release.tar?signature=fresh-%d", len(s.urls)+1)
	s.urls = append(s.urls, url)
	return url, nil
}

func (s *deploymentBridgeArtifactStore) presignedURLs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.urls...)
}

type deploymentBridgeWebhook struct {
	server *httptest.Server
	mu     sync.Mutex
	body   map[string]any
}

func newDeploymentBridgeWebhook(t *testing.T, manifest projectorchestration.ReleaseManifest) *deploymentBridgeWebhook {
	t.Helper()
	provider := &deploymentBridgeWebhook{}
	provider.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		provider.mu.Lock()
		provider.body = payload
		provider.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"provider": "test-provider", "external_ref": "release-42", "status": "succeeded",
			"evidence": map[string]any{
				"manifest_digest":     manifest.ManifestDigest,
				"artifact_digest":     manifest.ArtifactDigest,
				"deployed_commit_sha": manifest.CommitSHA,
				"artifact_verified":   true,
			},
		})
	}))
	t.Cleanup(provider.server.Close)
	return provider
}

func (p *deploymentBridgeWebhook) assertCalledWithFreshURL(t *testing.T, store *deploymentBridgeArtifactStore) {
	t.Helper()
	p.mu.Lock()
	body := p.body
	p.mu.Unlock()
	if body == nil {
		t.Fatal("deployment provider was not called")
	}
	gotURL, _ := body["artifact_url"].(string)
	urls := store.presignedURLs()
	if len(urls) != 1 || gotURL != urls[0] || !strings.Contains(gotURL, "signature=fresh-") {
		t.Fatalf("provider artifact URL = %q, presigned URLs = %#v; want the fresh one-time URL", gotURL, urls)
	}
}

func (f *autonomousDeploymentBridgeFixture) operation(t *testing.T, operationID pgtype.UUID) (string, []byte) {
	t.Helper()
	var status string
	var request []byte
	if err := f.pool.QueryRow(f.ctx, `SELECT status, request FROM deployment_operation WHERE id=$1`, operationID).Scan(&status, &request); err != nil {
		t.Fatalf("load persisted deployment operation: %v", err)
	}
	return status, request
}

func waitDeploymentOperation(t *testing.T, fixture *autonomousDeploymentBridgeFixture, operationID pgtype.UUID) struct {
	Status  string
	Request []byte
} {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, request := fixture.operation(t, operationID)
		if status == "succeeded" || status == "failed" || status == "canceled" {
			return struct {
				Status  string
				Request []byte
			}{status, request}
		}
		time.Sleep(10 * time.Millisecond)
	}
	status, _ := fixture.operation(t, operationID)
	t.Fatalf("generic deployment operation remained %s", status)
	return struct {
		Status  string
		Request []byte
	}{}
}

func assertDeploymentRequestHasNoURL(t *testing.T, request []byte) {
	t.Helper()
	encoded := strings.ToLower(string(request))
	if strings.Contains(encoded, "artifact_url") || strings.Contains(encoded, "signature=fresh-") {
		t.Fatalf("generic deployment request persisted a presigned URL: %s", request)
	}
}

func assertAutonomousDeploymentCompleted(t *testing.T, fixture *autonomousDeploymentBridgeFixture, deploymentID pgtype.UUID) {
	t.Helper()
	var deploymentStatus, nodeStatus string
	var operationID pgtype.UUID
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT d.status, n.status, d.generic_operation_id
		FROM autonomous_project_deployment d
		JOIN autonomous_project_plan_node n ON n.id=d.node_id
		WHERE d.id=$1
	`, deploymentID).Scan(&deploymentStatus, &nodeStatus, &operationID); err != nil {
		t.Fatalf("load projected autonomous deployment: %v", err)
	}
	if deploymentStatus != "succeeded" || nodeStatus != "completed" || !operationID.Valid {
		t.Fatalf("projected deployment=(%s,node=%s,op=%v), want succeeded/completed/correlated", deploymentStatus, nodeStatus, operationID.Valid)
	}
	var artifactCount int
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT count(*) FROM autonomous_project_artifact
		WHERE plan_id=$1 AND node_id=$2 AND artifact_type='deployment_record'
	`, fixture.planID, fixture.nodeID).Scan(&artifactCount); err != nil {
		t.Fatalf("count autonomous deployment records: %v", err)
	}
	if artifactCount != 1 {
		t.Fatalf("deployment artifact count = %d, want 1", artifactCount)
	}
}

func parseGenericOperationID(t *testing.T, result projectorchestration.DeploymentResult) pgtype.UUID {
	t.Helper()
	value, ok := result.Evidence["generic_operation_id"].(string)
	if !ok {
		t.Fatalf("deployment result omitted generic operation ID: %#v", result.Evidence)
	}
	return d2ParseUUID(t, value)
}

func d2ParseUUID(t *testing.T, value string) pgtype.UUID {
	t.Helper()
	parsed, err := uuid.Parse(value)
	if err != nil {
		t.Fatalf("parse fixture UUID %q: %v", value, err)
	}
	return pgtype.UUID{Bytes: [16]byte(parsed), Valid: true}
}

func d2UUIDText(value pgtype.UUID) string { return uuid.UUID(value.Bytes).String() }

func d2UUID(value pgtype.UUID) uuid.UUID { return uuid.UUID(value.Bytes) }

func d2PGUUID(value uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: [16]byte(value), Valid: value != uuid.Nil}
}

func d2StringPointer(value string) *string { return &value }
