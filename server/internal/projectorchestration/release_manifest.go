package projectorchestration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/util"
)

const releaseManifestVersion = 1

// ReleaseManifest binds a content-addressed package to the exact candidate
// tree, plan revision, and successful candidate-quality runs that produced it.
type ReleaseManifest struct {
	Version            int      `json:"version"`
	ManifestID         string   `json:"manifest_id"`
	WorkspaceID        string   `json:"workspace_id"`
	ProjectID          string   `json:"project_id"`
	PlanID             string   `json:"plan_id"`
	ChangeSetID        string   `json:"change_set_id"`
	PlanRevision       int64    `json:"plan_revision"`
	PlanDigest         string   `json:"plan_digest"`
	Environment        string   `json:"environment"`
	CommitSHA          string   `json:"commit_sha"`
	TreeSHA            string   `json:"tree_sha"`
	Builder            string   `json:"builder"`
	Outputs            []string `json:"outputs"`
	ArtifactDigest     string   `json:"artifact_digest"`
	ArtifactStorageKey string   `json:"artifact_storage_key"`
	QualityGateRunIDs  []string `json:"quality_gate_run_ids"`
	ManifestDigest     string   `json:"manifest_digest"`
}

// ProviderReleaseManifest is the non-secret release identity sent with a
// provider request. It deliberately omits the private storage key and URL.
type ProviderReleaseManifest struct {
	Version           int      `json:"version"`
	ManifestID        string   `json:"manifest_id"`
	ManifestDigest    string   `json:"manifest_digest"`
	PlanRevision      int64    `json:"plan_revision"`
	PlanDigest        string   `json:"plan_digest"`
	CommitSHA         string   `json:"commit_sha"`
	TreeSHA           string   `json:"tree_sha"`
	Builder           string   `json:"builder"`
	Outputs           []string `json:"outputs"`
	ArtifactDigest    string   `json:"artifact_digest"`
	QualityGateRunIDs []string `json:"quality_gate_run_ids"`
}

type releaseManifestDigestPayload struct {
	Version            int      `json:"version"`
	ManifestID         string   `json:"manifest_id"`
	WorkspaceID        string   `json:"workspace_id"`
	ProjectID          string   `json:"project_id"`
	PlanID             string   `json:"plan_id"`
	ChangeSetID        string   `json:"change_set_id"`
	PlanRevision       int64    `json:"plan_revision"`
	PlanDigest         string   `json:"plan_digest"`
	Environment        string   `json:"environment"`
	CommitSHA          string   `json:"commit_sha"`
	TreeSHA            string   `json:"tree_sha"`
	Builder            string   `json:"builder"`
	Outputs            []string `json:"outputs"`
	ArtifactDigest     string   `json:"artifact_digest"`
	ArtifactStorageKey string   `json:"artifact_storage_key"`
	QualityGateRunIDs  []string `json:"quality_gate_run_ids"`
}

func (m ReleaseManifest) ProviderView() ProviderReleaseManifest {
	return ProviderReleaseManifest{
		Version: m.Version, ManifestID: m.ManifestID, ManifestDigest: m.ManifestDigest, PlanRevision: m.PlanRevision,
		PlanDigest: m.PlanDigest, CommitSHA: m.CommitSHA, TreeSHA: m.TreeSHA,
		Builder: m.Builder, Outputs: append([]string(nil), m.Outputs...),
		ArtifactDigest: m.ArtifactDigest, QualityGateRunIDs: append([]string(nil), m.QualityGateRunIDs...),
	}
}

func SealReleaseManifest(manifest ReleaseManifest) (ReleaseManifest, error) {
	if manifest.ManifestID == "" {
		id, err := newReleaseManifestID()
		if err != nil {
			return ReleaseManifest{}, err
		}
		manifest.ManifestID = id
	}
	if err := validateReleaseManifest(manifest); err != nil {
		return ReleaseManifest{}, err
	}
	manifest.ManifestDigest = ""
	manifest.Outputs = append([]string(nil), manifest.Outputs...)
	manifest.QualityGateRunIDs = append([]string(nil), manifest.QualityGateRunIDs...)
	sort.Strings(manifest.QualityGateRunIDs)
	payload := manifest.digestPayload()
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ReleaseManifest{}, fmt.Errorf("encode release manifest: %w", err)
	}
	hash := sha256.Sum256(encoded)
	manifest.ManifestDigest = hex.EncodeToString(hash[:])
	return manifest, nil
}

