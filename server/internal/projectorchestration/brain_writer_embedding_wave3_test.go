package projectorchestration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestProjectBrainWritersPersistEmbeddings(t *testing.T) {
	server, requests, inputs := wave3BrainEmbeddingFakeServer(t)
	defer server.Close()
	t.Setenv("MULTICA_PROJECT_BRAIN_EMBEDDING_ENABLED", "true")
	t.Setenv("MULTICA_PROJECT_BRAIN_EMBEDDING_API_KEY", "test-key")
	t.Setenv("MULTICA_PROJECT_BRAIN_EMBEDDING_BASE_URL", server.URL)

	fixture := newWave3ChangeFixture(t)
	appendWave3DeltaToUnapprovedPlan(t, fixture,
		NodeSpec{Key: "runtime-discovered", Kind: NodeImplementation, Title: "Runtime discovered login requirement", Description: "Retain a discovered authentication invariant", AcceptanceCriteria: []string{"Reject refresh token reuse."}, Risk: RiskLow, MaxAttempts: 2},
		"Runtime discovered authentication invariant",
		map[string]any{"text": "Refresh token rotation must reject reused tokens."},
	)
	proposal := fixture.proposal("embedding-"+uuid.NewString(), "Expanded embedding test scope")
	result, err := fixture.store.ApplyProjectLeaderProposal(fixture.ctx, fixture.workspaceID, fixture.projectID, proposal, "Project Manager", "test-model")
	if err != nil {
		t.Fatalf("record project leader proposal: %v", err)
	}
	if err := fixture.store.AdvanceChangeRequest(fixture.ctx, mustProjectUUID(result.ChangeRequest.ID), ChangeApproved, ""); err != nil {
		t.Fatalf("approve proposal: %v", err)
	}
	operations, err := CompileProjectLeaderProposal(proposal, fixture.nodes)
	if err != nil {
		t.Fatalf("compile proposal operations: %v", err)
	}
	if _, err := fixture.store.ApplyChangePlanMutation(fixture.ctx, fixture.workspaceID, fixture.projectID, mustProjectUUID(result.ChangeRequest.ID), operations, "Project Manager", "test-model"); err != nil {
		t.Fatalf("apply proposal: %v", err)
	}
	var planRows, planEmbedded, decisionRows, decisionEmbedded, deltaRows, deltaEmbedded int
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT
			count(*) FILTER (WHERE source_type='project_plan'),
			count(*) FILTER (WHERE source_type='project_plan' AND embedding IS NOT NULL),
			count(*) FILTER (WHERE source_type='project_leader'),
			count(*) FILTER (WHERE source_type='project_leader' AND embedding IS NOT NULL),
			count(*) FILTER (WHERE source_type='discovered_issue'),
			count(*) FILTER (WHERE source_type='discovered_issue' AND embedding IS NOT NULL)
		FROM autonomous_project_brain_entry
		WHERE workspace_id=$1 AND project_id=$2
	`, fixture.workspaceID, fixture.projectID).Scan(&planRows, &planEmbedded, &decisionRows, &decisionEmbedded, &deltaRows, &deltaEmbedded); err != nil {
		t.Fatalf("count project brain embeddings: %v", err)
	}
	if planRows < 4 || planEmbedded != planRows || decisionRows != 1 || decisionEmbedded != 1 || deltaRows != 1 || deltaEmbedded != 1 {
		t.Fatalf("brain embedding counts plan=%d/%d decision=%d/%d delta=%d/%d; want every persisted writer row embedded", planEmbedded, planRows, decisionEmbedded, decisionRows, deltaEmbedded, deltaRows)
	}
	if got, want := requests.Load(), int32(planRows+decisionRows+deltaRows); got != want {
		t.Fatalf("fake embedding requests = %d, want %d for persisted memory rows", got, want)
	}
	joined := strings.Join(inputs(), "\n")
	for _, want := range []string{"Expanded embedding test scope", "Project Leader decision", "Runtime discovered authentication invariant", "reused tokens"} {
		if !strings.Contains(joined, want) {
			t.Errorf("embedding input does not contain %q", want)
		}
	}
}

func TestBrainWritersRemainLocalWhenEmbeddingsDisabled(t *testing.T) {
	server, requests, _ := wave3BrainEmbeddingFakeServer(t)
	defer server.Close()
	t.Setenv("MULTICA_PROJECT_BRAIN_EMBEDDING_ENABLED", "false")
	t.Setenv("MULTICA_PROJECT_BRAIN_EMBEDDING_API_KEY", "")
	t.Setenv("MULTICA_PROJECT_BRAIN_EMBEDDING_BASE_URL", server.URL)

	fixture := newWave3ChangeFixture(t)
	appendWave3DeltaToUnapprovedPlan(t, fixture,
		NodeSpec{Key: "runtime-discovered", Kind: NodeImplementation, Title: "Runtime discovered local-only node", Description: "Keep the discovered task in local memory", AcceptanceCriteria: []string{"Keep discovered memory in the database."}, Risk: RiskLow, MaxAttempts: 2},
		"Runtime discovered local-only fact",
		map[string]any{"text": "This discovery must remain in the local database."},
	)
	proposal := fixture.proposal("disabled-embedding-"+uuid.NewString(), "Expanded local-only scope")
	result, err := fixture.store.ApplyProjectLeaderProposal(fixture.ctx, fixture.workspaceID, fixture.projectID, proposal, "Project Manager", "test-model")
	if err != nil {
		t.Fatalf("record project leader proposal: %v", err)
	}
	if err := fixture.store.AdvanceChangeRequest(fixture.ctx, mustProjectUUID(result.ChangeRequest.ID), ChangeApproved, ""); err != nil {
		t.Fatalf("approve proposal: %v", err)
	}
	operations, err := CompileProjectLeaderProposal(proposal, fixture.nodes)
	if err != nil {
		t.Fatalf("compile proposal operations: %v", err)
	}
	if _, err := fixture.store.ApplyChangePlanMutation(fixture.ctx, fixture.workspaceID, fixture.projectID, mustProjectUUID(result.ChangeRequest.ID), operations, "Project Manager", "test-model"); err != nil {
		t.Fatalf("apply proposal: %v", err)
	}
	var total, embedded int
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT count(*), count(*) FILTER (WHERE embedding IS NOT NULL)
		FROM autonomous_project_brain_entry
		WHERE workspace_id=$1 AND project_id=$2
	`, fixture.workspaceID, fixture.projectID).Scan(&total, &embedded); err != nil {
		t.Fatalf("count local project brain entries: %v", err)
	}
	if total < 6 || embedded != 0 {
		t.Fatalf("local brain entries = %d total, %d embedded; want at least 6 entries and no vectors", total, embedded)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("disabled embedding provider received %d requests, want 0", got)
	}
}

