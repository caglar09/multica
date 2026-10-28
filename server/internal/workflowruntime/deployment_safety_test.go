package workflowruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/projectorchestration"
	"github.com/multica-ai/multica/server/internal/util"
)

func TestApprovalVerification_RejectsReusedNodeKey(t *testing.T) {
	fixture := newAutonomousDeploymentBridgeFixture(t)
	cleanupSafetyEscalations(t, fixture)
	stagingManifest := addManifestEnvironment(t, fixture, "staging")
	insertHealthyStagingDeployment(t, fixture, stagingManifest)
	runtime := fixture.runtime(nil)
	node := projectorchestration.ReadyNode{Key: "deploy", Kind: projectorchestration.NodeDeploy}

	if _, err := fixture.pool.Exec(fixture.ctx, `
		INSERT INTO autonomous_project_escalation (
			workspace_id, project_id, category, status, summary, context, resolution, resolved_at
		) VALUES ($1,$2,'approval_required','resolved','old node approval',
			'{"node_key":"deploy"}'::jsonb,'{"decision":"approved"}'::jsonb,now())
	`, fixture.workspaceID, fixture.projectID); err != nil {
		t.Fatalf("insert historical node-key approval: %v", err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `
		UPDATE autonomous_project_plan_node
		SET status='blocked',blocked_category='approval',blocked_reason='awaiting production approval'
		WHERE id=$1
	`, fixture.nodeID); err != nil {
		t.Fatalf("block production deployment node: %v", err)
	}
	if err := runtime.reconcileProjectBlockedNodes(fixture.ctx, fixture.workspaceID, fixture.projectID); err != nil {
		t.Fatalf("reconcile historical node-key approval: %v", err)
	}
	var blockedNodeStatus string
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT status FROM autonomous_project_plan_node WHERE id=$1`, fixture.nodeID).Scan(&blockedNodeStatus); err != nil {
		t.Fatalf("read node after stale approval reconciliation: %v", err)
	}
	if blockedNodeStatus != "blocked" {
		t.Fatalf("historical node-key approval resumed production node to %s", blockedNodeStatus)
	}

	if err := runtime.enforceProjectNodePolicy(fixture.ctx, fixture.workspaceID, fixture.projectID, node); !errors.Is(err, errProjectApprovalRequired) {
		t.Fatalf("historical node-key approval result = %v, want exact manifest approval required", err)
	}

	mutations := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "plan revision", mutate: func(fields map[string]any) { fields["plan_revision"] = int64(2) }},
		{name: "target environment", mutate: func(fields map[string]any) { fields["target_environment"] = "staging" }},
		{name: "manifest digest", mutate: func(fields map[string]any) { fields["manifest_digest"] = strings.Repeat("f", 64) }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			fields := productionApprovalFields(fixture, fixture.manifest)
			mutation.mutate(fields)
			insertResolvedApproval(t, fixture, fields)
			if err := runtime.enforceProjectNodePolicy(fixture.ctx, fixture.workspaceID, fixture.projectID, node); !errors.Is(err, errProjectApprovalRequired) {
				t.Fatalf("mismatched %s approval result = %v, want exact manifest approval required", mutation.name, err)
			}
		})
	}
}

func TestDeployment_TriggersRollbackOnHealthFailure(t *testing.T) {
	fixture := newAutonomousDeploymentBridgeFixture(t)
	targetManifest := fixture.manifest
	targetManifest.ManifestID = uuid.NewString()
	targetManifest.CommitSHA = strings.Repeat("a", 40)
	targetManifest.TreeSHA = strings.Repeat("b", 40)
	targetManifest.ArtifactDigest = strings.Repeat("e", 64)
	targetManifest.ArtifactStorageKey = "autonomous-project/releases/sha256/" + targetManifest.ArtifactDigest + ".tar"
	targetManifest.ManifestDigest = ""
	var err error
	targetManifest, err = projectorchestration.SealReleaseManifest(targetManifest)
	if err != nil {
		t.Fatalf("seal known-good rollback manifest: %v", err)
	}
	insertReleaseManifest(t, fixture, targetManifest)

	knownGoodID := insertDeploymentRecord(t, fixture, targetManifest, "succeeded", time.Now().Add(-time.Minute),
		map[string]any{})
	currentID := insertDeploymentRecord(t, fixture, fixture.manifest, "succeeded", time.Now(), map[string]any{})
	if _, err := fixture.pool.Exec(fixture.ctx, `
		INSERT INTO autonomous_project_artifact (
			workspace_id, project_id, plan_id, node_id, artifact_type, name, content
		) VALUES ($1,$2,$3,$4,'generic','known-good health evidence',
			jsonb_build_object('deployment_id',$5::text,'healthy',true))
	`, fixture.workspaceID, fixture.projectID, fixture.planID, fixture.nodeID, util.UUIDToString(knownGoodID)); err != nil {
		t.Fatalf("insert known-good health evidence: %v", err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `
		UPDATE autonomous_project_plan_node SET status='ready' WHERE id=$1
	`, fixture.nodeID); err != nil {
		t.Fatalf("make observation node ready: %v", err)
	}

	provider := &rollbackCaptureAdapter{result: projectorchestration.DeploymentResult{
		Provider: "test-provider", Status: "succeeded",
		Evidence: map[string]any{
			"target_manifest_id":     targetManifest.ManifestID,
			"target_manifest_digest": targetManifest.ManifestDigest,
		},
	}}
	runtime := fixture.runtime(nil)
	runtime.deploymentAdapter = provider
	runtime.observationAdapter = fixedObservationAdapter{result: projectorchestration.ObservationResult{
		Healthy: false, ErrorRate: 0.1, Signals: []string{"error_rate"},
	}}
	if err := runtime.executeObservationNode(fixture.ctx, fixture.workspaceID, fixture.projectID,
		projectorchestration.ReadyNode{Key: "deploy", Kind: projectorchestration.NodeObserve, Title: "Production health"}); err != nil {
		t.Fatalf("execute unhealthy deployment observation: %v", err)
	}
	if provider.request.TargetManifestID != targetManifest.ManifestID ||
		provider.request.TargetManifestDigest != targetManifest.ManifestDigest ||
		provider.request.TargetEnvironment != "production" || provider.request.DeploymentID != knownGoodID {
		t.Fatalf("rollback target = %#v, want known-good deployment and exact manifest", provider.request)
	}
	var status string
	var evidence []byte
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT status,evidence FROM autonomous_project_deployment WHERE id=$1
	`, currentID).Scan(&status, &evidence); err != nil {
		t.Fatalf("read rolled-back deployment: %v", err)
	}
	var persisted map[string]any
	if err := json.Unmarshal(evidence, &persisted); err != nil {
		t.Fatalf("decode rollback evidence: %v", err)
	}
	rollback, ok := persisted["health_rollback"].(map[string]any)
	if status != "rolled_back" || !ok || rollback["target_manifest_id"] != targetManifest.ManifestID ||
		rollback["target_manifest_digest"] != targetManifest.ManifestDigest || rollback["status"] != "succeeded" {
		t.Fatalf("persisted rollback outcome status=%s evidence=%#v", status, persisted)
	}
}

