package projectbundle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/issueposition"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func (s *PostgresStore) applyInTransaction(
	ctx context.Context,
	tx pgx.Tx,
	qtx *db.Queries,
	workspaceID, userID pgtype.UUID,
	projectID *pgtype.UUID,
	bundle Bundle,
	options ApplyOptions,
) (PortabilityResult, error) {
	creating := projectID == nil || !projectID.Valid
	if creating {
		if err := ValidateBundleForImport(bundle); err != nil {
			return PortabilityResult{}, err
		}
	}
	current := Bundle{SchemaVersion: SchemaVersionV1, Tasks: []PortableTask{}, Project: PortableProject{}}
	if !creating {
		if err := tx.QueryRow(ctx, `SELECT id FROM project WHERE id = $1 AND workspace_id = $2 FOR UPDATE`, *projectID, workspaceID).Scan(new(pgtype.UUID)); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return PortabilityResult{}, ErrProjectNotFound
			}
			return PortabilityResult{}, fmt.Errorf("lock project for portability sync: %w", err)
		}
		var err error
		current, err = exportProject(ctx, tx, workspaceID, *projectID)
		if err != nil {
			return PortabilityResult{}, err
		}
	}
	preview := BuildPreview(current, bundle, creating)
	if err := s.addReferenceWarnings(ctx, tx, workspaceID, bundle, &preview, options); err != nil {
		return PortabilityResult{}, err
	}
	if err := rejectIdentityConflicts(preview); err != nil {
		return PortabilityResult{}, err
	}

	targetProjectID := pgtype.UUID{}
	if creating {
		var err error
		targetProjectID, err = createProject(ctx, tx, workspaceID, userID, bundle.Project, &preview)
		if err != nil {
			return PortabilityResult{}, err
		}
	} else {
		targetProjectID = *projectID
		if _, err := updateProject(ctx, tx, workspaceID, targetProjectID, bundle.Project, &preview); err != nil {
			return PortabilityResult{}, err
		}
	}

	if err := applyResources(ctx, tx, workspaceID, targetProjectID, userID, bundle.Resources, &preview); err != nil {
		return PortabilityResult{}, err
	}
	if bundle.Bootstrap != nil {
		bootstrap := *bundle.Bootstrap
		if !creating && bootstrap.present != nil {
			merged, mergeErr := mergeBootstrap(ctx, tx, workspaceID, targetProjectID, bootstrap)
			if mergeErr != nil {
				return PortabilityResult{}, mergeErr
			}
			bootstrap = merged
		}
		if err := applyBootstrap(ctx, tx, workspaceID, targetProjectID, userID, bootstrap); err != nil {
			return PortabilityResult{}, err
		}
	}

	currentIDs := taskIssueIDs(current)
	taskIDs := make(map[string]pgtype.UUID, len(bundle.Tasks))
	for key, id := range currentIDs {
		taskIDs[key] = id
	}
	for _, task := range current.Tasks {
		if id, ok := currentIDs[task.Key]; ok {
			addTaskAliases(task, id, taskIDs)
		}
	}
	issueCountPolicy := service.ResolveIssueCountPolicy(ctx, s.Entitlements, workspaceID)
	orderedTasks, err := taskOrder(bundle.Tasks)
	if err != nil {
		return PortabilityResult{}, err
	}
	diffs := make(map[string]TaskSyncDiff, len(preview.Tasks))
	for _, diff := range preview.Tasks {
		diffs[diff.Key] = diff
	}
	for index, task := range orderedTasks {
		issueID, exists := resolveTaskID(task, current, currentIDs)
		if !exists {
			created, err := createTask(ctx, tx, qtx, workspaceID, userID, targetProjectID, task, taskIDs, issueCountPolicy, index, options, &preview)
			if err != nil {
				return PortabilityResult{}, taskChangeError(task.Key, "create", err)
			}
			issueID = created
		} else {
			diff := diffs[task.Key]
			if diff.Action == "update" {
				if err := updateTask(ctx, tx, qtx, workspaceID, userID, targetProjectID, issueID, task, taskIDs, options, &preview); err != nil {
					return PortabilityResult{}, taskChangeError(task.Key, "update", err)
				}
			}
		}
		addTaskAliases(task, issueID, taskIDs)
	}
	for _, task := range orderedTasks {
		if task.Has("blocked_by_keys") || currentTaskMissing(current, task) {
			if err := replaceDependencies(ctx, tx, qtx, workspaceID, taskIDs[task.Key], task, taskIDs); err != nil {
				return PortabilityResult{}, taskChangeError(task.Key, "blocked_by_keys", err)
			}
		}
	}
	if bundle.Bootstrap != nil && bundle.Bootstrap.Plan != nil {
		if err := applyPlan(ctx, tx, workspaceID, targetProjectID, *bundle.Bootstrap.Plan, taskIDs); err != nil {
			return PortabilityResult{}, err
		}
	}

	result := resultFromPreview(preview)
	result.ProjectID = uuidString(targetProjectID)
	result.Warnings = preview.Warnings
	return result, nil
}

