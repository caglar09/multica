package projectorchestration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/util"
)

var ErrMergeQueueEmpty = errors.New("project merge queue is empty")

type MergeReviewCandidate struct {
	ChangeSetID          pgtype.UUID
	WorkspaceID          pgtype.UUID
	ProjectID            pgtype.UUID
	PlanID               pgtype.UUID
	NodeID               pgtype.UUID
	IssueID              pgtype.UUID
	SourceTaskID         pgtype.UUID
	ApprovedReviewTaskID pgtype.UUID
	BaseSHA              string
	BranchName           string
	WorktreePath         string
	RepoPath             string
	CommitSHA            string
	SpecRevision         int64
	ReviewSpecRevision   int64
	ReviewArtifactID     pgtype.UUID
	ReviewTaskID         string
	ReviewResult         json.RawMessage
}

type MergeQueueClaim struct {
	MergeReviewCandidate
	MergeOrder        int64
	MergeAttempt      int32
	IntegrationBranch string
	MergeEvidence     json.RawMessage
}

type MergeResult struct {
	ChangeSetID  pgtype.UUID `json:"change_set_id"`
	MergedSHA    string      `json:"merged_sha,omitempty"`
	Status       string      `json:"status"`
	ErrorDetails string      `json:"error_details,omitempty"`
}