func TestProductionDeployment_RequiresExactHumanManifestApproval(t *testing.T) {
	fixture := newAutonomousDeploymentBridgeFixture(t)
	cleanupSafetyEscalations(t, fixture)
	stagingManifest := addManifestEnvironment(t, fixture, "staging")
	insertHealthyStagingDeployment(t, fixture, stagingManifest)
	runtime := fixture.runtime(nil)
	node := projectorchestration.ReadyNode{Key: "deploy", Kind: projectorchestration.NodeDeploy}
	if err := runtime.enforceProjectNodePolicy(fixture.ctx, fixture.workspaceID, fixture.projectID, node); !errors.Is(err, errProjectApprovalRequired) {
		t.Fatalf("production policy before human approval = %v, want approval required", err)
	}
	if err := runtime.openProjectEscalation(fixture.ctx, fixture.workspaceID, fixture.projectID, node, errProjectApprovalRequired); err != nil {
		t.Fatalf("open exact production approval: %v", err)
	}
	var escalationID pgtype.UUID
	var contextJSON []byte
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT id,context FROM autonomous_project_escalation
		WHERE workspace_id=$1 AND project_id=$2 AND category='approval_required'
		  AND status='open' AND context->>'approval_scope'='production_release'
	`, fixture.workspaceID, fixture.projectID).Scan(&escalationID, &contextJSON); err != nil {
		t.Fatalf("load exact production approval scope: %v", err)
	}
	var scope map[string]any
	if err := json.Unmarshal(contextJSON, &scope); err != nil {
		t.Fatalf("decode exact approval scope: %v", err)
	}
	for key, expected := range map[string]any{
		"workspace_id":       util.UUIDToString(fixture.workspaceID),
		"project_id":         util.UUIDToString(fixture.projectID),
		"plan_id":            util.UUIDToString(fixture.planID),
		"plan_revision":      float64(fixture.manifest.PlanRevision),
		"node_id":            util.UUIDToString(fixture.nodeID),
		"target_environment": "production",
		"manifest_id":        fixture.manifest.ManifestID,
		"manifest_digest":    fixture.manifest.ManifestDigest,
	} {
		if scope[key] != expected {
			t.Fatalf("approval context %s = %#v, want %#v", key, scope[key], expected)
		}
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `
		UPDATE autonomous_project_escalation
		SET status='resolved', resolution=context || '{"decision":"approved"}'::jsonb, resolved_at=now()
		WHERE id=$1
	`, escalationID); err != nil {
		t.Fatalf("record exact human approval decision: %v", err)
	}
	if err := runtime.enforceProjectNodePolicy(fixture.ctx, fixture.workspaceID, fixture.projectID, node); err != nil {
		t.Fatalf("exact production manifest approval was rejected: %v", err)
	}
}

func TestDeployment_StagesBeforeProductionPromotion(t *testing.T) {
	t.Run("healthy staging requires exact approval before production", func(t *testing.T) {
		fixture := newAutonomousDeploymentBridgeFixture(t)
		cleanupSafetyEscalations(t, fixture)
		stageManifest := addManifestEnvironment(t, fixture, "staging")
		provider := newSafetyPromotionProvider(t, true)
		adapter := projectorchestration.NewWebhookDeploymentAdapter(projectorchestration.WebhookAdapterConfig{DeploymentURL: provider.server.URL})
		bridge := newSafetyBridge(t, fixture, adapter)
		runtime := safetyRuntime(fixture, bridge, true)
		setSafetyNodeReady(t, fixture)

		if err := runtime.executeDeploymentNode(fixture.ctx, fixture.workspaceID, fixture.projectID,
			projectorchestration.ReadyNode{Key: "deploy", Kind: projectorchestration.NodeDeploy, Title: "Deploy"}); err != nil {
			t.Fatalf("dispatch staging deployment: %v", err)
		}
		waitAndProjectLatestOperation(t, fixture, runtime, "staging")
		assertDeploymentManifest(t, fixture, "staging", stageManifest.ManifestID, stageManifest.ManifestDigest)
		assertPlanManifest(t, fixture, "production", fixture.manifest.ManifestID, fixture.manifest.ManifestDigest)
		assertNodeState(t, fixture, "ready")
		provider.assertActions(t, []string{"deploy:staging", "staging_smoke_test"})

		node := projectorchestration.ReadyNode{Key: "deploy", Kind: projectorchestration.NodeDeploy}
		if err := runtime.enforceProjectNodePolicy(fixture.ctx, fixture.workspaceID, fixture.projectID, node); !errors.Is(err, errProjectApprovalRequired) {
			t.Fatalf("production policy before approval = %v, want approval required", err)
		}
		provider.assertNoProductionDispatch(t)
		if err := runtime.openProjectEscalation(fixture.ctx, fixture.workspaceID, fixture.projectID, node, errProjectApprovalRequired); err != nil {
			t.Fatalf("open production approval: %v", err)
		}
		approveCurrentProductionManifest(t, fixture)
		if err := runtime.enforceProjectNodePolicy(fixture.ctx, fixture.workspaceID, fixture.projectID, node); err != nil {
			t.Fatalf("exact production approval did not authorize dispatch: %v", err)
		}
		if err := runtime.executeDeploymentNode(fixture.ctx, fixture.workspaceID, fixture.projectID, node); err != nil {
			t.Fatalf("dispatch approved production deployment: %v", err)
		}
		waitAndProjectLatestOperation(t, fixture, runtime, "production")
		assertNodeState(t, fixture, "completed")
		var planStatus string
		if err := fixture.pool.QueryRow(fixture.ctx, "SELECT status FROM autonomous_project_plan WHERE id=$1", fixture.planID).Scan(&planStatus); err != nil {
			t.Fatalf("read final plan status: %v", err)
		}
		if planStatus != "completed" {
			t.Fatalf("plan status after production success = %s, want completed", planStatus)
		}
		provider.assertActions(t, []string{"deploy:staging", "staging_smoke_test", "deploy:production"})
	})

	for _, tc := range []struct {
		name        string
		smokePassed bool
		healthy     bool
	}{
		{name: "smoke failure leaves production unpinned", smokePassed: false, healthy: true},
		{name: "unhealthy staging leaves production unpinned", smokePassed: true, healthy: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newAutonomousDeploymentBridgeFixture(t)
			cleanupSafetyEscalations(t, fixture)
			stageManifest := addManifestEnvironment(t, fixture, "staging")
			provider := newSafetyPromotionProvider(t, tc.smokePassed)
			adapter := projectorchestration.NewWebhookDeploymentAdapter(projectorchestration.WebhookAdapterConfig{DeploymentURL: provider.server.URL})
			bridge := newSafetyBridge(t, fixture, adapter)
			runtime := safetyRuntime(fixture, bridge, tc.healthy)
			setSafetyNodeReady(t, fixture)
			if err := runtime.executeDeploymentNode(fixture.ctx, fixture.workspaceID, fixture.projectID,
				projectorchestration.ReadyNode{Key: "deploy", Kind: projectorchestration.NodeDeploy, Title: "Deploy"}); err != nil {
				t.Fatalf("dispatch staging deployment: %v", err)
			}
			waitAndProjectLatestOperation(t, fixture, runtime, "staging")
			assertPlanManifest(t, fixture, "staging", stageManifest.ManifestID, stageManifest.ManifestDigest)
			assertNodeState(t, fixture, "ready")
			provider.assertNoProductionDispatch(t)
			var status string
			var evidence []byte
			if err := fixture.pool.QueryRow(fixture.ctx, `
				SELECT status,evidence FROM autonomous_project_deployment WHERE plan_id=$1 AND environment='staging'
			`, fixture.planID).Scan(&status, &evidence); err != nil {
				t.Fatalf("read failed staging record: %v", err)
			}
			if status != "failed" {
				t.Fatalf("staging status = %s, want failed", status)
			}
			var persisted map[string]any
			if err := json.Unmarshal(evidence, &persisted); err != nil {
				t.Fatalf("decode failed staging evidence: %v", err)
			}
			if tc.smokePassed {
				if health, ok := persisted["stage_health"].(map[string]any); !ok || health["healthy"] != false {
					t.Fatalf("unhealthy stage did not persist explicit health failure: %#v", persisted)
				}
			} else {
				if smoke, ok := persisted["stage_smoke"].(map[string]any); !ok || smoke["passed"] != false || persisted["stage_health"] != nil {
					t.Fatalf("smoke failure did not stop before health/promotion: %#v", persisted)
				}
			}
		})
	}
}

func addManifestEnvironment(t *testing.T, fixture *autonomousDeploymentBridgeFixture, environment string) projectorchestration.ReleaseManifest {
	t.Helper()
	manifest := fixture.manifest
	manifest.ManifestID = uuid.NewString()
	manifest.Environment = environment
	manifest.ManifestDigest = ""
	sealed, err := projectorchestration.SealReleaseManifest(manifest)
	if err != nil {
		t.Fatalf("seal %s fixture manifest: %v", environment, err)
	}
	insertReleaseManifest(t, fixture, sealed)
	t.Cleanup(func() {
		_, _ = fixture.pool.Exec(context.Background(), `
			DELETE FROM autonomous_project_release_manifest
			WHERE workspace_id=$1 AND project_id=$2 AND manifest_id=$3
		`, fixture.workspaceID, fixture.projectID, d2ParseUUID(t, sealed.ManifestID))
	})
	return sealed
}

func cleanupSafetyEscalations(t *testing.T, fixture *autonomousDeploymentBridgeFixture) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = fixture.pool.Exec(context.Background(), `
			DELETE FROM autonomous_project_escalation WHERE workspace_id=$1 AND project_id=$2
		`, fixture.workspaceID, fixture.projectID)
	})
}

func insertReleaseManifest(t *testing.T, fixture *autonomousDeploymentBridgeFixture, manifest projectorchestration.ReleaseManifest) {
	t.Helper()
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("encode fixture release manifest: %v", err)
	}
	runs := make([]pgtype.UUID, 0, len(manifest.QualityGateRunIDs))
	for _, id := range manifest.QualityGateRunIDs {
		runs = append(runs, d2ParseUUID(t, id))
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `
		INSERT INTO autonomous_project_release_manifest (
			manifest_id,manifest_digest,workspace_id,project_id,plan_id,change_set_id,
			plan_revision,plan_digest,environment,commit_sha,tree_sha,builder,outputs,
			artifact_digest,artifact_storage_key,quality_gate_run_ids,manifest_json
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
	`, d2ParseUUID(t, manifest.ManifestID), manifest.ManifestDigest, fixture.workspaceID, fixture.projectID,
		fixture.planID, d2ParseUUID(t, manifest.ChangeSetID), manifest.PlanRevision, manifest.PlanDigest,
		manifest.Environment, manifest.CommitSHA, manifest.TreeSHA, manifest.Builder, manifest.Outputs,
		manifest.ArtifactDigest, manifest.ArtifactStorageKey, runs, encoded); err != nil {
		t.Fatalf("insert %s fixture release manifest: %v", manifest.Environment, err)
	}
}

func insertHealthyStagingDeployment(t *testing.T, fixture *autonomousDeploymentBridgeFixture, manifest projectorchestration.ReleaseManifest) pgtype.UUID {
	t.Helper()
	return insertDeploymentRecord(t, fixture, manifest, "succeeded", time.Now(), map[string]any{
		"stage_smoke":  map[string]any{"passed": true, "manifest_digest": manifest.ManifestDigest},
		"stage_health": map[string]any{"healthy": true},
	})
}

func insertDeploymentRecord(
	t *testing.T,
	fixture *autonomousDeploymentBridgeFixture,
	manifest projectorchestration.ReleaseManifest,
	status string,
	createdAt time.Time,
	evidence map[string]any,
) pgtype.UUID {
	t.Helper()
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatalf("encode deployment evidence fixture: %v", err)
	}
	var id pgtype.UUID
	if err := fixture.pool.QueryRow(fixture.ctx, `
		INSERT INTO autonomous_project_deployment (
			workspace_id,project_id,plan_id,node_id,environment,provider,status,evidence,
			policy_snapshot,release_manifest_id,release_manifest_digest,started_at,completed_at,created_at
		) VALUES ($1,$2,$3,$4,$5,'test',$6,$7,'{}'::jsonb,$8,$9,$10,$10,$10)
		RETURNING id
	`, fixture.workspaceID, fixture.projectID, fixture.planID, fixture.nodeID, manifest.Environment,
		status, encoded, d2ParseUUID(t, manifest.ManifestID), manifest.ManifestDigest, createdAt).Scan(&id); err != nil {
		t.Fatalf("insert deployment fixture: %v", err)
	}
	return id
}

func productionApprovalFields(fixture *autonomousDeploymentBridgeFixture, manifest projectorchestration.ReleaseManifest) map[string]any {
	return map[string]any{
		"approval_scope":     "production_release",
		"workspace_id":       util.UUIDToString(fixture.workspaceID),
		"project_id":         util.UUIDToString(fixture.projectID),
		"plan_id":            util.UUIDToString(fixture.planID),
		"plan_revision":      manifest.PlanRevision,
		"node_id":            util.UUIDToString(fixture.nodeID),
		"node_key":           "deploy",
		"target_environment": "production",
		"manifest_id":        manifest.ManifestID,
		"manifest_digest":    manifest.ManifestDigest,
	}
}

func insertResolvedApproval(t *testing.T, fixture *autonomousDeploymentBridgeFixture, fields map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("encode approval context fixture: %v", err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `
		INSERT INTO autonomous_project_escalation (
			workspace_id,project_id,plan_id,node_id,category,status,summary,context,resolution,resolved_at
		) VALUES ($1,$2,$3,$4,'approval_required','resolved','test approval',$5,$6,now())
	`, fixture.workspaceID, fixture.projectID, fixture.planID, fixture.nodeID, encoded,
		mustJSON(t, mergeJSON(fields, map[string]any{"decision": "approved"}))); err != nil {
		t.Fatalf("insert scoped approval fixture: %v", err)
	}
}

func mergeJSON(left, right map[string]any) []byte {
	merged := make(map[string]any, len(left)+len(right))
	for key, value := range left {
		merged[key] = value
	}
	for key, value := range right {
		merged[key] = value
	}
	encoded, _ := json.Marshal(merged)
	return encoded
}

func mustJSON(t *testing.T, value []byte) []byte {
	t.Helper()
	if !json.Valid(value) {
		t.Fatal("test JSON is invalid")
	}
	return value
}

func approveCurrentProductionManifest(t *testing.T, fixture *autonomousDeploymentBridgeFixture) {
	t.Helper()
	var id pgtype.UUID
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT id FROM autonomous_project_escalation
		WHERE workspace_id=$1 AND project_id=$2 AND category='approval_required'
		  AND status='open' AND context->>'approval_scope'='production_release'
		ORDER BY opened_at DESC LIMIT 1
	`, fixture.workspaceID, fixture.projectID).Scan(&id); err != nil {
		t.Fatalf("load production approval escalation: %v", err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `
		UPDATE autonomous_project_escalation
		SET status='resolved', resolution=context || '{"decision":"approved"}'::jsonb, resolved_at=now()
		WHERE id=$1
	`, id); err != nil {
		t.Fatalf("record production approval: %v", err)
	}
}

func assertPlanManifest(t *testing.T, fixture *autonomousDeploymentBridgeFixture, environment, manifestID, digest string) {
	t.Helper()
	var actualEnvironment string
	var actualID pgtype.UUID
	var actualDigest string
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT m.environment,p.release_manifest_id,p.release_manifest_digest
		FROM autonomous_project_plan p
		JOIN autonomous_project_release_manifest m
		  ON m.manifest_id=p.release_manifest_id AND m.manifest_digest=p.release_manifest_digest
		WHERE p.id=$1
	`, fixture.planID).Scan(&actualEnvironment, &actualID, &actualDigest); err != nil {
		t.Fatalf("read pinned release manifest: %v", err)
	}
	if actualEnvironment != environment || util.UUIDToString(actualID) != manifestID || actualDigest != digest {
		t.Fatalf("pinned manifest=(%s,%s,%s), want (%s,%s,%s)", actualEnvironment,
			util.UUIDToString(actualID), actualDigest, environment, manifestID, digest)
	}
}

func assertDeploymentManifest(t *testing.T, fixture *autonomousDeploymentBridgeFixture, environment, manifestID, digest string) {
	t.Helper()
	var actualID pgtype.UUID
	var actualDigest string
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT release_manifest_id,release_manifest_digest
		FROM autonomous_project_deployment
		WHERE plan_id=$1 AND environment=$2
		ORDER BY created_at DESC LIMIT 1
	`, fixture.planID, environment).Scan(&actualID, &actualDigest); err != nil {
		t.Fatalf("read %s deployment manifest: %v", environment, err)
	}
	if util.UUIDToString(actualID) != manifestID || actualDigest != digest {
		t.Fatalf("%s deployment manifest=(%s,%s), want (%s,%s)", environment,
			util.UUIDToString(actualID), actualDigest, manifestID, digest)
	}
}