func appendWave3DeltaToUnapprovedPlan(t *testing.T, fixture wave3ChangeFixture, node NodeSpec, subject string, content any) {
	t.Helper()
	planID := mustProjectUUID(fixture.initialPlan.ID)
	issueID := wave3AdditionalIssue(t, fixture)
	var approvedAt pgtype.Timestamptz
	var approvedBy pgtype.Text
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT approved_at,approved_by FROM autonomous_project_plan WHERE id=$1`, planID).Scan(&approvedAt, &approvedBy); err != nil {
		t.Fatalf("load initial plan approval state: %v", err)
	}
	if !approvedAt.Valid {
		t.Fatal("fixture initial plan must start approved before creating the unapproved delta case")
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE autonomous_project_plan SET approved_at=NULL,approved_by=NULL WHERE id=$1 AND status='active'`, planID); err != nil {
		t.Fatalf("make initial plan active and unapproved for delta: %v", err)
	}
	defer func() {
		if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE autonomous_project_plan SET approved_at=$2,approved_by=$3 WHERE id=$1`, planID, approvedAt, approvedBy); err != nil {
			t.Errorf("restore initial plan approval state: %v", err)
		}
	}()
	if err := fixture.store.AppendPlanDelta(
		fixture.ctx,
		fixture.workspaceID,
		fixture.projectID,
		planID,
		[]NodeSpec{node},
		nil,
		node.Key,
		issueID,
		"developer",
		pgtype.UUID{},
		"",
		subject,
		content,
	); err != nil {
		t.Fatalf("append discovered project plan delta: %v", err)
	}
}

func wave3AdditionalIssue(t *testing.T, fixture wave3ChangeFixture) pgtype.UUID {
	t.Helper()
	var userID string
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT creator_id::text FROM issue WHERE id=$1::uuid`, fixture.issueID).Scan(&userID); err != nil {
		t.Fatalf("load fixture issue creator: %v", err)
	}
	issueFixture := testutil.New(fixture.pool, uuid.UUID(fixture.workspaceID.Bytes).String(), userID)
	id := issueFixture.Issue(t, "Runtime discovered plan delta")
	return wave3UUID(t, id)
}

func wave3BrainEmbeddingFakeServer(t *testing.T) (*httptest.Server, *atomic.Int32, func() []string) {
	t.Helper()
	var requests atomic.Int32
	var seenMu sync.Mutex
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/embeddings" {
			t.Errorf("embedding request = %s %s, want POST /embeddings", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("embedding authorization = %q, want test key", r.Header.Get("Authorization"))
		}
		var input struct {
			Model      string `json:"model"`
			Input      string `json:"input"`
			Dimensions int    `json:"dimensions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Errorf("decode embedding request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if input.Model != "text-embedding-3-small" || input.Dimensions != 1536 {
			t.Errorf("embedding model/dimensions = %q/%d", input.Model, input.Dimensions)
		}
		seenMu.Lock()
		seen = append(seen, input.Input)
		seenMu.Unlock()
		vector := strings.TrimSuffix(strings.Repeat("0,", 1536), ",")
		_, _ = fmt.Fprintf(w, `{"data":[{"index":0,"embedding":[%s]}]}`, vector)
	}))
	return server, &requests, func() []string {
		seenMu.Lock()
		defer seenMu.Unlock()
		return append([]string(nil), seen...)
	}
}