func (s *Store) ListMergeReviewCandidates(ctx context.Context, workspaceID, projectID pgtype.UUID) ([]MergeReviewCandidate, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("project orchestration store is not configured")
	}
	if !workspaceID.Valid || !projectID.Valid {
		return nil, errors.New("workspace_id and project_id are required")
	}
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.workspace_id, c.project_id, c.plan_id, c.node_id, c.issue_id,
		       c.source_task_id, c.base_sha, c.branch_name, COALESCE(c.worktree_path,''),
		       COALESCE(c.repo_path,''), c.commit_sha, n.spec_revision,
	       review_artifact.id, review_artifact.content ->> 'task_id', review_node.spec_revision,
	       review_artifact.content -> 'result'
		FROM autonomous_project_change_set c
		JOIN autonomous_project_plan_node n
		  ON n.id=c.node_id AND n.plan_id=c.plan_id AND n.workspace_id=c.workspace_id
		 AND n.project_id=c.project_id AND n.kind IN ('implementation','migration','integration')
		JOIN autonomous_project_plan p
		  ON p.id=c.plan_id AND p.workspace_id=c.workspace_id AND p.project_id=c.project_id
		 AND p.status IN ('active','blocked')
		JOIN autonomous_project_artifact implementation_artifact
		  ON implementation_artifact.workspace_id=c.workspace_id
		 AND implementation_artifact.project_id=c.project_id
		 AND implementation_artifact.plan_id=c.plan_id AND implementation_artifact.node_id=c.node_id
		 AND implementation_artifact.artifact_type='implementation_handoff'
		 AND implementation_artifact.content ->> 'task_id'=c.source_task_id::text
		 AND implementation_artifact.status='active' AND implementation_artifact.valid=TRUE
		 AND implementation_artifact.artifact_revision=n.spec_revision
		JOIN agent_task_queue implementation_task
		  ON implementation_task.id=c.source_task_id AND implementation_task.issue_id=c.issue_id
		 AND implementation_task.status='completed'
		JOIN autonomous_project_plan_edge review_edge
		  ON review_edge.plan_id=c.plan_id AND review_edge.workspace_id=c.workspace_id
		 AND review_edge.project_id=c.project_id AND review_edge.from_node_key=n.node_key
		JOIN autonomous_project_plan_node review_node
		  ON review_node.plan_id=review_edge.plan_id AND review_node.workspace_id=review_edge.workspace_id
		 AND review_node.project_id=review_edge.project_id AND review_node.node_key=review_edge.to_node_key
		 AND review_node.kind='review' AND review_node.status='completed'
		 AND review_node.materialized_issue_id IS NOT NULL
		JOIN autonomous_project_artifact review_artifact
		  ON review_artifact.workspace_id=c.workspace_id AND review_artifact.project_id=c.project_id
		 AND review_artifact.plan_id=c.plan_id AND review_artifact.node_id=review_node.id
		 AND review_artifact.artifact_type='review' AND review_artifact.status='active'
		 AND review_artifact.valid=TRUE AND review_artifact.artifact_revision=review_node.spec_revision
		 AND review_artifact.id=(
			SELECT latest.id FROM autonomous_project_artifact latest
			WHERE latest.plan_id=c.plan_id AND latest.node_id=review_node.id
			  AND latest.artifact_type='review'
			ORDER BY latest.created_at DESC, latest.id DESC LIMIT 1
		 )
		 AND review_artifact.content #>> '{result,verdict}'='approved'
		JOIN agent_task_queue review_task
		  ON review_task.id::text=review_artifact.content ->> 'task_id'
		 AND review_task.issue_id=review_node.materialized_issue_id
		 AND review_task.status='completed' AND review_task.id<>c.source_task_id
		JOIN LATERAL (
			SELECT q.status
			FROM autonomous_project_quality_gate_run q
			WHERE q.workspace_id=c.workspace_id AND q.project_id=c.project_id
			  AND q.plan_id=c.plan_id AND q.node_id=c.node_id
			  AND q.gate_type='unit_test' AND q.required=TRUE
			  AND q.evidence ->> 'policy_requirement'='true'
			  AND q.evidence ->> 'source_task_id'=c.source_task_id::text
			  AND q.evidence #>> '{runner_evidence,task_id}'=c.source_task_id::text
			  AND q.evidence #>> '{runner_evidence,commit_sha}'=c.commit_sha
			  AND q.evidence #>> '{runner_evidence,worktree_path}'=c.worktree_path
			  AND q.evidence #>> '{runner_evidence,exit_code}'='0'
			  AND q.status='passed' AND q.commit_sha=c.commit_sha
			  AND q.worktree_path=c.worktree_path AND q.exit_code=0
			  AND q.runner_type IN ('daemon_oci','webhook_oci')
			  AND NULLIF(q.executed_command,'') IS NOT NULL
			ORDER BY q.updated_at DESC, q.id DESC
			LIMIT 1
		) unit_test ON unit_test.status='passed'
		WHERE c.workspace_id=$1 AND c.project_id=$2 AND c.merge_status='ready'
		  AND c.ready_at IS NOT NULL
		ORDER BY c.ready_at ASC, c.created_at ASC, c.id ASC
	`, workspaceID, projectID)
	if err != nil {
		return nil, fmt.Errorf("list reviewed project change sets: %w", err)
	}
	defer rows.Close()

	candidates := make([]MergeReviewCandidate, 0)
	for rows.Next() {
		var candidate MergeReviewCandidate
		if err := rows.Scan(
			&candidate.ChangeSetID, &candidate.WorkspaceID, &candidate.ProjectID,
			&candidate.PlanID, &candidate.NodeID, &candidate.IssueID,
			&candidate.SourceTaskID, &candidate.BaseSHA, &candidate.BranchName,
			&candidate.WorktreePath, &candidate.RepoPath, &candidate.CommitSHA,
			&candidate.SpecRevision, &candidate.ReviewArtifactID, &candidate.ReviewTaskID,
			&candidate.ReviewSpecRevision,
			&candidate.ReviewResult,
		); err != nil {
			return nil, fmt.Errorf("scan reviewed project change set: %w", err)
		}
		if !mergeReviewResultApproved(candidate.ReviewResult) {
			continue
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reviewed project change sets: %w", err)
	}
	return candidates, nil
}

func mergeReviewResultApproved(raw []byte) bool {
	var result struct {
		Verdict  string `json:"verdict"`
		Summary  string `json:"summary"`
		Findings []struct {
			Blocking bool `json:"blocking"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(raw, &result); err != nil ||
		result.Verdict != "approved" || strings.TrimSpace(result.Summary) == "" {
		return false
	}
	for _, finding := range result.Findings {
		if finding.Blocking {
			return false
		}
	}
	return true
}