func (m ReleaseManifest) Validate() error {
	if err := validateReleaseManifest(m); err != nil {
		return err
	}
	if !digestSHA256.MatchString(m.ManifestDigest) {
		return errors.New("release manifest digest is invalid")
	}
	encoded, err := json.Marshal(m.digestPayload())
	if err != nil {
		return fmt.Errorf("encode release manifest: %w", err)
	}
	hash := sha256.Sum256(encoded)
	if hex.EncodeToString(hash[:]) != m.ManifestDigest {
		return errors.New("release manifest digest does not match its contents")
	}
	return nil
}

func (m ReleaseManifest) digestPayload() releaseManifestDigestPayload {
	return releaseManifestDigestPayload{
		Version: m.Version, ManifestID: m.ManifestID, WorkspaceID: m.WorkspaceID, ProjectID: m.ProjectID,
		PlanID: m.PlanID, ChangeSetID: m.ChangeSetID, PlanRevision: m.PlanRevision,
		PlanDigest: m.PlanDigest, Environment: m.Environment, CommitSHA: strings.ToLower(m.CommitSHA),
		TreeSHA: strings.ToLower(m.TreeSHA), Builder: m.Builder,
		Outputs: append([]string(nil), m.Outputs...), ArtifactDigest: m.ArtifactDigest,
		ArtifactStorageKey: m.ArtifactStorageKey,
		QualityGateRunIDs:  append([]string(nil), m.QualityGateRunIDs...),
	}
}

func validateReleaseManifest(manifest ReleaseManifest) error {
	if manifest.Version != releaseManifestVersion {
		return fmt.Errorf("unsupported release manifest version %d", manifest.Version)
	}
	if _, err := util.ParseUUID(manifest.ManifestID); err != nil {
		return errors.New("release manifest identity is not a valid UUID")
	}
	for name, value := range map[string]string{
		"workspace_id": manifest.WorkspaceID, "project_id": manifest.ProjectID,
		"plan_id": manifest.PlanID, "change_set_id": manifest.ChangeSetID,
	} {
		if _, err := util.ParseUUID(value); err != nil {
			return fmt.Errorf("release manifest %s is invalid", name)
		}
	}
	if manifest.PlanRevision <= 0 || !digestSHA256.MatchString(manifest.PlanDigest) {
		return errors.New("release manifest requires a positive plan revision and plan digest")
	}
	if manifest.Environment != "staging" && manifest.Environment != "production" {
		return errors.New("release manifest environment must be staging or production")
	}
	if !gitCommitSHA.MatchString(manifest.CommitSHA) || !gitCommitSHA.MatchString(manifest.TreeSHA) {
		return errors.New("release manifest requires exact commit and tree SHAs")
	}
	if !digestSHA256.MatchString(manifest.ArtifactDigest) || manifest.ArtifactStorageKey == "" {
		return errors.New("release manifest requires a content digest and private storage key")
	}
	expectedArtifactStorageKey := "autonomous-project/releases/sha256/" + manifest.ArtifactDigest + ".tar"
	if manifest.ArtifactStorageKey != expectedArtifactStorageKey {
		return errors.New("release manifest storage key must match its artifact digest")
	}
	if _, err := ParseReleaseContract(mustReleaseContractJSON(manifest.Builder, manifest.Outputs)); err != nil {
		return fmt.Errorf("release manifest builder or outputs are invalid: %w", err)
	}
	if len(manifest.QualityGateRunIDs) < 3 {
		return errors.New("release manifest requires both candidate gates and the release artifact build run")
	}
	runIDs := append([]string(nil), manifest.QualityGateRunIDs...)
	sort.Strings(runIDs)
	for i, id := range runIDs {
		if _, err := util.ParseUUID(id); err != nil {
			return errors.New("release manifest contains an invalid quality gate run ID")
		}
		if i > 0 && id == runIDs[i-1] {
			return errors.New("release manifest contains duplicate quality gate run IDs")
		}
	}
	return nil
}