func createProject(ctx context.Context, q Queryer, workspaceID, userID pgtype.UUID, project PortableProject, preview *PortabilityPreview) (pgtype.UUID, error) {
	title := strings.TrimSpace(project.Title)
	if title == "" {
		return pgtype.UUID{}, errors.New("project.title is required")
	}
	status := "planned"
	if project.Status != nil && validProjectStatuses[*project.Status] {
		status = *project.Status
	}
	priority := "none"
	if project.Priority != nil && validPriorities[*project.Priority] {
		priority = *project.Priority
	}
	var leadType pgtype.Text
	var leadID pgtype.UUID
	if project.Lead != nil {
		var err error
		leadID, actorType, err := resolveActor(ctx, q, workspaceID, project.Lead)
		if err != nil {
			return pgtype.UUID{}, err
		}
		if leadID.Valid {
			leadType = pgtype.Text{String: actorType, Valid: true}
		} else {
			addWarning(preview, fmt.Sprintf("Project lead %q could not be matched in the target workspace and was left unassigned.", actorName(project.Lead)))
		}
	}
	startDate, err := nullableDate(project.StartDate)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("invalid project.start_date: %w", err)
	}
	dueDate, err := nullableDate(project.DueDate)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("invalid project.due_date: %w", err)
	}
	var id pgtype.UUID
	err = q.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title, description, icon, status, priority,
		                     lead_type, lead_id, start_date, due_date)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id
	`, workspaceID, title, nullableString(project.Description), nullableString(project.Icon),
		status, priority, leadType, leadID, startDate, dueDate).Scan(&id)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("create portable project: %w", err)
	}
	_ = userID
	return id, nil
}

func updateProject(ctx context.Context, q Queryer, workspaceID, projectID pgtype.UUID, project PortableProject, preview *PortabilityPreview) (bool, error) {
	currentStatus, currentPriority := "planned", "none"
	if err := q.QueryRow(ctx, `SELECT status, priority FROM project WHERE id = $1 AND workspace_id = $2`, projectID, workspaceID).Scan(&currentStatus, &currentPriority); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrProjectNotFound
		}
		return false, fmt.Errorf("load target project for update: %w", err)
	}
	sets := make([]string, 0, 8)
	args := []any{projectID, workspaceID}
	add := func(column string, value any) {
		args = append(args, value)
		sets = append(sets, fmt.Sprintf("%s = $%d", column, len(args)))
	}
	if project.Has("title") || project.present == nil {
		if strings.TrimSpace(project.Title) == "" {
			return false, errors.New("project.title cannot be empty")
		}
		add("title", strings.TrimSpace(project.Title))
	}
	if project.Has("description") || project.present == nil {
		add("description", nullableString(project.Description))
	}
	if project.Has("icon") || project.present == nil {
		add("icon", nullableString(project.Icon))
	}
	if project.Has("status") || project.present == nil {
		if project.Status != nil && validProjectStatuses[*project.Status] {
			add("status", *project.Status)
		} else if project.Status != nil {
			addWarning(preview, fmt.Sprintf("Project status %q is unavailable; the existing status was preserved.", *project.Status))
		}
	}
	if project.Has("priority") || project.present == nil {
		if project.Priority != nil && validPriorities[*project.Priority] {
			add("priority", *project.Priority)
		} else if project.Priority != nil {
			addWarning(preview, fmt.Sprintf("Project priority %q is unavailable; the existing priority was preserved.", *project.Priority))
		}
	}
	if project.Has("lead") || project.present == nil {
		var leadType pgtype.Text
		var leadID pgtype.UUID
		if project.Lead != nil {
			id, actorType, err := resolveActor(ctx, q, workspaceID, project.Lead)
			if err != nil {
				return false, err
			}
			if id.Valid {
				leadID = id
				leadType = pgtype.Text{String: actorType, Valid: true}
			} else {
				addWarning(preview, fmt.Sprintf("Project lead %q could not be matched in the target workspace and was left unassigned.", actorName(project.Lead)))
			}
		}
		add("lead_type", leadType)
		add("lead_id", leadID)
	}
	for _, field := range []struct {
		name  string
		value *string
	}{{"start_date", project.StartDate}, {"due_date", project.DueDate}} {
		if project.Has(field.name) || project.present == nil {
			date, err := nullableDate(field.value)
			if err != nil {
				return false, fmt.Errorf("invalid project.%s: %w", field.name, err)
			}
			add(field.name, date)
		}
	}
	_ = currentStatus
	_ = currentPriority
	if len(sets) == 0 {
		return false, nil
	}
	query := "UPDATE project SET " + strings.Join(sets, ", ") + ", updated_at = now() WHERE id = $1 AND workspace_id = $2"
	result, err := q.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("update portable project: %w", err)
	}
	if result.RowsAffected() == 0 {
		return false, ErrProjectNotFound
	}
	return true, nil
}

func applyResources(ctx context.Context, q Queryer, workspaceID, projectID, userID pgtype.UUID, resources []PortableResource, preview *PortabilityPreview) error {
	for index, resource := range resources {
		if resource.ResourceType != "github_repo" && resource.ResourceType != "local_directory" {
			continue
		}
		if resource.ResourceType == "local_directory" {
			var ref map[string]any
			if err := json.Unmarshal(resource.ResourceRef, &ref); err != nil {
				return fmt.Errorf("resource %d has invalid resource_ref: %w", index, err)
			}
			daemonID, _ := ref["daemon_id"].(string)
			matched, err := daemonMatches(ctx, q, workspaceID, daemonID)
			if err != nil {
				return fmt.Errorf("check local directory daemon: %w", err)
			}
			if !matched {
				continue
			}
		}
		var label pgtype.Text
		if resource.Label != nil && strings.TrimSpace(*resource.Label) != "" {
			label = pgtype.Text{String: strings.TrimSpace(*resource.Label), Valid: true}
		}
		position := int32(index)
		if resource.Position != nil {
			position = *resource.Position
		}
		if _, err := q.Exec(ctx, `
			INSERT INTO project_resource (project_id, workspace_id, resource_type, resource_ref, label, position, created_by)
			VALUES ($1, $2, $3, $4::jsonb, $5, $6, $7)
			ON CONFLICT (project_id, resource_type, resource_ref) DO NOTHING
		`, projectID, workspaceID, resource.ResourceType, []byte(resource.ResourceRef), label, position, userID); err != nil {
			return fmt.Errorf("create project resource %d: %w", index, err)
		}
	}
	_ = preview
	return nil
}

func createTask(
	ctx context.Context,
	tx pgx.Tx,
	qtx *db.Queries,
	workspaceID, userID, projectID pgtype.UUID,
	task PortableTask,
	taskIDs map[string]pgtype.UUID,
	policy service.IssueCountPolicy,
	positionIndex int,
	options ApplyOptions,
	preview *PortabilityPreview,
) (pgtype.UUID, error) {
	if strings.TrimSpace(task.Title) == "" {
		return pgtype.UUID{}, invalidInput(errors.New("task.title is required for a new task"))
	}
	statusInput := ""
	if task.Status != nil {
		statusInput = *task.Status
	}
	status, _, _, err := resolveStatus(ctx, tx, workspaceID, statusInput, stringValue(task.StatusCategory))
	if err != nil {
		return pgtype.UUID{}, err
	}
	status, err = lockAndResolveStatus(ctx, qtx, workspaceID, status, stringValue(task.StatusCategory), preview)
	if err != nil {
		return pgtype.UUID{}, err
	}
	priority := "none"
	if task.Priority != nil && validPriorities[*task.Priority] {
		priority = *task.Priority
	}
	var assigneeType pgtype.Text
	var assigneeID pgtype.UUID
	if task.Assignee != nil {
		id, kind, err := resolveActor(ctx, tx, workspaceID, task.Assignee)
		if err != nil {
			return pgtype.UUID{}, err
		}
		if id.Valid {
			assigneeID = id
			assigneeType = pgtype.Text{String: kind, Valid: true}
		} else {
			addWarning(preview, fmt.Sprintf("Task %q assignee %q could not be matched in the target workspace and was left unassigned.", task.Key, actorName(task.Assignee)))
		}
	}
	var parentID pgtype.UUID
	if task.ParentKey != nil {
		var ok bool
		parentID, ok = taskIDs[*task.ParentKey]
		if !ok {
			return pgtype.UUID{}, fmt.Errorf("parent task %q has not been created", *task.ParentKey)
		}
	}
	number, err := service.AllocateIssueNumber(ctx, qtx, workspaceID, policy)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("allocate issue number: %w", err)
	}
	position := float64(positionIndex)
	if task.Position != nil {
		position = *task.Position
	} else {
		position, err = issueposition.NextTopPosition(ctx, tx, workspaceID, status)
		if err != nil {
			return pgtype.UUID{}, fmt.Errorf("allocate issue position: %w", err)
		}
	}
	criteriaItems := task.AcceptanceCriteria
	if criteriaItems == nil {
		criteriaItems = []string{}
	}
	criteria, err := json.Marshal(criteriaItems)
	if err != nil {
		return pgtype.UUID{}, err
	}
	issue, err := qtx.CreateIssue(ctx, db.CreateIssueParams{
		WorkspaceID:   workspaceID,
		Title:         strings.TrimSpace(task.Title),
		Description:   nullableText(task.Description),
		Status:        status,
		Priority:      priority,
		AssigneeType:  assigneeType,
		AssigneeID:    assigneeID,
		CreatorType:   "member",
		CreatorID:     userID,
		ParentIssueID: parentID,
		Position:      position,
		Number:        number,
		ProjectID:     projectID,
		Stage:         nullableInt4(task.Stage),
		Properties:    []byte("{}"),
	})
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("insert task: %w", err)
	}
	metadata, err := portableMetadata(nil, task, true)
	if err != nil {
		return pgtype.UUID{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE issue SET acceptance_criteria = $3::jsonb, metadata = $4::jsonb
		WHERE id = $1 AND workspace_id = $2
	`, issue.ID, workspaceID, criteria, metadata); err != nil {
		return pgtype.UUID{}, fmt.Errorf("save imported task metadata: %w", err)
	}
	if task.Has("labels") || len(task.Labels) > 0 {
		if err := syncLabels(ctx, tx, workspaceID, issue.ID, task.Labels, options.CreateMissingLabels, task.Key, preview); err != nil {
			return pgtype.UUID{}, err
		}
	}
	if err := importComments(ctx, tx, workspaceID, userID, issue.ID, task, metadata, preview); err != nil {
		return pgtype.UUID{}, err
	}
	return issue.ID, nil
}