// EnqueueReviewedChangeSet rechecks the review artifact, completed review task,
// current implementation artifact, and exact-commit unit-test evidence while
// holding a per-project transaction lock. The verdict itself is parsed by
// workflowruntime before this method is called.
func (s *Store) EnqueueReviewedChangeSet(
	ctx context.Context,
	candidate MergeReviewCandidate,
	reviewTaskID pgtype.UUID,
) (bool, error) {
	if s == nil || s.pool == nil {
		return false, errors.New("project orchestration store is not configured")
	}
	if !candidate.ChangeSetID.Valid || !candidate.WorkspaceID.Valid || !candidate.ProjectID.Valid ||
		!candidate.SourceTaskID.Valid || !candidate.ReviewArtifactID.Valid || !reviewTaskID.Valid ||
		candidate.SpecRevision <= 0 || candidate.ReviewSpecRevision <= 0 {
		return false, errors.New("reviewed change set identity is incomplete")
	}
	if !mergeReviewResultApproved(candidate.ReviewResult) {
		return false, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin project merge enqueue: %w", err)
	}
	defer tx.Rollback(ctx)

	lockKey := "autonomous-project-merge:" + util.UUIDToString(candidate.WorkspaceID) + ":" + util.UUIDToString(candidate.ProjectID)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", lockKey); err != nil {
		return false, fmt.Errorf("lock project merge queue: %w", err)
	}
	var currentReviewTaskID string
	var currentReviewResult []byte
	err = tx.QueryRow(ctx, `
		SELECT review_artifact.content ->> 'task_id', review_artifact.content -> 'result'
		FROM autonomous_project_change_set c
		JOIN autonomous_project_plan_node n ON n.id=c.node_id AND n.plan_id=c.plan_id
		JOIN autonomous_project_plan_edge e ON e.plan_id=c.plan_id AND e.workspace_id=c.workspace_id
		  AND e.project_id=c.project_id AND e.from_node_key=n.node_key
		JOIN autonomous_project_plan_node review_node ON review_node.plan_id=e.plan_id
		  AND review_node.workspace_id=e.workspace_id AND review_node.project_id=e.project_id
		  AND review_node.node_key=e.to_node_key AND review_node.kind='review'
		  AND review_node.status='completed'
		JOIN autonomous_project_artifact review_artifact ON review_artifact.id=$5
		  AND review_artifact.workspace_id=c.workspace_id AND review_artifact.project_id=c.project_id
		  AND review_artifact.plan_id=c.plan_id AND review_artifact.node_id=review_node.id
		WHERE c.id=$1 AND c.workspace_id=$2 AND c.project_id=$3
		  AND c.source_task_id=$4 AND c.merge_status='ready'
		  AND n.spec_revision=$6 AND review_node.spec_revision=$7
		FOR SHARE OF review_artifact
	`, candidate.ChangeSetID, candidate.WorkspaceID, candidate.ProjectID,
		candidate.SourceTaskID, candidate.ReviewArtifactID, candidate.SpecRevision,
		candidate.ReviewSpecRevision).Scan(&currentReviewTaskID, &currentReviewResult)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lock current review artifact before enqueue: %w", err)
	}
	if currentReviewTaskID != util.UUIDToString(reviewTaskID) ||
		!bytes.Equal(bytes.TrimSpace(currentReviewResult), bytes.TrimSpace(candidate.ReviewResult)) {
		return false, nil
	}
	var mergeOrder int64
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(merge_order),0)+1
		FROM autonomous_project_change_set
		WHERE workspace_id=$1 AND project_id=$2
	`, candidate.WorkspaceID, candidate.ProjectID).Scan(&mergeOrder); err != nil {
		return false, fmt.Errorf("allocate project merge order: %w", err)
	}
	integrationBranch := "multica/integration/" + util.UUIDToString(candidate.ProjectID)
	tag, err := tx.Exec(ctx, `
		UPDATE autonomous_project_change_set c
		SET merge_status='queued', approved_review_task_id=$3, merge_order=$4,
		    integration_branch=COALESCE(NULLIF(c.integration_branch,''),$5),
		    merge_evidence=c.merge_evidence || jsonb_build_object(
			'review_artifact_id',$14::text, 'review_spec_revision',$11,
			'implementation_spec_revision',$12, 'approval_verdict','approved'
		    ),
		    queued_at=COALESCE(c.queued_at,now()), updated_at=now()
		WHERE c.id=$1 AND c.workspace_id=$2 AND c.project_id=$6
		  AND c.source_task_id=$7 AND c.merge_status='ready' AND c.ready_at IS NOT NULL
		  AND EXISTS (
			SELECT 1 FROM autonomous_project_plan_node n
			JOIN autonomous_project_plan p ON p.id=n.plan_id
			JOIN autonomous_project_artifact ia ON ia.workspace_id=n.workspace_id
			  AND ia.project_id=n.project_id AND ia.plan_id=n.plan_id AND ia.node_id=n.id
			  AND ia.artifact_type='implementation_handoff'
			  AND ia.content ->> 'task_id'=c.source_task_id::text
			  AND ia.status='active' AND ia.valid=TRUE AND ia.artifact_revision=n.spec_revision
			WHERE n.id=c.node_id AND n.plan_id=c.plan_id AND n.spec_revision=$12
			  AND n.kind IN ('implementation','migration','integration')
			  AND p.workspace_id=c.workspace_id AND p.project_id=c.project_id AND p.status IN ('active','blocked')
		  AND EXISTS (
			SELECT 1 FROM autonomous_project_plan_edge e
			JOIN autonomous_project_plan_node rn ON rn.plan_id=e.plan_id AND rn.workspace_id=e.workspace_id
			  AND rn.project_id=e.project_id AND rn.node_key=e.to_node_key
			JOIN autonomous_project_artifact ra ON ra.workspace_id=rn.workspace_id
			  AND ra.project_id=rn.project_id AND ra.plan_id=rn.plan_id AND ra.node_id=rn.id
			  AND ra.id=$8 AND ra.artifact_type='review' AND ra.status='active' AND ra.valid=TRUE
			  AND ra.artifact_revision=rn.spec_revision AND ra.content ->> 'task_id'=$9
			  AND ra.content -> 'result'=$13::jsonb
			  AND ra.content #>> '{result,verdict}'='approved'
			  AND ra.id=(
				SELECT latest.id FROM autonomous_project_artifact latest
				WHERE latest.plan_id=rn.plan_id AND latest.node_id=rn.id
				  AND latest.artifact_type='review'
				ORDER BY latest.created_at DESC, latest.id DESC LIMIT 1
			  )
			JOIN agent_task_queue rt ON rt.id=$10 AND rt.id::text=ra.content ->> 'task_id'
			  AND rt.issue_id=rn.materialized_issue_id AND rt.status='completed' AND rt.id<>c.source_task_id
			WHERE e.plan_id=c.plan_id AND e.workspace_id=c.workspace_id AND e.project_id=c.project_id
			  AND e.from_node_key=n.node_key AND rn.kind='review' AND rn.status='completed'
			  AND rn.spec_revision=$11 AND rn.materialized_issue_id IS NOT NULL
		  )
		  )
		  AND EXISTS (
			SELECT 1 FROM autonomous_project_quality_gate_run q
			WHERE q.workspace_id=c.workspace_id AND q.project_id=c.project_id
			  AND q.plan_id=c.plan_id AND q.node_id=c.node_id AND q.gate_type='unit_test'
			  AND q.required=TRUE AND q.evidence ->> 'policy_requirement'='true'
			  AND q.evidence ->> 'source_task_id'=c.source_task_id::text
			  AND q.evidence #>> '{runner_evidence,task_id}'=c.source_task_id::text
			  AND q.evidence #>> '{runner_evidence,commit_sha}'=c.commit_sha
			  AND q.evidence #>> '{runner_evidence,worktree_path}'=c.worktree_path
			  AND q.evidence #>> '{runner_evidence,exit_code}'='0'
			  AND q.status='passed' AND q.commit_sha=c.commit_sha AND q.worktree_path=c.worktree_path
			  AND q.exit_code=0 AND q.runner_type IN ('daemon_oci','webhook_oci')
			  AND NULLIF(q.executed_command,'') IS NOT NULL
			  AND NOT EXISTS (
				SELECT 1 FROM autonomous_project_quality_gate_run newer
				WHERE newer.workspace_id=q.workspace_id AND newer.project_id=q.project_id
				  AND newer.plan_id=q.plan_id AND newer.node_id=q.node_id
				  AND newer.gate_type=q.gate_type AND newer.required=TRUE
				  AND newer.evidence ->> 'policy_requirement'='true'
				  AND (newer.updated_at,newer.id)>(q.updated_at,q.id)
			  )
		  )
	`, candidate.ChangeSetID, candidate.WorkspaceID, reviewTaskID, mergeOrder,
		integrationBranch, candidate.ProjectID, candidate.SourceTaskID,
		candidate.ReviewArtifactID, util.UUIDToString(reviewTaskID), reviewTaskID,
		candidate.ReviewSpecRevision, candidate.SpecRevision, []byte(candidate.ReviewResult),
		candidate.ReviewArtifactID)
	if err != nil {
		return false, fmt.Errorf("enqueue reviewed project change set: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return false, nil
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit project merge enqueue: %w", err)
	}
	return true, nil
}

func (s *Store) mergeQueueConnection(ctx context.Context) (*pgxpool.Conn, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("project orchestration store is not configured")
	}
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire project merge queue connection: %w", err)
	}
	return conn, nil
}

func (s *Store) claimNextMerge(ctx context.Context, conn *pgxpool.Conn, workspaceID, projectID pgtype.UUID) (MergeQueueClaim, bool, error) {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return MergeQueueClaim{}, false, fmt.Errorf("begin project merge claim: %w", err)
	}
	defer tx.Rollback(ctx)

	claim, err := scanMergeQueueClaim(tx.QueryRow(ctx, mergeClaimSQL(`merging`), workspaceID, projectID))
	if err == nil {
		if mergeEvidenceString(normalizeMergeEvidence(claim.MergeEvidence), "phase") != "quality_verified" {
			evidence := normalizeMergeEvidence(claim.MergeEvidence)
			evidence["phase"] = "recovering"
			claim.MergeEvidence, err = json.Marshal(evidence)
			if err != nil {
				return MergeQueueClaim{}, false, fmt.Errorf("encode interrupted project merge recovery: %w", err)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return MergeQueueClaim{}, false, fmt.Errorf("commit recovered project merge claim: %w", err)
		}
		return claim, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return MergeQueueClaim{}, false, fmt.Errorf("load interrupted project merge: %w", err)
	}
	for {
		claim, err = scanMergeQueueClaim(tx.QueryRow(ctx, mergeClaimSQL(`queued`), workspaceID, projectID))
		if errors.Is(err, pgx.ErrNoRows) {
			if err := tx.Commit(ctx); err != nil {
				return MergeQueueClaim{}, false, err
			}
			return MergeQueueClaim{}, false, nil
		}
		if err != nil {
			return MergeQueueClaim{}, false, fmt.Errorf("load queued project merge: %w", err)
		}
		current, err := mergeQueueProofCurrent(ctx, tx, claim)
		if err != nil {
			return MergeQueueClaim{}, false, err
		}
		if !current {
			if _, err := tx.Exec(ctx, `
				UPDATE autonomous_project_change_set
				SET merge_status='changes_requested',
				    last_error='review or unit-test evidence is no longer current', updated_at=now()
				WHERE id=$1 AND workspace_id=$2 AND merge_status='queued'
			`, claim.ChangeSetID, claim.WorkspaceID); err != nil {
				return MergeQueueClaim{}, false, fmt.Errorf("return stale project merge to review: %w", err)
			}
			continue
		}
		claim.MergeAttempt++
		evidence := normalizeMergeEvidence(claim.MergeEvidence)
		evidence["attempt"] = claim.MergeAttempt
		evidence["phase"] = "claimed"
		claim.MergeEvidence, err = json.Marshal(evidence)
		if err != nil {
			return MergeQueueClaim{}, false, fmt.Errorf("encode project merge claim: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE autonomous_project_change_set
			SET merge_status='merging', merge_attempt=$3, last_error=NULL,
			    merge_evidence=$4::jsonb, updated_at=now()
			WHERE id=$1 AND workspace_id=$2 AND merge_status='queued'
		`, claim.ChangeSetID, claim.WorkspaceID, claim.MergeAttempt, []byte(claim.MergeEvidence)); err != nil {
			return MergeQueueClaim{}, false, fmt.Errorf("mark project change set merging: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return MergeQueueClaim{}, false, fmt.Errorf("commit project merge claim: %w", err)
		}
		return claim, true, nil
	}
}