func assertNodeState(t *testing.T, fixture *autonomousDeploymentBridgeFixture, expected string) {
	t.Helper()
	var state string
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT status FROM autonomous_project_plan_node WHERE id=$1", fixture.nodeID).Scan(&state); err != nil {
		t.Fatalf("read deployment node status: %v", err)
	}
	if state != expected {
		t.Fatalf("deployment node status = %s, want %s", state, expected)
	}
}

func newSafetyBridge(t *testing.T, fixture *autonomousDeploymentBridgeFixture, adapter projectorchestration.DeploymentAdapter) *AutonomousDeploymentBridge {
	t.Helper()
	return genericAutonomousBridge(t, fixture, adapter)
}

func genericAutonomousBridge(t *testing.T, fixture *autonomousDeploymentBridgeFixture, adapter projectorchestration.DeploymentAdapter) *AutonomousDeploymentBridge {
	t.Helper()
	bridge := NewAutonomousDeploymentBridge(fixture.pool, adapter, fixture.artifactStore)
	if bridge == nil {
		t.Fatal("autonomous deployment bridge was not configured")
	}
	return bridge
}

func safetyRuntime(
	fixture *autonomousDeploymentBridgeFixture,
	bridge *AutonomousDeploymentBridge,
	healthy bool,
) *Runtime {
	runtime := fixture.runtime(bridge)
	runtime.deploymentAdapter = bridge
	runtime.releaseArtifactStore = fixture.artifactStore
	runtime.observationAdapter = fixedObservationAdapter{result: projectorchestration.ObservationResult{Healthy: healthy}}
	return runtime
}

