package handler

import (
	"context"
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

func TestAutonomousRestartHandlerInvokesProjectScopedRecovery(t *testing.T) {
	project := createProjectPermissionTestProject(t, "autonomous restart project")

	previous := testHandler.AutonomousWorkflowRestart
	t.Cleanup(func() { testHandler.AutonomousWorkflowRestart = previous })

	called := false
	testHandler.AutonomousWorkflowRestart = func(ctx context.Context, workspaceID, projectID pgtype.UUID) error {
		called = true
		if workspaceID.String() != testWorkspaceID {
			t.Fatalf("restart workspace = %s, want %s", workspaceID.String(), testWorkspaceID)
		}
		if projectID.String() != project.ID {
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