func updateTask(
	ctx context.Context,
	tx pgx.Tx,
	qtx *db.Queries,
	workspaceID, userID, projectID, issueID pgtype.UUID,
	task PortableTask,
	taskIDs map[string]pgtype.UUID,
	options ApplyOptions,
	preview *PortabilityPreview,
) error {
	var currentStatus string
	var metadataRaw []byte
	if err := tx.QueryRow(ctx, `
		SELECT status, metadata FROM issue
		WHERE id = $1 AND workspace_id = $2 AND project_id = $3 FOR UPDATE
	`, issueID, workspaceID, projectID).Scan(&currentStatus, &metadataRaw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrProjectNotFound
		}
		return fmt.Errorf("lock task for update: %w", err)
	}
	sets := make([]string, 0, 12)
	args := []any{issueID, workspaceID, projectID}
	add := func(column string, value any) {
		args = append(args, value)
		sets = append(sets, fmt.Sprintf("%s = $%d", column, len(args)))
	}
	if task.Has("title") || task.present == nil {
		if strings.TrimSpace(task.Title) == "" {
			return invalidInput(errors.New("task.title cannot be empty"))
		}
		add("title", strings.TrimSpace(task.Title))
	}
	if task.Has("description") || task.present == nil {
		add("description", nullableString(task.Description))
	}
	statusChanged := false
	resolvedStatus := currentStatus
	if task.Has("status") || task.Has("status_category") || task.present == nil {
		statusInput := ""
		if task.Status != nil {
			statusInput = *task.Status
		}
		status, _, _, err := resolveStatus(ctx, tx, workspaceID, statusInput, stringValue(task.StatusCategory))
		if err != nil {
			return err
		}
		status, err = lockAndResolveStatus(ctx, qtx, workspaceID, status, stringValue(task.StatusCategory), preview)
		if err != nil {
			return err
		}
		if status != currentStatus {
			statusChanged = true
		}
		resolvedStatus = status
		add("status", status)
	}
	if task.Has("priority") || task.present == nil {
		priority := "none"
		if task.Priority != nil && validPriorities[*task.Priority] {
			priority = *task.Priority
		}
		add("priority", priority)
	}
	if task.Has("parent_key") || task.present == nil {
		var parentID pgtype.UUID
		if task.ParentKey != nil {
			var ok bool
			parentID, ok = taskIDs[*task.ParentKey]
			if !ok {
				return fmt.Errorf("parent task %q has not been resolved", *task.ParentKey)
			}
		}
		add("parent_issue_id", parentID)
	}
	if task.Has("stage") || task.present == nil {
		add("stage", nullableInt4(task.Stage))
	}
	if task.Has("position") || task.present == nil {
		if task.Position != nil {
			add("position", *task.Position)
		} else if statusChanged {
			position, err := issueposition.NextTopPosition(ctx, tx, workspaceID, resolvedStatus)
			if err != nil {
				return fmt.Errorf("allocate issue position: %w", err)
			}
			add("position", position)
		}
	} else if statusChanged {
		position, err := issueposition.NextTopPosition(ctx, tx, workspaceID, resolvedStatus)
		if err != nil {
			return fmt.Errorf("allocate issue position: %w", err)
		}
		add("position", position)
	}
	if task.Has("assignee") || task.present == nil {
		var assigneeType pgtype.Text
		var assigneeID pgtype.UUID
		if task.Assignee != nil {
			id, kind, err := resolveActor(ctx, tx, workspaceID, task.Assignee)
			if err != nil {
				return err
			}
			if id.Valid {
				assigneeType = pgtype.Text{String: kind, Valid: true}
				assigneeID = id
			} else {
				addWarning(preview, fmt.Sprintf("Task %q assignee %q could not be matched in the target workspace and was left unassigned.", task.Key, actorName(task.Assignee)))
			}
		}
		add("assignee_type", assigneeType)
		add("assignee_id", assigneeID)
	}
	if task.Has("acceptance_criteria") || task.present == nil {
		criteriaItems := task.AcceptanceCriteria
		if criteriaItems == nil {
			criteriaItems = []string{}
		}
		criteria, err := json.Marshal(criteriaItems)
		if err != nil {
			return err
		}
		add("acceptance_criteria", criteria)
	}
	metadata := decodeMetadata(metadataRaw)
	if task.Has("metadata") || task.present == nil {
		metadata = mergeMetadata(metadata, task.Metadata)
	}
	encodedMetadata, err := portableMetadata(metadata, task, false)
	if err != nil {
		return err
	}
	if !jsonEqual(metadataRaw, encodedMetadata) {
		add("metadata", encodedMetadata)
	}
	if len(sets) > 0 {
		query := "UPDATE issue SET " + strings.Join(sets, ", ") + ", revision = revision + 1, updated_at = now(), last_activity_at = GREATEST(COALESCE(last_activity_at, updated_at), now()) WHERE id = $1 AND workspace_id = $2 AND project_id = $3"
		result, err := tx.Exec(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("update task fields: %w", err)
		}
		if result.RowsAffected() == 0 {
			return ErrProjectNotFound
		}
	}
	if task.Has("labels") || task.present == nil {
		if err := syncLabels(ctx, tx, workspaceID, issueID, task.Labels, options.CreateMissingLabels, task.Key, preview); err != nil {
			return err
		}
	}
	if task.Has("comments") || task.present == nil {
		if err := importComments(ctx, tx, workspaceID, userID, issueID, task, encodedMetadata, preview); err != nil {
			return err
		}
	}
	return nil
}

