package projectorchestration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

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

type MemoryAuthority string

const (
	AuthorityUserDecision             MemoryAuthority = "user_decision"
	AuthorityAuthoritativeSpec        MemoryAuthority = "authoritative_spec"
	AuthorityDeterministicObservation MemoryAuthority = "deterministic_observation"
	AuthorityTrustedExternal          MemoryAuthority = "trusted_external"
	AuthoritySystemDerived            MemoryAuthority = "system_derived"
	AuthorityAgentInference           MemoryAuthority = "agent_inference"
)

type MemorySource struct {
	SourceType         string
	SourceID           string
	CreatedByType      string
	CreatedByID        pgtype.UUID
	Authority          MemoryAuthority
	Evidence           json.RawMessage
	ObservedAt         time.Time
	RepositoryRevision string
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
	if model.Valid {
		cfg.Model = strings.TrimSpace(model.String)
	}
	if thinking.Valid {
		cfg.ThinkingLevel = strings.TrimSpace(thinking.String)
	}
	if tier.Valid {
		cfg.ServiceTier = strings.TrimSpace(tier.String)
	}
	return cfg, nil
}

func (s *Store) EnqueueBrainLearning(ctx context.Context, workspaceID, projectID, taskID pgtype.UUID, evidence any) error {
	if s == nil || s.pool == nil || !workspaceID.Valid || !projectID.Valid || !taskID.Valid {
		return nil
	}
	raw, err := json.Marshal(evidence)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO autonomous_project_brain_learning_job (workspace_id, project_id, task_id, evidence)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (task_id) DO NOTHING
	`, workspaceID, projectID, taskID, raw)
	return err
}

func (s *Store) ClaimBrainLearning(ctx context.Context, lease time.Duration) (BrainLearningJob, bool, error) {
	if s == nil || s.pool == nil {
		return BrainLearningJob{}, false, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BrainLearningJob{}, false, err
	}
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
	`).Scan(&j.ID, &j.WorkspaceID, &j.ProjectID, &j.TaskID, &raw, &j.Attempts, &j.MaxAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return BrainLearningJob{}, false, nil
	}
	if err != nil {
		return BrainLearningJob{}, false, err
	}
	j.Evidence = append(json.RawMessage(nil), raw...)
	token := dbid.NewV7()
	_, err = tx.Exec(ctx, `
		UPDATE autonomous_project_brain_learning_job
		SET status='running', attempts=attempts+1, lease_token=$2,
		    lease_expires_at=now()+make_interval(secs => $3), updated_at=now()
		WHERE id=$1
	`, j.ID, token, int(lease.Seconds()))
	if err != nil {
		return BrainLearningJob{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return BrainLearningJob{}, false, err
	}
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
	if job.Attempts >= job.MaxAttempts {
		status = "deferred"
	}
	msg := "brain learning failed"
	if cause != nil {
		msg = cause.Error()
	}
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
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), ".")
}

type RecalledMemory struct {
	ID             string
	CanonicalKey   string
	Type           string
	Subject        string
	Content        string
	Confidence     float64
	Importance     float64
	Authority      MemoryAuthority
	Evidence       json.RawMessage
	ObservedAt     time.Time
	BrainRevision  int64
	RelevanceScore float64
}

type RecallOptions struct {
	Query                     string
	RoleFamily                string
	NodeKind                  NodeKind
	Limit                     int
	MaxTokens                 int
	CurrentRepositoryRevision string
}

func (s *Store) RecallMemories(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	query string,
	limit int,
	maxChars int,
) ([]RecalledMemory, error) {
	if maxChars <= 0 {
		maxChars = 12000
	}
	return s.RecallMemoriesV2(ctx, workspaceID, projectID, RecallOptions{
		Query: query, Limit: limit, MaxTokens: maxChars / 4,
	})
}

func (s *Store) RecallMemoriesV2(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	opts RecallOptions,
) ([]RecalledMemory, error) {
	if s == nil || s.pool == nil || !workspaceID.Valid || !projectID.Valid || strings.TrimSpace(opts.Query) == "" {
		return nil, nil
	}
	embedder, err := memoryEmbedderFromEnvironment()
	if err != nil {
		return nil, err
	}
	return s.recallMemoriesV2WithEmbedder(ctx, workspaceID, projectID, opts, embedder)
}

