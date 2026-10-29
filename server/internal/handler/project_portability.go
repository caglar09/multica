package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/projectbundle"
)

const maxProjectPortabilityBody = 10 << 20

type projectExportResponse struct {
	Bundle             projectbundle.Bundle `json:"bundle"`
	TasksMarkdown      string               `json:"tasks_markdown"`
	AgentGuideMarkdown string               `json:"agent_guide_markdown"`
}

func (h *Handler) ExportProject(w http.ResponseWriter, r *http.Request) {
	_, _, wsUUID, _, ok := h.projectPortabilityAccess(w, r)
	if !ok {
		return
	}
	projectID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "project id")
	if !ok {
		return
	}
	store := h.projectPortabilityStore()
	bundle, err := store.ExportProject(r.Context(), wsUUID, projectID)
	if err != nil {
		h.writeProjectPortabilityError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, projectExportResponse{
		Bundle:             bundle,
		TasksMarkdown:      projectbundle.RenderTasksMarkdown(bundle),
		AgentGuideMarkdown: projectbundle.RenderAgentGuideMarkdown(bundle),
	})
}

func (h *Handler) ImportProject(w http.ResponseWriter, r *http.Request) {
	_, _, wsUUID, userUUID, ok := h.projectPortabilityAccess(w, r)
	if !ok {
		return
	}
	var request projectbundle.ImportRequest
	if err := decodeProjectPortabilityBody(w, r, &request); err != nil {
		return
	}
	var (
		bundle    projectbundle.Bundle
		discovery *projectbundle.ProjectDiscoveryReport
		context   *projectbundle.ProjectContextPayload
		err       error
	)
	hasDiscovery := request.ProjectContext != nil || request.DiscoveryReport != nil
	if !hasDiscovery && (strings.TrimSpace(request.RuntimeID) != "" || strings.TrimSpace(request.Model) != "") {
		writeError(w, http.StatusBadRequest, "runtime_id and model require project_context discovery")
		return
	}
	if hasDiscovery {
		if request.Bundle != nil || strings.TrimSpace(request.Markdown) != "" {
			writeError(w, http.StatusBadRequest, "provide one project source")
			return
		}
		if len(request.Instruction) > 4000 {
			writeError(w, http.StatusBadRequest, "instruction exceeds 4000 characters")
			return
		}
		if request.ProjectContext != nil {
			clean, sanitizeErr := projectbundle.SanitizeProjectContext(*request.ProjectContext)
			if sanitizeErr != nil {
				writeError(w, http.StatusBadRequest, sanitizeErr.Error())
				return
			}
			context = &clean
		}
		if request.DiscoveryReport == nil {
			if !request.DryRun {
				writeError(w, http.StatusBadRequest, "preview project discovery before applying it")
				return
			}
			if request.ProjectContext == nil || h.ProjectDiscoveryAnalyzer == nil {
				writeError(w, http.StatusServiceUnavailable, "project discovery requires an online Multica runtime")
				return
			}
			if request.RuntimeID != "" {
				if _, ok := parseUUIDOrBadRequest(w, request.RuntimeID, "runtime_id"); !ok {
					return
				}
			}
			report, analyzeErr := h.ProjectDiscoveryAnalyzer(r.Context(), ProjectDiscoveryInput{
				WorkspaceID: wsUUID, UserID: userUUID, RuntimeID: request.RuntimeID, Model: request.Model,
				Context: *context, Instruction: request.Instruction,
			})
			switch {
			case errors.Is(analyzeErr, ErrDiscoveryRuntimeUnavailable):
				writeError(w, http.StatusServiceUnavailable, "connect an online Multica runtime to analyze this project")
				return
			case errors.Is(analyzeErr, ErrDiscoveryRuntimeForbidden):
				writeError(w, http.StatusForbidden, "you do not have access to the selected runtime")
				return
			case errors.Is(analyzeErr, ErrDiscoveryRuntimeOffline):
				writeError(w, http.StatusConflict, "the selected runtime is offline")
				return
			case errors.Is(analyzeErr, ErrDiscoveryReadOnlyUnsupported):
				writeError(w, http.StatusUnprocessableEntity, "choose a runtime with native read-only mode (Codex, Claude, or Antigravity for Gemini models)")
				return
			case analyzeErr != nil:
				writeError(w, http.StatusBadGateway, "project analysis failed; review the source and try again")
				return
			}
			discovery = &report
		} else {
			report := *request.DiscoveryReport
			if err := projectbundle.ValidateDiscoveryReport(report); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			discovery = &report
		}
		bundle, err = projectbundle.BuildBundleFromDiscovery(*discovery, context)
	} else {
		if request.ProjectContext != nil || request.DiscoveryReport != nil || strings.TrimSpace(request.Instruction) != "" {
			writeError(w, http.StatusBadRequest, "project_context is required for project discovery")
			return
		}
		bundle, err = bundleFromRequest(request.Bundle, request.Markdown, request.ProjectTitleHint)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := projectbundle.ValidateBundleForImport(bundle); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	options := portabilityOptions(request.CreateMissingLabels)
	store := h.projectPortabilityStore()
	if request.DryRun {
		result, err := store.DryRunPortability(r.Context(), wsUUID, nil, bundle, options)
		if err != nil {
			h.writeProjectPortabilityError(w, err)
			return
		}
		addDiscoveryWarnings(&result, discovery, context, bundle)
		writeJSON(w, http.StatusOK, result)
		return
	}
	result, err := store.ApplyPortability(r.Context(), wsUUID, userUUID, nil, bundle, options)
	if err != nil {
		h.writeProjectPortabilityError(w, err)
		return
	}
	addDiscoveryWarnings(&result, discovery, context, bundle)
	writeJSON(w, http.StatusCreated, result)
}

const sourceNotAttachedWarning = "The selected files were analyzed, but no agent-accessible local directory or Git source is attached; attach a source before agents edit files."

func addDiscoveryWarnings(result *projectbundle.PortabilityResult, report *projectbundle.ProjectDiscoveryReport, context *projectbundle.ProjectContextPayload, bundle projectbundle.Bundle) {
	if report == nil {
		return
	}
	result.DiscoveryReport = report
	attached := false
	for _, resource := range bundle.Resources {
		if resource.ResourceType == "github_repo" {
			attached = true
		}
		if resource.ResourceType == "local_directory" {
			warnings := append([]string{}, result.Warnings...)
			if result.Preview != nil {
				warnings = append(warnings, result.Preview.Warnings...)
			}
			attached = !hasProjectWarning(warnings, "A local_directory resource was skipped because its daemon is not registered in the target workspace.")
		}
	}
	warnings := []string{}
	if !attached {
		warnings = append(warnings, sourceNotAttachedWarning)
	}
	if context != nil {
		warnings = append(warnings, context.Warnings...)
	}
	for _, warning := range warnings {
		if warning == "" {
			continue
		}
		result.Warnings = append(result.Warnings, warning)
		if result.Preview != nil {
			result.Preview.Warnings = append(result.Preview.Warnings, warning)
		}
	}
	result.Warnings = uniqueProjectWarnings(result.Warnings)
	if result.Preview != nil {
		result.Preview.Warnings = uniqueProjectWarnings(result.Preview.Warnings)
	}
}

func hasProjectWarning(warnings []string, target string) bool {
	for _, warning := range warnings {
		if warning == target {
			return true
		}
	}
	return false
}

func uniqueProjectWarnings(warnings []string) []string {
	seen := make(map[string]bool, len(warnings))
	result := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		if warning != "" && !seen[warning] {
			seen[warning] = true
			result = append(result, warning)
		}
	}
	return result
}