func replaceDependencies(ctx context.Context, tx pgx.Tx, qtx *db.Queries, workspaceID, issueID pgtype.UUID, task PortableTask, taskIDs map[string]pgtype.UUID) error {
	if _, err := tx.Exec(ctx, `DELETE FROM issue_dependency WHERE issue_id = $1 AND type = 'blocked_by'`, issueID); err != nil {
		return fmt.Errorf("clear old task dependencies: %w", err)
	}
	for _, key := range task.BlockedByKeys {
		predecessorID, ok := taskIDs[key]
		if !ok {
			return fmt.Errorf("blocked_by task %q has not been resolved", key)
		}
		cycle, err := qtx.IssueDependencyWouldCycle(ctx, db.IssueDependencyWouldCycleParams{
			IssueID: issueID, CandidatePredecessorID: predecessorID, WorkspaceID: workspaceID,
		})
		if err != nil {
			return fmt.Errorf("check dependency cycle: %w", err)
		}
		if cycle {
			return invalidInput(fmt.Errorf("dependency to %q would create a cycle", key))
		}
		if _, err := qtx.CreateIssueDependency(ctx, db.CreateIssueDependencyParams{IssueID: issueID, DependsOnIssueID: predecessorID}); err != nil {
			return fmt.Errorf("create dependency on %q: %w", key, err)
		}
	}
	return nil
}

func syncLabels(ctx context.Context, q Queryer, workspaceID, issueID pgtype.UUID, labels []PortableLabelRef, createMissing bool, taskKey string, preview *PortabilityPreview) error {
	if _, err := q.Exec(ctx, `DELETE FROM issue_to_label WHERE issue_id = $1 AND EXISTS (SELECT 1 FROM issue WHERE id = $1 AND workspace_id = $2)`, issueID, workspaceID); err != nil {
		return fmt.Errorf("clear task labels: %w", err)
	}
	for _, label := range labels {
		name := strings.TrimSpace(label.Name)
		if name == "" {
			continue
		}
		var labelID pgtype.UUID
		err := q.QueryRow(ctx, `SELECT id FROM issue_label WHERE workspace_id = $1 AND resource_type = 'issue' AND lower(name) = lower($2) LIMIT 1`, workspaceID, name).Scan(&labelID)
		if errors.Is(err, pgx.ErrNoRows) && createMissing {
			if _, lockErr := q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, uuidString(workspaceID)+"/issue-label/"+strings.ToLower(name)); lockErr != nil {
				return fmt.Errorf("lock label name: %w", lockErr)
			}
			err = q.QueryRow(ctx, `SELECT id FROM issue_label WHERE workspace_id = $1 AND resource_type = 'issue' AND lower(name) = lower($2) LIMIT 1`, workspaceID, name).Scan(&labelID)
			if errors.Is(err, pgx.ErrNoRows) {
				color := strings.TrimSpace(label.Color)
				if color == "" {
					color = "#6b7280"
				}
				err = q.QueryRow(ctx, `
					INSERT INTO issue_label (workspace_id, resource_type, name, description, color)
					VALUES ($1, 'issue', $2, '', $3) RETURNING id
				`, workspaceID, name, color).Scan(&labelID)
			}
		}
		if errors.Is(err, pgx.ErrNoRows) {
			addWarning(preview, fmt.Sprintf("Task %q label %q was not found and was skipped.", taskKey, name))
			continue
		}
		if err != nil {
			return fmt.Errorf("resolve task label %q: %w", name, err)
		}
		if _, err := q.Exec(ctx, `
			INSERT INTO issue_to_label (issue_id, label_id)
			SELECT $1, $2 WHERE EXISTS (
			  SELECT 1 FROM issue i JOIN issue_label l ON l.id = $2
			  WHERE i.id = $1 AND i.workspace_id = $3 AND l.workspace_id = $3 AND l.resource_type = 'issue'
			) ON CONFLICT DO NOTHING
		`, issueID, labelID, workspaceID); err != nil {
			return fmt.Errorf("attach task label %q: %w", name, err)
		}
	}
	return nil
}

