package workflowruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/projectorchestration"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestAutonomousProjectOS_EndToEndScenario(t *testing.T) {
	fixture := newAutonomousDeploymentBridgeFixture(t)
	ctx := fixture.ctx
	store := projectorchestration.NewStore(fixture.pool)

	// 1. Preserve all five requirements, including the intentionally long one.
	longRequirement := strings.Join(repeatScenarioTokens(500), " ")
	spec := projectorchestration.ProjectSpecification{
		Goal:    "Build a traceable web product from an approved autonomous plan.",
		Summary: "A small web project brief with explicit requirements and boundaries.",
		Requirements: []projectorchestration.SpecificationRequirement{
			{ID: "REQ-1", Text: "Users can create and edit a project.", Rationale: "Project management is the primary user workflow.", Source: "approved brief"},
			{ID: "REQ-2", Text: "Users can search projects by name.", Rationale: "Search makes a growing project list usable.", Source: "approved brief"},
			{ID: "REQ-3", Text: "Project changes show an auditable history.", Rationale: "Users need to understand how project data changed.", Source: "approved brief"},
			{ID: "REQ-4", Text: "The interface works on narrow screens.", Rationale: "The product must remain usable on mobile devices.", Source: "approved brief"},
			{ID: "REQ-5", Text: longRequirement, Rationale: "Long requirements must survive persistence without truncation.", Source: "approved brief"},
		},
		NonGoals:         []string{"No billing or payment flow.", "No public user directory."},
		DefinitionOfDone: []string{"Every requirement is implemented and independently reviewed."},
	}
	if got := len(strings.Fields(spec.Requirements[4].Text)); got != 500 {
		t.Fatalf("long requirement word count = %d, want 500", got)
	}
	specRevision, err := store.CreateSpecificationRevision(ctx, fixture.workspaceID, fixture.projectID,
		"project_bootstrap", "e2e-"+uuid.NewString(), spec, true)
	if err != nil {
		t.Fatalf("persist complete project brief: %v", err)
	}

	// 2. Persist a two-level Epic -> task DAG and approve its exact digest.
	req123 := []string{"REQ-1", "REQ-2", "REQ-3"}
	req45 := []string{"REQ-4", "REQ-5"}
	acceptance := []string{"The linked requirements are met and recorded as evidence."}
	policy := projectorchestration.DefaultPolicy()
	policy.Autonomy = projectorchestration.AutonomyDelivery
	plan := projectorchestration.Plan{
		Version:                 projectorchestration.CurrentPlanVersion,
		Goal:                    spec.Goal,
		Specification:           spec.LegacySpecification(),
		SpecificationRevisionID: specRevision.ID,
		Policy:                  policy,
		Nodes: []projectorchestration.NodeSpec{
			{Key: "web-epic", IsEpic: true, Kind: projectorchestration.NodeProduct, Title: "Web product", AcceptanceCriteria: acceptance, Risk: projectorchestration.RiskLow, MaxAttempts: 2},
			{Key: "implement-a", ParentEpicKey: "web-epic", RequirementIDs: req123, Kind: projectorchestration.NodeImplementation, Title: "Implement project flows", AcceptanceCriteria: acceptance, Risk: projectorchestration.RiskLow, MaxAttempts: 2},
			{Key: "review-a", ParentEpicKey: "web-epic", RequirementIDs: req123, Kind: projectorchestration.NodeReview, Title: "Review project flows", AcceptanceCriteria: acceptance, Risk: projectorchestration.RiskLow, MaxAttempts: 2},
			{Key: "implement-b", ParentEpicKey: "web-epic", RequirementIDs: req45, Kind: projectorchestration.NodeImplementation, Title: "Implement responsive and long-form requirements", AcceptanceCriteria: acceptance, Risk: projectorchestration.RiskLow, MaxAttempts: 2},
			{Key: "review-b", ParentEpicKey: "web-epic", RequirementIDs: req45, Kind: projectorchestration.NodeReview, Title: "Review responsive and long-form requirements", AcceptanceCriteria: acceptance, Risk: projectorchestration.RiskLow, MaxAttempts: 2},
			{Key: "deploy", ParentEpicKey: "web-epic", Kind: projectorchestration.NodeDeploy, Title: "Deploy approved release", AcceptanceCriteria: acceptance, Risk: projectorchestration.RiskLow, MaxAttempts: 2},
		},
		Edges: []projectorchestration.EdgeSpec{
			{From: "web-epic", To: "implement-a", Type: projectorchestration.DependencyHard},
			{From: "implement-a", To: "review-a", Type: projectorchestration.DependencyHard},
			{From: "web-epic", To: "implement-b", Type: projectorchestration.DependencyHard},
			{From: "implement-b", To: "review-b", Type: projectorchestration.DependencyHard},
			{From: "review-a", To: "deploy", Type: projectorchestration.DependencyHard},
			{From: "review-b", To: "deploy", Type: projectorchestration.DependencyHard},
		},
	}
	stored, err := store.PersistPlan(ctx, fixture.workspaceID, fixture.projectID,
		"e2e-"+uuid.NewString(), "test-planner", "", plan)
	if err != nil {
		t.Fatalf("persist hierarchical project plan: %v", err)
	}
	if stored.Status != "awaiting_approval" {
		t.Fatalf("new plan status = %q, want awaiting_approval", stored.Status)
	}
	planID := d2ParseUUID(t, stored.ID)
	fixture.planID = planID
	fixture.nodeID = scenarioPlanNodeID(t, fixture, planID, "deploy")
	fixtures := testutil.New(fixture.pool, fixture.workspaceIDText, fixture.ownerID)
	runtimeID := fixtures.Runtime(t, "Autonomous project OS E2E runtime")
	fixtures.Cleanup(t, "DELETE FROM agent_runtime WHERE id=$1", d2ParseUUID(t, runtimeID))
	agentID := fixtures.Agent(t, "Autonomous project OS E2E agent", runtimeID)
	fixtures.Cleanup(t, "DELETE FROM agent WHERE id=$1", d2ParseUUID(t, agentID))
	implementationTasks := map[string][2]pgtype.UUID{}
	for _, key := range []string{"implement-a", "implement-b", "review-a", "review-b"} {
		issueText := fixtures.Issue(t, "E2E "+key, testutil.Cols{
			"project_id": fixture.projectIDText,
		})
		issueID := d2ParseUUID(t, issueText)
		fixtures.Cleanup(t, "DELETE FROM issue WHERE id=$1", issueID)
		taskText := fixtures.Task(t, agentID, testutil.Cols{
			"issue_id": issueText, "runtime_id": runtimeID, "status": "completed",
		})
		taskID := d2ParseUUID(t, taskText)
		fixtures.Cleanup(t, "DELETE FROM agent_task_queue WHERE id=$1", taskID)
		implementationTasks[key] = [2]pgtype.UUID{issueID, taskID}
	}
	nodeIDs := map[string]pgtype.UUID{}
	rows, err := fixture.pool.Query(ctx, `SELECT node_key,id FROM autonomous_project_plan_node WHERE plan_id=$1`, planID)
	if err != nil {
		t.Fatalf("load plan node identities: %v", err)
	}
	for rows.Next() {
		var key string
		var id pgtype.UUID
		if err := rows.Scan(&key, &id); err != nil {
			rows.Close()
			t.Fatalf("scan plan node identity: %v", err)
		}
		nodeIDs[key] = id
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("read plan node identities: %v", err)
	}
	rows.Close()
	for _, key := range []string{"implement-a", "review-a", "implement-b", "review-b"} {
		if _, err := fixture.pool.Exec(ctx, `
			UPDATE autonomous_project_plan_node
			SET materialized_issue_id=$3,status='completed',completed_at=now()
			WHERE plan_id=$1 AND project_id=$2 AND node_key=$4
		`, planID, fixture.projectID, implementationTasks[key][0], key); err != nil {
			t.Fatalf("materialize %s task in the approved plan: %v", key, err)
		}
	}
	for _, item := range []struct {
		key, kind string
		content   map[string]any
	}{
		{key: "implement-a", kind: "implementation_handoff", content: map[string]any{"task_id": implementationTasks["implement-a"][1].String()}},
		{key: "implement-b", kind: "implementation_handoff", content: map[string]any{"task_id": implementationTasks["implement-b"][1].String()}},
		{key: "review-a", kind: "review", content: map[string]any{"task_id": implementationTasks["review-a"][1].String(), "result": map[string]any{"verdict": "approved", "summary": "Independent review passed.", "findings": []any{}}}},
		{key: "review-b", kind: "review", content: map[string]any{"task_id": implementationTasks["review-b"][1].String(), "result": map[string]any{"verdict": "approved", "summary": "Independent review passed.", "findings": []any{}}}},
	} {
		encoded, err := json.Marshal(item.content)
		if err != nil {
			t.Fatalf("encode %s artifact: %v", item.key, err)
		}
		if _, err := fixture.pool.Exec(ctx, `
			INSERT INTO autonomous_project_artifact (
				workspace_id,project_id,plan_id,node_id,artifact_type,name,content,
				producer_agent_id,artifact_revision,status,valid
			)
			SELECT $1,$2,$3,n.id,$4,$4,$5::jsonb,$6,n.spec_revision,'active',TRUE
			FROM autonomous_project_plan_node n
			WHERE n.plan_id=$3 AND n.project_id=$2 AND n.node_key=$7
		`, fixture.workspaceID, fixture.projectID, planID, item.kind, encoded,
			d2ParseUUID(t, agentID), item.key); err != nil {
			t.Fatalf("persist %s plan evidence: %v", item.key, err)
		}
	}
	for _, requirement := range spec.Requirements {
		if !scenarioPlanCoversRequirement(plan, requirement.ID, projectorchestration.NodeImplementation) ||
			!scenarioPlanCoversRequirement(plan, requirement.ID, projectorchestration.NodeReview) {
			t.Fatalf("requirement %s lacks implementation or independent review coverage", requirement.ID)
		}
	}
	tx, err := fixture.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin exact plan approval: %v", err)
	}
	approved, err := projectorchestration.ApprovePlanTx(ctx, tx, fixture.workspaceID, fixture.projectID, stored.PlanDigest, "e2e approver")
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("approve current plan digest: %v", err)
	}
	if !approved {
		_ = tx.Rollback(ctx)
		t.Fatal("exact current plan digest was not approved")
	}
	if _, err := tx.Exec(ctx, `
		UPDATE autonomous_project_control SET paused=TRUE, updated_at=now()
		WHERE workspace_id=$1 AND project_id=$2
	`, fixture.workspaceID, fixture.projectID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("pause background project workers for the isolated scenario: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit plan approval: %v", err)
	}

	// 3. Two independent local worktrees make conflicting commits; the fake
	// quality adapter records commit-bound passing gates without starting OCI.
	repo, worktreeA, worktreeB, baseSHA := newScenarioGitWorktrees(t)
	commitA := scenarioGit(t, worktreeA, "rev-parse", "HEAD")
	commitB := scenarioGit(t, worktreeB, "rev-parse", "HEAD")
	quality := &scenarioQualityGateRunner{}
	changeSetIDs := map[string]pgtype.UUID{}
	taskIDs := map[string]pgtype.UUID{}
	issueIDs := map[string]pgtype.UUID{}
	for i, item := range []struct {
		key, worktree, commit string
	}{{"implement-a", worktreeA, commitA}, {"implement-b", worktreeB, commitB}} {
		nodeID := nodeIDs[item.key]
		issueID, taskID := implementationTasks[item.key][0], implementationTasks[item.key][1]
		result, err := quality.Run(ctx, projectorchestration.QualityGateRequest{
			WorkspaceID: fixture.workspaceID, ProjectID: fixture.projectID, PlanID: planID,
			NodeID: nodeID, IssueID: issueID, TaskID: taskID, GateType: "unit_test",
			WorktreePath: item.worktree, RepoPath: repo, CommitSHA: item.commit,
		})
		if err != nil || !result.Passed || result.CommitSHA != item.commit {
			t.Fatalf("agent %d quality result = %+v, error = %v", i+1, result, err)
		}
		changeSetID, sourceAgentID := scenarioUUID(), d2ParseUUID(t, agentID)
		changeSetIDs[item.key] = changeSetID
		taskIDs[item.key] = taskID
		issueIDs[item.key] = issueID
		if _, err := fixture.pool.Exec(ctx, `
			INSERT INTO autonomous_project_change_set (
				id,workspace_id,project_id,plan_id,node_id,issue_id,source_task_id,source_agent_id,
				base_sha,branch_name,worktree_path,repo_path,commit_sha,integration_branch,merge_status,ready_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'main','ready',now())
		`, changeSetID, fixture.workspaceID, fixture.projectID, planID, nodeID, issueID, taskID,
			sourceAgentID, baseSHA, fmt.Sprintf("task/e2e-agent-%d", i+1), item.worktree, repo, item.commit); err != nil {
			t.Fatalf("persist agent %d change set: %v", i+1, err)
		}
		gateID := scenarioUUID()
		gateEvidence, _ := json.Marshal(map[string]any{
			"policy_requirement": true,
			"source_task_id":     taskID.String(),
			"runner_evidence": map[string]any{
				"task_id": taskID.String(), "commit_sha": item.commit,
				"worktree_path": item.worktree, "exit_code": 0,
			},
		})
		if _, err := fixture.pool.Exec(ctx, `
			INSERT INTO autonomous_project_quality_gate_run (
				id,workspace_id,project_id,plan_id,node_id,gate_type,status,required,evidence,
				executed_command,exit_code,worktree_path,commit_sha,runner_type,completed_at
			) VALUES ($1,$2,$3,$4,$5,'unit_test','passed',TRUE,$6::jsonb,'go test -v ./...',0,$7,$8,'daemon_oci',now())
		`, gateID, fixture.workspaceID, fixture.projectID, planID, nodeID, gateEvidence, item.worktree, item.commit); err != nil {
			t.Fatalf("persist agent %d passing quality evidence: %v", i+1, err)
		}
	}
	if len(quality.commits) != 2 || quality.commits[0] == quality.commits[1] {
		t.Fatalf("parallel quality commits = %v, want two distinct commits", quality.commits)
	}

	// 4. The real merge queue integrates agent A, then routes agent B's real Git
	// conflict through the resolver. Re-review and re-run the changed commit.
	candidates, err := store.ListMergeReviewCandidates(ctx, fixture.workspaceID, fixture.projectID)
	if err != nil || len(candidates) != 2 {
		t.Fatalf("reviewable change sets = %d, error = %v; want two", len(candidates), err)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].BranchName < candidates[j].BranchName })
	for _, candidate := range candidates {
		queued, err := store.EnqueueReviewedChangeSet(ctx, candidate, d2ParseUUID(t, candidate.ReviewTaskID))
		if err != nil || !queued {
			t.Fatalf("enqueue current review for %s = %v, error = %v", candidate.ChangeSetID.String(), queued, err)
		}
	}
	queue := projectorchestration.NewMergeQueue(store, quality).WithConflictResolver(&scenarioConflictResolver{
		t: t, pool: fixture.pool, repo: repo,
	})
	firstMerge, _, err := queue.ProcessNextWithConflictResolution(ctx, fixture.workspaceID, fixture.projectID)
	if err != nil || firstMerge.Status != "merged" {
		t.Fatalf("first merge queue result = %+v, error = %v", firstMerge, err)
	}
	firstMergeSHA := firstMerge.MergedSHA
	secondMerge, conflict, err := queue.ProcessNextWithConflictResolution(ctx, fixture.workspaceID, fixture.projectID)
	if err != nil || secondMerge.Status != "conflict" || conflict == nil || conflict.Status != "resolution_pending_review" {
		t.Fatalf("conflict queue result = %+v, outcome = %+v, error = %v", secondMerge, conflict, err)
	}
	mergedSHA := conflict.ConflictResolutionResult.ResolutionSHA
	parents := strings.Fields(scenarioGit(t, repo, "rev-list", "--parents", "-n", "1", mergedSHA))
	if len(parents) != 3 || parents[1] != firstMergeSHA || parents[2] != commitB {
		t.Fatalf("resolved merge parents = %v, want both conflict parents", parents)
	}
	scenarioGit(t, worktreeB, "reset", "--hard", mergedSHA)
	if _, err := fixture.pool.Exec(ctx, `
		UPDATE autonomous_project_change_set
		SET commit_sha=$2,merge_status='ready',ready_at=now(),merge_order=NULL,
		    approved_review_task_id=NULL,merge_evidence='{}'::jsonb
		WHERE id=$1
	`, changeSetIDs["implement-b"], mergedSHA); err != nil {
		t.Fatalf("return resolved changeset for independent review: %v", err)
	}
	newReviewTask := fixtures.Task(t, agentID, testutil.Cols{
		"issue_id": implementationTasks["review-b"][0].String(), "runtime_id": runtimeID, "status": "completed",
	})
	newReviewTaskID := d2ParseUUID(t, newReviewTask)
	fixtures.Cleanup(t, "DELETE FROM agent_task_queue WHERE id=$1", newReviewTaskID)
	insertScenarioReviewArtifact(t, fixture, planID, nodeIDs["review-b"], agentID, newReviewTaskID)
	insertScenarioUnitGate(t, fixture, planID, nodeIDs["implement-b"],
		issueIDs["implement-b"], taskIDs["implement-b"], worktreeB, mergedSHA)
	candidates, err = store.ListMergeReviewCandidates(ctx, fixture.workspaceID, fixture.projectID)
	if err != nil {
		t.Fatalf("list conflict-resolved review candidate: %v", err)
	}
	var resolvedCandidate *projectorchestration.MergeReviewCandidate
	for i := range candidates {
		if candidates[i].ChangeSetID == changeSetIDs["implement-b"] {
			resolvedCandidate = &candidates[i]
			break
		}
	}
	if resolvedCandidate == nil || resolvedCandidate.CommitSHA != mergedSHA || resolvedCandidate.ReviewTaskID != newReviewTask {
		t.Fatalf("re-review candidate = %+v, want resolved commit with fresh review", resolvedCandidate)
	}
	queued, err := store.EnqueueReviewedChangeSet(ctx, *resolvedCandidate, newReviewTaskID)
	if err != nil || !queued {
		t.Fatalf("enqueue re-reviewed conflict resolution = %v, error = %v", queued, err)
	}
	finalMerge, _, err := queue.ProcessNextWithConflictResolution(ctx, fixture.workspaceID, fixture.projectID)
	if err != nil || finalMerge.Status != "merged" {
		t.Fatalf("resolved merge queue result = %+v, error = %v", finalMerge, err)
	}
	finalCommitSHA := finalMerge.MergedSHA
	if _, err := fixture.pool.Exec(ctx, `
		UPDATE autonomous_project_plan_node SET status='completed',completed_at=now()
		WHERE plan_id=$1 AND node_key IN ('implement-a','review-a','implement-b','review-b')
	`, planID); err != nil {
		t.Fatalf("record implementation and review completion: %v", err)
	}

	// 5. The merge queue's immutable production manifest is promoted to staging,
	// then the existing staging smoke/health and exact production approval flow runs.
	manifest, err := projectorchestration.LoadPlanReleaseManifest(ctx, fixture.pool, fixture.workspaceID, fixture.projectID, planID)
	if err != nil {
		t.Fatalf("load merge queue release manifest: %v", err)
	}
	if manifest.CommitSHA != finalCommitSHA || manifest.ChangeSetID != changeSetIDs["implement-b"].String() {
		t.Fatalf("merge queue release manifest = %+v, want final merged change set commit", manifest)
	}
	fixture.manifest = manifest
	fixture.artifactDigest = manifest.ArtifactDigest
	artifactReader, err := quality.GetReader(ctx, manifest.ArtifactStorageKey)
	if err != nil {
		t.Fatalf("read queue-built release artifact: %v", err)
	}
	fixture.artifactStore.content, err = io.ReadAll(artifactReader)
	if closeErr := artifactReader.Close(); closeErr != nil {
		t.Fatalf("close release artifact reader: %v", closeErr)
	}
	if err != nil {
		t.Fatalf("read queue-built release artifact: %v", err)
	}
	stageManifest, err := projectorchestration.PinPlanReleaseManifestEnvironment(ctx, fixture.pool,
		fixture.workspaceID, fixture.projectID, planID, "staging")
	if err != nil {
		t.Fatalf("pin exact release for staging: %v", err)
	}
	if stageManifest.CommitSHA != finalCommitSHA || stageManifest.ArtifactDigest != manifest.ArtifactDigest {
		t.Fatalf("staging manifest lost merged content identity: %+v", stageManifest)
	}
	fixture.manifest = manifest
	cleanupSafetyEscalations(t, fixture)
	provider := newSafetyPromotionProvider(t, true)
	adapter := projectorchestration.NewWebhookDeploymentAdapter(projectorchestration.WebhookAdapterConfig{DeploymentURL: provider.server.URL})
	bridge := newSafetyBridge(t, fixture, adapter)
	runtime := safetyRuntime(fixture, bridge, true)
	setSafetyNodeReady(t, fixture)
	node := projectorchestration.ReadyNode{Key: "deploy", Kind: projectorchestration.NodeDeploy, Title: "Deploy approved release"}
	if err := runtime.executeDeploymentNode(ctx, fixture.workspaceID, fixture.projectID, node); err != nil {
		t.Fatalf("dispatch staging deployment and Playwright smoke: %v", err)
	}
	waitAndProjectLatestOperation(t, fixture, runtime, "staging")
	assertDeploymentManifest(t, fixture, "staging", stageManifest.ManifestID, stageManifest.ManifestDigest)
	if err := runtime.enforceProjectNodePolicy(ctx, fixture.workspaceID, fixture.projectID, node); err != errProjectApprovalRequired {
		t.Fatalf("production policy before manifest approval = %v, want approval required", err)
	}
	if err := runtime.openProjectEscalation(ctx, fixture.workspaceID, fixture.projectID, node, errProjectApprovalRequired); err != nil {
		t.Fatalf("open exact-manifest production approval: %v", err)
	}
	approveCurrentProductionManifest(t, fixture)
	if err := runtime.enforceProjectNodePolicy(ctx, fixture.workspaceID, fixture.projectID, node); err != nil {
		t.Fatalf("current production manifest approval was rejected: %v", err)
	}
	if err := runtime.executeDeploymentNode(ctx, fixture.workspaceID, fixture.projectID, node); err != nil {
		t.Fatalf("dispatch approved production release: %v", err)
	}
	waitAndProjectLatestOperation(t, fixture, runtime, "production")
	assertNodeState(t, fixture, "completed")
	provider.assertActions(t, []string{"deploy:staging", "staging_smoke_test", "deploy:production"})

	// 6. The accepted plan retains every requirement in the implementation and
	// review graph and the final deployment is bound to the merged artifact.
	finalManifest, err := projectorchestration.LoadPlanReleaseManifest(ctx, fixture.pool, fixture.workspaceID, fixture.projectID, planID)
	if err != nil {
		t.Fatalf("load final production manifest: %v", err)
	}
	if finalManifest.Environment != "production" || finalManifest.CommitSHA != finalCommitSHA || finalManifest.ArtifactDigest != manifest.ArtifactDigest {
		t.Fatalf("final manifest = %+v, want production for merged commit/artifact", finalManifest)
	}
}

