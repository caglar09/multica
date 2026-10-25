package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/issuestatus"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type IssueDependencyIssueResponse struct {
	ID         string `json:"id"`
	Identifier string `json:"identifier"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	Done       bool   `json:"done"`
}

type IssueDependencyResponse struct {
	ID               string                       `json:"id"`
	Type             string                       `json:"type"`
	IssueID          string                       `json:"issue_id"`
	DependsOnIssueID string                       `json:"depends_on_issue_id"`
	RelatedIssue     IssueDependencyIssueResponse `json:"related_issue"`
}

type IssueDependencyStateResponse struct {
	BlockedBy              []IssueDependencyResponse `json:"blocked_by"`
	Blocks                 []IssueDependencyResponse `json:"blocks"`
	IsBlocked              bool                      `json:"is_blocked"`
	UnresolvedBlockerCount int                       `json:"unresolved_blocker_count"`
	DependencyState        string                    `json:"dependency_state"`
}

type createIssueDependencyRequest struct {
	DependsOnIssueID string `json:"depends_on_issue_id"`
}

func (h *Handler) loadIssueDependencyStates(ctx context.Context, workspaceID pgtype.UUID, issueIDs []pgtype.UUID) (map[string]IssueDependencyStateResponse, error) {
	states := make(map[string]IssueDependencyStateResponse, len(issueIDs))
	for _, issueID := range issueIDs {
		states[uuidToString(issueID)] = IssueDependencyStateResponse{
			BlockedBy:       []IssueDependencyResponse{},
			Blocks:          []IssueDependencyResponse{},
			DependencyState: "unblocked",
		}
	}
	if len(issueIDs) == 0 {
		return states, nil
	}

	rows, err := h.Queries.ListIssueDependencies(ctx, db.ListIssueDependenciesParams{
		WorkspaceID: workspaceID,
		IssueIds:    issueIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("list issue dependencies: %w", err)
	}
	prefix := h.getIssuePrefix(ctx, workspaceID)
	resolver := issuestatus.NewResolver(workspaceID)
	selected := make(map[string]struct{}, len(issueIDs))
	for _, issueID := range issueIDs {
		selected[uuidToString(issueID)] = struct{}{}
	}

	for _, row := range rows {
		ownerID := uuidToString(row.OwnerID)
		relatedID := uuidToString(row.RelatedID)
		if _, ok := selected[ownerID]; !ok {
			ownerID = ""
		}
		if _, ok := selected[relatedID]; !ok {
			relatedID = ""
		}
		if ownerID == "" && relatedID == "" {
			continue
		}
		relatedStatus := resolver.Effective(ctx, h.Queries, row.RelatedStatus)
		relatedDone := relatedStatus == "done" || relatedStatus == "cancelled"
		item := IssueDependencyResponse{
			ID:               uuidToString(row.ID),
			Type:             row.Type,
			IssueID:          uuidToString(row.IssueID),
			DependsOnIssueID: uuidToString(row.DependsOnIssueID),
			RelatedIssue: IssueDependencyIssueResponse{
				ID:         uuidToString(row.RelatedID),
				Identifier: prefix + "-" + fmt.Sprint(row.RelatedNumber),
				Title:      row.RelatedTitle,
				Status:     row.RelatedStatus,
				Done:       relatedDone,
			},
		}
		if ownerID != "" {
			state := states[ownerID]
			state.BlockedBy = append(state.BlockedBy, item)
			if !relatedDone {
				state.IsBlocked = true
				state.UnresolvedBlockerCount++
			}
			states[ownerID] = state
		}
		if relatedID != "" {
			state := states[relatedID]
			ownerStatus := resolver.Effective(ctx, h.Queries, row.OwnerStatus)
			blocksItem := item
			blocksItem.RelatedIssue = IssueDependencyIssueResponse{
				ID:         uuidToString(row.OwnerID),
				Identifier: prefix + "-" + fmt.Sprint(row.OwnerNumber),
				Title:      row.OwnerTitle,
				Status:     row.OwnerStatus,
				Done:       ownerStatus == "done" || ownerStatus == "cancelled",
			}
			state.Blocks = append(state.Blocks, blocksItem)
			states[relatedID] = state
		}
	}
	for id, state := range states {
		if state.IsBlocked {
			state.DependencyState = "blocked"
		} else if len(state.BlockedBy) > 0 {
			state.DependencyState = "ready"
		}
		states[id] = state
	}
	return states, nil
}

func (h *Handler) fillIssueDependencyStates(ctx context.Context, workspaceID pgtype.UUID, responses []*IssueResponse) error {
	ids := make([]pgtype.UUID, 0, len(responses))
	for _, response := range responses {
		if response == nil {
			continue
		}
		ids = append(ids, parseUUID(response.ID))
	}
	states, err := h.loadIssueDependencyStates(ctx, workspaceID, ids)
	if err != nil {
		return err
	}
	for _, response := range responses {
		if response == nil {
			continue
		}
		state := states[response.ID]
		response.BlockedBy = state.BlockedBy
		response.Blocks = state.Blocks
		response.IsBlocked = state.IsBlocked
		response.UnresolvedBlockerCount = state.UnresolvedBlockerCount
		response.DependencyState = state.DependencyState
	}
	return nil
}

func issueResponsePointers(responses []IssueResponse) []*IssueResponse {
	pointers := make([]*IssueResponse, len(responses))
	for i := range responses {
		pointers[i] = &responses[i]
	}
	return pointers
}

func (h *Handler) GetIssueDependencies(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	state, err := h.loadIssueDependencyStates(r.Context(), issue.WorkspaceID, []pgtype.UUID{issue.ID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load issue dependencies")
		return
	}
	writeJSON(w, http.StatusOK, state[uuidToString(issue.ID)])
}

func (h *Handler) CreateIssueDependency(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	var req createIssueDependencyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DependsOnIssueID == "" {
		writeError(w, http.StatusBadRequest, "depends_on_issue_id is required")
		return
	}
	predecessor, ok := h.loadIssueForUser(w, r, req.DependsOnIssueID)
	if !ok {
		return
	}
	predecessorID := predecessor.ID
	if predecessorID == issue.ID {
		writeError(w, http.StatusBadRequest, "an issue cannot depend on itself")
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin dependency update")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	wouldCycle, err := qtx.IssueDependencyWouldCycle(r.Context(), db.IssueDependencyWouldCycleParams{
		IssueID: issue.ID, CandidatePredecessorID: predecessorID, WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to validate dependency cycle")
		return
	}
	if wouldCycle {
		writeError(w, http.StatusBadRequest, "dependency would create a cycle")
		return
	}
	dependency, err := qtx.CreateIssueDependency(r.Context(), db.CreateIssueDependencyParams{
		IssueID: issue.ID, DependsOnIssueID: predecessorID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusConflict, "dependency already exists")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create issue dependency")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit issue dependency")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"id": uuidToString(dependency.ID), "issue_id": uuidToString(dependency.IssueID),
		"depends_on_issue_id": uuidToString(dependency.DependsOnIssueID), "type": dependency.Type,
	})
}

func (h *Handler) DeleteIssueDependency(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	dependencyID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "dependencyId"), "dependency_id")
	if !ok {
		return
	}
	_, err := h.Queries.DeleteIssueDependency(r.Context(), db.DeleteIssueDependencyParams{
		ID: dependencyID, IssueID: issue.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "issue dependency not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete issue dependency")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