func mergeClaimSQL(status string) string {
	return `SELECT id, workspace_id, project_id, plan_id, node_id, issue_id, source_task_id, approved_review_task_id,
	       base_sha, branch_name, COALESCE(worktree_path,''), COALESCE(repo_path,''), commit_sha,
	       COALESCE(integration_branch,''), merge_order, merge_attempt, merge_evidence
	FROM autonomous_project_change_set
	WHERE workspace_id=$1 AND project_id=$2 AND merge_status='` + status + `'
	ORDER BY merge_order ASC NULLS LAST, ready_at ASC, created_at ASC, id ASC
	LIMIT 1 FOR UPDATE SKIP LOCKED`
}

func scanMergeQueueClaim(row pgx.Row) (MergeQueueClaim, error) {
	var claim MergeQueueClaim
	if err := row.Scan(
		&claim.ChangeSetID, &claim.WorkspaceID, &claim.ProjectID,
		&claim.PlanID, &claim.NodeID, &claim.IssueID, &claim.SourceTaskID, &claim.ApprovedReviewTaskID,
		&claim.BaseSHA, &claim.BranchName, &claim.WorktreePath,
		&claim.RepoPath, &claim.CommitSHA, &claim.IntegrationBranch,
		&claim.MergeOrder, &claim.MergeAttempt, &claim.MergeEvidence,
	); err != nil {
		return MergeQueueClaim{}, err
	}
	claim.ReviewTaskID = util.UUIDToString(claim.ApprovedReviewTaskID)
	var err error
	claim.ReviewArtifactID, err = mergeEvidenceReviewArtifactID(claim.MergeEvidence)
	if err != nil {
		return MergeQueueClaim{}, err
	}
	return claim, nil
}