func repeatScenarioTokens(count int) []string {
	tokens := make([]string, count)
	for i := range tokens {
		tokens[i] = fmt.Sprintf("requirement-detail-%03d", i)
	}
	return tokens
}

func scenarioPlanCoversRequirement(plan projectorchestration.Plan, requirementID string, kind projectorchestration.NodeKind) bool {
	for _, node := range plan.Nodes {
		if node.Kind != kind {
			continue
		}
		for _, id := range node.RequirementIDs {
			if id == requirementID {
				return true
			}
		}
	}
	return false
}

func scenarioPlanNodeID(t *testing.T, fixture *autonomousDeploymentBridgeFixture, planID pgtype.UUID, key string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT id FROM autonomous_project_plan_node WHERE plan_id=$1 AND node_key=$2`, planID, key).Scan(&id); err != nil {
		t.Fatalf("load %s node identity: %v", key, err)
	}
	return id
}

func scenarioUUID() pgtype.UUID {
	return pgtype.UUID{Bytes: [16]byte(uuid.New()), Valid: true}
}

func newScenarioGitWorktrees(t *testing.T) (repo, worktreeA, worktreeB, baseSHA string) {
	t.Helper()
	root := t.TempDir()
	repo = filepath.Join(root, "repo")
	worktreeA = filepath.Join(root, "agent-a")
	worktreeB = filepath.Join(root, "agent-b")
	scenarioGit(t, root, "init", repo)
	scenarioGit(t, repo, "config", "user.name", "Multica E2E")
	scenarioGit(t, repo, "config", "user.email", "e2e@example.invalid")
	scenarioGit(t, repo, "branch", "-M", "main")
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module scenario\n\ngo 1.23\n"), 0o600); err != nil {
		t.Fatalf("write scenario module: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "scenario.go"), []byte("package scenario\n\nconst projectView = \"base\"\n"), 0o600); err != nil {
		t.Fatalf("write scenario base: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "cmd"), 0o700); err != nil {
		t.Fatalf("create release package: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "cmd", "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatalf("write release package: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "dist"), 0o700); err != nil {
		t.Fatalf("create release output directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "dist", ".keep"), nil, 0o600); err != nil {
		t.Fatalf("write release output directory marker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "multica.release.json"), []byte(`{"version":1,"builder":"go_binary","package":"./cmd","outputs":["dist/app"]}`), 0o600); err != nil {
		t.Fatalf("write committed release contract: %v", err)
	}
	scenarioGit(t, repo, "add", "go.mod", "scenario.go", "cmd/main.go", "dist/.keep", "multica.release.json")
	scenarioGit(t, repo, "commit", "-m", "Create autonomous project base")
	baseSHA = scenarioGit(t, repo, "rev-parse", "HEAD")
	scenarioGit(t, repo, "worktree", "add", "-b", "task/e2e-agent-1", worktreeA, baseSHA)
	scenarioGit(t, repo, "worktree", "add", "-b", "task/e2e-agent-2", worktreeB, baseSHA)
	for _, item := range []struct{ path, value string }{{worktreeA, "agent-a"}, {worktreeB, "agent-b"}} {
		if err := os.WriteFile(filepath.Join(item.path, "scenario.go"), []byte("package scenario\n\nconst projectView = \""+item.value+"\"\n"), 0o600); err != nil {
			t.Fatalf("write parallel agent change: %v", err)
		}
		scenarioGit(t, item.path, "add", "scenario.go")
		scenarioGit(t, item.path, "commit", "-m", "Implement "+item.value)
	}
	return repo, worktreeA, worktreeB, baseSHA
}

func scenarioGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, output)
	}
	return strings.TrimSpace(string(output))
}

type scenarioQualityGateRunner struct {
	mu        sync.Mutex
	commits   []string
	artifacts map[string][]byte
}

func (r *scenarioQualityGateRunner) Run(_ context.Context, request projectorchestration.QualityGateRequest) (projectorchestration.QualityGateResult, error) {
	r.mu.Lock()
	r.commits = append(r.commits, request.CommitSHA)
	r.mu.Unlock()
	exitCode := 0
	command := "go test -v ./..."
	image := "golang:1.27.3-alpine"
	if request.ReleaseContract != nil {
		command = "go build -trimpath -buildvcs=false -o " + request.ReleaseContract.Outputs[0] + " " + request.ReleaseContract.Package
	} else if request.GateType == "build" || request.GateType == "build_verification" {
		command = "go build ./..."
	}
	evidence := map[string]any{
		"task_id": d2UUIDText(request.TaskID), "gate_type": request.GateType,
		"worktree_path": request.WorktreePath, "repo_path": request.RepoPath,
		"commit_sha": request.CommitSHA, "executed_command": command,
		"runner_type": "daemon_oci", "exit_code": 0, "stdout": "ok", "stderr": "",
		"duration_ms": 1, "test_count": 1, "failed_count": 0, "output_truncated": false,
		"container_image": image, "container_image_id": "sha256:" + strings.Repeat("a", 64),
		"source_snapshot_digest": scenarioSHA256(request.CommitSHA),
		"source_snapshot":        "git_archive_of_recorded_commit",
		"network_disabled":       true, "host_environment_inherited": false, "task_worktree_mounted": false,
		"resource_limits": map[string]string{"memory": "2g", "cpus": "2", "pids": "128", "tmpfs": "2g"},
	}
	result := projectorchestration.QualityGateResult{
		Passed: true, TaskID: d2UUIDText(request.TaskID), GateType: request.GateType,
		WorktreePath: request.WorktreePath, RepoPath: request.RepoPath, CommitSHA: request.CommitSHA,
		RunnerType: "daemon_oci", ExecutedCommand: command, ExitCode: &exitCode,
		Evidence: evidence, DurationMs: 1, Stdout: "ok", TestCount: 1,
	}
	result.ArtifactDigest = scenarioQualityDigest(result)
	evidence["artifact_digest"] = result.ArtifactDigest
	return result, nil
}

func (r *scenarioQualityGateRunner) BuildReleaseArtifact(ctx context.Context, request projectorchestration.QualityGateRequest, contract projectorchestration.ReleaseContract) (projectorchestration.ReleaseArtifact, projectorchestration.QualityGateResult, error) {
	request.GateType = "build"
	request.ReleaseContract = &contract
	result, err := r.Run(ctx, request)
	if err != nil {
		return projectorchestration.ReleaseArtifact{}, result, err
	}
	content := []byte("artifact for " + request.CommitSHA)
	digest := scenarioSHA256(string(content))
	result.BuildArtifact = content
	result.BuildArtifactDigest = digest
	result.BuildArtifactFileCount = 1
	return projectorchestration.ReleaseArtifact{
		Bytes: content, Digest: digest, Builder: contract.Builder,
		Outputs: append([]string(nil), contract.Outputs...), FileCount: 1, Size: int64(len(content)),
	}, result, nil
}

func (r *scenarioQualityGateRunner) Upload(_ context.Context, key string, data []byte, _ string, _ string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.artifacts == nil {
		r.artifacts = make(map[string][]byte)
	}
	r.artifacts[key] = append([]byte(nil), data...)
	return key, nil
}

func (r *scenarioQualityGateRunner) GetReader(_ context.Context, key string) (io.ReadCloser, error) {
	r.mu.Lock()
	content, ok := r.artifacts[key]
	r.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("release artifact %q was not uploaded", key)
	}
	return io.NopCloser(bytes.NewReader(content)), nil
}

func (r *scenarioQualityGateRunner) PresignGet(_ context.Context, key string, _ time.Duration) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.artifacts[key]; !ok {
		return "", fmt.Errorf("release artifact %q was not uploaded", key)
	}
	return "https://artifact.example.invalid/" + key, nil
}

func scenarioSHA256(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func scenarioQualityDigest(result projectorchestration.QualityGateResult) string {
	exitCode := *result.ExitCode
	evidence := result.Evidence
	limits, _ := evidence["resource_limits"].(map[string]string)
	image, _ := evidence["container_image"].(string)
	imageID, _ := evidence["container_image_id"].(string)
	snapshotDigest, _ := evidence["source_snapshot_digest"].(string)
	sourceSnapshot, _ := evidence["source_snapshot"].(string)
	packageManagerVersion, _ := evidence["package_manager_version"].(string)
	networkDisabled, _ := evidence["network_disabled"].(bool)
	hostEnvironmentInherited, _ := evidence["host_environment_inherited"].(bool)
	taskWorktreeMounted, _ := evidence["task_worktree_mounted"].(bool)
	data, _ := json.Marshal(struct {
		Passed                   bool   `json:"passed"`
		RunnerType               string `json:"runner_type"`
		TaskID                   string `json:"task_id"`
		GateType                 string `json:"gate_type"`
		Command                  string `json:"command"`
		ContainerImage           string `json:"container_image"`
		ContainerImageID         string `json:"container_image_id"`
		SnapshotDigest           string `json:"source_snapshot_digest"`
		SourceSnapshot           string `json:"source_snapshot"`
		PackageManagerVersion    string `json:"package_manager_version"`
		NetworkDisabled          bool   `json:"network_disabled"`
		HostEnvironmentInherited bool   `json:"host_environment_inherited"`
		TaskWorktreeMounted      bool   `json:"task_worktree_mounted"`
		MemoryLimit              string `json:"memory_limit"`
		CPULimit                 string `json:"cpu_limit"`
		PIDLimit                 string `json:"pid_limit"`
		TmpfsLimit               string `json:"tmpfs_limit"`
		ExitCode                 *int   `json:"exit_code"`
		WorktreePath             string `json:"worktree_path"`
		RepoPath                 string `json:"repo_path"`
		CommitSHA                string `json:"commit_sha"`
		DurationMs               int64  `json:"duration_ms"`
		Stdout                   string `json:"stdout"`
		Stderr                   string `json:"stderr"`
		OutputTruncated          bool   `json:"output_truncated"`
		TestCount                int    `json:"test_count"`
		FailedCount              int    `json:"failed_count"`
		Error                    string `json:"error"`
	}{
		result.Passed, result.RunnerType, result.TaskID, result.GateType, result.ExecutedCommand,
		image, imageID, snapshotDigest, sourceSnapshot, packageManagerVersion, networkDisabled,
		hostEnvironmentInherited, taskWorktreeMounted, limits["memory"], limits["cpus"],
		limits["pids"], limits["tmpfs"], &exitCode, result.WorktreePath, result.RepoPath, result.CommitSHA,
		result.DurationMs, result.Stdout, result.Stderr, result.OutputTruncated, result.TestCount,
		result.FailedCount, result.Error,
	})
	return scenarioSHA256(string(data))
}

type scenarioConflictResolver struct {
	t    *testing.T
	pool *pgxpool.Pool
	repo string
}

func (r *scenarioConflictResolver) ResolveConflict(ctx context.Context, changeSetID uuid.UUID) (projectorchestration.ConflictResolutionResult, error) {
	id := pgtype.UUID{Bytes: [16]byte(changeSetID), Valid: true}
	var sourceSHA, integrationBranch string
	if err := r.pool.QueryRow(ctx, `
		SELECT commit_sha,integration_branch FROM autonomous_project_change_set WHERE id=$1
	`, id).Scan(&sourceSHA, &integrationBranch); err != nil {
		return projectorchestration.ConflictResolutionResult{}, err
	}
	baseSHA := scenarioGit(r.t, r.repo, "rev-parse", "refs/heads/"+integrationBranch)
	worktree, err := os.MkdirTemp(r.t.TempDir(), "conflict-resolution-")
	if err != nil {
		return projectorchestration.ConflictResolutionResult{}, err
	}
	if _, err := exec.CommandContext(ctx, "git", "-C", r.repo, "worktree", "add", "--detach", worktree, baseSHA).CombinedOutput(); err != nil {
		return projectorchestration.ConflictResolutionResult{}, fmt.Errorf("create conflict resolver worktree: %w", err)
	}
	merge := exec.CommandContext(ctx, "git", "-C", worktree, "merge", "--no-commit", "--no-ff", sourceSHA)
	if output, err := merge.CombinedOutput(); err == nil {
		return projectorchestration.ConflictResolutionResult{}, fmt.Errorf("expected conflicting merge, got: %s", output)
	}
	if err := os.WriteFile(filepath.Join(worktree, "scenario.go"), []byte("package scenario\n\nconst projectView = \"agent-a + agent-b\"\n"), 0o600); err != nil {
		return projectorchestration.ConflictResolutionResult{}, err
	}
	if _, err := exec.CommandContext(ctx, "git", "-C", worktree, "add", "scenario.go").CombinedOutput(); err != nil {
		return projectorchestration.ConflictResolutionResult{}, fmt.Errorf("stage resolved conflict: %w", err)
	}
	if _, err := exec.CommandContext(ctx, "git", "-C", worktree, "commit", "-m", "Resolve parallel project changes").CombinedOutput(); err != nil {
		return projectorchestration.ConflictResolutionResult{}, fmt.Errorf("commit resolved conflict: %w", err)
	}
	resolutionSHA := scenarioGit(r.t, worktree, "rev-parse", "HEAD")
	if _, err := exec.CommandContext(ctx, "git", "-C", r.repo, "worktree", "remove", "--force", worktree).CombinedOutput(); err != nil {
		return projectorchestration.ConflictResolutionResult{}, fmt.Errorf("remove conflict resolver worktree: %w", err)
	}
	return projectorchestration.ConflictResolutionResult{
		Resolved: true, ResolutionSHA: resolutionSHA,
		Explanation: "Combined both independent changes and preserved the tested behavior.",
	}, nil
}

func insertScenarioReviewArtifact(t *testing.T, fixture *autonomousDeploymentBridgeFixture, planID, nodeID pgtype.UUID, agentID string, taskID pgtype.UUID) {
	t.Helper()
	content, err := json.Marshal(map[string]any{
		"task_id": taskID.String(),
		"result":  map[string]any{"verdict": "approved", "findings": []any{}, "summary": "Conflict resolution reviewed."},
	})
	if err != nil {
		t.Fatalf("encode independent review artifact: %v", err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `
		INSERT INTO autonomous_project_artifact (
			workspace_id,project_id,plan_id,node_id,artifact_type,name,content,
			producer_agent_id,artifact_revision,status,valid,created_at
		)
		SELECT $1,$2,$3,n.id,'review','review',$4::jsonb,$5,n.spec_revision,'active',TRUE,
		       GREATEST(now(), COALESCE((
			       SELECT max(previous.created_at) + interval '1 microsecond'
			       FROM autonomous_project_artifact previous
			       WHERE previous.plan_id=$3 AND previous.node_id=n.id AND previous.artifact_type='review'
		       ), now()))
		FROM autonomous_project_plan_node n WHERE n.id=$6 AND n.plan_id=$3
	`, fixture.workspaceID, fixture.projectID, planID, content, d2ParseUUID(t, agentID), nodeID); err != nil {
		t.Fatalf("persist independent review artifact: %v", err)
	}
}

func insertScenarioUnitGate(t *testing.T, fixture *autonomousDeploymentBridgeFixture, planID, nodeID, issueID, taskID pgtype.UUID, worktree, commitSHA string) {
	t.Helper()
	evidence, err := json.Marshal(map[string]any{
		"policy_requirement": true,
		"source_task_id":     taskID.String(),
		"runner_evidence": map[string]any{
			"task_id": taskID.String(), "commit_sha": commitSHA,
			"worktree_path": worktree, "exit_code": 0,
		},
	})
	if err != nil {
		t.Fatalf("encode current unit gate evidence: %v", err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `
		INSERT INTO autonomous_project_quality_gate_run (
			workspace_id,project_id,plan_id,node_id,gate_type,status,required,evidence,
			executed_command,exit_code,worktree_path,commit_sha,runner_type,completed_at
		) VALUES ($1,$2,$3,$4,'unit_test','passed',TRUE,$5::jsonb,'go test -v ./...',0,$6,$7,'daemon_oci',now())
	`, fixture.workspaceID, fixture.projectID, planID, nodeID, evidence, worktree, commitSHA); err != nil {
		t.Fatalf("persist current unit gate evidence: %v", err)
	}
}
