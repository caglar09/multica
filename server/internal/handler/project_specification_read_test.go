package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestProjectSpecificationApprovalReadSurfaces(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	project := createProjectPermissionTestProject(t, "project specification approval read")
	proposal := addSpecificationApprovalFixture(t, project.ID, "project-leader-specification:"+uuid.NewString())

	type projectSpecificationResponse struct {
		Specification struct {
			RevisionID      string `json:"revision_id"`
			Digest          string `json:"digest"`
			Status          string `json:"status"`
			IsProposal      bool   `json:"is_proposal"`
			ChangeRequestID string `json:"change_request_id"`
			Specification   struct {
				Goal         string `json:"goal"`
				Requirements []struct {
					ID string `json:"id"`
				} `json:"requirements"`
			} `json:"specification"`
			ApprovalHistory []struct {
				Decision string `json:"decision"`
				Origin   string `json:"origin"`
			} `json:"approval_history"`
		} `json:"specification"`
	}
	read := func() projectSpecificationResponse {
		req := withURLParam(newRequestAs(testUserID, http.MethodGet, "/api/projects/"+project.ID+"/autonomous", nil), "id", project.ID)
		w := httptest.NewRecorder()
		testHandler.GetProjectAutonomousControlCenter(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("read autonomous project status = %d, want 200: %s", w.Code, w.Body.String())
		}
		var body projectSpecificationResponse
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode autonomous project response: %v", err)
		}
		return body
	}

	pending := read().Specification
	if pending.Status != "awaiting_approval" || !pending.IsProposal {
		t.Fatalf("pending proposal state = (%q, %v), want (awaiting_approval, true)", pending.Status, pending.IsProposal)
	}
	if pending.ChangeRequestID != proposal.changeRequestID || pending.Digest != proposal.digest {
		t.Fatalf("pending proposal identity/digest = (%q, %q), want (%q, %q)", pending.ChangeRequestID, pending.Digest, proposal.changeRequestID, proposal.digest)
	}
	if pending.RevisionID != proposal.revisionID || pending.Specification.Goal != "Ship an approved capability" || len(pending.Specification.Requirements) != 1 || pending.Specification.Requirements[0].ID != "REQ-1" {
		t.Fatalf("pending proposal payload = %+v, want current revision and requirement details", pending)
	}
	if len(pending.ApprovalHistory) != 0 {
		t.Fatalf("unapproved proposal has %d approval events, want none", len(pending.ApprovalHistory))
	}

	approved := callProjectSpecificationDecision(t, testHandler.ApproveProjectSpecification, project.ID, proposal.changeRequestID, proposal.digest, "", testUserID)
	if approved.Code != http.StatusOK {
		t.Fatalf("approve proposal status = %d, want 200: %s", approved.Code, approved.Body.String())
	}
	current := read().Specification
	if current.Status != "approved" || current.IsProposal || current.RevisionID != proposal.revisionID {
		t.Fatalf("approved specification state = (%q, %v, %q), want approved head %q", current.Status, current.IsProposal, current.RevisionID, proposal.revisionID)
	}
	if len(current.ApprovalHistory) != 1 || current.ApprovalHistory[0].Decision != "approved" || current.ApprovalHistory[0].Origin != "local" {
		t.Fatalf("approval timeline = %+v, want the local approval event", current.ApprovalHistory)
	}

	t.Log("PROJECT_SPECIFICATION_APPROVAL_READ_GATE_PASS")
}