func mustReleaseContractJSON(builder string, outputs []string) []byte {
	contract := ReleaseContract{Version: 1, Builder: builder, Outputs: outputs}
	if builder == "go_binary" {
		contract.Package = "./release-manifest-validation"
	} else if builder == "pnpm_build" {
		contract.Workdir = "."
	}
	encoded, _ := json.Marshal(contract)
	return encoded
}

func newReleaseManifestID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate release manifest identity: %w", err)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return hex.EncodeToString(raw[0:4]) + "-" + hex.EncodeToString(raw[4:6]) + "-" +
		hex.EncodeToString(raw[6:8]) + "-" + hex.EncodeToString(raw[8:10]) + "-" + hex.EncodeToString(raw[10:16]), nil
}

func ParseReleaseReference(ref string) (string, error) {
	const prefix = "sha256:"
	if !strings.HasPrefix(ref, prefix) {
		return "", errors.New("deployment release reference must be a manifest sha256 digest")
	}
	digest := strings.TrimPrefix(ref, prefix)
	if !digestSHA256.MatchString(digest) {
		return "", errors.New("deployment release reference has an invalid manifest digest")
	}
	return digest, nil
}

func ValidateReleaseAcknowledgement(evidence map[string]any, manifest ReleaseManifest) error {
	if err := manifest.Validate(); err != nil {
		return err
	}
	if evidence == nil {
		return errors.New("deployment provider omitted release acknowledgement")
	}
	manifestDigest, ok := evidence["manifest_digest"].(string)
	if !ok || manifestDigest != manifest.ManifestDigest {
		return errors.New("deployment provider acknowledged a different manifest digest")
	}
	artifactDigest, ok := evidence["artifact_digest"].(string)
	if !ok || artifactDigest != manifest.ArtifactDigest {
		return errors.New("deployment provider acknowledged a different artifact digest")
	}
	commitSHA, ok := evidence["deployed_commit_sha"].(string)
	if !ok || !strings.EqualFold(commitSHA, manifest.CommitSHA) {
		return errors.New("deployment provider acknowledged a different deployed commit")
	}
	verified, ok := evidence["artifact_verified"].(bool)
	if !ok || !verified {
		return errors.New("deployment provider did not verify the release artifact")
	}
	return nil
}

func ValidateProviderAcknowledgement(evidence map[string]any, manifest ProviderReleaseManifest, commitSHA, artifactDigest string) error {
	if _, err := util.ParseUUID(manifest.ManifestID); err != nil {
		return errors.New("deployment request has an invalid manifest identity")
	}
	if !digestSHA256.MatchString(manifest.ManifestDigest) || !digestSHA256.MatchString(manifest.ArtifactDigest) ||
		!gitCommitSHA.MatchString(manifest.CommitSHA) || manifest.CommitSHA != commitSHA || manifest.ArtifactDigest != artifactDigest ||
		manifest.ManifestDigest == "" {
		return errors.New("deployment request does not contain a complete immutable release identity")
	}
	if evidence == nil {
		return errors.New("deployment provider omitted release acknowledgement")
	}
	manifestDigest, ok := evidence["manifest_digest"].(string)
	if !ok || manifestDigest != manifest.ManifestDigest {
		return errors.New("deployment provider acknowledged a different manifest digest")
	}
	ackArtifactDigest, ok := evidence["artifact_digest"].(string)
	if !ok || ackArtifactDigest != manifest.ArtifactDigest {
		return errors.New("deployment provider acknowledged a different artifact digest")
	}
	deployedCommitSHA, ok := evidence["deployed_commit_sha"].(string)
	if !ok || !strings.EqualFold(deployedCommitSHA, manifest.CommitSHA) {
		return errors.New("deployment provider acknowledged a different deployed commit")
	}
	verified, ok := evidence["artifact_verified"].(bool)
	if !ok || !verified {
		return errors.New("deployment provider did not verify the release artifact")
	}
	return nil
}

func ReleaseAcknowledgementEvidence(evidence map[string]any) map[string]any {
	return map[string]any{
		"manifest_digest":     evidence["manifest_digest"],
		"artifact_digest":     evidence["artifact_digest"],
		"deployed_commit_sha": evidence["deployed_commit_sha"],
		"artifact_verified":   evidence["artifact_verified"],
	}
}

