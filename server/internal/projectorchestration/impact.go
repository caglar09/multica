package projectorchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type BrainImpactClassification string

const (
	BrainImpactNone  BrainImpactClassification = "NO_IMPACT"
	BrainImpactMinor BrainImpactClassification = "MINOR_IMPACT"
	BrainImpactMajor BrainImpactClassification = "MAJOR_IMPACT"
)

func classifyBrainImpact(candidate MemoryCandidate, retention MemoryRetention) BrainImpactClassification {
	if retention.Conflict || retention.GovernanceState == "conflicted" {
		return BrainImpactMajor
	}
	switch candidate.Type {
	case "requirement", "constraint", "architecture_decision", "product_decision":
		if candidate.Importance >= 0.65 {
			return BrainImpactMajor
		}
		return BrainImpactMinor
	case "risk", "dependency", "repository_fact":
		if candidate.Importance >= 0.50 {
			return BrainImpactMinor
		}
	case "fact", "assumption", "lesson":
		if candidate.Importance >= 0.85 {
			return BrainImpactMinor
		}
	}
	return BrainImpactNone
}

func brainImpactRationale(candidate MemoryCandidate, retention MemoryRetention, classification BrainImpactClassification) string {
	if retention.Conflict || retention.GovernanceState == "conflicted" {
		return "equal-authority Brain evidence conflicts with current project knowledge"
	}
	switch classification {
	case BrainImpactMajor:
		return fmt.Sprintf("new %s memory can invalidate project scope, constraints, or architecture", candidate.Type)
	case BrainImpactMinor:
		return fmt.Sprintf("new %s memory may affect one or more planned nodes", candidate.Type)
	default:
		return fmt.Sprintf("new %s memory is advisory and does not require a plan change", candidate.Type)
	}
}

// AssessMemoryImpact creates an advisory, deterministic impact proposal for a
// newly retained semantic memory. It never edits the plan DAG. The planner or a
// later Project Control flow must explicitly consume/review proposals before a
// plan revision can change.
func (s *Store) AssessMemoryImpact(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	candidate MemoryCandidate,
	retention MemoryRetention,
	source MemorySource,
) error {
	if s == nil || s.pool == nil || !workspaceID.Valid || !projectID.Valid || !retention.EntryID.Valid {
		return nil
	}
	// Confirmation/semantic compaction is not a new fact. Keep any existing
	// proposal for the retained entry intact rather than repeatedly reclassifying
	// identical evidence.
	if retention.Compacted && !retention.Conflict {
		return nil
	}

	classification := classifyBrainImpact(candidate, retention)
	query := strings.TrimSpace(candidate.Subject + " " + candidate.CanonicalKey)

	var planID pgtype.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT id
		FROM autonomous_project_plan
		WHERE workspace_id=$1 AND project_id=$2
		ORDER BY revision DESC
		LIMIT 1
	`, workspaceID, projectID).Scan(&planID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	affected := []string{}
	if planID.Valid && query != "" {
		rows, qerr := s.pool.Query(ctx, `
			WITH q AS (
				SELECT websearch_to_tsquery('simple'::regconfig, $2) AS tsq
			)
			SELECT n.node_key
			FROM autonomous_project_plan_node n
			CROSS JOIN q
			WHERE n.plan_id=$1
			  AND to_tsvector('simple'::regconfig,
			        COALESCE(n.node_key,'') || ' ' || COALESCE(n.title,'') || ' ' ||
			        COALESCE(n.description,'') || ' ' || COALESCE(n.acceptance_criteria::text,'')) @@ q.tsq
			ORDER BY ts_rank_cd(
			           to_tsvector('simple'::regconfig,
			             COALESCE(n.node_key,'') || ' ' || COALESCE(n.title,'') || ' ' ||
			             COALESCE(n.description,'') || ' ' || COALESCE(n.acceptance_criteria::text,'')),
			           q.tsq
			         ) DESC,
			         n.priority DESC,
			         n.node_key
			LIMIT 20
		`, planID, query)
		if qerr != nil {
			return qerr
		}
		for rows.Next() {
			var key string
			if err := rows.Scan(&key); err != nil {
				rows.Close()
				return err
			}
			affected = append(affected, key)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}

	affectedJSON, err := json.Marshal(affected)
	if err != nil {
		return err
	}
	evidence := source.Evidence
	if len(evidence) == 0 || !json.Valid(evidence) {
		evidence = json.RawMessage(`{}`)
	}

	action := "none"
	switch classification {
	case BrainImpactMajor:
		action = "replan_required"
	case BrainImpactMinor:
		action = "review_nodes"
	}
	delta, err := json.Marshal(map[string]any{
		"action":             action,
		"affected_node_keys": affected,
		"brain_revision":     retention.BrainRevision,
	})
	if err != nil {
		return err
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO autonomous_project_brain_impact_proposal (
			workspace_id, project_id, plan_id, brain_entry_id, classification,
			affected_node_keys, rationale, evidence, proposed_plan_delta,
			status, analyzer
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'proposed','deterministic_v1')
		ON CONFLICT (brain_entry_id) DO NOTHING
	`, workspaceID, projectID, planID, retention.EntryID, string(classification),
		affectedJSON, brainImpactRationale(candidate, retention, classification), evidence, delta)
	return err
}
