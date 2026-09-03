package projectorchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

type BrainConfig struct {
	WorkspaceID   pgtype.UUID
	ProjectID     pgtype.UUID
	Enabled       bool
	RuntimeMode   string
	RuntimeID     pgtype.UUID
	Model         string
	ThinkingLevel string
	ServiceTier   string
	LearningMode  string
}

type MemoryCandidate struct {
	CanonicalKey string          `json:"canonical_key"`
	Type         string          `json:"type"`
	Subject      string          `json:"subject"`
	Content      json.RawMessage `json:"content"`
	Confidence   float64         `json:"confidence"`
	Importance   float64         `json:"importance"`
}

type BrainLearningJob struct {
	ID          pgtype.UUID
	WorkspaceID pgtype.UUID
	ProjectID   pgtype.UUID
	TaskID      pgtype.UUID
	Evidence    json.RawMessage
	Attempts    int
	MaxAttempts int
}

var allowedBrainMemoryTypes = map[string]struct{}{
	"requirement": {}, "constraint": {}, "assumption": {}, "fact": {},
	"architecture_decision": {}, "product_decision": {}, "risk": {},
	"dependency": {}, "repository_fact": {}, "lesson": {},
}

func (s *Store) GetBrainConfig(ctx context.Context, workspaceID, projectID pgtype.UUID) (BrainConfig, error) {
	cfg := BrainConfig{WorkspaceID: workspaceID, ProjectID: projectID, Enabled: true, RuntimeMode: "inherit_mika", LearningMode: "adaptive"}
	if s == nil || s.pool == nil {
		return cfg, errors.New("project brain store is not configured")
	}
	var model, thinking, tier pgtype.Text
	err := s.pool.QueryRow(ctx, `
		SELECT enabled, runtime_mode, runtime_id, model, thinking_level, service_tier, learning_mode
		FROM autonomous_project_brain_config
		WHERE workspace_id = $1 AND project_id = $2
	`, workspaceID, projectID).Scan(&cfg.Enabled, &cfg.RuntimeMode, &cfg.RuntimeID, &model, &thinking, &tier, &cfg.LearningMode)
	if errors.Is(err, pgx.ErrNoRows) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if model.Valid { cfg.Model = strings.TrimSpace(model.String) }
	if thinking.Valid { cfg.ThinkingLevel = strings.TrimSpace(thinking.String) }
	if tier.Valid { cfg.ServiceTier = strings.TrimSpace(tier.String) }
	return cfg, nil
}

func (s *Store) EnqueueBrainLearning(ctx context.Context, workspaceID, projectID, taskID pgtype.UUID, evidence any) error {
	if s == nil || s.pool == nil || !workspaceID.Valid || !projectID.Valid || !taskID.Valid {
		return nil
	}
	raw, err := json.Marshal(evidence)
	if err != nil { return err }
	_, err = s.pool.Exec(ctx, `
		INSERT INTO autonomous_project_brain_learning_job (workspace_id, project_id, task_id, evidence)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (task_id) DO NOTHING
	`, workspaceID, projectID, taskID, raw)
	return err
}

func (s *Store) ClaimBrainLearning(ctx context.Context, lease time.Duration) (BrainLearningJob, bool, error) {
	if s == nil || s.pool == nil { return BrainLearningJob{}, false, nil }
	tx, err := s.pool.Begin(ctx)
	if err != nil { return BrainLearningJob{}, false, err }
	defer tx.Rollback(ctx)
	var j BrainLearningJob
	var raw []byte
	err = tx.QueryRow(ctx, `
		SELECT id, workspace_id, project_id, task_id, evidence, attempts, max_attempts
		FROM autonomous_project_brain_learning_job
		WHERE (status='pending' AND available_at <= now())
		   OR (status='running' AND lease_expires_at < now())
		ORDER BY created_at
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`).Scan(&j.ID,&j.WorkspaceID,&j.ProjectID,&j.TaskID,&raw,&j.Attempts,&j.MaxAttempts)
	if errors.Is(err, pgx.ErrNoRows) { return BrainLearningJob{}, false, nil }
	if err != nil { return BrainLearningJob{}, false, err }
	j.Evidence = append(json.RawMessage(nil), raw...)
	token := dbid.NewV7()
	_, err = tx.Exec(ctx, `
		UPDATE autonomous_project_brain_learning_job
		SET status='running', attempts=attempts+1, lease_token=$2,
		    lease_expires_at=now()+make_interval(secs => $3), updated_at=now()
		WHERE id=$1
	`, j.ID, token, int(lease.Seconds()))
	if err != nil { return BrainLearningJob{}, false, err }
	if err := tx.Commit(ctx); err != nil { return BrainLearningJob{}, false, err }
	j.Attempts++
	return j, true, nil
}