func (s *Store) recallMemoriesV2WithEmbedder(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	opts RecallOptions,
	embedder MemoryEmbedder,
) ([]RecalledMemory, error) {
	if s == nil || s.pool == nil || !workspaceID.Valid || !projectID.Valid {
		return nil, nil
	}
	if opts.Limit <= 0 || opts.Limit > 50 {
		opts.Limit = 12
	}
	if opts.MaxTokens <= 0 || opts.MaxTokens > 16000 {
		opts.MaxTokens = 700
	}
	query := strings.TrimSpace(opts.Query)
	if query == "" {
		return nil, nil
	}
	role := strings.ToLower(strings.TrimSpace(opts.RoleFamily))
	if role == "" {
		role = strings.ToLower(strings.TrimSpace(string(opts.NodeKind)))
	}

	if opts.CurrentRepositoryRevision != "" {
		if err := s.MarkRepositoryFactsStale(ctx, workspaceID, projectID, opts.CurrentRepositoryRevision); err != nil {
			return nil, err
		}
	}
	if err := s.ExpireMemories(ctx, workspaceID, projectID); err != nil {
		return nil, err
	}

	memories, err := queryHybridBrainMemories(ctx, s.pool, workspaceID, projectID, opts, embedder)
	if err != nil {
		return nil, err
	}
	out := make([]RecalledMemory, 0, opts.Limit)
	seen := make(map[string]struct{}, opts.Limit)
	usedTokens := 0
	for _, m := range memories {
		identity := m.Type + "\x00" + normalizeBrainKey(m.CanonicalKey)
		if _, exists := seen[identity]; exists {
			continue
		}
		itemTokens := estimateBrainTokens(m.Subject + " " + m.Content)
		if itemTokens <= 0 {
			continue
		}
		if usedTokens+itemTokens > opts.MaxTokens {
			continue
		}
		seen[identity] = struct{}{}
		usedTokens += itemTokens
		out = append(out, m)
		if len(out) >= opts.Limit {
			break
		}
	}
	return out, nil
}

type brainMemoryQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func queryHybridBrainMemories(
	ctx context.Context,
	queryer brainMemoryQueryer,
	workspaceID, projectID pgtype.UUID,
	opts RecallOptions,
	embedder MemoryEmbedder,
) ([]RecalledMemory, error) {
	query := strings.TrimSpace(opts.Query)
	if query == "" || queryer == nil {
		return nil, nil
	}
	if opts.Limit <= 0 || opts.Limit > 50 {
		opts.Limit = 12
	}
	role := strings.ToLower(strings.TrimSpace(opts.RoleFamily))
	if role == "" {
		role = strings.ToLower(strings.TrimSpace(string(opts.NodeKind)))
	}
	// Embeddings add recall coverage when available; provider failure keeps FTS usable.
	vectorLiteral, _ := embedMemoryText(ctx, embedder, query)
	rows, err := queryer.Query(ctx, `
		WITH q AS (
			SELECT regexp_replace(
				websearch_to_tsquery('simple'::regconfig, $3::text)::text,
				' & ', ' | ', 'g'
			)::tsquery AS tsq
		),
	eligible AS NOT MATERIALIZED (
			SELECT b.*,
			       to_tsvector('simple'::regconfig,
			         COALESCE(b.subject,'') || ' ' || COALESCE(b.canonical_key,'') || ' ' || COALESCE(b.content::text,'')) AS document
			FROM autonomous_project_brain_entry b
			WHERE b.workspace_id=$1 AND b.project_id=$2
			  AND b.status='active' AND b.superseded_by IS NULL
			  AND b.governance_state='current'
			  AND (b.expires_at IS NULL OR b.expires_at > now())
			  AND ($5::text = '' OR b.repository_revision IS NULL OR b.repository_revision = $5::text)
		),
	fts AS (
			SELECT e.id, row_number() OVER (
				ORDER BY ts_rank_cd(e.document, q.tsq)
				       + CASE
				           WHEN $4::text IN ('qa','review') AND e.entry_type IN ('requirement','risk','lesson') THEN 0.8
				           WHEN $4::text = 'security' AND e.entry_type IN ('risk','constraint','architecture_decision') THEN 0.8
				           WHEN $4::text IN ('frontend','design') AND e.entry_type IN ('product_decision','requirement','architecture_decision') THEN 0.8
				           WHEN $4::text IN ('backend','implementation','architecture') AND e.entry_type IN ('architecture_decision','repository_fact','constraint','dependency') THEN 0.8
				           WHEN $4::text = 'product' AND e.entry_type IN ('requirement','product_decision','constraint') THEN 0.8
				           ELSE 0 END DESC,
				         e.importance DESC, e.brain_revision DESC, e.id
			) AS rank
			FROM eligible e CROSS JOIN q
			WHERE e.document @@ q.tsq
			ORDER BY ts_rank_cd(e.document, q.tsq)
			       + CASE
			           WHEN $4::text IN ('qa','review') AND e.entry_type IN ('requirement','risk','lesson') THEN 0.8
			           WHEN $4::text = 'security' AND e.entry_type IN ('risk','constraint','architecture_decision') THEN 0.8
			           WHEN $4::text IN ('frontend','design') AND e.entry_type IN ('product_decision','requirement','architecture_decision') THEN 0.8
			           WHEN $4::text IN ('backend','implementation','architecture') AND e.entry_type IN ('architecture_decision','repository_fact','constraint','dependency') THEN 0.8
			           WHEN $4::text = 'product' AND e.entry_type IN ('requirement','product_decision','constraint') THEN 0.8
			           ELSE 0 END DESC,
			         e.importance DESC, e.brain_revision DESC, e.id
			LIMIT $6
		),
	semantic AS (
			SELECT e.id, row_number() OVER (
				ORDER BY e.embedding <=> $8::vector ASC,
				         e.importance DESC, e.brain_revision DESC, e.id
			) AS rank
			FROM eligible e
			WHERE $7::boolean AND e.embedding IS NOT NULL
			ORDER BY e.embedding <=> $8::vector ASC,
			         e.importance DESC, e.brain_revision DESC, e.id
			LIMIT $6
		),
	fused AS (
			SELECT id, sum(1.0 / (60.0 + rank::float8)) AS rrf_score
			FROM (
				SELECT id, rank FROM fts
				UNION ALL
				SELECT id, rank FROM semantic
			) ranks
			GROUP BY id
		)
		SELECT b.id, COALESCE(b.canonical_key,''), b.entry_type, b.subject, left(b.content::text,6000),
		       COALESCE(b.confidence,0), b.importance, b.authority, b.evidence,
		       b.observed_at, b.brain_revision, fused.rrf_score
		FROM fused
		JOIN autonomous_project_brain_entry b ON b.id=fused.id
		ORDER BY fused.rrf_score DESC, b.importance DESC,
		         b.brain_revision DESC, b.id
		LIMIT $6
	`, workspaceID, projectID, query, role, strings.TrimSpace(opts.CurrentRepositoryRevision), opts.Limit*3, vectorLiteral != "", nullableMemoryVector(vectorLiteral))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	memories := make([]RecalledMemory, 0, opts.Limit*3)
	for rows.Next() {
		var memory RecalledMemory
		var id pgtype.UUID
		var authority string
		var evidence []byte
		if err := rows.Scan(
			&id, &memory.CanonicalKey, &memory.Type, &memory.Subject, &memory.Content,
			&memory.Confidence, &memory.Importance, &authority, &evidence,
			&memory.ObservedAt, &memory.BrainRevision, &memory.RelevanceScore,
		); err != nil {
			return nil, err
		}
		memory.ID = util.UUIDToString(id)
		memory.Authority = MemoryAuthority(authority)
		memory.Evidence = append(json.RawMessage(nil), evidence...)
		memories = append(memories, memory)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return memories, nil
}

func nullableMemoryVector(vector string) any {
	if strings.TrimSpace(vector) == "" {
		return nil
	}
	return vector
}

func estimateBrainTokens(value string) int {
	runes := len([]rune(value))
	if runes == 0 {
		return 0
	}
	return (runes + 3) / 4
}

func authorityForSource(sourceType string) MemoryAuthority {
	switch strings.ToLower(strings.TrimSpace(sourceType)) {
	case "member", "user", "user_decision":
		return AuthorityUserDecision
	case "project_specification", "specification", "authoritative_spec":
		return AuthorityAuthoritativeSpec
	case "repository", "repository_analyzer", "repository_observation", "deterministic_observation":
		return AuthorityDeterministicObservation
	case "trusted_external", "verified_external":
		return AuthorityTrustedExternal
	case "system", "planner", "system_derived":
		return AuthoritySystemDerived
	default:
		return AuthorityAgentInference
	}
}

func authorityRank(authority MemoryAuthority) int {
	switch authority {
	case AuthorityUserDecision:
		return 60
	case AuthorityAuthoritativeSpec:
		return 50
	case AuthorityDeterministicObservation:
		return 40
	case AuthorityTrustedExternal:
		return 30
	case AuthoritySystemDerived:
		return 20
	case AuthorityAgentInference:
		return 10
	default:
		return 0
	}
}

func memoryExpiry(memoryType string, observed time.Time) *time.Time {
	var ttl time.Duration
	switch memoryType {
	case "repository_fact":
		ttl = 7 * 24 * time.Hour
	case "assumption", "dependency", "risk":
		ttl = 30 * 24 * time.Hour
	case "fact":
		ttl = 90 * 24 * time.Hour
	case "lesson":
		ttl = 120 * 24 * time.Hour
	default:
		return nil
	}
	expires := observed.Add(ttl)
	return &expires
}

func semanticMemoryFingerprint(candidate MemoryCandidate) string {
	var compact bytes.Buffer
	if err := json.Compact(&compact, candidate.Content); err != nil {
		compact.Write(candidate.Content)
	}
	raw := strings.ToLower(strings.TrimSpace(candidate.Type) + "\n" + strings.TrimSpace(candidate.Subject) + "\n" + compact.String())
	var normalized strings.Builder
	space := false
	for _, r := range raw {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			normalized.WriteRune(r)
			space = false
			continue
		}
		if !space {
			normalized.WriteByte(' ')
			space = true
		}
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(normalized.String())))
	return hex.EncodeToString(sum[:])
}

