package projectbundle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *PostgresStore) addReferenceWarnings(ctx context.Context, q Queryer, workspaceID pgtype.UUID, bundle Bundle, preview *PortabilityPreview, options ApplyOptions) error {
	if bundle.Project.Lead != nil {
		id, _, err := resolveActor(ctx, q, workspaceID, bundle.Project.Lead)
		if err != nil {
			return err
		} else if !id.Valid {
			addWarning(preview, fmt.Sprintf("Project lead %q could not be matched in the target workspace and was left unassigned.", actorName(bundle.Project.Lead)))
		}
	}
	if bundle.Project.Status != nil && !validProjectStatuses[*bundle.Project.Status] {
		addWarning(preview, fmt.Sprintf("Project status %q is unavailable and will use planned for a new project.", *bundle.Project.Status))
	}
	if bundle.Project.Priority != nil && !validPriorities[*bundle.Project.Priority] {
		addWarning(preview, fmt.Sprintf("Project priority %q is unavailable and will use none for a new project.", *bundle.Project.Priority))
	}
	for _, resource := range bundle.Resources {
		switch resource.ResourceType {
		case "github_repo":
		case "local_directory":
			var ref map[string]any
			if err := json.Unmarshal(resource.ResourceRef, &ref); err != nil {
				return fmt.Errorf("invalid local_directory resource_ref: %w", err)
			}
			daemonID, _ := ref["daemon_id"].(string)
			matched, err := daemonMatches(ctx, q, workspaceID, daemonID)
			if err != nil {
				return fmt.Errorf("check local directory daemon: %w", err)
			}
			if !matched {
				addWarning(preview, "A local_directory resource was skipped because its daemon is not registered in the target workspace.")
			}
		default:
			addWarning(preview, fmt.Sprintf("Resource type %q is not supported by portability import and was skipped.", resource.ResourceType))
		}
	}
	for _, task := range bundle.Tasks {
		if task.Status != nil {
			_, _, fallback, err := resolveStatus(ctx, q, workspaceID, *task.Status, stringValue(task.StatusCategory))
			if err != nil {
				return fmt.Errorf("resolve status for task %q: %w", task.Key, err)
			}
			if fallback {
				addWarning(preview, fmt.Sprintf("Task %q status %q was unavailable; its status category was used when possible.", task.Key, *task.Status))
			}
		}
		if task.Priority != nil && !validPriorities[*task.Priority] {
			addWarning(preview, fmt.Sprintf("Task %q priority %q is unavailable and will use none.", task.Key, *task.Priority))
		}
		if task.Assignee != nil {
			id, _, err := resolveActor(ctx, q, workspaceID, task.Assignee)
			if err != nil {
				return err
			} else if !id.Valid {
				addWarning(preview, fmt.Sprintf("Task %q assignee %q could not be matched in the target workspace and was left unassigned.", task.Key, actorName(task.Assignee)))
			}
		}
		for _, label := range task.Labels {
			found, err := labelExists(ctx, q, workspaceID, label.Name)
			if err != nil {
				return fmt.Errorf("resolve label %q: %w", label.Name, err)
			}
			if !found && options.CreateMissingLabels {
				addWarning(preview, fmt.Sprintf("Label %q will be created in the target workspace.", label.Name))
			} else if !found {
				addWarning(preview, fmt.Sprintf("Label %q was not found and will be skipped.", label.Name))
			}
		}
		if task.attachmentCount > 0 {
			addWarning(preview, fmt.Sprintf("Task %q has attachments; portability import skips attachments.", task.Key))
		}
		for _, comment := range task.Comments {
			if comment.attachmentCount > 0 {
				addWarning(preview, fmt.Sprintf("Task %q comment %q has attachments; portability import skips attachments.", task.Key, comment.Key))
			}
		}
	}
	return nil
}