func persistReleaseManifest(ctx context.Context, pool *pgxpool.Pool, manifest ReleaseManifest) error {
	if pool == nil {
		return errors.New("release manifest database is not configured")
	}
	sealed, err := SealReleaseManifest(manifest)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(sealed)
	if err != nil {
		return fmt.Errorf("encode sealed release manifest: %w", err)
	}
	workspaceID, _ := util.ParseUUID(sealed.WorkspaceID)
	projectID, _ := util.ParseUUID(sealed.ProjectID)
	planID, _ := util.ParseUUID(sealed.PlanID)
	changeSetID, _ := util.ParseUUID(sealed.ChangeSetID)
	manifestID, err := util.ParseUUID(sealed.ManifestID)
	if err != nil {
		return errors.New("sealed release manifest identity is invalid")
	}
	qualityRunIDs := make([]pgtype.UUID, 0, len(sealed.QualityGateRunIDs))
	for _, value := range sealed.QualityGateRunIDs {
		id, _ := util.ParseUUID(value)
		qualityRunIDs = append(qualityRunIDs, id)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin release manifest transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	var revision int64
	var planDigest string
	if err := tx.QueryRow(ctx, `
		SELECT revision, plan_digest
		FROM autonomous_project_plan
		WHERE id=$1 AND workspace_id=$2 AND project_id=$3
		FOR UPDATE
	`, planID, workspaceID, projectID).Scan(&revision, &planDigest); err != nil {
		return fmt.Errorf("read release plan revision: %w", err)
	}
	if revision != sealed.PlanRevision || planDigest != sealed.PlanDigest {
		return errors.New("release plan changed before manifest sealing")
	}
	var verifiedRunCount int
	var hasCandidateBuild, hasIntegrationTest, hasReleaseBuild bool
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*),
		       COALESCE(bool_or(q.gate_type='build' AND q.evidence->>'scope'='merged_candidate'), FALSE),
		       COALESCE(bool_or(q.gate_type='integration_test' AND q.evidence->>'scope'='merged_candidate'), FALSE),
		       COALESCE(bool_or(q.gate_type='build' AND q.evidence->>'scope'='merged_candidate_release_artifact'), FALSE)
		FROM autonomous_project_quality_gate_run q
		JOIN autonomous_project_change_set c
		  ON c.id=$5 AND c.workspace_id=q.workspace_id AND c.project_id=q.project_id
		 AND c.plan_id=q.plan_id AND c.node_id=q.node_id
		WHERE q.id=ANY($1::uuid[]) AND q.workspace_id=$2 AND q.project_id=$3 AND q.plan_id=$4
		  AND q.status='passed' AND q.required=TRUE AND q.commit_sha=$6
		  AND q.evidence->>'candidate_tree_sha'=$7
		  AND q.evidence->>'change_set_id'=$5::text
	`, qualityRunIDs, workspaceID, projectID, planID, changeSetID, sealed.CommitSHA, sealed.TreeSHA).Scan(
		&verifiedRunCount, &hasCandidateBuild, &hasIntegrationTest, &hasReleaseBuild,
	); err != nil {
		return fmt.Errorf("verify candidate quality evidence before manifest sealing: %w", err)
	}
	if verifiedRunCount != len(qualityRunIDs) || !hasCandidateBuild || !hasIntegrationTest || !hasReleaseBuild {
		return errors.New("release manifest quality run IDs do not prove both candidate gates and the exact artifact build")
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO autonomous_project_release_manifest (
			manifest_id, manifest_digest, workspace_id, project_id, plan_id, change_set_id,
			plan_revision, plan_digest, environment, commit_sha, tree_sha,
			builder, outputs, artifact_digest, artifact_storage_key,
			quality_gate_run_ids, manifest_json, created_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,now())
		ON CONFLICT DO NOTHING
	`, manifestID, sealed.ManifestDigest, workspaceID, projectID, planID, changeSetID,
		sealed.PlanRevision, sealed.PlanDigest, sealed.Environment, sealed.CommitSHA, sealed.TreeSHA,
		sealed.Builder, sealed.Outputs, sealed.ArtifactDigest, sealed.ArtifactStorageKey,
		qualityRunIDs, encoded)
	if err != nil {
		return fmt.Errorf("persist immutable release manifest: %w", err)
	}
	tag, err := tx.Exec(ctx, `
		UPDATE autonomous_project_plan
		SET release_manifest_id=$4, release_manifest_digest=$5, updated_at=now()
		WHERE id=$1 AND workspace_id=$2 AND project_id=$3 AND revision=$6 AND plan_digest=$7
	`, planID, workspaceID, projectID, manifestID, sealed.ManifestDigest, sealed.PlanRevision, sealed.PlanDigest)
	if err != nil {
		return fmt.Errorf("attach release manifest to plan: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errors.New("release manifest could not be attached to the current plan revision")
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit release manifest transaction: %w", err)
	}
	return nil
}