func lockAndResolveStatus(ctx context.Context, qtx *db.Queries, workspaceID pgtype.UUID, status, category string, preview *PortabilityPreview) (string, error) {
	if !isBuiltInStatus(status) {
		if err := qtx.LockIssueStatusCatalogShared(ctx, workspaceID); err != nil {
			return "", fmt.Errorf("lock issue status catalog: %w", err)
		}
		entry, err := qtx.GetIssueStatusEntryByKey(ctx, db.GetIssueStatusEntryByKeyParams{WorkspaceID: workspaceID, Key: status})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("recheck task status %q: %w", status, err)
		}
		if errors.Is(err, pgx.ErrNoRows) || entry.ArchivedAt.Valid {
			fallback := statusForCategory(category)
			addWarning(preview, fmt.Sprintf("Task status %q became unavailable during import; %q was used.", status, fallback))
			return fallback, nil
		}
	}
	return status, nil
}

func isBuiltInStatus(status string) bool {
	switch status {
	case "backlog", "todo", "in_progress", "in_review", "blocked", "done", "cancelled":
		return true
	default:
		return false
	}
}

func portableMetadata(existing map[string]any, task PortableTask, creating bool) ([]byte, error) {
	metadata := make(map[string]any, len(existing)+len(task.Metadata)+2)
	for key, value := range existing {
		metadata[key] = value
	}
	if task.Has("metadata") || task.present == nil || creating {
		for key, value := range task.Metadata {
			if key == "multica_portable_key" || key == "multica_origin_id" || key == portableCommentsMetadataKey {
				continue
			}
			metadata[key] = value
		}
	}
	metadata["multica_portable_key"] = task.Key
	if task.OriginID != "" {
		metadata["multica_origin_id"] = task.OriginID
	}
	if err := validateMetadataKeys(metadata); err != nil {
		return nil, invalidInput(err)
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("encode task metadata: %w", err)
	}
	if len(raw) > 8192 {
		return nil, invalidInput(errors.New("task metadata exceeds the 8 KB limit"))
	}
	return raw, nil
}

func importComments(ctx context.Context, tx pgx.Tx, workspaceID, userID, issueID pgtype.UUID, task PortableTask, metadataRaw []byte, preview *PortabilityPreview) error {
	metadata := decodeMetadata(metadataRaw)
	provenance := readCommentProvenance(metadata)
	if len(task.Comments) == 0 && len(provenance) == 0 {
		return nil
	}
	localByKey := make(map[string]pgtype.UUID, len(provenance)+len(task.Comments))
	for key, item := range provenance {
		if item.LocalCommentID == "" {
			continue
		}
		id, err := util.ParseUUID(item.LocalCommentID)
		if err != nil {
			continue
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM comment WHERE id = $1 AND issue_id = $2 AND workspace_id = $3)`, id, issueID, workspaceID).Scan(&exists); err != nil {
			return fmt.Errorf("check imported comment: %w", err)
		}
		if exists {
			localByKey[key] = id
		}
	}
	ordered := commentOrder(task.Comments)
	for _, comment := range ordered {
		if localByKey[comment.Key].Valid {
			continue
		}
		var parentID pgtype.UUID
		if comment.ParentKey != nil {
			parentID = localByKey[*comment.ParentKey]
			if !parentID.Valid {
				return fmt.Errorf("reply parent %q has not been imported", *comment.ParentKey)
			}
		}
		commentType := comment.Type
		if commentType != "comment" && commentType != "status_change" && commentType != "progress_update" && commentType != "system" {
			commentType = "comment"
		}
		var localID pgtype.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content, type, parent_id)
			VALUES ($1, $2, 'member', $3, $4, $5, $6)
			RETURNING id
		`, issueID, workspaceID, userID, comment.Content, commentType, parentID).Scan(&localID); err != nil {
			return fmt.Errorf("import comment %q: %w", comment.Key, err)
		}
		localByKey[comment.Key] = localID
		provenance[comment.Key] = commentProvenance{
			LocalCommentID: uuidString(localID), Key: comment.Key, OriginID: comment.OriginID,
			ParentKey: comment.ParentKey, AuthorType: comment.AuthorType, AuthorName: comment.AuthorName,
			AuthorEmail: comment.AuthorEmail, Type: comment.Type, Content: comment.Content,
			CreatedAt: comment.CreatedAt, Metadata: comment.Metadata,
		}
		if comment.attachmentCount > 0 {
			addWarning(preview, fmt.Sprintf("Task %q comment %q has attachments; portability import skips attachments.", task.Key, comment.Key))
		}
	}
	encoded, err := json.Marshal(provenance)
	if err != nil {
		return fmt.Errorf("encode comment provenance: %w", err)
	}
	metadata[portableCommentsMetadataKey] = string(encoded)
	if err := validateMetadataKeys(metadata); err != nil {
		return invalidInput(err)
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode task provenance metadata: %w", err)
	}
	if len(metadataJSON) > 8192 {
		return invalidInput(errors.New("task metadata with comment provenance exceeds the 8 KB limit"))
	}
	if _, err := tx.Exec(ctx, `
		UPDATE issue SET metadata = $3::jsonb, revision = revision + 1, updated_at = now()
		WHERE id = $1 AND workspace_id = $2
	`, issueID, workspaceID, metadataJSON); err != nil {
		return fmt.Errorf("save imported comment provenance: %w", err)
	}
	return nil
}

