package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	projectorchestration "github.com/multica-ai/multica/server/internal/projectorchestration"
)

type importedProjectAdoptionTarget struct {
	planID                  pgtype.UUID
	planRevision            int64
	planDigest              string
	specificationRevisionID pgtype.UUID
	specificationRevision   int64
	specificationDigest     string
}

// AdoptImportedProjectPlan records a local specification approval and import
// adoption event for the exact imported plan and current specification.
func (h *Handler) AdoptImportedProjectPlan(w http.ResponseWriter, r *http.Request) {
	projectID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "project id")
	if !ok {
		return
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace id")
	if !ok {
		return
	}
	userID, ok := h.requireAutonomousControlAdmin(w, r, workspaceID)
	if !ok {
		return
	}
	var request struct {
		SpecificationDigest string `json:"specification_digest"`
		PlanDigest          string `json:"plan_digest"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid imported project adoption request")
		return
	}
	if !validProjectSpecificationDigest(request.SpecificationDigest) || !validProjectSpecificationDigest(request.PlanDigest) {
		writeError(w, http.StatusBadRequest, "specification_digest and plan_digest must be lowercase SHA-256 digests")
		return
	}
	tx, ok := beginProjectSpecificationApprovalTx(w, r, h, workspaceID, projectID, userID)
	if !ok {
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var lockedProject pgtype.UUID
	if err := tx.QueryRow(r.Context(), `SELECT id FROM project WHERE id=$1 AND workspace_id=$2 FOR UPDATE`, projectID, workspaceID).Scan(&lockedProject); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "project not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to lock project for adoption")
		}
		return
	}
	target, err := loadImportedProjectAdoptionTargetTx(r, tx, workspaceID, projectID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "project has no current imported plan and specification to adopt")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to load imported project plan")
		}
		return
	}
	if request.SpecificationDigest != target.specificationDigest || request.PlanDigest != target.planDigest {
		writeError(w, http.StatusConflict, "imported project changed; review the current specification and plan digests")
		return
	}
	originRef, err := json.Marshal(map[string]string{
		"specification_revision_id": uuidToString(target.specificationRevisionID),
		"specification_digest":      target.specificationDigest,
		"plan_id":                   uuidToString(target.planID),
		"plan_digest":               target.planDigest,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to prepare imported project adoption")
		return
	}
	actorDisplay := uuidToString(userID)
	if err := projectorchestration.RecordApprovalTx(r.Context(), tx, projectorchestration.ApprovalEvent{
		WorkspaceID: workspaceID, ProjectID: projectID,
		IdempotencyKey: "import-adoption-spec:" + uuidToString(target.planID) + ":" + target.specificationDigest,
		SubjectType:    "specification_revision", SubjectID: uuidToString(target.specificationRevisionID),
		SubjectRevision: pgtype.Int8{Int64: target.specificationRevision, Valid: true},
		SubjectDigest:   target.specificationDigest, Decision: "approved",
		ActorType: "human", ActorID: userID, ActorDisplay: actorDisplay,
		Rationale:      "Approved the exact imported specification during local adoption",
		SourceEndpoint: "/api/projects/:id/autonomous/import/adopt", Origin: "local", OriginRef: originRef,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record local specification approval")
		return
	}
	if err := projectorchestration.RecordApprovalTx(r.Context(), tx, projectorchestration.ApprovalEvent{
		WorkspaceID: workspaceID, ProjectID: projectID,
		IdempotencyKey: "import-adoption:" + uuidToString(target.planID) + ":" + target.planDigest,
		SubjectType:    "import_adoption", SubjectID: uuidToString(target.planID),
		SubjectRevision: pgtype.Int8{Int64: target.planRevision, Valid: true},
		SubjectDigest:   target.planDigest, Decision: "approved",
		ActorType: "human", ActorID: userID, ActorDisplay: actorDisplay,
		Rationale:      "Adopted the imported project plan after reviewing its exact digests",
		SourceEndpoint: "/api/projects/:id/autonomous/import/adopt", Origin: "local", OriginRef: originRef,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record imported project adoption")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit imported project adoption")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"adopted":                   true,
		"specification_revision_id": uuidToString(target.specificationRevisionID),
		"specification_revision":    target.specificationRevision,
		"specification_digest":      target.specificationDigest,
		"plan_id":                   uuidToString(target.planID),
		"plan_revision":             target.planRevision,
		"plan_digest":               target.planDigest,
	})
}

func loadImportedProjectAdoptionTargetTx(r *http.Request, tx pgx.Tx, workspaceID, projectID pgtype.UUID) (importedProjectAdoptionTarget, error) {
	var target importedProjectAdoptionTarget
	var plannerName, sourceRevision, status string
	var approvedAt pgtype.Timestamptz
	var approvedBy pgtype.UUID
	var computedPlanDigest string
	var sourceRef pgtype.Text
	err := tx.QueryRow(r.Context(), `
		SELECT p.id,p.revision,p.plan_digest,p.status,p.planner_name,p.source_revision,
		       p.approved_at,p.approved_by,p.specification_revision_id,
	       s.revision,s.source_ref,
	       autonomous_project_plan_digest(p.id),autonomous_project_specification_digest(s.id)
		FROM autonomous_project_plan p
		JOIN autonomous_project_specification_head h
		  ON h.workspace_id=p.workspace_id AND h.project_id=p.project_id
		 AND h.specification_revision_id=p.specification_revision_id
		JOIN autonomous_project_specification_revision s
		  ON s.id=p.specification_revision_id AND s.workspace_id=p.workspace_id AND s.project_id=p.project_id
		WHERE p.workspace_id=$1 AND p.project_id=$2
		ORDER BY p.revision DESC,p.id DESC
		LIMIT 1
		FOR UPDATE OF p,h,s
	`, workspaceID, projectID).Scan(
		&target.planID, &target.planRevision, &target.planDigest, &status, &plannerName, &sourceRevision,
		&approvedAt, &approvedBy, &target.specificationRevisionID,
		&target.specificationRevision, &sourceRef,
		&computedPlanDigest, &target.specificationDigest,
	)
	if err != nil {
		return importedProjectAdoptionTarget{}, err
	}
	if plannerName != "portable-import" || !strings.HasPrefix(sourceRevision, "portable:") ||
		status != "awaiting_approval" || approvedAt.Valid || approvedBy.Valid || !sourceRef.Valid ||
		(!strings.HasPrefix(sourceRef.String, "portable-import:v1:") && !strings.HasPrefix(sourceRef.String, "portable:")) ||
		target.planDigest != computedPlanDigest {
		return importedProjectAdoptionTarget{}, pgx.ErrNoRows
	}
	return target, nil
}