func LoadPlanReleaseManifest(ctx context.Context, pool *pgxpool.Pool, workspaceID, projectID, planID pgtype.UUID) (ReleaseManifest, error) {
	if pool == nil {
		return ReleaseManifest{}, errors.New("release manifest database is not configured")
	}
	var encoded []byte
	err := pool.QueryRow(ctx, `
		SELECT m.manifest_json
		FROM autonomous_project_plan p
		JOIN autonomous_project_release_manifest m
		  ON m.manifest_id=p.release_manifest_id AND m.manifest_digest=p.release_manifest_digest
		 AND m.plan_revision=p.revision AND m.plan_digest=p.plan_digest
		 AND m.workspace_id=p.workspace_id AND m.project_id=p.project_id AND m.plan_id=p.id
		WHERE p.id=$1 AND p.workspace_id=$2 AND p.project_id=$3
	`, planID, workspaceID, projectID).Scan(&encoded)
	if errors.Is(err, pgx.ErrNoRows) {
		return ReleaseManifest{}, errors.New("deployment requires a persisted immutable release manifest")
	}
	if err != nil {
		return ReleaseManifest{}, fmt.Errorf("load plan release manifest: %w", err)
	}
	var manifest ReleaseManifest
	if err := json.Unmarshal(encoded, &manifest); err != nil {
		return ReleaseManifest{}, fmt.Errorf("decode persisted release manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return ReleaseManifest{}, fmt.Errorf("validate persisted release manifest: %w", err)
	}
	if manifest.WorkspaceID != util.UUIDToString(workspaceID) || manifest.ProjectID != util.UUIDToString(projectID) || manifest.PlanID != util.UUIDToString(planID) {
		return ReleaseManifest{}, errors.New("persisted release manifest scope does not match deployment plan")
	}
	return manifest, nil
}

// PinPlanReleaseManifestEnvironment selects the immutable manifest for the
// requested environment. When the plan has only the other environment, it
// creates a sealed copy of the same candidate release and atomically pins it.
func PinPlanReleaseManifestEnvironment(
	ctx context.Context,
	pool *pgxpool.Pool,
	workspaceID, projectID, planID pgtype.UUID,
	environment string,
) (ReleaseManifest, error) {
	if environment != "staging" && environment != "production" {
		return ReleaseManifest{}, errors.New("release manifest environment must be staging or production")
	}
	current, err := LoadPlanReleaseManifest(ctx, pool, workspaceID, projectID, planID)
	if err != nil {
		return ReleaseManifest{}, err
	}
	if current.Environment == environment {
		return current, nil
	}

	rows, err := pool.Query(ctx, `
		SELECT manifest_json
		FROM autonomous_project_release_manifest
		WHERE workspace_id=$1 AND project_id=$2 AND plan_id=$3
		  AND plan_revision=$4 AND plan_digest=$5 AND environment=$6
		  AND change_set_id=$7 AND commit_sha=$8 AND tree_sha=$9
		  AND builder=$10 AND outputs=$11 AND artifact_digest=$12 AND artifact_storage_key=$13
		ORDER BY created_at DESC
		LIMIT 20
	`, workspaceID, projectID, planID, current.PlanRevision, current.PlanDigest, environment,
		current.ChangeSetID, current.CommitSHA, current.TreeSHA, current.Builder, current.Outputs,
		current.ArtifactDigest, current.ArtifactStorageKey)
	if err != nil {
		return ReleaseManifest{}, fmt.Errorf("query immutable %s release manifest: %w", environment, err)
	}
	var candidate *ReleaseManifest
	for rows.Next() {
		var encoded []byte
		if err := rows.Scan(&encoded); err != nil {
			rows.Close()
			return ReleaseManifest{}, fmt.Errorf("scan immutable %s release manifest: %w", environment, err)
		}
		var existing ReleaseManifest
		if err := json.Unmarshal(encoded, &existing); err != nil {
			rows.Close()
			return ReleaseManifest{}, fmt.Errorf("decode immutable %s release manifest: %w", environment, err)
		}
		if err := existing.Validate(); err != nil {
			rows.Close()
			return ReleaseManifest{}, fmt.Errorf("validate immutable %s release manifest: %w", environment, err)
		}
		if sameReleaseManifestCandidate(current, existing) {
			copy := existing
			candidate = &copy
			break
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ReleaseManifest{}, fmt.Errorf("read immutable %s release manifests: %w", environment, err)
	}
	rows.Close()
	if candidate == nil {
		copy := current
		copy.Environment = environment
		copy.ManifestID = uuid.NewSHA1(uuid.NameSpaceURL, []byte(strings.Join([]string{
			"multica-release-manifest", copy.WorkspaceID, copy.ProjectID, copy.PlanID,
			fmt.Sprint(copy.PlanRevision), copy.PlanDigest, copy.ChangeSetID, copy.CommitSHA,
			copy.TreeSHA, copy.ArtifactDigest, environment, strings.Join(copy.QualityGateRunIDs, ","),
		}, ":"))).String()
		copy.ManifestDigest = ""
		sealed, err := SealReleaseManifest(copy)
		if err != nil {
			return ReleaseManifest{}, fmt.Errorf("seal %s release manifest: %w", environment, err)
		}
		if err := persistReleaseManifest(ctx, pool, sealed); err != nil {
			return ReleaseManifest{}, fmt.Errorf("persist %s release manifest: %w", environment, err)
		}
		return LoadPlanReleaseManifest(ctx, pool, workspaceID, projectID, planID)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return ReleaseManifest{}, fmt.Errorf("begin %s release manifest pin: %w", environment, err)
	}
	defer tx.Rollback(ctx)
	candidateID, err := util.ParseUUID(candidate.ManifestID)
	if err != nil {
		return ReleaseManifest{}, errors.New("immutable release manifest has an invalid UUID identity")
	}
	tag, err := tx.Exec(ctx, `
		UPDATE autonomous_project_plan
		SET release_manifest_id=$4, release_manifest_digest=$5, updated_at=now()
		WHERE id=$1 AND workspace_id=$2 AND project_id=$3
		  AND revision=$6 AND plan_digest=$7
	`, planID, workspaceID, projectID, candidateID, candidate.ManifestDigest,
		candidate.PlanRevision, candidate.PlanDigest)
	if err != nil {
		return ReleaseManifest{}, fmt.Errorf("pin immutable %s release manifest: %w", environment, err)
	}
	if tag.RowsAffected() != 1 {
		return ReleaseManifest{}, errors.New("release plan changed before environment manifest could be pinned")
	}
	if err := tx.Commit(ctx); err != nil {
		return ReleaseManifest{}, fmt.Errorf("commit %s release manifest pin: %w", environment, err)
	}
	return *candidate, nil
}

func sameReleaseManifestCandidate(left, right ReleaseManifest) bool {
	if left.WorkspaceID != right.WorkspaceID || left.ProjectID != right.ProjectID || left.PlanID != right.PlanID ||
		left.ChangeSetID != right.ChangeSetID || left.PlanRevision != right.PlanRevision || left.PlanDigest != right.PlanDigest ||
		!strings.EqualFold(left.CommitSHA, right.CommitSHA) || !strings.EqualFold(left.TreeSHA, right.TreeSHA) ||
		left.Builder != right.Builder || left.ArtifactDigest != right.ArtifactDigest || left.ArtifactStorageKey != right.ArtifactStorageKey ||
		len(left.Outputs) != len(right.Outputs) || len(left.QualityGateRunIDs) != len(right.QualityGateRunIDs) {
		return false
	}
	for i := range left.Outputs {
		if left.Outputs[i] != right.Outputs[i] {
			return false
		}
	}
	leftRuns := append([]string(nil), left.QualityGateRunIDs...)
	rightRuns := append([]string(nil), right.QualityGateRunIDs...)
	sort.Strings(leftRuns)
	sort.Strings(rightRuns)
	for i := range leftRuns {
		if leftRuns[i] != rightRuns[i] {
			return false
		}
	}
	return true
}