func normalizeMemorySource(source MemorySource) (MemorySource, error) {
	source.SourceType = strings.TrimSpace(source.SourceType)
	source.SourceID = strings.TrimSpace(source.SourceID)
	if source.Authority == "" {
		source.Authority = authorityForSource(source.SourceType)
	}
	if authorityRank(source.Authority) == 0 {
		return MemorySource{}, fmt.Errorf("unsupported brain memory authority %q", source.Authority)
	}
	if source.CreatedByType == "" {
		source.CreatedByType = "system"
	}
	switch source.CreatedByType {
	case "system", "agent", "member":
	default:
		return MemorySource{}, fmt.Errorf("unsupported brain memory creator type %q", source.CreatedByType)
	}
	if source.ObservedAt.IsZero() {
		source.ObservedAt = time.Now().UTC()
	}
	if len(source.Evidence) == 0 {
		source.Evidence = json.RawMessage(`{}`)
	}
	if !json.Valid(source.Evidence) {
		return MemorySource{}, errors.New("brain memory evidence must be valid JSON")
	}
	return source, nil
}

func (s *Store) RetainMemory(ctx context.Context, workspaceID, projectID pgtype.UUID, candidate MemoryCandidate, sourceType, sourceID string) error {
	_, err := s.RetainMemoryGoverned(ctx, workspaceID, projectID, candidate, MemorySource{
		SourceType: sourceType,
		SourceID:   sourceID,
		Authority:  authorityForSource(sourceType),
	})
	return err
}

type MemoryRetention struct {
	EntryID         pgtype.UUID
	BrainRevision   int64
	GovernanceState string
	Compacted       bool
	Conflict        bool
}

// SynchronousBrainDecision is a structured completion decision whose memory
// row must commit atomically with the project artifact that produced it.
type SynchronousBrainDecision struct {
	Subject   string
	Content   json.RawMessage
	SourceID  string
	Embedding string
}

