package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestAutonomousPauseResumePersistsControlState(t *testing.T) {
	project := createProjectPermissionTestProject(t, "autonomous pause resume project")
	_, _ = testPool.Exec(context.Background(), `DELETE FROM autonomous_project_control WHERE project_id = $1`, project.ID)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM autonomous_project_control WHERE project_id = $1`, project.ID)
	})

	pauseReq := withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/autonomous/pause", nil), "id", project.ID)
	pauseW := httptest.NewRecorder()
	testHandler.PauseProjectAutonomous(pauseW, pauseReq)
	if pauseW.Code != http.StatusOK {
		t.Fatalf("pause status = %d, want 200: %s", pauseW.Code, pauseW.Body.String())
	}

	var paused bool
	if err := testPool.QueryRow(context.Background(), `
		SELECT paused FROM autonomous_project_control WHERE project_id = $1
	`, project.ID).Scan(&paused); err != nil {
		t.Fatalf("read paused control state: %v", err)
	}
	if !paused {
		t.Fatal("pause endpoint returned success but persisted paused=false")
	}

	resumeReq := withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/autonomous/resume", nil), "id", project.ID)
	resumeW := httptest.NewRecorder()
	testHandler.ResumeProjectAutonomous(resumeW, resumeReq)
	if resumeW.Code != http.StatusOK {
		t.Fatalf("resume status = %d, want 200: %s", resumeW.Code, resumeW.Body.String())
	}
	if err := testPool.QueryRow(context.Background(), `
		SELECT paused FROM autonomous_project_control WHERE project_id = $1
	`, project.ID).Scan(&paused); err != nil {
		t.Fatalf("read resumed control state: %v", err)
	}
	if paused {
		t.Fatal("resume endpoint returned success but persisted paused=true")
	}
}

func TestStartProjectAutonomousPlanningRequiresExplicitHumanGate(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	project := createProjectPermissionTestProject(t, "autonomous planning gate project")
	_, err := testPool.Exec(context.Background(), `
		INSERT INTO autonomous_project_team_draft (
			project_id, workspace_id, plan, planner_name, status, confirmed_at, confirmed_by
		)
		VALUES ($1, $2, '{}'::jsonb, 'Mika', 'applied', now(), $3)
	`, project.ID, testWorkspaceID, testUserID)
	if err != nil {
		t.Fatalf("seed provisioned team draft: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM autonomous_project_team_draft WHERE project_id = $1`, project.ID)
	})

	req := withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/autonomous/planning/start", nil), "id", project.ID)
	w := httptest.NewRecorder()
	testHandler.StartProjectAutonomousPlanning(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("start planning status = %d, want 202: %s", w.Code, w.Body.String())
	}

	var startedAt pgtype.Timestamptz
	if err := testPool.QueryRow(context.Background(), `
		SELECT continuation_started_at
		FROM autonomous_project_team_draft
		WHERE project_id = $1
	`, project.ID).Scan(&startedAt); err != nil {
		t.Fatalf("read planning gate: %v", err)
	}
	if !startedAt.Valid {
		t.Fatal("planning gate did not persist the explicit start")
	}
}

func TestAutonomousRestartHandlerInvokesProjectScopedRecovery(t *testing.T) {
	project := createProjectPermissionTestProject(t, "autonomous restart project")

	previous := testHandler.AutonomousWorkflowRestart
	t.Cleanup(func() { testHandler.AutonomousWorkflowRestart = previous })

	called := false
	testHandler.AutonomousWorkflowRestart = func(ctx context.Context, workspaceID, projectID pgtype.UUID) error {
		called = true
		if uuidToString(workspaceID) != testWorkspaceID {
			t.Fatalf("restart workspace = %s, want %s", workspaceID.String(), testWorkspaceID)
		}
		if uuidToString(projectID) != project.ID {
			t.Fatalf("restart project = %s, want %s", projectID.String(), project.ID)
		}
		return nil
	}

	req := withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/autonomous/workflow/restart", nil), "id", project.ID)
	w := httptest.NewRecorder()
	testHandler.RestartProjectAutonomousWorkflow(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("restart status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if !called {
		t.Fatal("restart endpoint did not invoke runtime recovery")
	}
}


func TestAutonomousPauseRepairsStaleControlWorkspaceOwnership(t *testing.T) {
	project := createProjectPermissionTestProject(t, "autonomous stale control ownership project")
	_, _ = testPool.Exec(context.Background(), `DELETE FROM autonomous_project_control WHERE project_id = $1`, project.ID)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM autonomous_project_control WHERE project_id = $1`, project.ID)
	})

	var staleWorkspaceID pgtype.UUID
	if err := testPool.QueryRow(context.Background(), `SELECT gen_random_uuid()`).Scan(&staleWorkspaceID); err != nil {
		t.Fatalf("generate stale workspace id: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO autonomous_project_control (project_id, workspace_id, paused)
		VALUES ($1, $2, FALSE)
	`, project.ID, staleWorkspaceID); err != nil {
		t.Fatalf("seed stale control row: %v", err)
	}

	req := withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/autonomous/pause", nil), "id", project.ID)
	w := httptest.NewRecorder()
	testHandler.PauseProjectAutonomous(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("pause status = %d, want 200: %s", w.Code, w.Body.String())
	}

	var workspaceID pgtype.UUID
	var paused bool
	if err := testPool.QueryRow(context.Background(), `
		SELECT workspace_id, paused
		FROM autonomous_project_control
		WHERE project_id = $1
	`, project.ID).Scan(&workspaceID, &paused); err != nil {
		t.Fatalf("read repaired control state: %v", err)
	}
	if uuidToString(workspaceID) != testWorkspaceID {
		t.Fatalf("control workspace = %s, want %s", uuidToString(workspaceID), testWorkspaceID)
	}
	if !paused {
		t.Fatal("repaired control row did not persist paused=true")
	}
}

func TestAutonomousRestartReturnsStableRepairStageCode(t *testing.T) {
	project := createProjectPermissionTestProject(t, "autonomous restart diagnostic project")

	previous := testHandler.AutonomousWorkflowRestart
	t.Cleanup(func() { testHandler.AutonomousWorkflowRestart = previous })
	testHandler.AutonomousWorkflowRestart = func(context.Context, pgtype.UUID, pgtype.UUID) error {
		return errors.New("refresh project readiness after restart: injected failure")
	}

	req := withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/autonomous/workflow/restart", nil), "id", project.ID)
	w := httptest.NewRecorder()
	testHandler.RestartProjectAutonomousWorkflow(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("restart status = %d, want 500: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode restart error: %v", err)
	}
	if got := body["code"]; got != "autonomous_repair_readiness_failed" {
		t.Fatalf("repair error code = %q, want autonomous_repair_readiness_failed", got)
	}
}
