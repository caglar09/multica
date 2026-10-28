package workflowruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/projectorchestration"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type synchronousBrainFixture struct {
	ctx              context.Context
	pool             *pgxpool.Pool
	workspaceID      pgtype.UUID
	projectID        pgtype.UUID
	planID           pgtype.UUID
	predecessorID    pgtype.UUID
	dependentID      pgtype.UUID
	predecessorIssue db.Issue
	dependentIssue   db.Issue
	task             db.AgentTaskQueue
	agentID          pgtype.UUID
	planFixture      *testutil.Fixture
}

func newSynchronousBrainFixture(t *testing.T) *synchronousBrainFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	dbURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
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
	userID := base.User(t, "Synchronous brain test", "brain-sync-"+uuid.NewString()+"@example.invalid")
	workspaceIDText := base.Workspace(t, "Synchronous brain test", "brain-sync-"+uuid.NewString())
	base.Member(t, workspaceIDText, userID, "owner")
	fixture := testutil.New(pool, workspaceIDText, userID)
	projectIDText := fixture.Project(t, "Synchronous brain project")
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		for _, table := range []string{
			"autonomous_project_context_compilation",
			"autonomous_project_brain_snapshot",
			"autonomous_project_brain_entry",
			"autonomous_project_artifact",
			"autonomous_control_plane_job",
			"autonomous_project_brain_state",
			"autonomous_project_budget",
		} {
			_, _ = pool.Exec(cleanupCtx, "DELETE FROM "+table+" WHERE project_id=$1", projectIDText)
		}
	})
	projectID, err := util.ParseUUID(projectIDText)
	if err != nil {
		t.Fatal(err)
	}
	workspaceID, err := util.ParseUUID(workspaceIDText)
	if err != nil {
		t.Fatal(err)
	}
	agentIDText := fixture.Agent(t, "Synchronous brain agent", "")
	agentID, err := util.ParseUUID(agentIDText)
	if err != nil {
		t.Fatal(err)
	}
	predecessorIssueIDText := fixture.Issue(t, "Choose PostgreSQL pgvector memory", testutil.Cols{
		"project_id": projectIDText, "description": "Choose the vector storage and recall technology.",
	})
	dependentIssueIDText := fixture.Issue(t, "Implement PostgreSQL pgvector retrieval", testutil.Cols{
		"project_id": projectIDText, "description": "Use the selected PostgreSQL pgvector memory design.",
	})
	predecessorIssueID, err := util.ParseUUID(predecessorIssueIDText)
	if err != nil {
		t.Fatal(err)
	}
	dependentIssueID, err := util.ParseUUID(dependentIssueIDText)
	if err != nil {
		t.Fatal(err)
	}
	predecessorIssue, err := db.New(pool).GetIssue(ctx, predecessorIssueID)
	if err != nil {
		t.Fatalf("load predecessor issue: %v", err)
	}
	dependentIssue, err := db.New(pool).GetIssue(ctx, dependentIssueID)
	if err != nil {
		t.Fatalf("load dependent issue: %v", err)
	}
	policy, err := json.Marshal(projectorchestration.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	planIDText := fixture.Insert(t, "autonomous_project_plan", testutil.Cols{
		"workspace_id":    workspaceIDText,
		"project_id":      projectIDText,
		"revision":        1,
		"source_revision": "brain-sync-" + uuid.NewString(),
		"planner_name":    "test",
		"goal":            "Select and implement project memory retrieval.",
		"specification":   testutil.Raw(`'{"requirements":["Use PostgreSQL pgvector"]}'::jsonb`),
		"policy":          policy,
		"plan_digest":     strings.Repeat("0", 64),
		"status":          "active",
	})
	planID, err := util.ParseUUID(planIDText)
	if err != nil {
		t.Fatal(err)
	}
	predecessorIDText := fixture.Insert(t, "autonomous_project_plan_node", testutil.Cols{
		"plan_id": planIDText, "workspace_id": workspaceIDText, "project_id": projectIDText,
		"node_key": "architecture", "kind": "architecture", "title": "Choose PostgreSQL pgvector memory",
		"description": "Select vector storage.", "status": "running", "required_role_family": "architecture",
		"acceptance_criteria":   testutil.Raw(`'["select the vector store"]'::jsonb`),
		"materialized_issue_id": predecessorIssueIDText, "assigned_agent_id": agentIDText,
	})
	predecessorID, err := util.ParseUUID(predecessorIDText)
	if err != nil {
		t.Fatal(err)
	}
	dependentIDText := fixture.Insert(t, "autonomous_project_plan_node", testutil.Cols{
		"plan_id": planIDText, "workspace_id": workspaceIDText, "project_id": projectIDText,
		"node_key": "implementation", "kind": "implementation", "title": "Implement PostgreSQL pgvector retrieval",
		"description": "Use the approved project memory design.", "status": "pending", "required_role_family": "backend",
		"acceptance_criteria":   testutil.Raw(`'["recall the architecture decision"]'::jsonb`),
		"materialized_issue_id": dependentIssueIDText, "assigned_agent_id": agentIDText,
	})
	dependentID, err := util.ParseUUID(dependentIDText)
	if err != nil {
		t.Fatal(err)
	}
	fixture.Insert(t, "autonomous_project_plan_edge", testutil.Cols{
		"plan_id": planIDText, "workspace_id": workspaceIDText, "project_id": projectIDText,
		"from_node_key": "architecture", "to_node_key": "implementation", "dependency_type": "hard",
	})
	if _, err := pool.Exec(ctx, `
		UPDATE autonomous_project_plan
		SET plan_digest=autonomous_project_plan_digest(id), approved_at=now(), approved_by='brain sync test'
		WHERE id=$1
	`, planID); err != nil {
		t.Fatalf("approve test plan: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO autonomous_project_budget (workspace_id,project_id,max_parallel_nodes,max_total_attempts)
		VALUES ($1,$2,4,100)
		ON CONFLICT (project_id) DO UPDATE SET max_parallel_nodes=4,max_total_attempts=100
	`, workspaceID, projectID); err != nil {
		t.Fatalf("insert project budget: %v", err)
	}
	decisionResult := []byte(`{"summary":"Choose PostgreSQL pgvector memory","decisions":["Use PostgreSQL with pgvector for vector recall; keep remote embeddings opt-in."],"architecture_choices":["Use HNSW cosine ranking with the PostgreSQL pgvector architecture choice."],"artifacts":[],"changed_files":[],"commit_sha":"","diff":"","tests":[],"findings":[],"risks":["Keep remote embeddings opt-in."],"blockers":[]}`)
	taskIDText := fixture.Task(t, agentIDText, testutil.Cols{
		"issue_id": predecessorIssueIDText,
		"status":   "completed", "completed_at": testutil.Raw("now()"), "result": decisionResult,
	})
	taskID, err := util.ParseUUID(taskIDText)
	if err != nil {
		t.Fatal(err)
	}
	return &synchronousBrainFixture{
		ctx: ctx, pool: pool, workspaceID: workspaceID, projectID: projectID, planID: planID,
		predecessorID: predecessorID, dependentID: dependentID,
		predecessorIssue: predecessorIssue, dependentIssue: dependentIssue, agentID: agentID,
		planFixture: fixture,
		task:        db.AgentTaskQueue{ID: taskID, AgentID: agentID, IssueID: predecessorIssueID, Status: "completed", Result: decisionResult},
	}
}

func disableBrainEmbeddingRequests(t *testing.T) *atomic.Int64 {
	t.Helper()
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	t.Setenv("MULTICA_PROJECT_BRAIN_EMBEDDING_ENABLED", "false")
	t.Setenv("MULTICA_PROJECT_BRAIN_EMBEDDING_API_KEY", "")
	t.Setenv("MULTICA_PROJECT_BRAIN_EMBEDDING_BASE_URL", server.URL)
	return &requests
}

func compileSynchronousBrainContext(t *testing.T, fixture *synchronousBrainFixture) projectorchestration.ContextPackage {
	t.Helper()
	tx, err := fixture.pool.Begin(fixture.ctx)
	if err != nil {
		t.Fatalf("begin context compilation: %v", err)
	}
	defer tx.Rollback(fixture.ctx)
	workflowActionID := pgtype.UUID{Bytes: [16]byte(uuid.New()), Valid: true}
	pkg, err := projectorchestration.CompileWorkflowContext(
		fixture.ctx, tx, fixture.dependentIssue, fixture.agentID, workflowActionID, "implementation",
	)
	if err != nil {
		t.Fatalf("compile dependent workflow context: %v", err)
	}
	if err := tx.Commit(fixture.ctx); err != nil {
		t.Fatalf("commit context compilation: %v", err)
	}
	return pkg
}

func TestSynchronousBrain_ImmediateDecisionAvailability(t *testing.T) {
	fixture := newSynchronousBrainFixture(t)
	requests := disableBrainEmbeddingRequests(t)
	runtime := &Runtime{pool: fixture.pool, projectStore: projectorchestration.NewStore(fixture.pool)}
	before, err := runtime.projectStore.CurrentBrainRevision(fixture.ctx, fixture.workspaceID, fixture.projectID)
	if err != nil {
		t.Fatalf("read initial brain revision: %v", err)
	}
	if err := runtime.enqueueBrainLearning(fixture.ctx, fixture.task, fixture.predecessorIssue); err != nil {
		t.Fatalf("enqueue asynchronous learning evidence: %v", err)
	}
	if _, err := runtime.recordProjectTaskArtifact(fixture.ctx, fixture.task, fixture.predecessorIssue); err != nil {
		t.Fatalf("persist architecture completion: %v", err)
	}
	after, err := runtime.projectStore.CurrentBrainRevision(fixture.ctx, fixture.workspaceID, fixture.projectID)
	if err != nil {
		t.Fatalf("read updated brain revision: %v", err)
	}
	if after != before+1 {
		t.Fatalf("brain revision advanced from %d to %d, want one synchronous increment", before, after)
	}
	var entryCount int
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT count(*) FROM autonomous_project_brain_entry
		WHERE workspace_id=$1 AND project_id=$2 AND node_id=$3
		  AND source_type='task_completion_decision'
	`, fixture.workspaceID, fixture.projectID, fixture.predecessorID).Scan(&entryCount); err != nil {
		t.Fatalf("count synchronous decision entries: %v", err)
	}
	if entryCount != 1 {
		t.Fatalf("synchronous architecture decision entries = %d, want 1", entryCount)
	}
	pkg := compileSynchronousBrainContext(t, fixture)
	if pkg.BrainRevision != after {
		t.Fatalf("compiled brain revision = %d, want %d", pkg.BrainRevision, after)
	}
	found := false
	for _, section := range pkg.Sections {
		if section.Name != "brain" {
			continue
		}
		for _, item := range section.Items {
			found = found || strings.Contains(item.Text, "Use PostgreSQL with pgvector for vector recall") &&
				strings.Contains(item.Text, "Use HNSW cosine ranking")
		}
	}
	if !found {
		t.Fatalf("compiled context did not contain the completed architecture decision: %+v", pkg.Sections)
	}
	var jobStatus string
	var jobPayload []byte
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT status,payload FROM autonomous_control_plane_job
		WHERE workspace_id=$1 AND project_id=$2 AND job_type='brain_learning'
		  AND idempotency_key=$3
	`, fixture.workspaceID, fixture.projectID, "task:"+util.UUIDToString(fixture.task.ID)).Scan(&jobStatus, &jobPayload); err != nil {
		t.Fatalf("load asynchronous learning job: %v", err)
	}
	if jobStatus != "pending" {
		t.Fatalf("asynchronous learning job status = %q, want pending", jobStatus)
	}
	var queuedEvidence struct {
		Evidence struct {
			Result map[string]any `json:"result"`
		} `json:"evidence"`
	}
	if err := json.Unmarshal(jobPayload, &queuedEvidence); err != nil {
		t.Fatalf("decode asynchronous learning evidence: %v", err)
	}
	for _, field := range []string{"decisions", "architecture_choices"} {
		if _, ok := queuedEvidence.Evidence.Result[field]; ok {
			t.Fatalf("synchronously persisted %s were duplicated in async evidence: %+v", field, queuedEvidence.Evidence.Result)
		}
	}
	if _, ok := queuedEvidence.Evidence.Result["risks"]; !ok {
		t.Fatalf("general completion risks were not preserved in async evidence: %+v", queuedEvidence.Evidence.Result)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("unconfigured embeddings made %d HTTP requests, want none", got)
	}
}

func TestBrainRaceCondition_Prevented(t *testing.T) {
	fixture := newSynchronousBrainFixture(t)
	requests := disableBrainEmbeddingRequests(t)
	runtime := &Runtime{pool: fixture.pool, projectStore: projectorchestration.NewStore(fixture.pool)}
	if _, err := fixture.pool.Exec(fixture.ctx, `
		UPDATE autonomous_project_plan_node SET status='ready' WHERE id=$1
	`, fixture.dependentID); err != nil {
		t.Fatalf("seed stale ready dependent node: %v", err)
	}
	if err := runtime.ensurePredecessorBrainDecisionsCurrent(fixture.ctx, fixture.workspaceID, fixture.projectID, "implementation"); err == nil {
		t.Fatal("dependent node passed dispatch guard while its predecessor was still running")
	}
	artifactContent, err := json.Marshal(map[string]any{
		"task_id": util.UUIDToString(fixture.task.ID),
		"result":  json.RawMessage(fixture.task.Result),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `
		INSERT INTO autonomous_project_artifact (
			workspace_id,project_id,plan_id,node_id,artifact_type,name,content,
			producer_agent_id,status,valid,artifact_revision
		) VALUES ($1,$2,$3,$4,'architecture','task result',$5,$6,'active',TRUE,1)
	`, fixture.workspaceID, fixture.projectID, fixture.planID, fixture.predecessorID, artifactContent, fixture.agentID); err != nil {
		t.Fatalf("seed completed predecessor artifact: %v", err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `
		UPDATE autonomous_project_plan_node SET status='completed', completed_at=now() WHERE id=$1
	`, fixture.predecessorID); err != nil {
		t.Fatalf("complete predecessor fixture without brain write: %v", err)
	}
	if err := runtime.ensurePredecessorBrainDecisionsCurrent(fixture.ctx, fixture.workspaceID, fixture.projectID, "implementation"); err == nil {
		t.Fatal("dependent node passed dispatch guard with predecessor decisions missing from the brain")
	}
	if err := runtime.enqueueBrainLearning(fixture.ctx, fixture.task, fixture.predecessorIssue); err != nil {
		t.Fatalf("enqueue decision learning evidence: %v", err)
	}
	if _, err := runtime.recordProjectTaskArtifact(fixture.ctx, fixture.task, fixture.predecessorIssue); err != nil {
		t.Fatalf("repair completed decision memory in artifact transaction: %v", err)
	}
	if err := runtime.ensurePredecessorBrainDecisionsCurrent(fixture.ctx, fixture.workspaceID, fixture.projectID, "implementation"); err != nil {
		t.Fatalf("dependent node remained blocked after synchronous brain write: %v", err)
	}
	ready, err := runtime.projectStore.ListReadyNodes(fixture.ctx, fixture.workspaceID, fixture.projectID, 10)
	if err != nil {
		t.Fatalf("list ready dependent nodes: %v", err)
	}
	if len(ready) != 1 || ready[0].Key != "implementation" {
		t.Fatalf("ready nodes = %+v, want implementation after brain write", ready)
	}
	pkg := compileSynchronousBrainContext(t, fixture)
	revision, err := runtime.projectStore.CurrentBrainRevision(fixture.ctx, fixture.workspaceID, fixture.projectID)
	if err != nil {
		t.Fatalf("read current brain revision: %v", err)
	}
	if pkg.BrainRevision != revision || pkg.BrainRevision == 0 {
		t.Fatalf("dependent context brain revision = %d, current revision = %d", pkg.BrainRevision, revision)
	}
	generalTaskIDText := fixture.planFixture.Task(t, util.UUIDToString(fixture.agentID), testutil.Cols{
		"issue_id": util.UUIDToString(fixture.predecessorIssue.ID),
		"status":   "completed", "completed_at": testutil.Raw("now()"),
		"result": []byte(`{"observation":"` + strings.Repeat("A general project observation remains asynchronous. ", 8) + `"}`),
	})
	generalTaskID, err := util.ParseUUID(generalTaskIDText)
	if err != nil {
		t.Fatal(err)
	}
	generalTask := db.AgentTaskQueue{
		ID: generalTaskID, AgentID: fixture.agentID, IssueID: fixture.predecessorIssue.ID,
		Status: "completed", Result: []byte(`{"observation":"` + strings.Repeat("A general project observation remains asynchronous. ", 8) + `"}`),
	}
	if err := runtime.enqueueBrainLearning(fixture.ctx, generalTask, fixture.predecessorIssue); err != nil {
		t.Fatalf("enqueue general observation: %v", err)
	}
	var generalStatus string
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT status FROM autonomous_control_plane_job
		WHERE workspace_id=$1 AND project_id=$2 AND job_type='brain_learning'
		  AND idempotency_key=$3
	`, fixture.workspaceID, fixture.projectID, "task:"+generalTaskIDText).Scan(&generalStatus); err != nil {
		t.Fatalf("load asynchronous observation job: %v", err)
	}
	if generalStatus != "pending" {
		t.Fatalf("general observation job status = %q, want pending", generalStatus)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("unconfigured embeddings made %d HTTP requests, want none", got)
	}
}
