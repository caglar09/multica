package handler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/projectorchestration"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestMergeQueueRequiresCurrentLinkedReviewAndUnitEvidence(t *testing.T) {
	if testPool == nil || testHandler == nil {
		t.Skip("database not available")
	}
	project := createProjectPermissionTestProject(t, "autonomous merge queue evidence project")
	ctx := context.Background()
	workspaceID, projectID := parseUUID(testWorkspaceID), parseUUID(project.ID)
	store := projectorchestration.NewStore(testPool)
	var specificationRevisionID pgtype.UUID
	if err := testPool.QueryRow(ctx, `
		INSERT INTO autonomous_project_specification_revision (
			workspace_id, project_id, revision, source_kind, goal, specification
		) VALUES ($1, $2, 1, 'planner', 'Merge reviewed change', '{}'::jsonb)
		RETURNING id
	`, workspaceID, projectID).Scan(&specificationRevisionID); err != nil {
		t.Fatalf("insert specification revision: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		if _, err := testPool.Exec(cleanupCtx, `DELETE FROM autonomous_project_change_set WHERE workspace_id=$1 AND project_id=$2`, workspaceID, projectID); err != nil {
			t.Errorf("clean project change sets: %v", err)
		}
		if err := store.CleanupProject(cleanupCtx, workspaceID, projectID); err != nil {
			t.Errorf("clean project orchestration rows: %v", err)
		}
		for _, statement := range []string{
			`DELETE FROM autonomous_project_specification_head WHERE workspace_id=$1 AND project_id=$2`,
			`DELETE FROM autonomous_project_specification_revision WHERE workspace_id=$1 AND project_id=$2`,
			`DELETE FROM autonomous_project_control WHERE workspace_id=$1 AND project_id=$2`,
		} {
			if _, err := testPool.Exec(cleanupCtx, statement, workspaceID, projectID); err != nil {
				t.Errorf("clean specification and control rows: %v", err)
			}
		}
	})

	plan, err := store.PersistPlan(ctx, workspaceID, projectID, "merge-queue-test", "test-planner", "",
		projectorchestration.Plan{
			Version: projectorchestration.CurrentPlanVersion,
			Goal:    "Merge reviewed change",
			Specification: projectorchestration.Specification{
				Summary:          "Implement and review one change.",
				Requirements:     []string{"Implement the reviewed change."},
				DefinitionOfDone: []string{"Implementation is independently reviewed."},
				RequirementDetails: []projectorchestration.SpecificationRequirement{{
					ID: "REQ-MERGE-1", Text: "Implement the reviewed change.",
				}},
			},
			SpecificationRevisionID: specificationRevisionID.String(),
			Policy:                  projectorchestration.DefaultPolicy(),
			Nodes: []projectorchestration.NodeSpec{
				{Key: "arch", Kind: projectorchestration.NodeArchitecture, Title: "Architecture", Risk: projectorchestration.RiskLow, MaxAttempts: 2},
				{Key: "implement", Kind: projectorchestration.NodeImplementation, Title: "Implementation", RequirementIDs: []string{"REQ-MERGE-1"}, AcceptanceCriteria: []string{"Implement the reviewed change."}, Risk: projectorchestration.RiskLow, MaxAttempts: 2},
				{Key: "review", Kind: projectorchestration.NodeReview, Title: "Independent review", RequirementIDs: []string{"REQ-MERGE-1"}, AcceptanceCriteria: []string{"Independently review the change."}, Risk: projectorchestration.RiskLow, MaxAttempts: 2},
			},
			Edges: []projectorchestration.EdgeSpec{
				{From: "arch", To: "implement", Type: projectorchestration.DependencyHard},
				{From: "implement", To: "review", Type: projectorchestration.DependencyHard},
			},
		})
	if err != nil {
		t.Fatalf("persist merge queue plan: %v", err)
	}
	runtimeID := dbfx.Runtime(t, "merge queue evidence runtime")
	agentID := dbfx.Agent(t, "merge queue evidence agent", runtimeID)
	implementationIssueID := dbfx.Issue(t, "Merge queue implementation", testutil.Cols{"project_id": project.ID})
	reviewIssueID := dbfx.Issue(t, "Merge queue review", testutil.Cols{"project_id": project.ID})
	implementationTaskID := dbfx.Task(t, agentID, testutil.Cols{"issue_id": implementationIssueID, "runtime_id": runtimeID, "status": "completed"})
	reviewTaskID := dbfx.Task(t, agentID, testutil.Cols{"issue_id": reviewIssueID, "runtime_id": runtimeID, "status": "completed"})
	implementationIssue, reviewIssue := parseUUID(implementationIssueID), parseUUID(reviewIssueID)
	if _, err := testPool.Exec(ctx, `
		UPDATE autonomous_project_plan_node
		SET materialized_issue_id=CASE node_key WHEN 'implement' THEN $3::uuid ELSE $4::uuid END,
		    status='completed'
		WHERE plan_id=$1 AND project_id=$2 AND node_key IN ('implement','review')
	`, parseUUID(plan.ID), projectID, implementationIssue, reviewIssue); err != nil {
		t.Fatalf("bind completed implementation and review nodes: %v", err)
	}
	commitSHA, worktreePath, repoPath := strings.Repeat("a", 40), "/tmp/task-worktree", "/tmp/repo"
	insertArtifact := func(nodeKey, artifactType string, content map[string]any) pgtype.UUID {
		t.Helper()
		encoded, err := json.Marshal(content)
		if err != nil {
			t.Fatal(err)
		}
		var artifactID pgtype.UUID
		if err := testPool.QueryRow(ctx, `
			INSERT INTO autonomous_project_artifact (
				workspace_id, project_id, plan_id, node_id, artifact_type, name,
				content, producer_agent_id, artifact_revision, status, valid
			)
			SELECT $1, $2, $3, n.id, $4, $5, $6::jsonb, $7, n.spec_revision, 'active', TRUE
			FROM autonomous_project_plan_node n
			WHERE n.plan_id=$3 AND n.project_id=$2 AND n.node_key=$8
			RETURNING id
		`, workspaceID, projectID, parseUUID(plan.ID), artifactType, artifactType, encoded,
			parseUUID(agentID), nodeKey).Scan(&artifactID); err != nil {
			t.Fatalf("insert %s artifact: %v", artifactType, err)
		}
		return artifactID
	}
	insertArtifact("implement", "implementation_handoff", map[string]any{"task_id": implementationTaskID})
	reviewResult := map[string]any{"verdict": "approved", "findings": []any{}, "summary": "No blocking findings."}
	reviewArtifactID := insertArtifact("review", "review", map[string]any{
		"task_id": reviewTaskID, "result": reviewResult,
	})
	unitEvidence, err := json.Marshal(map[string]any{
		"policy_requirement": true,
		"source_task_id":     implementationTaskID,
		"runner_evidence": map[string]any{
			"task_id": implementationTaskID, "commit_sha": commitSHA,
			"worktree_path": worktreePath, "exit_code": 0,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO autonomous_project_quality_gate_run (
			workspace_id, project_id, plan_id, node_id, gate_type, status, required,
			evidence, executed_command, exit_code, worktree_path, commit_sha, runner_type
		)
		SELECT $1, $2, $3, n.id, 'unit_test', 'passed', TRUE, $4::jsonb,
		       'go test ./...', 0, $5, $6, 'daemon_oci'
		FROM autonomous_project_plan_node n
		WHERE n.plan_id=$3 AND n.project_id=$2 AND n.node_key='implement'
	`, workspaceID, projectID, parseUUID(plan.ID), unitEvidence, worktreePath, commitSHA); err != nil {
		t.Fatalf("insert current deterministic unit evidence: %v", err)
	}
	changeSetID := pgtype.UUID{Valid: true, Bytes: [16]byte{11}}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO autonomous_project_change_set (
			id, workspace_id, project_id, plan_id, node_id, issue_id, source_task_id,
			source_agent_id, base_sha, branch_name, worktree_path, repo_path,
			commit_sha, merge_status, ready_at
		) SELECT $1, $2, $3, $4, n.id, $5, $6, $7, $8, 'task/merge-queue-test',
		         $9, $10, $11, 'ready', now()
		  FROM autonomous_project_plan_node n
		 WHERE n.plan_id=$4 AND n.project_id=$3 AND n.node_key='implement'
	`, changeSetID, workspaceID, projectID, parseUUID(plan.ID), implementationIssue,
		parseUUID(implementationTaskID), parseUUID(agentID), strings.Repeat("b", 40), worktreePath, repoPath, commitSHA); err != nil {
		t.Fatalf("insert reviewed change set: %v", err)
	}

	candidates, err := store.ListMergeReviewCandidates(ctx, workspaceID, projectID)
	if err != nil {
		t.Fatalf("list eligible merge candidates: %v", err)
	}
	if len(candidates) != 1 || candidates[0].ChangeSetID != changeSetID || candidates[0].ReviewArtifactID != reviewArtifactID {
		t.Fatalf("merge candidates = %+v, want the implementation linked to its completed review node", candidates)
	}
	candidate := candidates[0]
	rejectedReviewResult := map[string]any{
		"verdict": "changes_requested", "summary": "Blocking finding.",
		"findings": []any{map[string]any{"blocking": true}},
	}
	rejectedReviewJSON, err := json.Marshal(rejectedReviewResult)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE autonomous_project_artifact
		SET content=jsonb_set(content, '{result}', $2::jsonb)
		WHERE id=$1
	`, reviewArtifactID, rejectedReviewJSON); err != nil {
		t.Fatalf("replace review with a newer requested change: %v", err)
	}
	rejectedCandidates, err := store.ListMergeReviewCandidates(ctx, workspaceID, projectID)
	if err != nil {
		t.Fatalf("list candidates after review changes: %v", err)
	}
	if len(rejectedCandidates) != 0 {
		t.Fatalf("non-approved review candidates = %+v, want none", rejectedCandidates)
	}
	candidate.ReviewResult = rejectedReviewJSON
	queued, err := store.EnqueueReviewedChangeSet(ctx, candidate, parseUUID(reviewTaskID))
	if err != nil || queued {
		t.Fatalf("enqueue changes-requested review = %v, error = %v", queued, err)
	}
	approvedReviewJSON, err := json.Marshal(reviewResult)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE autonomous_project_artifact
		SET content=jsonb_set(content, '{result}', $2::jsonb)
		WHERE id=$1
	`, reviewArtifactID, approvedReviewJSON); err != nil {
		t.Fatalf("restore approved review artifact: %v", err)
	}
	queued, err = store.EnqueueReviewedChangeSet(ctx, candidates[0], parseUUID(reviewTaskID))
	if err != nil || !queued {
		t.Fatalf("enqueue explicitly approved current review = %v, error = %v", queued, err)
	}
	var mergeStatus string
	var approvedReviewTaskID pgtype.UUID
	var mergeEvidence []byte
	if err := testPool.QueryRow(ctx, `
		SELECT merge_status, approved_review_task_id, merge_evidence
		FROM autonomous_project_change_set WHERE id=$1
	`, changeSetID).Scan(&mergeStatus, &approvedReviewTaskID, &mergeEvidence); err != nil {
		t.Fatalf("read queued change set: %v", err)
	}
	var evidence map[string]any
	if err := json.Unmarshal(mergeEvidence, &evidence); err != nil {
		t.Fatalf("decode merge evidence: %v", err)
	}
	if mergeStatus != "queued" || approvedReviewTaskID.String() != reviewTaskID || evidence["approval_verdict"] != "approved" {
		t.Fatalf("queued change set status=%q review_task=%q evidence=%v", mergeStatus, approvedReviewTaskID.String(), evidence)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE autonomous_project_change_set
		SET merge_status='merging', merge_attempt=100,
		    merge_evidence=merge_evidence || '{"phase":"verifying"}'::jsonb
		WHERE id=$1
	`, changeSetID); err != nil {
		t.Fatalf("seed exhausted interrupted merge: %v", err)
	}
	result, err := projectorchestration.NewMergeQueue(store, projectorchestration.NewDaemonQualityGateRunner()).ProcessNext(ctx, workspaceID, projectID)
	if err != nil || result.Status != "integration_failed" {
		t.Fatalf("exhausted merge result=%+v, error=%v; want visible integration_failed", result, err)
	}
	var exhaustedStatus, lastError string
	var exhaustedEvidence []byte
	if err := testPool.QueryRow(ctx, `
		SELECT merge_status, last_error, merge_evidence
		FROM autonomous_project_change_set WHERE id=$1
	`, changeSetID).Scan(&exhaustedStatus, &lastError, &exhaustedEvidence); err != nil {
		t.Fatalf("read exhausted merge state: %v", err)
	}
	var terminalEvidence map[string]any
	if err := json.Unmarshal(exhaustedEvidence, &terminalEvidence); err != nil {
		t.Fatalf("decode exhausted merge evidence: %v", err)
	}
	if exhaustedStatus != "integration_failed" || !strings.Contains(lastError, "retry limit exhausted") || terminalEvidence["phase"] != "integration_failed" {
		t.Fatalf("exhausted merge status=%q last_error=%q evidence=%v", exhaustedStatus, lastError, terminalEvidence)
	}
}