func setSafetyNodeReady(t *testing.T, fixture *autonomousDeploymentBridgeFixture) {
	t.Helper()
	if _, err := fixture.pool.Exec(fixture.ctx, "UPDATE autonomous_project_plan_node SET status='ready' WHERE id=$1", fixture.nodeID); err != nil {
		t.Fatalf("make deployment node ready: %v", err)
	}
}

func waitAndProjectLatestOperation(t *testing.T, fixture *autonomousDeploymentBridgeFixture, runtime *Runtime, environment string) {
	t.Helper()
	var operationID pgtype.UUID
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		err := fixture.pool.QueryRow(fixture.ctx, `
			SELECT id FROM deployment_operation WHERE workspace_id=$1 AND target_id=$2 AND environment=$3
			ORDER BY created_at DESC LIMIT 1
		`, fixture.workspaceID, fixture.projectID, environment).Scan(&operationID)
		if err == nil {
			operation := waitDeploymentOperation(t, fixture, operationID)
			if operation.Status != "succeeded" {
				t.Fatalf("%s generic operation status = %s, want succeeded", environment, operation.Status)
			}
			if err := runtime.reconcileAutonomousDeploymentResults(fixture.ctx); err != nil {
				t.Fatalf("project %s terminal deployment: %v", environment, err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no %s generic deployment operation was persisted", environment)
}

func newSafetyPromotionProvider(t *testing.T, smokePassed bool) *safetyPromotionProvider {
	t.Helper()
	provider := &safetyPromotionProvider{smokePassed: smokePassed}
	provider.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		provider.mu.Lock()
		action := fmt.Sprint(payload["action"])
		if action == "deploy" {
			action += ":" + fmt.Sprint(payload["environment"])
		}
		provider.actions = append(provider.actions, action)
		providerPayload := payload
		provider.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch payload["action"] {
		case "deploy":
			manifest, _ := payload["manifest"].(map[string]any)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"provider": "test-provider", "external_ref": "release-" + fmt.Sprint(payload["environment"]), "status": "succeeded",
				"evidence": map[string]any{
					"manifest_digest": manifest["manifest_digest"], "artifact_digest": payload["artifact_digest"],
					"deployed_commit_sha": payload["commit_sha"], "artifact_verified": true,
				},
			})
		case "staging_smoke_test":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"passed": provider.smokePassed, "manifest_digest": payload["manifest_digest"],
				"evidence": map[string]any{"provider_ref": providerPayload["external_ref"]},
			})
		default:
			http.Error(w, "unknown action", http.StatusBadRequest)
		}
	}))
	t.Cleanup(provider.server.Close)
	return provider
}