func (s *Store) CompleteBrainLearning(ctx context.Context, id pgtype.UUID, provider, model string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE autonomous_project_brain_learning_job
		SET status='completed', provider=NULLIF($2,''), model=NULLIF($3,''),
		    completed_at=now(), lease_token=NULL, lease_expires_at=NULL, last_error=NULL, updated_at=now()
		WHERE id=$1
	`, id, provider, model)
	return err
}

func (s *Store) FailBrainLearning(ctx context.Context, job BrainLearningJob, cause error) error {
	status := "pending"
	if job.Attempts >= job.MaxAttempts { status = "deferred" }
	msg := "brain learning failed"
	if cause != nil { msg = cause.Error() }
	_, err := s.pool.Exec(ctx, `
		UPDATE autonomous_project_brain_learning_job
		SET status=$2, available_at=CASE WHEN $2='pending' THEN now()+interval '30 seconds' ELSE available_at END,
		    lease_token=NULL, lease_expires_at=NULL, last_error=$3, updated_at=now()
		WHERE id=$1
	`, job.ID, status, msg)
	return err
}

func normalizeBrainKey(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	v = strings.ReplaceAll(v, " ", ".")
	var b strings.Builder
	for _, r := range v {
		if (r>='a'&&r<='z') || (r>='0'&&r<='9') || r=='.' || r=='_' || r=='-' { b.WriteRune(r) }
	}
	return strings.Trim(b.String(), ".")
}


type RecalledMemory struct {
	CanonicalKey string
	Type         string
	Subject      string
	Content      string
	Confidence   float64
	Importance   float64
}

func (s *Store) RecallMemories(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	query string,
	limit int,
	maxChars int,
) ([]RecalledMemory, error) {
	if s == nil || s.pool == nil || !workspaceID.Valid || !projectID.Valid {
		return nil, nil
	}
	if limit <= 0 || limit > 50 { limit = 12 }
	if maxChars <= 0 || maxChars > 64000 { maxChars = 12000 }
	query = strings.TrimSpace(query)

	rows, err := s.pool.Query(ctx, `
		SELECT COALESCE(canonical_key,''), entry_type, subject, content::text,
		       COALESCE(confidence,0), importance
		FROM autonomous_project_brain_entry
		WHERE workspace_id=$1 AND project_id=$2
		  AND status='active' AND superseded_by IS NULL
		ORDER BY
		  CASE WHEN $3 <> '' AND (
		    lower(subject) LIKE '%' || lower($3) || '%' OR
		    lower(content::text) LIKE '%' || lower($3) || '%'
		  ) THEN 0 ELSE 1 END,
		  importance DESC,
		  confirmation_count DESC,
		  confidence DESC NULLS LAST,
		  COALESCE(last_confirmed_at, created_at) DESC
		LIMIT $4
	`, workspaceID, projectID, query, limit)
	if err != nil { return nil, err }
	defer rows.Close()

	out := make([]RecalledMemory, 0, limit)
	used := 0
	for rows.Next() {
		var m RecalledMemory
		if err := rows.Scan(&m.CanonicalKey,&m.Type,&m.Subject,&m.Content,&m.Confidence,&m.Importance); err != nil {
			return nil, err
		}
		remaining := maxChars - used
		if remaining <= 0 { break }
		if len(m.Content) > remaining { m.Content = m.Content[:remaining] }
		used += len(m.Content)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) RetainMemory(ctx context.Context, workspaceID, projectID pgtype.UUID, candidate MemoryCandidate, sourceType, sourceID string) error {
	if s == nil || s.pool == nil { return errors.New("project brain store is not configured") }
	key := normalizeBrainKey(candidate.CanonicalKey)
	if key == "" { return errors.New("brain memory canonical key is required") }
	if _, ok := allowedBrainMemoryTypes[candidate.Type]; !ok { return fmt.Errorf("unsupported brain memory type %q", candidate.Type) }
	candidate.Subject = strings.TrimSpace(candidate.Subject)
	if candidate.Subject == "" { candidate.Subject = key }
	if len(candidate.Content) == 0 || !json.Valid(candidate.Content) { return errors.New("brain memory content must be valid JSON") }
	if candidate.Confidence < 0 || candidate.Confidence > 1 { return errors.New("brain memory confidence out of range") }
	if candidate.Importance < 0 || candidate.Importance > 1 { return errors.New("brain memory importance out of range") }

	tx, err := s.pool.Begin(ctx)
	if err != nil { return err }
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "project-brain:"+util.UUIDToString(projectID)+":"+key); err != nil { return err }

	var currentID pgtype.UUID
	var currentRevision int64
	var currentContent []byte
	err = tx.QueryRow(ctx, `
		SELECT id, revision, content
		FROM autonomous_project_brain_entry
		WHERE workspace_id=$1 AND project_id=$2 AND canonical_key=$3
		  AND status='active' AND superseded_by IS NULL
		LIMIT 1
		FOR UPDATE
	`, workspaceID, projectID, key).Scan(&currentID,&currentRevision,&currentContent)
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, `
			INSERT INTO autonomous_project_brain_entry
			(workspace_id,project_id,entry_type,subject,content,source_type,source_id,confidence,revision,created_by_type,canonical_key,status,importance,last_confirmed_at)
			VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,1,'system',$9,'active',$10,now())
		`,workspaceID,projectID,candidate.Type,candidate.Subject,candidate.Content,sourceType,sourceID,candidate.Confidence,key,candidate.Importance)
		if err != nil { return err }
		return tx.Commit(ctx)
	}
	if err != nil { return err }

	var same bool
	if err := tx.QueryRow(ctx, "SELECT $1::jsonb = $2::jsonb", currentContent, candidate.Content).Scan(&same); err != nil { return err }
	if same {
		_, err = tx.Exec(ctx, `
			UPDATE autonomous_project_brain_entry
			SET confirmation_count=confirmation_count+1,
			    confidence=GREATEST(COALESCE(confidence,0),$2),
			    importance=GREATEST(importance,$3),
			    last_confirmed_at=now()
			WHERE id=$1
		`, currentID, candidate.Confidence, candidate.Importance)
		if err != nil { return err }
		return tx.Commit(ctx)
	}

	newID := dbid.NewV7()
	_, err = tx.Exec(ctx, `
		UPDATE autonomous_project_brain_entry
		SET status='superseded', superseded_by=$2
		WHERE id=$1
	`, currentID, newID)
	if err != nil { return err }
	_, err = tx.Exec(ctx, `
		INSERT INTO autonomous_project_brain_entry
		(id,workspace_id,project_id,entry_type,subject,content,source_type,source_id,confidence,revision,created_by_type,canonical_key,status,importance,last_confirmed_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),$9,$10,'system',$11,'active',$12,now())
	`,newID,workspaceID,projectID,candidate.Type,candidate.Subject,candidate.Content,sourceType,sourceID,candidate.Confidence,currentRevision+1,key,candidate.Importance)
	if err != nil { return err }
	return tx.Commit(ctx)
}