func (h *Handler) SyncProject(w http.ResponseWriter, r *http.Request) {
	_, _, wsUUID, userUUID, ok := h.projectPortabilityAccess(w, r)
	if !ok {
		return
	}
	projectID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "project id")
	if !ok {
		return
	}
	var request projectbundle.SyncRequest
	if err := decodeProjectPortabilityBody(w, r, &request); err != nil {
		return
	}
	store := h.projectPortabilityStore()
	var bundle projectbundle.Bundle
	var err error
	hasMarkdown := strings.TrimSpace(request.Markdown) != ""
	if request.Bundle != nil && hasMarkdown {
		err = errors.New("provide exactly one of bundle or markdown")
	} else if request.Bundle != nil {
		bundle = *request.Bundle
		err = projectbundle.ValidateBundle(bundle)
	} else if hasMarkdown {
		var current projectbundle.Bundle
		current, err = store.ExportProject(r.Context(), wsUUID, projectID)
		if err == nil {
			bundle, err = projectbundle.ReconcileBundleWithMarkdown(current, request.Markdown)
		}
	} else {
		err = errors.New("provide exactly one of bundle or markdown")
	}
	if err != nil {
		if errors.Is(err, projectbundle.ErrProjectNotFound) {
			h.writeProjectPortabilityError(w, err)
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	options := portabilityOptions(request.CreateMissingLabels)
	if request.DryRun {
		result, err := store.DryRunPortability(r.Context(), wsUUID, &projectID, bundle, options)
		if err != nil {
			h.writeProjectPortabilityError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
		return
	}
	result, err := store.ApplyPortability(r.Context(), wsUUID, userUUID, &projectID, bundle, options)
	if err != nil {
		h.writeProjectPortabilityError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) projectPortabilityAccess(w http.ResponseWriter, r *http.Request) (string, string, pgtype.UUID, pgtype.UUID, bool) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return "", "", pgtype.UUID{}, pgtype.UUID{}, false
	}
	workspaceID := h.resolveWorkspaceID(r)
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return "", "", pgtype.UUID{}, pgtype.UUID{}, false
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return "", "", pgtype.UUID{}, pgtype.UUID{}, false
	}
	userUUID, ok := parseUUIDOrBadRequest(w, userID, "user id")
	if !ok {
		return "", "", pgtype.UUID{}, pgtype.UUID{}, false
	}
	// Do not rely solely on workspace middleware context; direct handler calls,
	// alternate routers, and stale membership context must still pass the DB gate.
	if _, err := h.getWorkspaceMember(r.Context(), userID, workspaceID); err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return "", "", pgtype.UUID{}, pgtype.UUID{}, false
	}
	return workspaceID, userID, wsUUID, userUUID, true
}