func mergeEvidenceReviewArtifactID(raw json.RawMessage) (pgtype.UUID, error) {
	evidence := normalizeMergeEvidence(raw)
	id := mergeEvidenceString(evidence, "review_artifact_id")
	if id == "" {
		return pgtype.UUID{}, errors.New("review artifact ID is missing from merge evidence")
	}
	return util.ParseUUID(id)
}

type mergeQueueQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func mergeQueueProofCurrent(ctx context.Context, q mergeQueueQueryer, claim MergeQueueClaim) (bool, error) {
	if !claim.ApprovedReviewTaskID.Valid || !claim.ReviewArtifactID.Valid {
		return false, nil
	}
	if mergeEvidenceString(normalizeMergeEvidence(claim.MergeEvidence), "approval_verdict") != "approved" {
		return false, nil
	}
	var current bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM autonomous_project_change_set c
			JOIN autonomous_project_plan_node n ON n.id=c.node_id AND n.plan_id=c.plan_id
			  AND n.workspace_id=c.workspace_id AND n.project_id=c.project_id
			JOIN autonomous_project_plan p ON p.id=c.plan_id AND p.workspace_id=c.workspace_id
		  AND p.project_id=c.project_id AND p.status IN ('active','blocked')
			JOIN autonomous_project_artifact ia ON ia.workspace_id=c.workspace_id
			  AND ia.project_id=c.project_id AND ia.plan_id=c.plan_id AND ia.node_id=n.id
			  AND ia.artifact_type='implementation_handoff'
			  AND ia.content ->> 'task_id'=c.source_task_id::text
			  AND ia.status='active' AND ia.valid=TRUE AND ia.artifact_revision=n.spec_revision
			JOIN agent_task_queue implementation_task ON implementation_task.id=c.source_task_id
			  AND implementation_task.issue_id=c.issue_id AND implementation_task.status='completed'
			JOIN autonomous_project_plan_edge e ON e.plan_id=c.plan_id AND e.workspace_id=c.workspace_id
			  AND e.project_id=c.project_id AND e.from_node_key=n.node_key
			JOIN autonomous_project_plan_node rn ON rn.plan_id=e.plan_id AND rn.workspace_id=e.workspace_id
			  AND rn.project_id=e.project_id AND rn.node_key=e.to_node_key
			  AND rn.kind='review' AND rn.status='completed'
			JOIN autonomous_project_artifact ra ON ra.id=$3 AND ra.workspace_id=c.workspace_id
			  AND ra.project_id=c.project_id AND ra.plan_id=c.plan_id AND ra.node_id=rn.id
			  AND ra.artifact_type='review' AND ra.status='active' AND ra.valid=TRUE
			  AND ra.artifact_revision=rn.spec_revision AND ra.content ->> 'task_id'=c.approved_review_task_id::text
			  AND ra.content #>> '{result,verdict}'='approved'
			  AND ra.id=(
				SELECT latest.id FROM autonomous_project_artifact latest
				WHERE latest.plan_id=rn.plan_id AND latest.node_id=rn.id
				  AND latest.artifact_type='review'
				ORDER BY latest.created_at DESC, latest.id DESC LIMIT 1
			  )
			JOIN agent_task_queue review_task ON review_task.id=c.approved_review_task_id
			  AND review_task.issue_id=rn.materialized_issue_id AND review_task.status='completed'
			WHERE c.id=$1 AND c.workspace_id=$2 AND c.merge_status IN ('queued','merging')
			  AND c.approved_review_task_id=$4 AND rn.materialized_issue_id IS NOT NULL
			  AND n.kind IN ('implementation','migration','integration')
			  AND EXISTS (
				SELECT 1 FROM autonomous_project_quality_gate_run qg
				WHERE qg.workspace_id=c.workspace_id AND qg.project_id=c.project_id
				  AND qg.plan_id=c.plan_id AND qg.node_id=c.node_id
				  AND qg.gate_type='unit_test' AND qg.required=TRUE
				  AND qg.evidence ->> 'policy_requirement'='true'
				  AND qg.evidence ->> 'source_task_id'=c.source_task_id::text
				  AND qg.evidence #>> '{runner_evidence,task_id}'=c.source_task_id::text
				  AND qg.evidence #>> '{runner_evidence,commit_sha}'=c.commit_sha
				  AND qg.evidence #>> '{runner_evidence,worktree_path}'=c.worktree_path
				  AND qg.evidence #>> '{runner_evidence,exit_code}'='0'
				  AND qg.status='passed' AND qg.commit_sha=c.commit_sha
				  AND qg.worktree_path=c.worktree_path AND qg.exit_code=0
				  AND qg.runner_type IN ('daemon_oci','webhook_oci')
				  AND NULLIF(qg.executed_command,'') IS NOT NULL
				  AND NOT EXISTS (
					SELECT 1 FROM autonomous_project_quality_gate_run newer
					WHERE newer.workspace_id=qg.workspace_id AND newer.project_id=qg.project_id
					  AND newer.plan_id=qg.plan_id AND newer.node_id=qg.node_id
					  AND newer.gate_type=qg.gate_type AND newer.required=TRUE
					  AND newer.evidence ->> 'policy_requirement'='true'
					  AND (newer.updated_at,newer.id)>(qg.updated_at,qg.id)
				  )
			  )
		)
	`, claim.ChangeSetID, claim.WorkspaceID, claim.ReviewArtifactID, claim.ApprovedReviewTaskID).Scan(&current)
	if err != nil {
		return false, fmt.Errorf("recheck project merge review and unit-test evidence: %w", err)
	}
	return current, nil
}

func (s *Store) mergeQueueProofCurrent(ctx context.Context, claim MergeQueueClaim) (bool, error) {
	return mergeQueueProofCurrent(ctx, s.pool, claim)
}

func (s *Store) returnMergeToReview(ctx context.Context, claim MergeQueueClaim, evidence map[string]any) error {
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return fmt.Errorf("encode stale merge evidence: %w", err)
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE autonomous_project_change_set
		SET merge_status='changes_requested', last_error='review or unit-test evidence is no longer current',
		    merge_evidence=$2::jsonb, updated_at=now()
		WHERE id=$1 AND merge_status='merging' AND merge_attempt=$3
	`, claim.ChangeSetID, encoded, claim.MergeAttempt)
	if err != nil {
		return fmt.Errorf("return stale project merge for fresh review: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errors.New("project merge attempt lost its claim before stale-evidence update")
	}
	return nil
}

