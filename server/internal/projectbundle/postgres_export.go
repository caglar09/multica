package projectbundle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const portableCommentsMetadataKey = "_multica_portable_comments"

type commentProvenance struct {
	LocalCommentID string         `json:"local_comment_id,omitempty"`
	Key            string         `json:"key"`
	OriginID       string         `json:"origin_id,omitempty"`
	ParentKey      *string        `json:"parent_key,omitempty"`
	AuthorType     string         `json:"author_type,omitempty"`
	AuthorName     string         `json:"author_name,omitempty"`
	AuthorEmail    string         `json:"author_email,omitempty"`
	Type           string         `json:"type,omitempty"`
	Content        string         `json:"content,omitempty"`
	CreatedAt      string         `json:"created_at,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

type exportTaskRow struct {
	task         PortableTask
	id           pgtype.UUID
	parentID     pgtype.UUID
	assigneeID   pgtype.UUID
	assigneeType pgtype.Text
	comments     map[string]commentProvenance
}

func exportProject(ctx context.Context, q Queryer, workspaceID, projectID pgtype.UUID) (Bundle, error) {
	var (
		projectWorkspace pgtype.UUID
		project          PortableProject
		projectIDValue   pgtype.UUID
		leadType         pgtype.Text
		leadID           pgtype.UUID
		startDate        pgtype.Date
		dueDate          pgtype.Date
		description      pgtype.Text
		icon             pgtype.Text
		status           string
		priority         string
	)
	err := q.QueryRow(ctx, `
		SELECT id, workspace_id, title, description, icon, status, priority,
		       lead_type, lead_id, start_date, due_date
		FROM project
		WHERE id = $1 AND workspace_id = $2
	`, projectID, workspaceID).Scan(
		&projectIDValue, &projectWorkspace, &project.Title, &description, &icon,
		&status, &priority, &leadType, &leadID, &startDate, &dueDate,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Bundle{}, ErrProjectNotFound
		}
		return Bundle{}, fmt.Errorf("load project for export: %w", err)
	}
	if description.Valid {
		project.Description = &description.String
	}
	if icon.Valid {
		project.Icon = &icon.String
	}
	project.Status = &status
	project.Priority = &priority
	if startDate.Valid {
		value := startDate.Time.Format("2006-01-02")
		project.StartDate = &value
	}
	if dueDate.Valid {
		value := dueDate.Time.Format("2006-01-02")
		project.DueDate = &value
	}
	if leadType.Valid && leadID.Valid {
		project.Lead, err = exportActor(ctx, q, projectWorkspace, leadType.String, leadID)
		if err != nil {
			return Bundle{}, err
		}
	}

	var prefix string
	if err := q.QueryRow(ctx, `SELECT issue_prefix FROM workspace WHERE id = $1`, workspaceID).Scan(&prefix); err != nil {
		return Bundle{}, fmt.Errorf("load issue prefix for export: %w", err)
	}
	bundle := Bundle{
		SchemaVersion: SchemaVersionV1,
		ExportedAt:    time.Now().UTC().Format(time.RFC3339Nano),
		Origin:        PortableOrigin{WorkspaceID: uuidString(workspaceID), ProjectID: uuidString(projectIDValue)},
		Project:       project,
		Resources:     []PortableResource{},
		Tasks:         []PortableTask{},
	}
	if err := exportResources(ctx, q, workspaceID, projectIDValue, &bundle); err != nil {
		return Bundle{}, err
	}
	if err := exportTasks(ctx, q, workspaceID, projectIDValue, prefix, &bundle); err != nil {
		return Bundle{}, err
	}
	if err := exportBootstrap(ctx, q, workspaceID, projectIDValue, &bundle); err != nil {
		return Bundle{}, err
	}
	if err := ValidateBundle(bundle); err != nil {
		return Bundle{}, fmt.Errorf("exported project is not portable: %w", err)
	}
	return bundle, nil
}

func exportResources(ctx context.Context, q Queryer, workspaceID, projectID pgtype.UUID, bundle *Bundle) error {
	rows, err := q.Query(ctx, `
		SELECT resource_type, label, resource_ref, position
		FROM project_resource
		WHERE project_id = $1 AND workspace_id = $2
		ORDER BY position, created_at, id
	`, projectID, workspaceID)
	if err != nil {
		return fmt.Errorf("list project resources for export: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var resource PortableResource
		var label pgtype.Text
		if err := rows.Scan(&resource.ResourceType, &label, &resource.ResourceRef, &resource.Position); err != nil {
			return fmt.Errorf("read project resource for export: %w", err)
		}
		if label.Valid {
			resource.Label = &label.String
		}
		bundle.Resources = append(bundle.Resources, resource)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate project resources for export: %w", err)
	}
	return nil
}

func exportTasks(ctx context.Context, q Queryer, workspaceID, projectID pgtype.UUID, prefix string, bundle *Bundle) error {
	rows, err := q.Query(ctx, `
		SELECT id, number, title, description, status, priority, assignee_type,
		       assignee_id, parent_issue_id, position, stage, acceptance_criteria, metadata
		FROM issue
		WHERE workspace_id = $1 AND project_id = $2
		ORDER BY number, id
	`, workspaceID, projectID)
	if err != nil {
		return fmt.Errorf("list project tasks for export: %w", err)
	}
	defer rows.Close()
	var issueRows []exportTaskRow
	for rows.Next() {
		var row exportTaskRow
		var number int32
		var description pgtype.Text
		var status, priority string
		var position float64
		var stage pgtype.Int4
		var criteria, metadata []byte
		if err := rows.Scan(&row.id, &number, &row.task.Title, &description, &status, &priority,
			&row.assigneeType, &row.assigneeID, &row.parentID, &position,
			&stage, &criteria, &metadata); err != nil {
			return fmt.Errorf("read project task for export: %w", err)
		}
		identifier := fmt.Sprintf("%s-%d", prefix, number)
		row.task.identifier = identifier
		row.task.localIssueID = uuidString(row.id)
		row.task.Position = &position
		row.task.Key = identifier
		row.task.OriginID = uuidString(row.id)
		row.task.Status = stringPtr(status)
		row.task.StatusCategory = stringPtr(statusCategory(status))
		row.task.Priority = stringPtr(priority)
		if description.Valid {
			row.task.Description = &description.String
		}
		if stage.Valid {
			row.task.Stage = &stage.Int32
		}
		if len(criteria) > 0 {
			if err := json.Unmarshal(criteria, &row.task.AcceptanceCriteria); err != nil {
				return fmt.Errorf("decode acceptance criteria for task %s: %w", identifier, err)
			}
		}
		row.task.Metadata = mergeMetadata(row.task.Metadata, decodeMetadata(metadata))
		row.comments = readCommentProvenance(row.task.Metadata)
		if originID, ok := row.task.Metadata["multica_origin_id"].(string); ok && strings.TrimSpace(originID) != "" {
			row.task.OriginID = originID
		}
		if portableKey, ok := row.task.Metadata["multica_portable_key"].(string); ok && strings.TrimSpace(portableKey) != "" {
			row.task.portableKey = portableKey
			row.task.Key = portableKey
		}
		delete(row.task.Metadata, "multica_portable_key")
		delete(row.task.Metadata, "multica_origin_id")
		delete(row.task.Metadata, portableCommentsMetadataKey)
		issueRows = append(issueRows, row)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate project tasks for export: %w", err)
	}
	byID := make(map[string]int, len(issueRows))
	byLocalComment := make(map[string]map[string]commentProvenance, len(issueRows))
	for i := range issueRows {
		byID[uuidString(issueRows[i].id)] = i
		byLocalComment[uuidString(issueRows[i].id)] = issueRows[i].comments
		if issueRows[i].task.ParentKey == nil && issueRows[i].parentID.Valid {
			if parentIndex, ok := byID[uuidString(issueRows[i].parentID)]; ok {
				key := issueRows[parentIndex].task.Key
				issueRows[i].task.ParentKey = &key
			}
		}
	}
	// Resolve parents after the complete identifier map is available.
	for i := range issueRows {
		if issueRows[i].parentID.Valid {
			if parentIndex, ok := byID[uuidString(issueRows[i].parentID)]; ok {
				key := issueRows[parentIndex].task.Key
				issueRows[i].task.ParentKey = &key
			}
		}
	}
	statusCategories, err := loadStatusCategories(ctx, q, workspaceID)
	if err != nil {
		return err
	}
	for i := range issueRows {
		status := *issueRows[i].task.Status
		category, ok := statusCategories[status]
		if !ok {
			category = statusCategory(status)
		}
		issueRows[i].task.StatusCategory = stringPtr(category)
		if issueRows[i].assigneeID.Valid && issueRows[i].assigneeType.Valid {
			issueRows[i].task.Assignee, err = exportActor(ctx, q, workspaceID, issueRows[i].assigneeType.String, issueRows[i].assigneeID)
			if err != nil {
				return err
			}
		}
		if err := exportLabels(ctx, q, workspaceID, issueRows[i].id, &issueRows[i].task); err != nil {
			return err
		}
	}
	if err := exportDependencies(ctx, q, workspaceID, projectID, issueRows, byID); err != nil {
		return err
	}
	if err := exportComments(ctx, q, workspaceID, issueRows, byID, byLocalComment); err != nil {
		return err
	}
	for _, row := range issueRows {
		bundle.Tasks = append(bundle.Tasks, row.task)
	}
	sort.SliceStable(bundle.Tasks, func(i, j int) bool {
		if bundle.Tasks[i].ParentKey == nil && bundle.Tasks[j].ParentKey != nil {
			return true
		}
		if bundle.Tasks[i].ParentKey != nil && bundle.Tasks[j].ParentKey == nil {
			return false
		}
		if bundle.Tasks[i].Position != nil && bundle.Tasks[j].Position != nil && *bundle.Tasks[i].Position != *bundle.Tasks[j].Position {
			return *bundle.Tasks[i].Position < *bundle.Tasks[j].Position
		}
		return bundle.Tasks[i].Key < bundle.Tasks[j].Key
	})
	return nil
}

func exportLabels(ctx context.Context, q Queryer, workspaceID, issueID pgtype.UUID, task *PortableTask) error {
	rows, err := q.Query(ctx, `
		SELECT l.name, l.color
		FROM issue_to_label il
		JOIN issue_label l ON l.id = il.label_id
		WHERE il.issue_id = $1 AND l.workspace_id = $2 AND l.resource_type = 'issue'
		ORDER BY lower(l.name), l.name
	`, issueID, workspaceID)
	if err != nil {
		return fmt.Errorf("list labels for task export: %w", err)
	}
	defer rows.Close()
	task.Labels = []PortableLabelRef{}
	for rows.Next() {
		var label PortableLabelRef
		if err := rows.Scan(&label.Name, &label.Color); err != nil {
			return fmt.Errorf("read label for task export: %w", err)
		}
		task.Labels = append(task.Labels, label)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate labels for task export: %w", err)
	}
	return nil
}

func exportDependencies(ctx context.Context, q Queryer, workspaceID, projectID pgtype.UUID, issueRows []exportTaskRow, byID map[string]int) error {
	if len(issueRows) == 0 {
		return nil
	}
	ids := make([]pgtype.UUID, 0, len(issueRows))
	for _, row := range issueRows {
		ids = append(ids, row.id)
	}
	rows, err := q.Query(ctx, `
		SELECT d.issue_id, d.depends_on_issue_id
		FROM issue_dependency d
		JOIN issue owner ON owner.id = d.issue_id AND owner.workspace_id = $1 AND owner.project_id = $2
		JOIN issue predecessor ON predecessor.id = d.depends_on_issue_id
		  AND predecessor.workspace_id = owner.workspace_id AND predecessor.project_id = owner.project_id
		WHERE d.type = 'blocked_by' AND d.issue_id = ANY($3::uuid[])
		ORDER BY d.id
	`, workspaceID, projectID, ids)
	if err != nil {
		return fmt.Errorf("list task dependencies for export: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var issueID, predecessorID pgtype.UUID
		if err := rows.Scan(&issueID, &predecessorID); err != nil {
			return fmt.Errorf("read task dependency for export: %w", err)
		}
		issueIndex, ok := byID[uuidString(issueID)]
		if !ok {
			continue
		}
		predecessorIndex, ok := byID[uuidString(predecessorID)]
		if !ok {
			continue
		}
		issueRows[issueIndex].task.BlockedByKeys = append(issueRows[issueIndex].task.BlockedByKeys, issueRows[predecessorIndex].task.Key)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate task dependencies for export: %w", err)
	}
	for i := range issueRows {
		sort.Strings(issueRows[i].task.BlockedByKeys)
	}
	return nil
}

func exportComments(ctx context.Context, q Queryer, workspaceID pgtype.UUID, issueRows []exportTaskRow, byID map[string]int, provenance map[string]map[string]commentProvenance) error {
	if len(issueRows) == 0 {
		return nil
	}
	ids := make([]pgtype.UUID, 0, len(issueRows))
	for _, row := range issueRows {
		ids = append(ids, row.id)
	}
	rows, err := q.Query(ctx, `
		SELECT c.issue_id, c.id, c.parent_id, c.author_type, c.author_id, c.content, c.type,
		       c.created_at, COALESCE(u.name, a.name, ''), COALESCE(u.email, '')
		FROM comment c
		LEFT JOIN member m ON c.author_type = 'member' AND m.workspace_id = c.workspace_id AND m.user_id = c.author_id
		LEFT JOIN "user" u ON u.id = m.user_id
		LEFT JOIN agent a ON c.author_type = 'agent' AND a.workspace_id = c.workspace_id AND a.id = c.author_id
		WHERE c.workspace_id = $1 AND c.issue_id = ANY($2::uuid[]) AND c.deleted_at IS NULL
		ORDER BY c.issue_id, c.created_at, c.id
	`, workspaceID, ids)
	if err != nil {
		return fmt.Errorf("list task comments for export: %w", err)
	}
	defer rows.Close()
	type commentRow struct {
		issueID  pgtype.UUID
		id       pgtype.UUID
		parentID pgtype.UUID
		comment  PortableComment
	}
	var comments []commentRow
	keyByID := make(map[string]string)
	for rows.Next() {
		var row commentRow
		var authorID pgtype.UUID
		var createdAt pgtype.Timestamptz
		if err := rows.Scan(&row.issueID, &row.id, &row.parentID, &row.comment.AuthorType, &authorID,
			&row.comment.Content, &row.comment.Type, &createdAt, &row.comment.AuthorName, &row.comment.AuthorEmail); err != nil {
			return fmt.Errorf("read task comment for export: %w", err)
		}
		_ = authorID
		localID := uuidString(row.id)
		issueID := uuidString(row.issueID)
		row.comment.Key = "comment:" + localID
		row.comment.OriginID = localID
		row.comment.CreatedAt = createdAt.Time.UTC().Format(time.RFC3339Nano)
		if source, ok := provenance[issueID][localID]; ok {
			row.comment.Key = source.Key
			if source.OriginID != "" {
				row.comment.OriginID = source.OriginID
			}
			row.comment.ParentKey = source.ParentKey
			if source.AuthorType != "" {
				row.comment.AuthorType = source.AuthorType
			}
			if source.AuthorName != "" {
				row.comment.AuthorName = source.AuthorName
			}
			if source.AuthorEmail != "" {
				row.comment.AuthorEmail = source.AuthorEmail
			}
			if source.Type != "" {
				row.comment.Type = source.Type
			}
			if source.Content != "" {
				row.comment.Content = source.Content
			}
			if source.CreatedAt != "" {
				row.comment.CreatedAt = source.CreatedAt
			}
			row.comment.Metadata = source.Metadata
		}
		keyByID[localID] = row.comment.Key
		comments = append(comments, row)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate task comments for export: %w", err)
	}
	issueComments := make(map[string][]PortableComment)
	for _, row := range comments {
		if row.parentID.Valid {
			if parentKey, ok := keyByID[uuidString(row.parentID)]; ok {
				row.comment.ParentKey = &parentKey
			} else {
				row.comment.ParentKey = nil
			}
		}
		issueComments[uuidString(row.issueID)] = append(issueComments[uuidString(row.issueID)], row.comment)
	}
	for i := range issueRows {
		issueRows[i].task.Comments = issueComments[uuidString(issueRows[i].id)]
	}
	return nil
}

func exportBootstrap(ctx context.Context, q Queryer, workspaceID, projectID pgtype.UUID, bundle *Bundle) error {
	var bootstrap PortableBootstrap
	var knowledge, policy, budget []byte
	err := q.QueryRow(ctx, `
		SELECT autonomy_mode, autonomy_level, brief, knowledge, policy, budget
		FROM autonomous_project_bootstrap
		WHERE project_id = $1 AND workspace_id = $2
	`, projectID, workspaceID).Scan(&bootstrap.AutonomyMode, &bootstrap.AutonomyLevel,
		&bootstrap.Brief, &knowledge, &policy, &budget)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("load project bootstrap for export: %w", err)
	}
	if err == nil {
		if len(knowledge) > 0 {
			if err := json.Unmarshal(knowledge, &bootstrap.Knowledge); err != nil {
				return fmt.Errorf("decode project bootstrap knowledge: %w", err)
			}
		}
		bootstrap.Policy = policy
		bootstrap.Budget = budget
		bundle.Bootstrap = &bootstrap
	}

	var planID pgtype.UUID
	var plan PortablePlan
	var specification []byte
	planFound := false
	err = q.QueryRow(ctx, `
		SELECT id, goal, specification
		FROM autonomous_project_plan
		WHERE project_id = $1 AND workspace_id = $2
		ORDER BY revision DESC, created_at DESC
		LIMIT 1
	`, projectID, workspaceID).Scan(&planID, &plan.Goal, &specification)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("load project plan for export: %w", err)
	}
	if err == nil {
		planFound = true
		var spec struct {
			Requirements []PlanRequirement `json:"requirements"`
		}
		if len(specification) > 0 {
			_ = json.Unmarshal(specification, &spec)
		}
		plan.Requirements = spec.Requirements
		taskKeyByID := make(map[string]string, len(bundle.Tasks))
		for _, task := range bundle.Tasks {
			if task.localIssueID != "" {
				taskKeyByID[task.localIssueID] = task.Key
			}
		}
		nodeKeyMap := make(map[string]string)
		nodes, err := q.Query(ctx, `
			SELECT node_key, title, description, kind, status, priority, acceptance_criteria, materialized_issue_id
			FROM autonomous_project_plan_node
			WHERE plan_id = $1 AND workspace_id = $2 AND project_id = $3
			ORDER BY node_key
		`, planID, workspaceID, projectID)
		if err != nil {
			return fmt.Errorf("list project plan nodes for export: %w", err)
		}
		for nodes.Next() {
			var node PlanNode
			var criteria []byte
			var materializedIssueID pgtype.UUID
			if err := nodes.Scan(&node.Key, &node.Title, &node.Description, &node.Kind, &node.Status, &node.Priority, &criteria, &materializedIssueID); err != nil {
				nodes.Close()
				return fmt.Errorf("read project plan node for export: %w", err)
			}
			if len(criteria) > 0 {
				if err := json.Unmarshal(criteria, &node.AcceptanceCriteria); err != nil {
					nodes.Close()
					return fmt.Errorf("decode project plan criteria: %w", err)
				}
			}
			originalKey := node.Key
			if materializedIssueID.Valid {
				if taskKey := taskKeyByID[uuidString(materializedIssueID)]; taskKey != "" {
					node.Key = taskKey
				}
			}
			nodeKeyMap[originalKey] = node.Key
			plan.Nodes = append(plan.Nodes, node)
		}
		if err := nodes.Err(); err != nil {
			nodes.Close()
			return fmt.Errorf("iterate project plan nodes: %w", err)
		}
		nodes.Close()
		edges, err := q.Query(ctx, `
			SELECT from_node_key, to_node_key, dependency_type
			FROM autonomous_project_plan_edge
			WHERE plan_id = $1 AND workspace_id = $2 AND project_id = $3
			ORDER BY from_node_key, to_node_key
		`, planID, workspaceID, projectID)
		if err != nil {
			return fmt.Errorf("list project plan edges for export: %w", err)
		}
		for edges.Next() {
			var edge PlanEdge
			if err := edges.Scan(&edge.From, &edge.To, &edge.Type); err != nil {
				edges.Close()
				return fmt.Errorf("read project plan edge for export: %w", err)
			}
			if key, ok := nodeKeyMap[edge.From]; ok {
				edge.From = key
			}
			if key, ok := nodeKeyMap[edge.To]; ok {
				edge.To = key
			}
			plan.Edges = append(plan.Edges, edge)
		}
		if err := edges.Err(); err != nil {
			edges.Close()
			return fmt.Errorf("iterate project plan edges: %w", err)
		}
		edges.Close()
	}

	seenKnowledge := make(map[string]bool)
	if bundle.Bootstrap != nil {
		unique := make([]PortableKnowledge, 0, len(bundle.Bootstrap.Knowledge))
		for _, item := range bundle.Bootstrap.Knowledge {
			key := knowledgeKey(item)
			if seenKnowledge[key] {
				continue
			}
			seenKnowledge[key] = true
			unique = append(unique, item)
		}
		bundle.Bootstrap.Knowledge = unique
	}
	brain, err := q.Query(ctx, `
		SELECT entry_type, subject, content
		FROM autonomous_project_brain_entry
		WHERE workspace_id = $1 AND project_id = $2 AND superseded_by IS NULL
		ORDER BY created_at, id
	`, workspaceID, projectID)
	if err != nil {
		return fmt.Errorf("list project brain entries for export: %w", err)
	}
	for brain.Next() {
		var item PortableKnowledge
		if err := brain.Scan(&item.Kind, &item.Title, &item.Content); err != nil {
			brain.Close()
			return fmt.Errorf("read project brain entry for export: %w", err)
		}
		if bundle.Bootstrap == nil {
			bundle.Bootstrap = &PortableBootstrap{}
		}
		if seenKnowledge[knowledgeKey(item)] {
			continue
		}
		seenKnowledge[knowledgeKey(item)] = true
		bundle.Bootstrap.Knowledge = append(bundle.Bootstrap.Knowledge, item)
	}
	if err := brain.Err(); err != nil {
		brain.Close()
		return fmt.Errorf("iterate project brain entries: %w", err)
	}
	brain.Close()
	if planFound {
		if bundle.Bootstrap == nil {
			bundle.Bootstrap = &PortableBootstrap{}
		}
		bundle.Bootstrap.Plan = &plan
	}
	return nil
}

func knowledgeKey(item PortableKnowledge) string {
	var decoded any
	if len(item.Content) > 0 && json.Unmarshal(item.Content, &decoded) == nil {
		if canonical, err := json.Marshal(decoded); err == nil {
			return item.Kind + "\x00" + item.Title + "\x00" + string(canonical)
		}
	}
	return item.Kind + "\x00" + item.Title + "\x00" + string(item.Content)
}

func exportActor(ctx context.Context, q Queryer, workspaceID pgtype.UUID, actorType string, actorID pgtype.UUID) (*PortableActorRef, error) {
	var actor PortableActorRef
	actor.Type = actorType
	switch actorType {
	case "member":
		err := q.QueryRow(ctx, `
			SELECT u.name, u.email FROM member m
			JOIN "user" u ON u.id = m.user_id
			WHERE m.workspace_id = $1 AND m.user_id = $2
		`, workspaceID, actorID).Scan(&actor.Name, &actor.Email)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("resolve member for export: %w", err)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
	case "agent":
		err := q.QueryRow(ctx, `SELECT name FROM agent WHERE workspace_id = $1 AND id = $2`, workspaceID, actorID).Scan(&actor.Name)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("resolve agent for export: %w", err)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
	default:
		return nil, nil
	}
	return &actor, nil
}

func loadStatusCategories(ctx context.Context, q Queryer, workspaceID pgtype.UUID) (map[string]string, error) {
	rows, err := q.Query(ctx, `SELECT key, category FROM issue_status WHERE workspace_id = $1`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("load issue status catalog for export: %w", err)
	}
	defer rows.Close()
	result := make(map[string]string)
	for rows.Next() {
		var key, category string
		if err := rows.Scan(&key, &category); err != nil {
			return nil, err
		}
		result[key] = category
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func decodeMetadata(raw []byte) map[string]any {
	var metadata map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &metadata)
	}
	if metadata == nil {
		metadata = make(map[string]any)
	}
	return metadata
}

func mergeMetadata(target, source map[string]any) map[string]any {
	if target == nil {
		target = make(map[string]any)
	}
	for key, value := range source {
		target[key] = value
	}
	return target
}

func readCommentProvenance(metadata map[string]any) map[string]commentProvenance {
	result := make(map[string]commentProvenance)
	raw, ok := metadata[portableCommentsMetadataKey].(string)
	if !ok || raw == "" {
		return result
	}
	_ = json.Unmarshal([]byte(raw), &result)
	return result
}

func statusCategory(status string) string {
	switch status {
	case "done":
		return "done"
	case "cancelled":
		return "closed"
	case "in_progress", "in_review", "blocked":
		return "started"
	default:
		return "unstarted"
	}
}

func stringPtr(value string) *string { return &value }

func uuidString(value pgtype.UUID) string {
	if !value.Valid {
		return ""
	}
	return uuid.UUID(value.Bytes).String()
}

func pgtypeUUIDString(value pgtype.UUID) string { return uuidString(value) }

func dateValue(value string) (pgtype.Date, error) {
	if value == "" {
		return pgtype.Date{}, nil
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return pgtype.Date{}, err
	}
	return pgtype.Date{Time: parsed, Valid: true}, nil
}