func commentOrder(comments []PortableComment) []PortableComment {
	byKey := make(map[string]PortableComment, len(comments))
	for _, comment := range comments {
		byKey[comment.Key] = comment
	}
	result := make([]PortableComment, 0, len(comments))
	seen := make(map[string]bool, len(comments))
	var visit func(PortableComment)
	visit = func(comment PortableComment) {
		if seen[comment.Key] {
			return
		}
		if comment.ParentKey != nil {
			visit(byKey[*comment.ParentKey])
		}
		seen[comment.Key] = true
		result = append(result, comment)
	}
	for _, comment := range comments {
		visit(comment)
	}
	return result
}

func applyBootstrap(ctx context.Context, q Queryer, workspaceID, projectID, userID pgtype.UUID, bootstrap PortableBootstrap) error {
	mode := strings.TrimSpace(bootstrap.AutonomyMode)
	if mode == "" {
		mode = "standard"
	}
	if mode != "standard" && mode != "autonomous" {
		return fmt.Errorf("bootstrap.autonomy_mode must be standard or autonomous")
	}
	level := strings.TrimSpace(bootstrap.AutonomyLevel)
	if level == "" {
		level = "development"
	}
	switch level {
	case "assisted", "development", "delivery", "closed_loop":
	default:
		return fmt.Errorf("bootstrap.autonomy_level is invalid")
	}
	knowledgeItems := bootstrap.Knowledge
	if knowledgeItems == nil {
		knowledgeItems = []PortableKnowledge{}
	}
	knowledge, err := json.Marshal(knowledgeItems)
	if err != nil {
		return fmt.Errorf("encode bootstrap knowledge: %w", err)
	}
	policy := validJSONObjectOr(bootstrap.Policy, []byte("{}"))
	budget := validJSONObjectOr(bootstrap.Budget, []byte("{}"))
	if _, err := q.Exec(ctx, `
		INSERT INTO autonomous_project_bootstrap (
			project_id, workspace_id, autonomy_mode, autonomy_level, brief, knowledge, policy, budget, status, created_by
		) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8::jsonb, 'ready', $9)
		ON CONFLICT (project_id) DO UPDATE SET
			workspace_id = EXCLUDED.workspace_id,
			autonomy_mode = EXCLUDED.autonomy_mode,
			autonomy_level = EXCLUDED.autonomy_level,
			brief = EXCLUDED.brief,
			knowledge = EXCLUDED.knowledge,
			policy = EXCLUDED.policy,
			budget = EXCLUDED.budget,
			updated_at = now()
	`, projectID, workspaceID, mode, level, bootstrap.Brief, knowledge, policy, budget, userID); err != nil {
		return fmt.Errorf("save project bootstrap: %w", err)
	}
	for _, item := range bootstrap.Knowledge {
		content := item.Content
		if len(content) == 0 || !json.Valid(content) {
			content, _ = json.Marshal(string(content))
		}
		entryType := brainEntryType(item.Kind)
		sourceDigest := sha256.Sum256(append(append([]byte(item.Kind+"\x00"+item.Title+"\x00"), content...), []byte("")...))
		sourceID := "portable:" + hex.EncodeToString(sourceDigest[:])
		var exists bool
		if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM autonomous_project_brain_entry WHERE workspace_id = $1 AND project_id = $2 AND source_type = 'portable_import' AND source_id = $3)`, workspaceID, projectID, sourceID).Scan(&exists); err != nil {
			return fmt.Errorf("check imported brain entry: %w", err)
		}
		if exists {
			continue
		}
		if _, err := q.Exec(ctx, `
			INSERT INTO autonomous_project_brain_entry (
				workspace_id, project_id, entry_type, subject, content, source_type, source_id,
				confidence, created_by_type, created_by_id
			) VALUES ($1, $2, $3, $4, $5::jsonb, 'portable_import', $6, 1.0, 'member', $7)
		`, workspaceID, projectID, entryType, item.Title, content, sourceID, userID); err != nil {
			return fmt.Errorf("save imported brain entry: %w", err)
		}
	}
	return nil
}

func mergeBootstrap(ctx context.Context, q Queryer, workspaceID, projectID pgtype.UUID, incoming PortableBootstrap) (PortableBootstrap, error) {
	var mode, level, brief string
	var knowledge, policy, budget []byte
	err := q.QueryRow(ctx, `
		SELECT autonomy_mode, autonomy_level, brief, knowledge, policy, budget
		FROM autonomous_project_bootstrap
		WHERE project_id = $1 AND workspace_id = $2
	`, projectID, workspaceID).Scan(&mode, &level, &brief, &knowledge, &policy, &budget)
	if errors.Is(err, pgx.ErrNoRows) {
		return incoming, nil
	}
	if err != nil {
		return PortableBootstrap{}, fmt.Errorf("load existing project bootstrap: %w", err)
	}
	if !incoming.Has("autonomy_mode") {
		incoming.AutonomyMode = mode
	}
	if !incoming.Has("autonomy_level") {
		incoming.AutonomyLevel = level
	}
	if !incoming.Has("brief") {
		incoming.Brief = brief
	}
	if !incoming.Has("knowledge") {
		if len(knowledge) > 0 {
			if err := json.Unmarshal(knowledge, &incoming.Knowledge); err != nil {
				return PortableBootstrap{}, fmt.Errorf("decode existing project knowledge: %w", err)
			}
		}
	}
	if !incoming.Has("policy") {
		incoming.Policy = policy
	}
	if !incoming.Has("budget") {
		incoming.Budget = budget
	}
	return incoming, nil
}

func applyPlan(ctx context.Context, q Queryer, workspaceID, projectID pgtype.UUID, plan PortablePlan, taskIDs map[string]pgtype.UUID) error {
	raw, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	sourceRevision := "portable:" + hex.EncodeToString(digest[:])
	var existing bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM autonomous_project_plan WHERE workspace_id = $1 AND project_id = $2 AND source_revision = $3)`, workspaceID, projectID, sourceRevision).Scan(&existing); err != nil {
		return fmt.Errorf("check imported plan: %w", err)
	}
	if existing {
		return nil
	}
	var revision int64
	if err := q.QueryRow(ctx, `SELECT COALESCE(MAX(revision), 0) + 1 FROM autonomous_project_plan WHERE workspace_id = $1 AND project_id = $2`, workspaceID, projectID).Scan(&revision); err != nil {
		return fmt.Errorf("allocate project plan revision: %w", err)
	}
	requirementTexts := make([]string, 0, len(plan.Requirements))
	for _, requirement := range plan.Requirements {
		requirementTexts = append(requirementTexts, requirement.Text)
	}
	legacySpecification := map[string]any{"requirements": requirementTexts}
	specificationRevisionID := pgtype.UUID{}
	if plan.Specification != nil {
		details := make([]map[string]string, 0, len(plan.Specification.Requirements))
		for _, requirement := range plan.Specification.Requirements {
			details = append(details, map[string]string{"id": requirement.ID, "text": requirement.Text, "rationale": requirement.Rationale, "source": requirement.Source})
		}
		legacySpecification["summary"] = plan.Specification.Summary
		legacySpecification["requirement_details"] = details
		legacySpecification["non_functional_requirements"] = plan.Specification.NonFunctionalRequirements
		legacySpecification["constraints"] = plan.Specification.Constraints
		legacySpecification["non_goals"] = plan.Specification.NonGoals
		legacySpecification["definition_of_done"] = plan.Specification.DefinitionOfDone
		legacySpecification["acceptance_expectations"] = plan.Specification.AcceptanceExpectations
		specificationRevisionID, err = createPortableSpecification(ctx, q, workspaceID, projectID, *plan.Specification, sourceRevision)
		if err != nil {
			return fmt.Errorf("create imported project specification: %w", err)
		}
	}
	specification, err := json.Marshal(legacySpecification)
	if err != nil {
		return err
	}
	var planID pgtype.UUID
	if err := q.QueryRow(ctx, `
		INSERT INTO autonomous_project_plan (
			workspace_id, project_id, revision, source_revision, planner_name, goal,
			specification, policy, status, specification_revision_id, plan_digest
		) VALUES ($1, $2, $3, $4, 'portable-import', $5, $6::jsonb, '{}'::jsonb, 'draft', $7, repeat('0', 64))
		RETURNING id
	`, workspaceID, projectID, revision, sourceRevision, plan.Goal, specification, specificationRevisionID).Scan(&planID); err != nil {
		return fmt.Errorf("create imported project plan: %w", err)
	}
	for _, node := range plan.Nodes {
		criteria, err := json.Marshal(node.AcceptanceCriteria)
		if err != nil {
			return err
		}
		status := planNodeStatus(node.Status)
		var materializedIssue pgtype.UUID
		if id, ok := taskIDs[node.Key]; ok {
			materializedIssue = id
		}
		if _, err := q.Exec(ctx, `
			INSERT INTO autonomous_project_plan_node (
				plan_id, workspace_id, project_id, node_key, kind, title, description,
				status, priority, acceptance_criteria, materialized_issue_id
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb, $11)
		`, planID, workspaceID, projectID, node.Key, nonEmpty(node.Kind, "implementation"), node.Title,
			node.Description, status, max32(node.Priority, 0), criteria, materializedIssue); err != nil {
			return fmt.Errorf("create imported plan node %q: %w", node.Key, err)
		}
	}
	for _, edge := range plan.Edges {
		edgeType := edge.Type
		if edgeType != "hard" && edgeType != "soft" && edgeType != "artifact" {
			edgeType = "hard"
		}
		if _, err := q.Exec(ctx, `
			INSERT INTO autonomous_project_plan_edge (plan_id, workspace_id, project_id, from_node_key, to_node_key, dependency_type)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, planID, workspaceID, projectID, edge.From, edge.To, edgeType); err != nil {
			return fmt.Errorf("create imported plan edge %q -> %q: %w", edge.From, edge.To, err)
		}
	}
	var planDigest string
	if err := q.QueryRow(ctx, `SELECT autonomous_project_plan_digest($1)`, planID).Scan(&planDigest); err != nil {
		return fmt.Errorf("digest imported project plan: %w", err)
	}
	if _, err := q.Exec(ctx, `
		UPDATE autonomous_project_plan
		SET plan_digest = $2, status = 'awaiting_approval', approved_at = NULL, approved_by = NULL, updated_at = now()
		WHERE id = $1
	`, planID, planDigest); err != nil {
		return fmt.Errorf("require approval for imported project plan: %w", err)
	}
	return nil
}

func createPortableSpecification(ctx context.Context, q Queryer, workspaceID, projectID pgtype.UUID, spec PortableSpecification, sourceRef string) (pgtype.UUID, error) {
	if _, err := q.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", "autonomous-project-specification:"+uuidString(workspaceID)+":"+uuidString(projectID)); err != nil {
		return pgtype.UUID{}, err
	}
	var existingID pgtype.UUID
	var existingRevision int64
	err := q.QueryRow(ctx, `
		SELECT id, revision
		FROM autonomous_project_specification_revision
		WHERE workspace_id=$1 AND project_id=$2 AND source_kind='project_bootstrap' AND source_ref=$3
	`, workspaceID, projectID, sourceRef).Scan(&existingID, &existingRevision)
	if err == nil {
		if _, err := q.Exec(ctx, `
			INSERT INTO autonomous_project_specification_head(workspace_id,project_id,specification_revision_id,revision)
			VALUES($1,$2,$3,$4)
			ON CONFLICT(workspace_id,project_id) DO UPDATE
			SET specification_revision_id=EXCLUDED.specification_revision_id, revision=EXCLUDED.revision, updated_at=now()
		`, workspaceID, projectID, existingID, existingRevision); err != nil {
			return pgtype.UUID{}, err
		}
		return existingID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, err
	}
	var revision int64
	if err := q.QueryRow(ctx, `SELECT COALESCE(MAX(revision),0)+1 FROM autonomous_project_specification_revision WHERE workspace_id=$1 AND project_id=$2`, workspaceID, projectID).Scan(&revision); err != nil {
		return pgtype.UUID{}, err
	}
	encoded, err := json.Marshal(spec)
	if err != nil {
		return pgtype.UUID{}, err
	}
	var id pgtype.UUID
	if err := q.QueryRow(ctx, `
		INSERT INTO autonomous_project_specification_revision(workspace_id,project_id,revision,source_kind,source_ref,goal,specification)
		VALUES($1,$2,$3,'project_bootstrap',$4,$5,$6::jsonb)
		RETURNING id
	`, workspaceID, projectID, revision, sourceRef, spec.Goal, encoded).Scan(&id); err != nil {
		return pgtype.UUID{}, err
	}
	if _, err := q.Exec(ctx, `
		INSERT INTO autonomous_project_specification_head(workspace_id,project_id,specification_revision_id,revision)
		VALUES($1,$2,$3,$4)
		ON CONFLICT(workspace_id,project_id) DO UPDATE
		SET specification_revision_id=EXCLUDED.specification_revision_id, revision=EXCLUDED.revision, updated_at=now()
	`, workspaceID, projectID, id, revision); err != nil {
		return pgtype.UUID{}, err
	}
	return id, nil
}

func taskIssueIDs(bundle Bundle) map[string]pgtype.UUID {
	result := make(map[string]pgtype.UUID, len(bundle.Tasks))
	for _, task := range bundle.Tasks {
		if task.localIssueID != "" {
			if id, err := util.ParseUUID(task.localIssueID); err == nil {
				result[task.Key] = id
			}
			continue
		}
		if id, err := util.ParseUUID(task.OriginID); err == nil {
			result[task.Key] = id
		}
	}
	return result
}

func addTaskAliases(task PortableTask, issueID pgtype.UUID, taskIDs map[string]pgtype.UUID) {
	if !issueID.Valid {
		return
	}
	taskIDs[task.Key] = issueID
	for _, alias := range []string{task.identifier, task.OriginID, task.localIssueID} {
		if alias != "" {
			if _, exists := taskIDs[alias]; !exists {
				taskIDs[alias] = issueID
			}
		}
	}
}

func resolveTaskID(task PortableTask, current Bundle, currentIDs map[string]pgtype.UUID) (pgtype.UUID, bool) {
	if id, ok := currentIDs[task.Key]; ok && id.Valid {
		return id, true
	}
	for _, currentTask := range current.Tasks {
		if currentTask.identifier == task.Key || (task.OriginID != "" && currentTask.OriginID == task.OriginID) {
			if id, ok := currentIDs[currentTask.Key]; ok {
				return id, true
			}
			if parsed, err := util.ParseUUID(currentTask.localIssueID); err == nil {
				return parsed, true
			}
			if parsed, err := util.ParseUUID(currentTask.OriginID); err == nil {
				return parsed, true
			}
		}
	}
	return pgtype.UUID{}, false
}

func currentTaskMissing(current Bundle, task PortableTask) bool {
	_, ok := resolveTaskID(task, current, taskIssueIDs(current))
	return !ok
}

func rejectIdentityConflicts(preview PortabilityPreview) error {
	for _, diff := range preview.Tasks {
		if strings.Contains(strings.Join(diff.Changes, ","), "identity_conflict") {
			return invalidInput(fmt.Errorf("task %q resolves to an issue already matched by another task", diff.Key))
		}
	}
	return nil
}

func taskOrder(tasks []PortableTask) ([]PortableTask, error) {
	byKey := make(map[string]PortableTask, len(tasks))
	order := make(map[string]int, len(tasks))
	for i, task := range tasks {
		byKey[task.Key] = task
		order[task.Key] = i
	}
	state := make(map[string]uint8, len(tasks))
	result := make([]PortableTask, 0, len(tasks))
	var visit func(string) error
	visit = func(key string) error {
		if state[key] == 1 {
			return fmt.Errorf("task parent graph contains a cycle at %q", key)
		}
		if state[key] == 2 {
			return nil
		}
		state[key] = 1
		task := byKey[key]
		if task.ParentKey != nil {
			if _, ok := byKey[*task.ParentKey]; !ok {
				return fmt.Errorf("task %q references unknown parent %q", key, *task.ParentKey)
			}
			if err := visit(*task.ParentKey); err != nil {
				return err
			}
		}
		state[key] = 2
		result = append(result, task)
		return nil
	}
	keys := make([]string, 0, len(tasks))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return order[keys[i]] < order[keys[j]] })
	for _, key := range keys {
		if err := visit(key); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func nullableText(value *string) pgtype.Text {
	if value == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *value, Valid: true}
}

func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableInt4(value *int32) pgtype.Int4 {
	if value == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: *value, Valid: true}
}

func nullableDate(value *string) (pgtype.Date, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return pgtype.Date{}, nil
	}
	return dateValue(*value)
}

func jsonEqual(raw []byte, encoded []byte) bool {
	var left, right any
	return json.Unmarshal(raw, &left) == nil && json.Unmarshal(encoded, &right) == nil && fmt.Sprint(left) == fmt.Sprint(right)
}

func planNodeStatus(status string) string {
	switch status {
	case "ready", "running", "verification", "completed", "blocked", "failed", "cancelled", "pending":
		return status
	case "done":
		return "completed"
	case "in_progress":
		return "ready"
	default:
		return "pending"
	}
}

func brainEntryType(kind string) string {
	switch kind {
	case "requirement", "constraint", "assumption", "fact", "architecture_decision", "product_decision", "risk", "dependency", "repository_fact", "lesson":
		return kind
	case "requirements":
		return "requirement"
	case "architecture", "api_spec":
		return "architecture_decision"
	case "design", "prd":
		return "product_decision"
	default:
		return "fact"
	}
}

func validJSONObjectOr(raw []byte, fallback []byte) []byte {
	if len(raw) > 0 && json.Valid(raw) {
		return raw
	}
	return fallback
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func max32(value, minimum int32) int32 {
	if value < minimum {
		return minimum
	}
	return value
}