func (h *Handler) projectPortabilityStore() projectbundle.Store {
	return projectbundle.NewPostgresStore(h.Queries, h.DB, h.TxStarter, h.Entitlements)
}

func (h *Handler) writeProjectPortabilityError(w http.ResponseWriter, err error) {
	if errors.Is(err, projectbundle.ErrProjectNotFound) {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	var inputErr *projectbundle.InvalidInputError
	if errors.As(err, &inputErr) {
		writeError(w, http.StatusBadRequest, inputErr.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, "project portability operation failed")
}

func decodeProjectPortabilityBody(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxProjectPortabilityBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body is too large")
		} else {
			writeError(w, http.StatusBadRequest, "invalid request body")
		}
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request body must contain one JSON value")
		if err == nil {
			return errors.New("request body contains multiple JSON values")
		}
		return fmt.Errorf("trailing JSON request data: %w", err)
	}
	return nil
}

func bundleFromRequest(bundle *projectbundle.Bundle, markdown, titleHint string) (projectbundle.Bundle, error) {
	hasMarkdown := strings.TrimSpace(markdown) != ""
	if (bundle != nil) == hasMarkdown {
		return projectbundle.Bundle{}, errors.New("provide exactly one of bundle or markdown")
	}
	if bundle != nil {
		value := *bundle
		if strings.TrimSpace(value.Project.Title) == "" && strings.TrimSpace(titleHint) != "" {
			value.Project.Title = strings.TrimSpace(titleHint)
		}
		if err := projectbundle.ValidateBundle(value); err != nil {
			return projectbundle.Bundle{}, err
		}
		return value, nil
	}
	return projectbundle.ParseMarkdownToBundle(titleHint, markdown)
}

func portabilityOptions(createMissingLabels *bool) projectbundle.ApplyOptions {
	create := true
	if createMissingLabels != nil {
		create = *createMissingLabels
	}
	return projectbundle.ApplyOptions{CreateMissingLabels: create}
}