func (s *Store) recordMergeError(ctx context.Context, claim MergeQueueClaim, cause error) error {
	message := strings.TrimSpace(cause.Error())
	if len(message) > 4000 {
		message = message[:4000]
	}
	exhausted := mergeAttemptExhausted(claim.MergeAttempt)
	phase := "recovering"
	if exhausted {
		phase = "integration_failed"
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE autonomous_project_change_set
		SET merge_status=CASE WHEN $3::boolean THEN 'integration_failed' ELSE merge_status END,
		    last_error=$2,
		    merge_evidence=merge_evidence || jsonb_build_object('last_error',$2,'phase',$4), updated_at=now()
		WHERE id=$1 AND merge_status='merging' AND merge_attempt=$5
	`, claim.ChangeSetID, message, exhausted, phase, claim.MergeAttempt)
	if err != nil {
		return fmt.Errorf("record project merge error: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errors.New("project merge attempt lost its claim before error recording")
	}
	return nil
}

func (s *Store) restartMergeAttempt(ctx context.Context, claim MergeQueueClaim) (MergeQueueClaim, error) {
	var nextAttempt int32
	evidenceMap := normalizeMergeEvidence(claim.MergeEvidence)
	evidenceMap["attempt"] = claim.MergeAttempt + 1
	evidenceMap["phase"] = "claimed"
	evidence, err := json.Marshal(evidenceMap)
	if err != nil {
		return MergeQueueClaim{}, fmt.Errorf("encode restarted project merge attempt: %w", err)
	}
	err = s.pool.QueryRow(ctx, `
		UPDATE autonomous_project_change_set
		SET merge_attempt=merge_attempt+1, last_error=NULL,
		    merge_evidence=$2::jsonb, updated_at=now()
		WHERE id=$1 AND merge_status='merging'
		RETURNING merge_attempt
	`, claim.ChangeSetID, evidence).Scan(&nextAttempt)
	if err != nil {
		return MergeQueueClaim{}, fmt.Errorf("restart project merge attempt: %w", err)
	}
	claim.MergeAttempt = nextAttempt
	claim.MergeEvidence = evidence
	return claim, nil
}

func (s *Store) persistMergeEvidence(ctx context.Context, claim MergeQueueClaim, evidence map[string]any) error {
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return fmt.Errorf("encode merge attempt evidence: %w", err)
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE autonomous_project_change_set
		SET merge_evidence=$2::jsonb, updated_at=now()
		WHERE id=$1 AND merge_status='merging' AND merge_attempt=$3
	`, claim.ChangeSetID, encoded, claim.MergeAttempt)
	if err != nil {
		return fmt.Errorf("persist merge attempt evidence: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errors.New("project merge attempt lost its claim")
	}
	return nil
}

func (s *Store) finishMerge(ctx context.Context, claim MergeQueueClaim, status, mergedSHA, message string, evidence map[string]any) error {
	if status != "merged" && status != "conflict" && status != "stale_base" && status != "integration_failed" {
		return fmt.Errorf("unsupported project merge result status %q", status)
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return fmt.Errorf("encode project merge result: %w", err)
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE autonomous_project_change_set
		SET merge_status=$4, merged_sha=NULLIF($5,''),
		    merged_at=CASE WHEN $4='merged' THEN now() ELSE NULL END,
		    last_error=NULLIF($6,''), merge_evidence=$7::jsonb, updated_at=now()
		WHERE id=$1 AND workspace_id=$2 AND project_id=$3
		  AND merge_status='merging' AND merge_attempt=$8
	`, claim.ChangeSetID, claim.WorkspaceID, claim.ProjectID, status,
		mergedSHA, message, encoded, claim.MergeAttempt)
	if err != nil {
		return fmt.Errorf("finish project merge attempt: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errors.New("project merge attempt lost its claim before finalization")
	}
	return nil
}

func projectMergeLockKey(workspaceID, projectID pgtype.UUID) string {
	return "autonomous-project-merge:" + util.UUIDToString(workspaceID) + ":" + util.UUIDToString(projectID)
}

func normalizeMergeEvidence(raw json.RawMessage) map[string]any {
	var evidence map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &evidence) != nil || evidence == nil {
		return map[string]any{}
	}
	return evidence
}

func mergeEvidenceString(evidence map[string]any, key string) string {
	value, _ := evidence[key].(string)
	return strings.TrimSpace(value)
}