func resolveStatus(ctx context.Context, q Queryer, workspaceID pgtype.UUID, requested, category string) (resolved, resolvedCategory string, fallback bool, err error) {
	requested = strings.TrimSpace(requested)
	if requested != "" {
		var actualCategory string
		var archived pgtype.Timestamptz
		err = q.QueryRow(ctx, `
			SELECT category, archived_at FROM issue_status
			WHERE workspace_id = $1 AND key = $2
		`, workspaceID, requested).Scan(&actualCategory, &archived)
		if err == nil && !archived.Valid {
			return requested, actualCategory, false, nil
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return "", "", false, err
		}
		if category == "" {
			category = categoryForStatus(requested)
		}
		if category == "" {
			category = "unstarted"
		}
		return statusForCategory(category), category, true, nil
	}
	if category == "" {
		category = "unstarted"
	}
	return statusForCategory(category), category, true, nil
}

func statusForCategory(category string) string {
	switch category {
	case "started":
		return "in_progress"
	case "done":
		return "done"
	case "closed":
		return "cancelled"
	default:
		return "todo"
	}
}

func categoryForStatus(status string) string {
	switch status {
	case "done":
		return "done"
	case "cancelled":
		return "closed"
	case "in_progress", "in_review", "blocked":
		return "started"
	case "backlog", "todo":
		return "unstarted"
	default:
		return ""
	}
}

func resolveActor(ctx context.Context, q Queryer, workspaceID pgtype.UUID, actor *PortableActorRef) (pgtype.UUID, string, error) {
	if actor == nil {
		return pgtype.UUID{}, "", nil
	}
	name := strings.TrimSpace(actor.Name)
	email := strings.TrimSpace(actor.Email)
	actorType := strings.TrimSpace(actor.Type)
	if actorType == "" || actorType == "member" {
		var id pgtype.UUID
		err := q.QueryRow(ctx, `
			SELECT m.user_id FROM member m JOIN "user" u ON u.id = m.user_id
			WHERE m.workspace_id = $1
			  AND (($2 <> '' AND lower(u.email) = lower($2)) OR ($3 <> '' AND lower(u.name) = lower($3)))
		ORDER BY CASE WHEN lower(u.email) = lower($2) AND $2 <> '' THEN 0 ELSE 1 END, u.created_at, m.user_id
		LIMIT 1
		`, workspaceID, email, name).Scan(&id)
		if err == nil {
			return id, "member", nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return pgtype.UUID{}, "", fmt.Errorf("resolve workspace member: %w", err)
		}
	}
	if actorType == "" || actorType == "agent" {
		var id pgtype.UUID
		err := q.QueryRow(ctx, `
			SELECT id FROM agent
			WHERE workspace_id = $1 AND $2 <> '' AND lower(name) = lower($2)
		ORDER BY created_at, id LIMIT 1
		`, workspaceID, name).Scan(&id)
		if err == nil {
			return id, "agent", nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return pgtype.UUID{}, "", fmt.Errorf("resolve workspace agent: %w", err)
		}
	}
	return pgtype.UUID{}, "", nil
}

func actorName(actor *PortableActorRef) string {
	if actor == nil {
		return ""
	}
	if strings.TrimSpace(actor.Name) != "" {
		return actor.Name
	}
	return actor.Email
}

func labelExists(ctx context.Context, q Queryer, workspaceID pgtype.UUID, name string) (bool, error) {
	var exists bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM issue_label
		 WHERE workspace_id = $1 AND resource_type = 'issue' AND lower(name) = lower($2))
	`, workspaceID, strings.TrimSpace(name)).Scan(&exists)
	return exists, err
}

func daemonMatches(ctx context.Context, q Queryer, workspaceID pgtype.UUID, daemonID string) (bool, error) {
	if strings.TrimSpace(daemonID) == "" {
		return false, nil
	}
	var exists bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1 FROM daemon_connection dc
		  JOIN agent a ON a.id = dc.agent_id
		  WHERE a.workspace_id = $1 AND dc.daemon_id = $2
		)
	`, workspaceID, daemonID).Scan(&exists)
	return exists, err
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