// PersistSynchronousBrainDecisions stores critical completion decisions in
// the caller's transaction. Embedding preparation happens before this call so
// no network request is made while the transaction is open.
func PersistSynchronousBrainDecisions(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, projectID, planID, nodeID, taskID, agentID pgtype.UUID,
	decisions []SynchronousBrainDecision,
) (int64, error) {
	if tx == nil || !workspaceID.Valid || !projectID.Valid || !taskID.Valid || !agentID.Valid {
		return 0, errors.New("synchronous brain decisions require transaction and source identifiers")
	}
	clean := make([]SynchronousBrainDecision, 0, len(decisions))
	seen := make(map[string]struct{}, len(decisions))
	for _, decision := range decisions {
		decision.Subject = strings.TrimSpace(decision.Subject)
		decision.SourceID = strings.TrimSpace(decision.SourceID)
		if decision.Subject == "" || decision.SourceID == "" || len(decision.Content) == 0 || !json.Valid(decision.Content) {
			return 0, errors.New("synchronous brain decision requires subject, source id, and valid JSON content")
		}
		if _, ok := seen[decision.SourceID]; ok {
			continue
		}
		seen[decision.SourceID] = struct{}{}
		clean = append(clean, decision)
	}
	if len(clean) == 0 {
		var revision int64
		err := tx.QueryRow(ctx, `
			SELECT COALESCE((SELECT revision FROM autonomous_project_brain_state
			                 WHERE workspace_id=$1 AND project_id=$2),0)
		`, workspaceID, projectID).Scan(&revision)
		return revision, err
	}
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "project-brain:"+util.UUIDToString(projectID)); err != nil {
		return 0, err
	}
	newDecisions := make([]SynchronousBrainDecision, 0, len(clean))
	for _, decision := range clean {
		var exists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM autonomous_project_brain_entry
				WHERE workspace_id=$1 AND project_id=$2
				  AND source_type='task_completion_decision' AND source_id=$3
			)
		`, workspaceID, projectID, decision.SourceID).Scan(&exists); err != nil {
			return 0, err
		}
		if !exists {
			newDecisions = append(newDecisions, decision)
		}
	}
	if len(newDecisions) == 0 {
		var revision int64
		err := tx.QueryRow(ctx, `
			SELECT COALESCE((SELECT revision FROM autonomous_project_brain_state
			                 WHERE workspace_id=$1 AND project_id=$2),0)
		`, workspaceID, projectID).Scan(&revision)
		return revision, err
	}
	var brainRevision int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO autonomous_project_brain_state (project_id,workspace_id,revision,updated_at)
		VALUES ($1,$2,1,now())
		ON CONFLICT (project_id) DO UPDATE
		SET revision=autonomous_project_brain_state.revision+1, updated_at=now()
		WHERE autonomous_project_brain_state.workspace_id=EXCLUDED.workspace_id
		RETURNING revision
	`, projectID, workspaceID).Scan(&brainRevision); err != nil {
		return 0, fmt.Errorf("allocate synchronous brain revision: %w", err)
	}
	firstEntryID := dbid.NewV7()
	if nodeID.Valid {
		if _, err := tx.Exec(ctx, `
			UPDATE autonomous_project_brain_entry
			SET status='superseded',governance_state='superseded',
			    superseded_by=$4,brain_revision=$5
			WHERE workspace_id=$1 AND project_id=$2 AND node_id=$3
			  AND source_type='task_completion_decision'
			  AND status='active' AND superseded_by IS NULL
		`, workspaceID, projectID, nodeID, firstEntryID, brainRevision); err != nil {
			return 0, fmt.Errorf("supersede prior synchronous brain decisions: %w", err)
		}
	}
	for i, decision := range newDecisions {
		candidate := MemoryCandidate{
			CanonicalKey: "completion-decision:" + util.UUIDToString(nodeID) + ":" + fmt.Sprint(i),
			Type:         "architecture_decision",
			Subject:      decision.Subject,
			Content:      decision.Content,
			Confidence:   0.9,
			Importance:   0.9,
		}
		fingerprint := semanticMemoryFingerprint(candidate)
		entryID := firstEntryID
		if i > 0 {
			entryID = dbid.NewV7()
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO autonomous_project_brain_entry (
				id,workspace_id,project_id,plan_id,node_id,entry_type,subject,content,
				source_type,source_id,confidence,revision,created_by_type,created_by_id,
				canonical_key,status,importance,confirmation_count,last_confirmed_at,
				authority,evidence,observed_at,semantic_fingerprint,governance_state,
				brain_revision,embedding
			) VALUES (
				$1,$2,$3,$4,$5,'architecture_decision',$6,$7,
				'task_completion_decision',$8,0.9,1,'agent',$9,$10,'active',0.9,1,
				now(),'agent_inference',$11,now(),$12,'current',$13,NULLIF($14,'')::vector
			)
		`, entryID, workspaceID, projectID, nullableBrainUUID(planID), nullableBrainUUID(nodeID),
			decision.Subject, decision.Content, decision.SourceID, agentID,
			candidate.CanonicalKey, []byte(`{"kind":"structured_completion_decision"}`),
			fingerprint, brainRevision, decision.Embedding)
		if err != nil {
			return 0, fmt.Errorf("persist synchronous brain decision: %w", err)
		}
	}
	return brainRevision, nil
}

func nullableBrainUUID(value pgtype.UUID) any {
	if value.Valid {
		return value
	}
	return nil
}

func (s *Store) RetainMemoryGoverned(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	candidate MemoryCandidate,
	source MemorySource,
) (MemoryRetention, error) {
	if s == nil || s.pool == nil {
		return MemoryRetention{}, errors.New("project brain store is not configured")
	}
	key := normalizeBrainKey(candidate.CanonicalKey)
	if key == "" {
		return MemoryRetention{}, errors.New("brain memory canonical key is required")
	}
	if _, ok := allowedBrainMemoryTypes[candidate.Type]; !ok {
		return MemoryRetention{}, fmt.Errorf("unsupported brain memory type %q", candidate.Type)
	}
	candidate.Subject = strings.TrimSpace(candidate.Subject)
	if candidate.Subject == "" {
		candidate.Subject = key
	}
	if len(candidate.Content) == 0 || !json.Valid(candidate.Content) {
		return MemoryRetention{}, errors.New("brain memory content must be valid JSON")
	}
	if candidate.Confidence < 0 || candidate.Confidence > 1 {
		return MemoryRetention{}, errors.New("brain memory confidence out of range")
	}
	if candidate.Importance < 0 || candidate.Importance > 1 {
		return MemoryRetention{}, errors.New("brain memory importance out of range")
	}
	var err error
	source, err = normalizeMemorySource(source)
	if err != nil {
		return MemoryRetention{}, err
	}
	if candidate.Type != "repository_fact" {
		source.RepositoryRevision = ""
	}
	fingerprint := semanticMemoryFingerprint(candidate)
	expiresAt := memoryExpiry(candidate.Type, source.ObservedAt)
	// Do not persist a silent FTS-only row when the server opted into embeddings.
	embedding, err := PrepareBrainMemoryEmbedding(ctx, candidate.Subject, candidate.Content)
	if err != nil {
		return MemoryRetention{}, fmt.Errorf("embed retained project memory: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return MemoryRetention{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "project-brain:"+util.UUIDToString(projectID)); err != nil {
		return MemoryRetention{}, err
	}

	var brainRevision int64
	err = tx.QueryRow(ctx, `
		INSERT INTO autonomous_project_brain_state (project_id,workspace_id,revision,updated_at)
		VALUES ($1,$2,1,now())
		ON CONFLICT (project_id) DO UPDATE
		SET revision=autonomous_project_brain_state.revision+1, updated_at=now()
		WHERE autonomous_project_brain_state.workspace_id=EXCLUDED.workspace_id
		RETURNING revision
	`, projectID, workspaceID).Scan(&brainRevision)
	if err != nil {
		return MemoryRetention{}, fmt.Errorf("allocate brain revision: %w", err)
	}

	// Deterministic semantic compaction: content-equivalent memories collapse
	// even when an LLM proposed a different canonical key. The fingerprint is
	// normalized independent of canonical_key, and authority never decreases.
	var duplicateID pgtype.UUID
	var duplicateAuthority string
	err = tx.QueryRow(ctx, `
		SELECT id, authority
		FROM autonomous_project_brain_entry
		WHERE workspace_id=$1 AND project_id=$2 AND entry_type=$3
		  AND semantic_fingerprint=$4
		  AND status='active' AND superseded_by IS NULL
		  AND governance_state='current'
		ORDER BY brain_revision DESC
		LIMIT 1
		FOR UPDATE
	`, workspaceID, projectID, candidate.Type, fingerprint).Scan(&duplicateID, &duplicateAuthority)
	if err == nil {
		authority := MemoryAuthority(duplicateAuthority)
		if authorityRank(source.Authority) > authorityRank(authority) {
			authority = source.Authority
		}
		_, err = tx.Exec(ctx, `
			UPDATE autonomous_project_brain_entry
			SET confirmation_count=confirmation_count+1,
			    confidence=GREATEST(COALESCE(confidence,0),$2),
			    importance=GREATEST(importance,$3), authority=$4,
			    evidence=evidence || $5::jsonb,
			    observed_at=GREATEST(observed_at,$6),
			    expires_at=$7, brain_revision=$8,
			    embedding=COALESCE(embedding,NULLIF($9,'')::vector),
			    last_confirmed_at=now()
			WHERE id=$1
		`, duplicateID, candidate.Confidence, candidate.Importance, string(authority),
			source.Evidence, source.ObservedAt, expiresAt, brainRevision, embedding)
		if err != nil {
			return MemoryRetention{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return MemoryRetention{}, err
		}
		return MemoryRetention{EntryID: duplicateID, BrainRevision: brainRevision, GovernanceState: "current", Compacted: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return MemoryRetention{}, err
	}

	type currentMemory struct {
		id        pgtype.UUID
		revision  int64
		content   []byte
		authority MemoryAuthority
	}
	rows, err := tx.Query(ctx, `
		SELECT id, revision, content, authority
		FROM autonomous_project_brain_entry
		WHERE workspace_id=$1 AND project_id=$2 AND canonical_key=$3
		  AND status='active' AND superseded_by IS NULL
		ORDER BY revision DESC
		FOR UPDATE
	`, workspaceID, projectID, key)
	if err != nil {
		return MemoryRetention{}, err
	}
	currents := []currentMemory{}
	for rows.Next() {
		var item currentMemory
		var authority string
		if err := rows.Scan(&item.id, &item.revision, &item.content, &authority); err != nil {
			rows.Close()
			return MemoryRetention{}, err
		}
		item.authority = MemoryAuthority(authority)
		currents = append(currents, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return MemoryRetention{}, err
	}
	rows.Close()

	newID := dbid.NewV7()
	maxRevision := int64(0)
	bestRank := -1
	var preferred pgtype.UUID
	for _, current := range currents {
		if current.revision > maxRevision {
			maxRevision = current.revision
		}
		rank := authorityRank(current.authority)
		if rank > bestRank {
			bestRank = rank
			preferred = current.id
		}
		var same bool
		if err := tx.QueryRow(ctx, "SELECT $1::jsonb = $2::jsonb", current.content, candidate.Content).Scan(&same); err != nil {
			return MemoryRetention{}, err
		}
		if same {
			authority := current.authority
			if authorityRank(source.Authority) > authorityRank(authority) {
				authority = source.Authority
			}
			_, err = tx.Exec(ctx, `
				UPDATE autonomous_project_brain_entry
				SET confirmation_count=confirmation_count+1,
				    confidence=GREATEST(COALESCE(confidence,0),$2),
				    importance=GREATEST(importance,$3), authority=$4,
				    evidence=evidence || $5::jsonb,
				    observed_at=GREATEST(observed_at,$6), expires_at=$7,
				    semantic_fingerprint=$8, brain_revision=$9,
				    embedding=COALESCE(embedding,NULLIF($10,'')::vector),
				    last_confirmed_at=now()
				WHERE id=$1
			`, current.id, candidate.Confidence, candidate.Importance, string(authority),
				source.Evidence, source.ObservedAt, expiresAt, fingerprint, brainRevision, embedding)
			if err != nil {
				return MemoryRetention{}, err
			}
			if err := tx.Commit(ctx); err != nil {
				return MemoryRetention{}, err
			}
			return MemoryRetention{EntryID: current.id, BrainRevision: brainRevision, GovernanceState: "current", Compacted: true}, nil
		}
	}

	insert := func(state, status string, supersededBy, conflictID pgtype.UUID) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO autonomous_project_brain_entry
			(id,workspace_id,project_id,entry_type,subject,content,source_type,source_id,
			 confidence,revision,created_by_type,created_by_id,canonical_key,status,importance,
			 confirmation_count,last_confirmed_at,authority,evidence,observed_at,expires_at,
			 repository_revision,semantic_fingerprint,governance_state,conflict_group_id,
			 brain_revision,superseded_by,embedding)
			VALUES ($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),$9,$10,$11,$12,$13,$14,$15,
			        1,now(),$16,$17,$18,$19,NULLIF($20,''),$21,$22,$23,$24,$25,NULLIF($26,'')::vector)
		`, newID, workspaceID, projectID, candidate.Type, candidate.Subject, candidate.Content,
			source.SourceType, source.SourceID, candidate.Confidence, maxRevision+1,
			source.CreatedByType, source.CreatedByID, key, status, candidate.Importance,
			string(source.Authority), source.Evidence, source.ObservedAt, expiresAt,
			source.RepositoryRevision, fingerprint, state, conflictID, brainRevision, supersededBy, embedding)
		return err
	}

	if len(currents) == 0 {
		if err := insert("current", "active", pgtype.UUID{}, pgtype.UUID{}); err != nil {
			return MemoryRetention{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return MemoryRetention{}, err
		}
		return MemoryRetention{EntryID: newID, BrainRevision: brainRevision, GovernanceState: "current"}, nil
	}

	newRank := authorityRank(source.Authority)
	if newRank > bestRank {
		if _, err := tx.Exec(ctx, `
			UPDATE autonomous_project_brain_entry
			SET status='superseded', governance_state='superseded', superseded_by=$4
			WHERE workspace_id=$1 AND project_id=$2 AND canonical_key=$3
			  AND status='active' AND superseded_by IS NULL
		`, workspaceID, projectID, key, newID); err != nil {
			return MemoryRetention{}, err
		}
		if err := insert("current", "active", pgtype.UUID{}, pgtype.UUID{}); err != nil {
			return MemoryRetention{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return MemoryRetention{}, err
		}
		return MemoryRetention{EntryID: newID, BrainRevision: brainRevision, GovernanceState: "current"}, nil
	}

	if newRank < bestRank {
		if err := insert("superseded", "superseded", preferred, pgtype.UUID{}); err != nil {
			return MemoryRetention{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return MemoryRetention{}, err
		}
		return MemoryRetention{EntryID: newID, BrainRevision: brainRevision, GovernanceState: "superseded"}, nil
	}

	// Equal authority + different value is a real contradiction. Neither side
	// wins silently; conflicted memories are excluded from normal retrieval
	// until a higher-authority observation or explicit decision resolves them.
	conflictID := dbid.NewV7()
	if _, err := tx.Exec(ctx, `
		UPDATE autonomous_project_brain_entry
		SET governance_state='conflicted', conflict_group_id=$4, brain_revision=$5
		WHERE workspace_id=$1 AND project_id=$2 AND canonical_key=$3
		  AND status='active' AND superseded_by IS NULL
	`, workspaceID, projectID, key, conflictID, brainRevision); err != nil {
		return MemoryRetention{}, err
	}
	if err := insert("conflicted", "active", pgtype.UUID{}, conflictID); err != nil {
		return MemoryRetention{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return MemoryRetention{}, err
	}
	return MemoryRetention{EntryID: newID, BrainRevision: brainRevision, GovernanceState: "conflicted", Conflict: true}, nil
}

func (s *Store) ExpireMemories(ctx context.Context, workspaceID, projectID pgtype.UUID) error {
	if s == nil || s.pool == nil {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE autonomous_project_brain_entry
		SET governance_state='stale'
		WHERE workspace_id=$1 AND project_id=$2
		  AND status='active' AND superseded_by IS NULL
		  AND governance_state='current'
		  AND expires_at IS NOT NULL AND expires_at <= now()
	`, workspaceID, projectID)
	return err
}

func (s *Store) MarkRepositoryFactsStale(ctx context.Context, workspaceID, projectID pgtype.UUID, currentRevision string) error {
	currentRevision = strings.TrimSpace(currentRevision)
	if s == nil || s.pool == nil || currentRevision == "" {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE autonomous_project_brain_entry
		SET governance_state = CASE
		    WHEN repository_revision=$3 AND (expires_at IS NULL OR expires_at > now()) THEN 'current'
		    ELSE 'stale' END
		WHERE workspace_id=$1 AND project_id=$2
		  AND entry_type='repository_fact'
		  AND status='active' AND superseded_by IS NULL
		  AND governance_state IN ('current','stale')
	`, workspaceID, projectID, currentRevision)
	return err
}

type MemoryTrustState struct {
	CanonicalKey string
	State        string
	Authority    MemoryAuthority
	EntryIDs     []string
	Reason       string
}

func (s *Store) MemoryTrust(ctx context.Context, workspaceID, projectID pgtype.UUID, canonicalKey string) (MemoryTrustState, error) {
	key := normalizeBrainKey(canonicalKey)
	out := MemoryTrustState{CanonicalKey: key, State: "missing"}
	if s == nil || s.pool == nil || key == "" {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, authority, governance_state
		FROM autonomous_project_brain_entry
		WHERE workspace_id=$1 AND project_id=$2 AND canonical_key=$3
		  AND status='active' AND superseded_by IS NULL
		ORDER BY brain_revision DESC
	`, workspaceID, projectID, key)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	bestRank := -1
	states := map[string]bool{}
	for rows.Next() {
		var id pgtype.UUID
		var authority, state string
		if err := rows.Scan(&id, &authority, &state); err != nil {
			return out, err
		}
		out.EntryIDs = append(out.EntryIDs, util.UUIDToString(id))
		states[state] = true
		if rank := authorityRank(MemoryAuthority(authority)); rank > bestRank {
			bestRank = rank
			out.Authority = MemoryAuthority(authority)
		}
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	if len(out.EntryIDs) == 0 {
		return out, nil
	}
	switch {
	case states["conflicted"]:
		out.State = "conflicted"
		out.Reason = "equal-authority contradictory memories require resolution"
	case states["current"]:
		out.State = "current"
		out.Reason = "highest-authority non-expired memory is current"
	case states["stale"]:
		out.State = "stale"
		out.Reason = "memory TTL or repository revision is stale"
	default:
		out.State = "unknown"
	}
	return out, nil
}

func (s *Store) CurrentBrainRevision(ctx context.Context, workspaceID, projectID pgtype.UUID) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, nil
	}
	var revision int64
	err := s.pool.QueryRow(ctx, `
		SELECT revision FROM autonomous_project_brain_state
		WHERE workspace_id=$1 AND project_id=$2
	`, workspaceID, projectID).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return revision, err
}

func (s *Store) CreateBrainSnapshot(
	ctx context.Context,
	workspaceID, projectID, planID pgtype.UUID,
	planRevision int64,
	entryIDs []string,
) (pgtype.UUID, error) {
	if s == nil || s.pool == nil {
		return pgtype.UUID{}, errors.New("project brain store is not configured")
	}
	brainRevision, err := s.CurrentBrainRevision(ctx, workspaceID, projectID)
	if err != nil {
		return pgtype.UUID{}, err
	}
	raw, err := json.Marshal(entryIDs)
	if err != nil {
		return pgtype.UUID{}, err
	}
	var id pgtype.UUID
	err = s.pool.QueryRow(ctx, `
		INSERT INTO autonomous_project_brain_snapshot (
			workspace_id,project_id,plan_id,plan_revision,brain_revision,entry_ids
		) VALUES ($1,$2,$3,NULLIF($4,0),$5,$6)
		RETURNING id
	`, workspaceID, projectID, planID, planRevision, brainRevision, raw).Scan(&id)
	return id, err
}