type safetyPromotionProvider struct {
	server      *httptest.Server
	mu          sync.Mutex
	actions     []string
	smokePassed bool
}

func (p *safetyPromotionProvider) assertActions(t *testing.T, expected []string) {
	t.Helper()
	p.mu.Lock()
	got := append([]string(nil), p.actions...)
	p.mu.Unlock()
	if fmt.Sprint(got) != fmt.Sprint(expected) {
		t.Fatalf("provider actions = %v, want %v", got, expected)
	}
}

func (p *safetyPromotionProvider) assertNoProductionDispatch(t *testing.T) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, action := range p.actions {
		if action == "deploy:production" {
			t.Fatal("provider received production dispatch before exact approval/staging gates")
		}
	}
}

type fixedObservationAdapter struct {
	result projectorchestration.ObservationResult
	err    error
}

func (a fixedObservationAdapter) Observe(context.Context, projectorchestration.ObservationRequest) (projectorchestration.ObservationResult, error) {
	return a.result, a.err
}

type rollbackCaptureAdapter struct {
	request projectorchestration.RollbackRequest
	result  projectorchestration.DeploymentResult
	err     error
}

func (a *rollbackCaptureAdapter) Deploy(context.Context, projectorchestration.DeploymentRequest) (projectorchestration.DeploymentResult, error) {
	return projectorchestration.DeploymentResult{}, errors.New("unexpected deployment call")
}

func (a *rollbackCaptureAdapter) Rollback(_ context.Context, request projectorchestration.RollbackRequest) (projectorchestration.DeploymentResult, error) {
	a.request = request
	return a.result, a.err
}
