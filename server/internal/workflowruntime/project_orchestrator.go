package workflowruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/issuestatus"
	"github.com/multica-ai/multica/server/internal/projectorchestration"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/teamprovision"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func (r *Runtime) processProjectBootstrap(ctx context.Context) error {
	if r == nil || r.team == nil {
		return nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT b.workspace_id, b.project_id
		FROM autonomous_project_bootstrap b
		WHERE b.autonomy_mode = 'autonomous'
		  AND (
			b.status = 'ready'
			OR (b.status = 'started' AND b.updated_at < now() - interval '30 seconds')
		  )
		  AND NOT EXISTS (
			SELECT 1
			FROM autonomous_project_team t
			WHERE t.workspace_id = b.workspace_id
			  AND t.project_id = b.project_id
			  AND t.status = 'active'
		  )
		  AND NOT EXISTS (
			SELECT 1
			FROM autonomous_project_team_draft d
			WHERE d.workspace_id = b.workspace_id
			  AND d.project_id = b.project_id
			  AND d.status IN ('awaiting_configuration', 'provisioning', 'applied')
		  )
		ORDER BY b.updated_at ASC
		LIMIT 10
	`)
	if err != nil {
		return fmt.Errorf("query autonomous project bootstraps: %w", err)
	}
	type bootstrapProject struct {
		workspaceID pgtype.UUID
		projectID   pgtype.UUID
	}
	items := make([]bootstrapProject, 0, 10)
	for rows.Next() {
		var item bootstrapProject
		if err := rows.Scan(&item.workspaceID, &item.projectID); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, item := range items {
		if _, err := r.pool.Exec(ctx, `
			UPDATE autonomous_project_bootstrap
			SET status = 'started', updated_at = now()
			WHERE workspace_id = $1
			  AND project_id = $2
			  AND autonomy_mode = 'autonomous'
			  AND status IN ('ready', 'started')
		`, item.workspaceID, item.projectID); err != nil {
			return fmt.Errorf("mark project bootstrap started: %w", err)
		}
		if _, err := r.team.PrepareProject(ctx, item.workspaceID, item.projectID); err != nil {
			message := err.Error()
			if len(message) > 2000 {
				message = message[:2000]
			}
			_, _ = r.pool.Exec(ctx, `
				INSERT INTO autonomous_project_control (
					project_id, workspace_id, last_error, updated_at
				)
				VALUES ($1, $2, $3, now())
				ON CONFLICT (project_id) DO UPDATE
				SET last_error = EXCLUDED.last_error, updated_at = now()
				WHERE autonomous_project_control.workspace_id = EXCLUDED.workspace_id
			`, item.projectID, item.workspaceID, "Project bootstrap failed: "+message)
			continue
		}
		_, _ = r.pool.Exec(ctx, `
			INSERT INTO autonomous_project_control (
				project_id, workspace_id, last_error, updated_at
			)
			VALUES ($1, $2, NULL, now())
			ON CONFLICT (project_id) DO UPDATE
			SET last_error = NULL, updated_at = now()
			WHERE autonomous_project_control.workspace_id = EXCLUDED.workspace_id
		`, item.projectID, item.workspaceID)
	}
	return nil
}

func (r *Runtime) processAutomaticTeamConfiguration(ctx context.Context) error {
	if r == nil || !r.config.AutoConfigureTeam || r.team == nil {
		return nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT d.workspace_id, d.project_id, a.owner_id
		FROM autonomous_project_team_draft d
		JOIN agent a
		  ON a.workspace_id = d.workspace_id
		 AND a.system_key = $1
		 AND a.archived_at IS NULL
		WHERE d.status = 'awaiting_configuration'
		ORDER BY d.updated_at ASC
		LIMIT 10
	`, service.MikaSystemKey)
	if err != nil {
		return fmt.Errorf("query team drafts for automatic configuration: %w", err)
	}
	type draft struct{ workspaceID, projectID, ownerID pgtype.UUID }
	items := make([]draft, 0, 10)
	for rows.Next() {
		var item draft
		if err := rows.Scan(&item.workspaceID, &item.projectID, &item.ownerID); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, item := range items {
		tag, err := r.pool.Exec(ctx, `
			UPDATE autonomous_project_team_draft
			SET status = 'provisioning',
			    selections = '{}'::jsonb,
			    confirmed_at = now(),
			    confirmed_by = $3,
			    updated_at = now()
			WHERE workspace_id = $1
			  AND project_id = $2
			  AND status = 'awaiting_configuration'
		`, item.workspaceID, item.projectID, item.ownerID)
		if err != nil {
			return fmt.Errorf("auto-configure autonomous team draft: %w", err)
		}
		if tag.RowsAffected() > 0 {
			slog.Info("autonomous team draft auto-configured from Mika runtime",
				"workspace_id", util.UUIDToString(item.workspaceID),
				"project_id", util.UUIDToString(item.projectID),
			)
		}
	}
	return nil
}

func (r *Runtime) processProjectPlanning(ctx context.Context) error {
	if r.projectPlanner == nil || r.projectStore == nil || r.team == nil {
		return nil
	}

	rows, err := r.pool.Query(ctx, `
		SELECT d.project_id, d.workspace_id, d.confirmed_by, d.confirmed_at
		FROM autonomous_project_team_draft d
		WHERE d.status = 'applied'
		  AND d.confirmed_at IS NOT NULL
		  AND NOT EXISTS (
			SELECT 1
			FROM autonomous_project_plan p
			WHERE p.workspace_id = d.workspace_id
			  AND p.project_id = d.project_id
			  AND p.status IN ('active', 'completed')
		  )
		ORDER BY d.confirmed_at ASC
		LIMIT 5
	`)
	if err != nil {
		return fmt.Errorf("query projects requiring durable planning: %w", err)
	}
	type candidate struct {
		projectID   pgtype.UUID
		workspaceID pgtype.UUID
		confirmedBy pgtype.UUID
		confirmedAt time.Time
	}
	items := make([]candidate, 0, 5)
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.projectID, &item.workspaceID, &item.confirmedBy, &item.confirmedAt); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, item := range items {
		planCtx, cancel := context.WithTimeout(ctx, autonomousPlanningTimeout)
		err := r.planProjectRevision(planCtx, item.workspaceID, item.projectID, item.confirmedAt.UTC().Format(time.RFC3339Nano))
		cancel()
		if err != nil {
			message := err.Error()
			if len(message) > 2000 {
				message = message[:2000]
			}
			_, _ = r.pool.Exec(ctx, `
				INSERT INTO autonomous_project_control (project_id, workspace_id, last_error, updated_at)
				VALUES ($1, $2, $3, now())
				ON CONFLICT (project_id) DO UPDATE
				SET last_error = EXCLUDED.last_error, updated_at = now()
				WHERE autonomous_project_control.workspace_id = EXCLUDED.workspace_id
			`, item.projectID, item.workspaceID, "Project planning failed: "+message)
			continue
		}
		_, _ = r.pool.Exec(ctx, `
			INSERT INTO autonomous_project_control (project_id, workspace_id, last_error, updated_at)
			VALUES ($1, $2, NULL, now())
			ON CONFLICT (project_id) DO UPDATE
			SET last_error = NULL, updated_at = now()
			WHERE autonomous_project_control.workspace_id = EXCLUDED.workspace_id
		`, item.projectID, item.workspaceID)
	}
	return nil
}

func (r *Runtime) refreshRepositoryIntelligence(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
) error {
	if r.repositoryAnalyzer == nil {
		return nil
	}
	snapshot, err := r.repositoryAnalyzer.Snapshot(ctx, workspaceID, projectID)
	if err != nil {
		return err
	}
	content, _ := json.Marshal(map[string]any{
		"revision": snapshot.Revision,
		"modules": snapshot.Modules,
		"test_targets": snapshot.TestTargets,
		"api_surfaces": snapshot.APISurfaces,
		"data_stores": snapshot.DataStores,
		"dependencies": snapshot.Dependencies,
		"evidence": snapshot.Evidence,
	})
	sourceID := strings.TrimSpace(snapshot.Revision)
	if sourceID == "" {
		sourceID = "latest"
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO autonomous_project_brain_entry (
			workspace_id, project_id, entry_type, subject, content,
			source_type, source_id, confidence, created_by_type
		)
		SELECT $1, $2, 'repository_fact', 'Repository snapshot', $3,
		       'repository_analyzer', $4, 1.0, 'system'
		WHERE NOT EXISTS (
			SELECT 1
			FROM autonomous_project_brain_entry
			WHERE workspace_id = $1
			  AND project_id = $2
			  AND entry_type = 'repository_fact'
			  AND source_type = 'repository_analyzer'
			  AND source_id = $4
			  AND superseded_by IS NULL
		)
	`, workspaceID, projectID, content, sourceID)
	return err
}

func (r *Runtime) loadProjectPlanningBootstrap(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
) (string, []projectorchestration.PlanningContextItem, []projectorchestration.PlanningResource, projectorchestration.Policy, error) {
	requested := r.config.ProjectPolicy
	var level string
	var brief string
	var knowledgeJSON, approvalsJSON, budgetJSON []byte
	err := r.pool.QueryRow(ctx, `
		SELECT autonomy_level, brief, knowledge, policy, budget
		FROM autonomous_project_bootstrap
		WHERE workspace_id = $1 AND project_id = $2 AND autonomy_mode = 'autonomous'
	`, workspaceID, projectID).Scan(&level, &brief, &knowledgeJSON, &approvalsJSON, &budgetJSON)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil, nil, requested, nil
		}
		return "", nil, nil, requested, err
	}
	requested.Autonomy = projectorchestration.AutonomyLevel(level)
	_ = json.Unmarshal(approvalsJSON, &requested.Approvals)
	_ = json.Unmarshal(budgetJSON, &requested.Budget)

	contextItems := make([]projectorchestration.PlanningContextItem, 0)
	var knowledge []struct {
		Kind    string `json:"kind"`
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if len(knowledgeJSON) > 0 && json.Unmarshal(knowledgeJSON, &knowledge) == nil {
		for _, item := range knowledge {
			if strings.TrimSpace(item.Content) == "" {
				continue
			}
			contextItems = append(contextItems, projectorchestration.PlanningContextItem{
				Type: strings.TrimSpace(item.Kind),
				Title: strings.TrimSpace(item.Title),
				Content: strings.TrimSpace(item.Content),
			})
		}
	}

	brainRows, brainErr := r.pool.Query(ctx, `
		SELECT entry_type, subject, content
		FROM autonomous_project_brain_entry
		WHERE workspace_id = $1
		  AND project_id = $2
		  AND superseded_by IS NULL
		  AND status = 'active'
		  AND source_type <> 'bootstrap'
		ORDER BY created_at ASC
		LIMIT 200
	`, workspaceID, projectID)
	if brainErr != nil {
		return "", nil, nil, requested, brainErr
	}
	for brainRows.Next() {
		var entryType, subject string
		var contentJSON []byte
		if err := brainRows.Scan(&entryType, &subject, &contentJSON); err != nil {
			brainRows.Close()
			return "", nil, nil, requested, err
		}
		contextItems = append(contextItems, projectorchestration.PlanningContextItem{
			Type: entryType,
			Title: subject,
			Content: string(contentJSON),
		})
	}
	if err := brainRows.Err(); err != nil {
		brainRows.Close()
		return "", nil, nil, requested, err
	}
	brainRows.Close()

	resources := make([]projectorchestration.PlanningResource, 0)
	resourceRows, resourceErr := r.pool.Query(ctx, `
		SELECT resource_type, resource_ref
		FROM project_resource
		WHERE workspace_id = $1 AND project_id = $2
		ORDER BY position ASC, created_at ASC
	`, workspaceID, projectID)
	if resourceErr != nil {
		return "", nil, nil, requested, resourceErr
	}
	for resourceRows.Next() {
		var resourceType string
		var ref []byte
		if err := resourceRows.Scan(&resourceType, &ref); err != nil {
			resourceRows.Close()
			return "", nil, nil, requested, err
		}
		resources = append(resources, projectorchestration.PlanningResource{
			Type: resourceType,
			Ref: append(json.RawMessage(nil), ref...),
		})
	}
	if err := resourceRows.Err(); err != nil {
		resourceRows.Close()
		return "", nil, nil, requested, err
	}
	resourceRows.Close()

	return brief, contextItems, resources, requested, nil
}

func (r *Runtime) planProjectRevision(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	sourceRevision string,
) error {
	project, err := r.taskSvc.Queries.GetProjectInWorkspace(ctx, dbGetProjectParams(projectID, workspaceID))
	if err != nil {
		return fmt.Errorf("load project for durable planning: %w", err)
	}
	team, ok, err := r.team.FindProject(ctx, workspaceID, projectID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("project team is not provisioned")
	}

	roles := make([]projectorchestration.TeamRole, 0, len(team.Plan.Roles))
	for _, role := range team.Plan.Roles {
		agentID, exists := team.Agent(role.Role)
		if !exists {
			continue
		}
		roles = append(roles, projectorchestration.TeamRole{
			Role: role.Role, Family: role.Family, DisplayName: role.DisplayName,
			Capabilities: role.Capabilities, AgentID: util.UUIDToString(agentID),
		})
	}
	description := ""
	if project.Description.Valid {
		description = project.Description.String
	}
	var currentPlan *projectorchestration.Plan
	if stored, exists, loadErr := r.projectStore.LoadLatestPlan(ctx, workspaceID, projectID); loadErr != nil {
		return loadErr
	} else if exists {
		current := stored.Plan
		currentPlan = &current
	}
	if err := r.refreshRepositoryIntelligence(ctx, workspaceID, projectID); err != nil {
		slog.Warn("repository intelligence refresh failed; planning without fresh snapshot",
			"project_id", util.UUIDToString(projectID),
			"error", err,
		)
	}
	brief, planningContext, resources, requestedPolicy, err := r.loadProjectPlanningBootstrap(ctx, workspaceID, projectID)
	if err != nil {
		return fmt.Errorf("load autonomous project bootstrap context: %w", err)
	}
	plan, execution, err := r.projectPlanner.Plan(ctx, projectorchestration.PlanningInput{
		WorkspaceID: workspaceID,
		ProjectID: projectID,
		ProjectTitle: project.Title,
		ProjectDescription: description,
		BootstrapBrief: brief,
		Context: planningContext,
		Resources: resources,
		Team: roles,
		CurrentPlan: currentPlan,
		Policy: requestedPolicy,
	})
	if err != nil {
		return err
	}
	_, err = r.projectStore.PersistPlan(
		ctx, workspaceID, projectID, sourceRevision,
		execution.Provider, execution.Model, plan,
	)
	return err
}

func dbGetProjectParams(projectID, workspaceID pgtype.UUID) db.GetProjectInWorkspaceParams {
	return db.GetProjectInWorkspaceParams{ID: projectID, WorkspaceID: workspaceID}
}

func (r *Runtime) processDiscoveredProjectWork(ctx context.Context) error {
	if r == nil || r.projectStore == nil || r.team == nil {
		return nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT i.id
		FROM issue i
		JOIN autonomous_project_team t
		  ON t.workspace_id = i.workspace_id
		 AND t.project_id = i.project_id
		 AND t.status = 'active'
		JOIN autonomous_project_team_member tm
		  ON tm.team_id = t.id
		 AND tm.agent_id = i.creator_id
		WHERE i.creator_type = 'agent'
		  AND i.origin_type = 'agent_create'
		  AND i.project_id IS NOT NULL
		  AND i.status NOT IN ('done', 'cancelled')
		  AND EXISTS (
			SELECT 1
			FROM autonomous_project_plan p
			WHERE p.workspace_id = i.workspace_id
			  AND p.project_id = i.project_id
			  AND p.status IN ('active', 'blocked')
		  )
		  AND NOT EXISTS (
			SELECT 1
			FROM autonomous_project_plan_node n
			WHERE n.workspace_id = i.workspace_id
			  AND n.project_id = i.project_id
			  AND n.materialized_issue_id = i.id
		  )
		ORDER BY i.created_at ASC
		LIMIT 20
	`)
	if err != nil {
		return fmt.Errorf("query runtime-discovered project work: %w", err)
	}
	issueIDs := make([]pgtype.UUID, 0, 20)
	for rows.Next() {
		var issueID pgtype.UUID
		if err := rows.Scan(&issueID); err != nil {
			rows.Close()
			return err
		}
		issueIDs = append(issueIDs, issueID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, issueID := range issueIDs {
		issue, err := r.taskSvc.Queries.GetIssue(ctx, issueID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return err
		}
		effectiveStatus := issuestatus.Effective(ctx, r.taskSvc.Queries, issue.WorkspaceID, issue.Status)
		if effectiveStatus == issuestatus.Done || effectiveStatus == issuestatus.Cancelled {
			continue
		}
		if err := r.adoptDiscoveredProjectIssue(ctx, issue); err != nil {
			slog.Warn("runtime-discovered project work adoption failed",
				"project_id", util.UUIDToString(issue.ProjectID),
				"issue_id", util.UUIDToString(issue.ID),
				"error", err,
			)
		}
	}
	return nil
}

func (r *Runtime) adoptDiscoveredProjectIssue(ctx context.Context, issue db.Issue) error {
	if !issue.ProjectID.Valid {
		return nil
	}
	team, ok, err := r.team.FindProject(ctx, issue.WorkspaceID, issue.ProjectID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("active autonomous project team unavailable")
	}

	agentID := issue.CreatorID
	if issue.AssigneeType.Valid && issue.AssigneeType.String == "agent" && issue.AssigneeID.Valid {
		if _, belongs := team.RoleForAgent(issue.AssigneeID); belongs {
			agentID = issue.AssigneeID
		}
	}
	role, ok := team.RoleForAgent(agentID)
	if !ok {
		role, ok = team.RoleForAgent(issue.CreatorID)
		agentID = issue.CreatorID
	}
	if !ok {
		return errors.New("discovered issue actor is not part of autonomous project team")
	}
	roleSpec, _ := team.RoleSpec(role)
	family := strings.TrimSpace(roleSpec.Family)
	kind := discoveredProjectNodeKind(family)

	stored, exists, err := r.projectStore.LoadLatestPlan(ctx, issue.WorkspaceID, issue.ProjectID)
	if err != nil {
		return err
	}
	if !exists || (stored.Status != "active" && stored.Status != "blocked") {
		return errors.New("active autonomous project plan unavailable")
	}
	planID, err := util.ParseUUID(stored.ID)
	if err != nil {
		return err
	}

	issueKey := strings.ReplaceAll(util.UUIDToString(issue.ID), "-", "")
	if len(issueKey) > 20 {
		issueKey = issueKey[:20]
	}
	nodeKey := "discovered_" + issueKey
	description := strings.TrimSpace(issue.Description.String)
	if description == "" {
		description = "Runtime-discovered project work: " + issue.Title
	}
	node := projectorchestration.NodeSpec{
		Key: nodeKey,
		Kind: kind,
		Title: issue.Title,
		Description: description,
		Priority: discoveredProjectPriority(issue.Priority),
		RequiredRoleFamily: family,
		RequiredCapabilities: append([]string(nil), roleSpec.Capabilities...),
		AcceptanceCriteria: []string{
			"Requested runtime-discovered work is completed and its result is recorded on the issue.",
		},
		Risk: projectorchestration.RiskMedium,
		MaxAttempts: 3,
	}

	plan := stored.Plan
	existingNodes := make(map[string]struct{}, len(plan.Nodes))
	for _, existing := range plan.Nodes {
		existingNodes[existing.Key] = struct{}{}
	}
	existingEdges := make(map[string]struct{}, len(plan.Edges))
	edgeIdentity := func(edge projectorchestration.EdgeSpec) string {
		return edge.From + "\x00" + edge.To + "\x00" + string(edge.Type)
	}
	for _, edge := range plan.Edges {
		existingEdges[edgeIdentity(edge)] = struct{}{}
	}
	if _, exists := existingNodes[nodeKey]; exists {
		return r.projectStore.BindNodeIssue(
			ctx, issue.WorkspaceID, issue.ProjectID, nodeKey, issue.ID, role, agentID,
		)
	}

	plan.Nodes = append(plan.Nodes, node)
	blockSourceKey := ""
	sourceKey, sourceNodeStatus, sourceIssueStatus, sourceFound, err := r.discoveredIssueSource(
		ctx, planID, issue,
	)
	if err != nil {
		return err
	}
	if sourceFound {
		sourceBlocked := sourceNodeStatus == "blocked" ||
			issuestatus.Effective(ctx, r.taskSvc.Queries, issue.WorkspaceID, sourceIssueStatus) == issuestatus.Blocked
		if sourceBlocked {
			plan.Edges = append(plan.Edges, projectorchestration.EdgeSpec{
				From: nodeKey,
				To: sourceKey,
				Type: projectorchestration.DependencyHard,
			})
			blockSourceKey = sourceKey
		} else {
			// Provenance edge only: runtime-discovered work may execute in
			// parallel unless an explicit blocker/dependency says otherwise.
			plan.Edges = append(plan.Edges, projectorchestration.EdgeSpec{
				From: sourceKey,
				To: nodeKey,
				Type: projectorchestration.DependencySoft,
			})
		}
	}

	plan = projectorchestration.HardenPlan(plan)
	plan = projectorchestration.EnsureLifecycle(plan)
	plan = projectorchestration.HardenPlan(plan)
	if err := projectorchestration.ValidatePlan(plan, projectorchestration.DefaultMaxNodes); err != nil {
		return fmt.Errorf("runtime-discovered work produced invalid plan delta: %w", err)
	}

	deltaNodes := make([]projectorchestration.NodeSpec, 0)
	for _, candidate := range plan.Nodes {
		if _, exists := existingNodes[candidate.Key]; !exists {
			deltaNodes = append(deltaNodes, candidate)
		}
	}
	deltaEdges := make([]projectorchestration.EdgeSpec, 0)
	for _, candidate := range plan.Edges {
		if _, exists := existingEdges[edgeIdentity(candidate)]; !exists {
			deltaEdges = append(deltaEdges, candidate)
		}
	}
	brainContent := map[string]any{
		"issue_id": util.UUIDToString(issue.ID),
		"title": issue.Title,
		"description": description,
		"origin_task_id": util.UUIDToString(issue.OriginID),
		"source_node_key": sourceKey,
		"source_blocked": blockSourceKey != "",
		"assigned_role": role,
		"required_role_family": family,
	}
	if err := r.projectStore.AppendPlanDelta(
		ctx,
		issue.WorkspaceID,
		issue.ProjectID,
		planID,
		deltaNodes,
		deltaEdges,
		nodeKey,
		issue.ID,
		role,
		agentID,
		blockSourceKey,
		"Runtime-discovered work: "+issue.Title,
		brainContent,
	); err != nil {
		return err
	}
	if issuestatus.Effective(ctx, r.taskSvc.Queries, issue.WorkspaceID, issue.Status) == issuestatus.Blocked {
		if err := r.projectStore.SetNodeBlocked(
			ctx,
			issue.WorkspaceID,
			issue.ProjectID,
			nodeKey,
			"manual",
			"runtime-discovered issue is currently blocked",
		); err != nil {
			return err
		}
	}

	slog.Info("runtime-discovered issue adopted into project plan",
		"project_id", util.UUIDToString(issue.ProjectID),
		"issue_id", util.UUIDToString(issue.ID),
		"node", nodeKey,
		"source_node", sourceKey,
		"blocks_source", blockSourceKey != "",
	)
	return nil
}

func (r *Runtime) discoveredIssueSource(
	ctx context.Context,
	planID pgtype.UUID,
	issue db.Issue,
) (nodeKey, nodeStatus, issueStatus string, found bool, err error) {
	if !issue.OriginType.Valid || issue.OriginType.String != "agent_create" || !issue.OriginID.Valid {
		return "", "", "", false, nil
	}
	err = r.pool.QueryRow(ctx, `
		SELECT n.node_key, n.status, source_issue.status
		FROM agent_task_queue origin_task
		JOIN issue source_issue ON source_issue.id = origin_task.issue_id
		JOIN autonomous_project_plan_node n
		  ON n.plan_id = $1
		 AND n.materialized_issue_id = source_issue.id
		WHERE origin_task.id = $2
		LIMIT 1
	`, planID, issue.OriginID).Scan(&nodeKey, &nodeStatus, &issueStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", false, nil
	}
	if err != nil {
		return "", "", "", false, err
	}
	return nodeKey, nodeStatus, issueStatus, true, nil
}

func discoveredProjectNodeKind(family string) projectorchestration.NodeKind {
	switch strings.TrimSpace(family) {
	case "product":
		return projectorchestration.NodeProduct
	case "architecture":
		return projectorchestration.NodeArchitecture
	case "design":
		return projectorchestration.NodeDesign
	case "security":
		return projectorchestration.NodeSecurity
	case "qa":
		return projectorchestration.NodeQA
	case "review":
		return projectorchestration.NodeReview
	case "release":
		return projectorchestration.NodeRelease
	default:
		return projectorchestration.NodeImplementation
	}
}

func discoveredProjectPriority(priority string) int {
	switch strings.TrimSpace(priority) {
	case "urgent":
		return 100
	case "high":
		return 75
	case "medium":
		return 50
	case "low":
		return 25
	default:
		return 0
	}
}

func (r *Runtime) processProjectScheduling(ctx context.Context) error {
	if r.projectStore == nil || r.issueSvc == nil || r.team == nil {
		return nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT p.workspace_id, p.project_id
		FROM autonomous_project_plan p
		LEFT JOIN autonomous_project_control c
		  ON c.project_id = p.project_id AND c.workspace_id = p.workspace_id
		WHERE p.status IN ('active', 'blocked')
		  AND COALESCE(c.paused, FALSE) = FALSE
		ORDER BY p.project_id
		LIMIT 20
	`)
	if err != nil {
		return fmt.Errorf("query schedulable project plans: %w", err)
	}
	type projectRef struct{ workspaceID, projectID pgtype.UUID }
	projects := make([]projectRef, 0, 20)
	for rows.Next() {
		var item projectRef
		if err := rows.Scan(&item.workspaceID, &item.projectID); err != nil {
			rows.Close()
			return err
		}
		projects = append(projects, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, project := range projects {
		// Evaluate unblock conditions before projecting node state back to the
		// board. Otherwise a human/condition resolver moving a technical block
		// out of Blocked would be overwritten before the conductor could see it.
		if err := r.reconcileProjectBlockedNodes(ctx, project.workspaceID, project.projectID); err != nil {
			slog.Warn("project conductor blocked-node reconciliation failed",
				"project_id", util.UUIDToString(project.projectID), "error", err)
		}

		// Board projection is intentionally eager: every issue-backed DAG node
		// becomes visible as soon as the plan exists. Execution is a separate
		// claim below, so pending dependencies stay parked in Backlog.
		planned, err := r.projectStore.ListPlanNodes(ctx, project.workspaceID, project.projectID, 200)
		if err != nil {
			slog.Warn("project conductor plan projection failed",
				"project_id", util.UUIDToString(project.projectID), "error", err)
			continue
		}
		materialized := false
		for _, node := range planned {
			if node.Kind == projectorchestration.NodeDeploy || node.Kind == projectorchestration.NodeObserve {
				continue
			}
			if node.MaterializedIssueID == "" {
				if err := r.ensureProjectNodeIssue(ctx, project.workspaceID, project.projectID, node); err != nil {
					if escalationErr := r.openProjectEscalation(ctx, project.workspaceID, project.projectID, node.ReadyNode, err); escalationErr != nil {
						slog.Warn("project conductor materialization escalation failed",
							"project_id", util.UUIDToString(project.projectID), "node", node.Key, "error", escalationErr)
					}
					continue
				}
				materialized = true
				continue
			}
			if err := r.syncProjectNodeBoardState(ctx, project.workspaceID, node); err != nil {
				slog.Warn("project conductor board projection failed",
					"project_id", util.UUIDToString(project.projectID), "node", node.Key, "error", err)
			}
		}
		// Give newly materialized plan issues one reconciliation interval as a
		// stable board projection before dispatching them. This also avoids a
		// create/backlog -> in_progress transition racing the issue-created
		// event on slow clients.
		if materialized {
			continue
		}

		ready, err := r.projectStore.ListReadyNodes(ctx, project.workspaceID, project.projectID, 20)
		if err != nil {
			slog.Warn("project scheduler readiness failed", "project_id", util.UUIDToString(project.projectID), "error", err)
			continue
		}
		for _, node := range ready {
			if err := r.startReadyProjectNode(ctx, project.workspaceID, project.projectID, node); err != nil {
				if escalationErr := r.openProjectEscalation(ctx, project.workspaceID, project.projectID, node, err); escalationErr != nil {
					slog.Warn("project scheduler escalation failed", "project_id", util.UUIDToString(project.projectID), "node", node.Key, "error", escalationErr)
				}
				if blockErr := r.blockProjectNodeForSchedulingCause(ctx, project.workspaceID, project.projectID, node, err); blockErr != nil {
					slog.Warn("project scheduler block projection failed", "project_id", util.UUIDToString(project.projectID), "node", node.Key, "error", blockErr)
				}
			}
		}
	}
	return nil
}

func (r *Runtime) ensureProjectNodeIssue(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	node projectorchestration.PlannedNode,
) error {
	team, ok, err := r.team.FindProject(ctx, workspaceID, projectID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("project team unavailable")
	}
	agentID, role, family, err := r.selectProjectNodeAgent(ctx, team, node.ReadyNode)
	if err != nil {
		return err
	}
	var ownerUserID pgtype.UUID
	if err := r.pool.QueryRow(ctx, `
		SELECT owner_user_id
		FROM autonomous_project_team
		WHERE workspace_id = $1 AND project_id = $2 AND status = 'active'
	`, workspaceID, projectID).Scan(&ownerUserID); err != nil {
		return fmt.Errorf("resolve project accountable user: %w", err)
	}

	criteria := ""
	if len(node.AcceptanceCriteria) > 0 {
		var b strings.Builder
		b.WriteString("\n\nAcceptance criteria:\n")
		for _, item := range node.AcceptanceCriteria {
			b.WriteString("- ")
			b.WriteString(item)
			b.WriteString("\n")
		}
		criteria = b.String()
	}
	description := fmt.Sprintf(
		"[autonomous-project-node:%s]\nStage: %s\nRisk: %s\nRequired role: %s (%s)\n\n%s%s",
		node.Key, node.Kind, node.Risk, role, family, node.Description, criteria,
	)
	res, err := r.issueSvc.Create(ctx, service.IssueCreateParams{
		WorkspaceID: workspaceID,
		Title: node.Title,
		Description: pgtype.Text{String: description, Valid: true},
		Status: issuestatus.Backlog,
		Priority: projectNodePriority(node.Priority),
		AssigneeType: pgtype.Text{String: "agent", Valid: true},
		AssigneeID: agentID,
		CreatorType: "member",
		CreatorID: ownerUserID,
		ProjectID: projectID,
		AllowDuplicate: false,
	}, service.IssueCreateOpts{
		ActorID: util.UUIDToString(ownerUserID),
		AnalyticsAgentID: util.UUIDToString(agentID),
		Platform: "autonomous",
	})
	var issue db.Issue
	switch {
	case errors.Is(err, service.ErrActiveDuplicate) && res.DuplicateIssue != nil:
		issue = *res.DuplicateIssue
	case err != nil:
		return fmt.Errorf("materialize project node %q: %w", node.Key, err)
	default:
		issue = res.Issue
	}
	if err := r.projectStore.BindNodeIssue(
		ctx, workspaceID, projectID, node.Key, issue.ID, role, agentID,
	); err != nil {
		return err
	}
	if node.Status == "ready" && issue.Status == issuestatus.Backlog {
		if _, err := r.taskSvc.SetIssueStatusForWorkflow(ctx, issue.ID, issuestatus.Todo); err != nil {
			return fmt.Errorf("promote ready project node to todo: %w", err)
		}
	}
	return nil
}

func (r *Runtime) syncProjectNodeBoardState(
	ctx context.Context,
	workspaceID pgtype.UUID,
	node projectorchestration.PlannedNode,
) error {
	if node.MaterializedIssueID == "" {
		return nil
	}
	issueID, err := util.ParseUUID(node.MaterializedIssueID)
	if err != nil {
		return err
	}
	issue, err := r.taskSvc.Queries.GetIssue(ctx, issueID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return r.projectStore.ResetDeletedIssue(ctx, workspaceID, issueID)
		}
		return err
	}
	target := ""
	switch node.Status {
	case "pending":
		target = issuestatus.Backlog
	case "ready":
		if issuestatus.Effective(ctx, r.taskSvc.Queries, issue.WorkspaceID, issue.Status) != issuestatus.Blocked {
			target = issuestatus.Todo
		}
	case "running":
		target = issuestatus.InProgress
	case "verification":
		target = issuestatus.InReview
	case "blocked":
		target = issuestatus.Blocked
	}
	if target == "" || issue.Status == target {
		return nil
	}
	_, err = r.taskSvc.SetIssueStatusForWorkflow(ctx, issue.ID, target)
	return err
}

func (r *Runtime) startReadyProjectNode(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	node projectorchestration.ReadyNode,
) error {
	if err := r.enforceProjectNodePolicy(ctx, workspaceID, projectID, node); err != nil {
		return err
	}
	switch node.Kind {
	case projectorchestration.NodeDeploy:
		if r.deploymentAdapter == nil {
			return projectorchestration.ErrAdapterNotConfigured
		}
		return r.executeDeploymentNode(ctx, workspaceID, projectID, node)
	case projectorchestration.NodeObserve:
		if r.observationAdapter == nil {
			return projectorchestration.ErrAdapterNotConfigured
		}
		return r.executeObservationNode(ctx, workspaceID, projectID, node)
	}

	var issueID, agentID pgtype.UUID
	var assignedRole string
	if err := r.pool.QueryRow(ctx, `
		SELECT materialized_issue_id, assigned_agent_id, COALESCE(assigned_role, '')
		FROM autonomous_project_plan_node n
		JOIN autonomous_project_plan p ON p.id = n.plan_id
		WHERE n.workspace_id = $1
		  AND n.project_id = $2
		  AND n.node_key = $3
		  AND n.status = 'ready'
		  AND p.status IN ('active', 'blocked')
		ORDER BY p.revision DESC
		LIMIT 1
	`, workspaceID, projectID, node.Key).Scan(&issueID, &agentID, &assignedRole); err != nil {
		return err
	}
	if !issueID.Valid || !agentID.Valid {
		return fmt.Errorf("ready project node %q is not fully materialized", node.Key)
	}
	issue, err := r.taskSvc.Queries.GetIssue(ctx, issueID)
	if err != nil {
		return err
	}
	effectiveIssueStatus := issuestatus.Effective(ctx, r.taskSvc.Queries, issue.WorkspaceID, issue.Status)
	if effectiveIssueStatus == issuestatus.Blocked {
		return nil
	}
	if effectiveIssueStatus == issuestatus.Backlog {
		issue, err = r.taskSvc.SetIssueStatusForWorkflow(ctx, issue.ID, issuestatus.Todo)
		if err != nil {
			return err
		}
	}

	var ownerUserID pgtype.UUID
	if err := r.pool.QueryRow(ctx, `
		SELECT owner_user_id
		FROM autonomous_project_team
		WHERE workspace_id = $1 AND project_id = $2 AND status = 'active'
	`, workspaceID, projectID).Scan(&ownerUserID); err != nil {
		return fmt.Errorf("resolve project accountable user: %w", err)
	}

	if err := r.projectStore.ClaimReadyNode(ctx, workspaceID, projectID, node.Key); err != nil {
		return err
	}
	if err := r.startMaterializedNode(ctx, issue, node, agentID, ownerUserID); err != nil {
		_ = r.projectStore.ReleaseNodeClaim(ctx, workspaceID, projectID, node.Key, "dispatch failed: "+err.Error())
		_, _ = r.taskSvc.SetIssueStatusForWorkflow(ctx, issue.ID, issuestatus.Todo)
		return err
	}
	_ = assignedRole // retained for durable assignment/debug projection.
	return nil
}

func (r *Runtime) blockProjectNodeForSchedulingCause(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	node projectorchestration.ReadyNode,
	cause error,
) error {
	category := ""
	switch {
	case errors.Is(cause, errProjectApprovalRequired):
		category = "approval"
	case errors.Is(cause, projectorchestration.ErrAdapterNotConfigured):
		category = "external_dependency"
	case errors.Is(cause, projectorchestration.ErrBudgetExceeded):
		category = "budget"
	}
	if category == "" {
		return nil
	}
	if err := r.projectStore.SetNodeBlocked(ctx, workspaceID, projectID, node.Key, category, cause.Error()); err != nil {
		return err
	}
	var issueID pgtype.UUID
	err := r.pool.QueryRow(ctx, `
		SELECT materialized_issue_id
		FROM autonomous_project_plan_node n
		JOIN autonomous_project_plan p ON p.id = n.plan_id
		WHERE n.workspace_id = $1
		  AND n.project_id = $2
		  AND n.node_key = $3
		  AND p.status IN ('active', 'blocked')
		ORDER BY p.revision DESC
		LIMIT 1
	`, workspaceID, projectID, node.Key).Scan(&issueID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if issueID.Valid {
		_, err = r.taskSvc.SetIssueStatusForWorkflow(ctx, issueID, issuestatus.Blocked)
	}
	return err
}

func (r *Runtime) reconcileProjectBlockedNodes(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
) error {
	blocked, err := r.projectStore.ListBlockedNodes(ctx, workspaceID, projectID)
	if err != nil {
		return err
	}
	for _, node := range blocked {
		resolved := false
		switch node.Category {
		case "dependency":
			resolved = true // ResumeBlockedNode performs the authoritative dependency check.
		case "approval":
			err := r.pool.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1
					FROM autonomous_project_escalation
					WHERE workspace_id = $1
					  AND project_id = $2
					  AND category = 'approval_required'
					  AND status = 'resolved'
					  AND context ->> 'node_key' = $3
					  AND resolution ->> 'decision' = 'approved'
				)
			`, workspaceID, projectID, node.Key).Scan(&resolved)
			if err != nil {
				return err
			}
		case "external_dependency":
			// A provider that was simply absent can auto-resume once configured.
			// Work that already exhausted its node retry budget requires an
			// explicit resolved escalation before another attempt.
			if node.Attempt < node.MaxAttempts {
				switch node.Kind {
				case projectorchestration.NodeDeploy:
					resolved = r.deploymentAdapter != nil
				case projectorchestration.NodeObserve:
					resolved = r.observationAdapter != nil
				}
			}
			if !resolved {
				err := r.pool.QueryRow(ctx, `
					SELECT EXISTS (
						SELECT 1
						FROM autonomous_project_escalation
						WHERE workspace_id = $1
						  AND project_id = $2
						  AND category = 'external_dependency'
						  AND status = 'resolved'
						  AND context ->> 'node_key' = $3
					)
				`, workspaceID, projectID, node.Key).Scan(&resolved)
				if err != nil {
					return err
				}
			}
		case "budget":
			err := r.pool.QueryRow(ctx, `
				SELECT
					total_attempts < max_total_attempts
					AND (token_limit IS NULL OR tokens_used < token_limit)
					AND (runtime_seconds_limit IS NULL OR runtime_seconds_used < runtime_seconds_limit)
					AND (cost_microunits_limit IS NULL OR cost_microunits_used < cost_microunits_limit)
				FROM autonomous_project_budget
				WHERE workspace_id = $1 AND project_id = $2
			`, workspaceID, projectID).Scan(&resolved)
			if errors.Is(err, pgx.ErrNoRows) {
				resolved = true
			} else if err != nil {
				return err
			}
		case "technical_failure", "manual":
			if node.MaterializedIssueID != "" {
				issueID, parseErr := util.ParseUUID(node.MaterializedIssueID)
				if parseErr != nil {
					return parseErr
				}
				issue, loadErr := r.taskSvc.Queries.GetIssue(ctx, issueID)
				if errors.Is(loadErr, pgx.ErrNoRows) {
					resolved = true
				} else if loadErr != nil {
					return loadErr
				} else {
					resolved = issuestatus.Effective(ctx, r.taskSvc.Queries, issue.WorkspaceID, issue.Status) != issuestatus.Blocked
				}
			}
			if !resolved {
				// A resolved technical escalation is an explicit retry signal.
				err := r.pool.QueryRow(ctx, `
					SELECT EXISTS (
						SELECT 1
						FROM autonomous_project_escalation
						WHERE workspace_id = $1
						  AND project_id = $2
						  AND status = 'resolved'
						  AND context ->> 'node_key' = $3
						  AND category IN ('technical_failure', 'external_dependency')
					)
				`, workspaceID, projectID, node.Key).Scan(&resolved)
				if err != nil {
					return err
				}
			}
		}
		if !resolved {
			continue
		}
		resumed, err := r.projectStore.ResumeBlockedNode(ctx, workspaceID, projectID, node.Key)
		if err != nil {
			return err
		}
		if !resumed || node.MaterializedIssueID == "" {
			continue
		}
		issueID, err := util.ParseUUID(node.MaterializedIssueID)
		if err != nil {
			return err
		}
		if _, err := r.taskSvc.SetIssueStatusForWorkflow(ctx, issueID, issuestatus.Todo); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		slog.Info("project conductor resumed blocked node",
			"project_id", util.UUIDToString(projectID),
			"node", node.Key,
			"category", node.Category,
		)
	}
	return r.projectStore.ResumePlanAfterNodeRetry(ctx, workspaceID, projectID)
}

var errProjectApprovalRequired = errors.New("autonomous project node requires approval")

func (r *Runtime) executeDeploymentNode(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	node projectorchestration.ReadyNode,
) error {
	nodeID, planID, err := r.projectStore.StartExternalNode(ctx, workspaceID, projectID, node.Key)
	if err != nil {
		return err
	}

	fail := func(cause error) error {
		_, failErr := r.projectStore.FailExternalNode(ctx, workspaceID, projectID, node.Key, cause.Error())
		if failErr != nil {
			return fmt.Errorf("%v; persist deployment node failure: %w", cause, failErr)
		}
		return cause
	}

	var sourceRevision string
	var policyJSON []byte
	if err := r.pool.QueryRow(ctx, `
		SELECT source_revision, policy
		FROM autonomous_project_plan
		WHERE id = $1 AND workspace_id = $2 AND project_id = $3
	`, planID, workspaceID, projectID).Scan(&sourceRevision, &policyJSON); err != nil {
		return fail(err)
	}
	var policy projectorchestration.Policy
	if err := json.Unmarshal(policyJSON, &policy); err != nil {
		return fail(fmt.Errorf("decode deployment policy: %w", err))
	}

	var deploymentID pgtype.UUID
	if err := r.pool.QueryRow(ctx, `
		INSERT INTO autonomous_project_deployment (
			workspace_id, project_id, plan_id, environment, provider,
			status, policy_snapshot, started_at
		)
		VALUES ($1, $2, $3, 'production', 'webhook', 'running', $4, now())
		RETURNING id
	`, workspaceID, projectID, planID, policyJSON).Scan(&deploymentID); err != nil {
		return fail(fmt.Errorf("create deployment record: %w", err))
	}

	result, err := r.deploymentAdapter.Deploy(ctx, projectorchestration.DeploymentRequest{
		WorkspaceID: workspaceID,
		ProjectID: projectID,
		PlanID: planID,
		Environment: "production",
		ReleaseRef: sourceRevision,
		Policy: policy,
	})
	if err != nil {
		_, _ = r.pool.Exec(ctx, `
			UPDATE autonomous_project_deployment
			SET status = 'failed',
			    evidence = jsonb_build_object('error', $2),
			    completed_at = now()
			WHERE id = $1
		`, deploymentID, err.Error())
		return fail(err)
	}

	evidence, _ := json.Marshal(result.Evidence)
	status := result.Status
	if status != "succeeded" {
		status = "failed"
	}
	if _, err := r.pool.Exec(ctx, `
		UPDATE autonomous_project_deployment
		SET provider = $2,
		    external_ref = NULLIF($3, ''),
		    status = $4,
		    evidence = $5,
		    completed_at = now()
		WHERE id = $1
	`, deploymentID, result.Provider, result.ExternalRef, status, evidence); err != nil {
		return fail(fmt.Errorf("update deployment record: %w", err))
	}
	if status != "succeeded" {
		return fail(errors.New("deployment provider reported failure"))
	}

	artifactContent, _ := json.Marshal(map[string]any{
		"deployment_id": util.UUIDToString(deploymentID),
		"provider": result.Provider,
		"external_ref": result.ExternalRef,
		"environment": "production",
		"release_ref": sourceRevision,
		"evidence": result.Evidence,
	})
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO autonomous_project_artifact (
			workspace_id, project_id, plan_id, node_id, artifact_type, name, content
		)
		VALUES ($1, $2, $3, $4, 'deployment_record', $5, $6)
	`, workspaceID, projectID, planID, nodeID, "Deployment: "+node.Title, artifactContent); err != nil {
		return fail(fmt.Errorf("record deployment artifact: %w", err))
	}
	return r.projectStore.CompleteExternalNode(ctx, workspaceID, projectID, node.Key)
}

func (r *Runtime) executeObservationNode(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	node projectorchestration.ReadyNode,
) error {
	nodeID, planID, err := r.projectStore.StartExternalNode(ctx, workspaceID, projectID, node.Key)
	if err != nil {
		return err
	}
	fail := func(cause error) error {
		_, failErr := r.projectStore.FailExternalNode(ctx, workspaceID, projectID, node.Key, cause.Error())
		if failErr != nil {
			return fmt.Errorf("%v; persist observation node failure: %w", cause, failErr)
		}
		return cause
	}

	var deploymentID pgtype.UUID
	if err := r.pool.QueryRow(ctx, `
		SELECT id
		FROM autonomous_project_deployment
		WHERE workspace_id = $1
		  AND project_id = $2
		  AND status = 'succeeded'
		ORDER BY completed_at DESC NULLS LAST, created_at DESC
		LIMIT 1
	`, workspaceID, projectID).Scan(&deploymentID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fail(fmt.Errorf("%w: no successful deployment exists for observation", projectorchestration.ErrAdapterNotConfigured))
		}
		return fail(err)
	}

	result, err := r.observationAdapter.Observe(ctx, projectorchestration.ObservationRequest{
		WorkspaceID: workspaceID,
		ProjectID: projectID,
		DeploymentID: deploymentID,
		WindowSeconds: 300,
	})
	if err != nil {
		return fail(err)
	}

	content, _ := json.Marshal(map[string]any{
		"deployment_id": util.UUIDToString(deploymentID),
		"healthy": result.Healthy,
		"error_rate": result.ErrorRate,
		"latency_p95": result.LatencyP95,
		"signals": result.Signals,
		"evidence": result.Evidence,
	})
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO autonomous_project_artifact (
			workspace_id, project_id, plan_id, node_id, artifact_type, name, content
		)
		VALUES ($1, $2, $3, $4, 'generic', $5, $6)
	`, workspaceID, projectID, planID, nodeID, "Observation: "+node.Title, content); err != nil {
		return fail(fmt.Errorf("record observation artifact: %w", err))
	}

	if !result.Healthy {
		severity := "medium"
		if result.ErrorRate >= 0.05 {
			severity = "high"
		}
		if _, err := r.pool.Exec(ctx, `
			INSERT INTO autonomous_project_incident (
				workspace_id, project_id, deployment_id, severity, status, title, evidence
			)
			VALUES ($1, $2, $3, $4, 'open', $5, $6)
		`, workspaceID, projectID, deploymentID, severity,
			"Autonomous observation detected regression: "+node.Title, content); err != nil {
			return fail(fmt.Errorf("record autonomous incident: %w", err))
		}
	}

	return r.projectStore.CompleteExternalNode(ctx, workspaceID, projectID, node.Key)
}

func (r *Runtime) startMaterializedNode(
	ctx context.Context,
	issue db.Issue,
	node projectorchestration.ReadyNode,
	agentID, accountableID pgtype.UUID,
) error {
	updated, err := r.taskSvc.SetIssueStatusForWorkflow(ctx, issue.ID, issuestatus.InProgress)
	if err != nil {
		return err
	}
	if projectNodeUsesIssueWorkflow(node.Kind) {
		return nil
	}
	handoff := fmt.Sprintf(
		"Autonomous project stage %s (%s). Complete the stage against the issue acceptance criteria. "+
			"Do not create or dispatch follow-up work and do not manage dependency state; the Project OS scheduler owns orchestration. "+
			"Finish this task normally when the stage output is ready.",
		node.Kind, node.Key,
	)
	_, err = r.taskSvc.EnqueueTaskForWorkflow(ctx, updated, agentID, accountableID, handoff)
	if errors.Is(err, service.ErrDuplicatePendingTask) {
		return nil
	}
	return err
}

func projectNodeUsesIssueWorkflow(kind projectorchestration.NodeKind) bool {
	switch kind {
	case projectorchestration.NodeImplementation, projectorchestration.NodeMigration, projectorchestration.NodeIntegration:
		return true
	default:
		return false
	}
}

func projectNodePriority(priority int) string {
	switch {
	case priority >= 90:
		return "urgent"
	case priority >= 60:
		return "high"
	case priority >= 30:
		return "medium"
	case priority > 0:
		return "low"
	default:
		return "none"
	}
}

func (r *Runtime) selectProjectNodeAgent(
	ctx context.Context,
	team teamprovision.Team,
	node projectorchestration.ReadyNode,
) (pgtype.UUID, string, string, error) {
	families := []string{}
	addFamily := func(family string) {
		family = strings.TrimSpace(family)
		if family == "" {
			return
		}
		for _, existing := range families {
			if existing == family {
				return
			}
		}
		families = append(families, family)
	}
	addFamily(node.RequiredRoleFamily)
	switch node.Kind {
	case projectorchestration.NodeResearch, projectorchestration.NodeProduct:
		addFamily("product")
	case projectorchestration.NodeArchitecture:
		addFamily("architecture")
	case projectorchestration.NodeDesign:
		addFamily("design"); addFamily("frontend")
	case projectorchestration.NodeReview:
		addFamily("review")
	case projectorchestration.NodeQA:
		addFamily("qa"); addFamily("review")
	case projectorchestration.NodeSecurity:
		addFamily("security")
	case projectorchestration.NodeRelease:
		addFamily("release"); addFamily("devops")
	case projectorchestration.NodeDeploy:
		addFamily("devops"); addFamily("release"); addFamily("sre")
	case projectorchestration.NodeObserve, projectorchestration.NodeIncident:
		addFamily("sre"); addFamily("devops")
	}

	type candidate struct {
		id     pgtype.UUID
		role   string
		family string
		score  float64
	}
	candidates := []candidate{}
	for familyIndex, family := range families {
		for _, spec := range team.Plan.Roles {
			if spec.Family != family {
				continue
			}
			id, ok := team.Agent(spec.Role)
			if !ok {
				continue
			}
			if projectNodeUsesIssueWorkflow(node.Kind) && !teamprovision.IsImplementationFamily(spec.Family) {
				continue
			}
			performance, err := r.projectStore.AgentPerformance(ctx, team.WorkspaceID, id, spec.Family)
			if err != nil {
				return pgtype.UUID{}, "", "", err
			}
			var active int
			if err := r.pool.QueryRow(ctx, `
				SELECT COUNT(*)
				FROM agent_task_queue
				WHERE agent_id = $1
				  AND status IN ('queued','dispatched','running','waiting_local_directory','deferred')
			`, id).Scan(&active); err != nil {
				return pgtype.UUID{}, "", "", err
			}
			// Earlier family preferences are a weak tiebreaker; live queue load
			// dominates history so a strong but saturated agent does not hoard work.
			score := performance.Score() - float64(active*25) - float64(familyIndex)
			candidates = append(candidates, candidate{id: id, role: spec.Role, family: spec.Family, score: score})
		}
	}
	if len(candidates) == 0 && projectNodeUsesIssueWorkflow(node.Kind) && team.Plan.ImplementationRole != "" {
		if id, ok := team.Agent(team.Plan.ImplementationRole); ok {
			if spec, found := team.RoleSpec(team.Plan.ImplementationRole); found {
				return id, spec.Role, spec.Family, nil
			}
		}
	}
	if len(candidates) == 0 {
		return pgtype.UUID{}, "", "", fmt.Errorf(
			"no provisioned specialist can execute node %q (kind=%s family=%q capabilities=%v)",
			node.Key, node.Kind, node.RequiredRoleFamily, node.RequiredCapabilities,
		)
	}
	best := candidates[0]
	for _, current := range candidates[1:] {
		if current.score > best.score {
			best = current
		}
	}
	return best.id, best.role, best.family, nil
}

func (r *Runtime) enforceProjectNodePolicy(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	node projectorchestration.ReadyNode,
) error {
	var raw []byte
	err := r.pool.QueryRow(ctx, `
		SELECT policy
		FROM autonomous_project_plan
		WHERE workspace_id = $1 AND project_id = $2 AND status IN ('active', 'blocked')
		ORDER BY revision DESC
		LIMIT 1
	`, workspaceID, projectID).Scan(&raw)
	if err != nil {
		return err
	}
	var policy projectorchestration.Policy
	if err := json.Unmarshal(raw, &policy); err != nil {
		return fmt.Errorf("decode autonomous project policy: %w", err)
	}
	requiresApproval := false
	switch {
	case node.Kind == projectorchestration.NodeMigration && policy.Approvals.DatabaseMigration:
		requiresApproval = true
	case node.Kind == projectorchestration.NodeDeploy && policy.Approvals.ProductionDeploy:
		requiresApproval = true
	case node.Risk == projectorchestration.RiskCritical && policy.Approvals.CriticalRisk:
		requiresApproval = true
	}
	if !requiresApproval {
		return nil
	}
	var approved bool
	err = r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM autonomous_project_escalation
			WHERE workspace_id = $1
			  AND project_id = $2
			  AND category = 'approval_required'
			  AND status = 'resolved'
			  AND context ->> 'node_key' = $3
			  AND resolution ->> 'decision' = 'approved'
		)
	`, workspaceID, projectID, node.Key).Scan(&approved)
	if err != nil {
		return err
	}
	if !approved {
		return errProjectApprovalRequired
	}
	return nil
}

func (r *Runtime) openProjectEscalation(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	node projectorchestration.ReadyNode,
	cause error,
) error {
	category := "technical_failure"
	summary := "Project node could not be scheduled: " + node.Title
	if errors.Is(cause, errProjectApprovalRequired) {
		category = "approval_required"
		summary = "Project node requires approval: " + node.Title
	} else if errors.Is(cause, projectorchestration.ErrBudgetExceeded) {
		category = "budget_exceeded"
		summary = "Project budget prevents scheduling: " + node.Title
	} else if errors.Is(cause, projectorchestration.ErrAdapterNotConfigured) {
		category = "external_dependency"
		summary = "Project delivery adapter is not configured: " + node.Title
	}
	contextJSON, _ := json.Marshal(map[string]any{
		"node_key": node.Key,
		"kind": node.Kind,
		"required_role_family": node.RequiredRoleFamily,
		"required_capabilities": node.RequiredCapabilities,
		"error": cause.Error(),
	})
	_, err := r.pool.Exec(ctx, `
		INSERT INTO autonomous_project_escalation (
			workspace_id, project_id, category, severity, summary, context
		)
		SELECT $1, $2, $7,
		       CASE WHEN $3 IN ('high', 'critical') THEN $3 ELSE 'medium' END,
		       $4, $5
		WHERE NOT EXISTS (
			SELECT 1
			FROM autonomous_project_escalation
			WHERE workspace_id = $1
			  AND project_id = $2
			  AND status IN ('open', 'acknowledged')
			  AND context ->> 'node_key' = $6
		)
	`, workspaceID, projectID, string(node.Risk),
		summary, contextJSON, node.Key, category)
	return err
}


func (r *Runtime) recordAgentPerformance(
	ctx context.Context,
	task db.AgentTaskQueue,
	issue db.Issue,
	outcome projectorchestration.AgentOutcome,
) error {
	if r.projectStore == nil || !issue.ProjectID.Valid || !task.AgentID.Valid {
		return nil
	}
	var family string
	err := r.pool.QueryRow(ctx, `
		SELECT m.role_family
		FROM autonomous_project_team_member m
		JOIN autonomous_project_team t ON t.id = m.team_id
		WHERE t.workspace_id = $1
		  AND t.project_id = $2
		  AND t.status = 'active'
		  AND m.agent_id = $3
		  AND m.active = TRUE
		LIMIT 1
	`, issue.WorkspaceID, issue.ProjectID, task.AgentID).Scan(&family)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	runtime := time.Duration(0)
	if task.CreatedAt.Valid {
		end := time.Now()
		if task.CompletedAt.Valid {
			end = task.CompletedAt.Time
		}
		if end.After(task.CreatedAt.Time) {
			runtime = end.Sub(task.CreatedAt.Time)
		}
	}
	return r.projectStore.RecordAgentOutcome(
		ctx, issue.WorkspaceID, task.AgentID, family, outcome, runtime,
	)
}

func (r *Runtime) accountProjectTaskUsage(
	ctx context.Context,
	task db.AgentTaskQueue,
	issue db.Issue,
) (bool, error) {
	if r.projectStore == nil || !issue.ProjectID.Valid {
		return false, nil
	}
	var nodeKey string
	err := r.pool.QueryRow(ctx, `
		SELECT node_key
		FROM autonomous_project_plan_node
		WHERE workspace_id = $1
		  AND project_id = $2
		  AND materialized_issue_id = $3
		ORDER BY updated_at DESC
		LIMIT 1
	`, issue.WorkspaceID, issue.ProjectID, issue.ID).Scan(&nodeKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	var tokens, costTicks int64
	if err := r.pool.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(input_tokens + output_tokens + cache_read_tokens + cache_write_tokens), 0)::bigint,
			COALESCE(SUM(cost_usd_ticks), 0)::bigint
		FROM task_usage
		WHERE task_id = $1
	`, task.ID).Scan(&tokens, &costTicks); err != nil {
		return false, err
	}
	var runtimeSeconds int64
	if task.StartedAt.Valid && task.CompletedAt.Valid && task.CompletedAt.Time.After(task.StartedAt.Time) {
		runtimeSeconds = int64(task.CompletedAt.Time.Sub(task.StartedAt.Time).Seconds())
	}
	// task_usage cost is stored in 1e-10 USD ticks; Project OS budget uses
	// micro-USD, so 10,000 ticks = 1 micro-USD. Round up so tiny authoritative
	// charges are never silently treated as free.
	costMicrounits := int64(0)
	if costTicks > 0 {
		costMicrounits = (costTicks + 9999) / 10000
	}
	err = r.projectStore.AccountTaskUsage(
		ctx,
		issue.WorkspaceID,
		issue.ProjectID,
		task.ID,
		tokens,
		runtimeSeconds,
		costMicrounits,
	)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, projectorchestration.ErrBudgetExceeded) {
		return false, err
	}

	reason := "project token/runtime/cost budget exceeded after task " + util.UUIDToString(task.ID)
	_, _ = r.pool.Exec(ctx, `
		UPDATE autonomous_project_plan_node
		SET status = 'blocked',
		    blocked_category = 'budget',
		    blocked_reason = $4,
		    updated_at = now()
		WHERE workspace_id = $1
		  AND project_id = $2
		  AND materialized_issue_id = $3
		  AND status NOT IN ('completed', 'cancelled')
	`, issue.WorkspaceID, issue.ProjectID, issue.ID, reason)
	_, _ = r.pool.Exec(ctx, `
		UPDATE autonomous_project_plan
		SET status = 'blocked', updated_at = now()
		WHERE workspace_id = $1 AND project_id = $2 AND status = 'active'
	`, issue.WorkspaceID, issue.ProjectID)
	contextJSON, _ := json.Marshal(map[string]any{
		"node_key": nodeKey,
		"task_id": util.UUIDToString(task.ID),
		"tokens": tokens,
		"runtime_seconds": runtimeSeconds,
		"cost_microunits": costMicrounits,
	})
	_, _ = r.pool.Exec(ctx, `
		INSERT INTO autonomous_project_escalation (
			workspace_id, project_id, category, severity, summary, context
		)
		VALUES ($1, $2, 'budget_exceeded', 'high', $3, $4)
	`, issue.WorkspaceID, issue.ProjectID, "Project usage budget exceeded: "+nodeKey, contextJSON)
	_, _ = r.taskSvc.SetIssueStatusForWorkflow(ctx, issue.ID, issuestatus.Blocked)
	return true, nil
}

func (r *Runtime) recordProjectTaskArtifact(ctx context.Context, task db.AgentTaskQueue, issue db.Issue) (bool, error) {
	var nodeID, planID pgtype.UUID
	var kind string
	err := r.pool.QueryRow(ctx, `
		SELECT id, plan_id, kind
		FROM autonomous_project_plan_node
		WHERE workspace_id = $1
		  AND materialized_issue_id = $2
		ORDER BY updated_at DESC
		LIMIT 1
	`, issue.WorkspaceID, issue.ID).Scan(&nodeID, &planID, &kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	var taskResult any = map[string]any{}
	if len(task.Result) > 0 {
		if err := json.Unmarshal(task.Result, &taskResult); err != nil {
			taskResult = map[string]any{"raw": string(task.Result)}
		}
	}
	artifactPayload := map[string]any{
		"task_id": util.UUIDToString(task.ID),
		"agent_id": util.UUIDToString(task.AgentID),
		"issue_id": util.UUIDToString(issue.ID),
		"result": taskResult,
	}
	content, _ := json.Marshal(artifactPayload)
	artifactType := projectArtifactType(projectorchestration.NodeKind(kind))
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO autonomous_project_artifact (
			workspace_id, project_id, plan_id, node_id, artifact_type,
			name, content, producer_agent_id
		)
		SELECT $1, $2, $3, $4, $5, $6, $7, $8
		WHERE NOT EXISTS (
			SELECT 1
			FROM autonomous_project_artifact
			WHERE workspace_id = $1
			  AND project_id = $2
			  AND node_id = $4
			  AND content ->> 'task_id' = $9
		)
	`, issue.WorkspaceID, issue.ProjectID, planID, nodeID, artifactType,
		"Task result: "+issue.Title, content, task.AgentID, util.UUIDToString(task.ID)); err != nil {
		return false, fmt.Errorf("record project task artifact: %w", err)
	}

	if brainType := projectBrainEntryType(projectorchestration.NodeKind(kind)); brainType != "" {
		brainContent, _ := json.Marshal(map[string]any{
			"artifact_type": artifactType,
			"task_id": util.UUIDToString(task.ID),
			"issue_id": util.UUIDToString(issue.ID),
			"result": taskResult,
		})
		if _, err := r.pool.Exec(ctx, `
			INSERT INTO autonomous_project_brain_entry (
				workspace_id, project_id, plan_id, node_id, entry_type,
				subject, content, source_type, source_id, confidence,
				created_by_type, created_by_id
			)
			SELECT $1, $2, $3, $4, $5, $6, $7,
			       'artifact', $8, 0.9, 'agent', $9
			WHERE NOT EXISTS (
				SELECT 1
				FROM autonomous_project_brain_entry
				WHERE workspace_id = $1
				  AND project_id = $2
				  AND source_type = 'artifact'
				  AND source_id = $8
				  AND entry_type = $5
			)
		`, issue.WorkspaceID, issue.ProjectID, planID, nodeID, brainType,
			issue.Title, brainContent, util.UUIDToString(task.ID), task.AgentID); err != nil {
			return false, fmt.Errorf("record project brain artifact knowledge: %w", err)
		}
	}

	gateType := projectQualityGateType(projectorchestration.NodeKind(kind))
	if gateType == "" {
		return false, nil
	}

	gateStatus := "passed"
	gateError := ""
	gateEvidence := map[string]any{
		"task_id": util.UUIDToString(task.ID),
		"artifact_type": artifactType,
		"mode": "semantic_task_completion",
	}
	if r.qualityGateRunner != nil {
		result, runErr := r.qualityGateRunner.Run(ctx, projectorchestration.QualityGateRequest{
			WorkspaceID: issue.WorkspaceID,
			ProjectID: issue.ProjectID,
			PlanID: planID,
			NodeID: nodeID,
			IssueID: issue.ID,
			GateType: gateType,
			Artifact: artifactPayload,
		})
		gateEvidence = map[string]any{
			"task_id": util.UUIDToString(task.ID),
			"artifact_type": artifactType,
			"mode": "deterministic_runner",
			"runner_evidence": result.Evidence,
		}
		if runErr != nil {
			gateStatus = "failed"
			gateError = runErr.Error()
		} else if !result.Passed {
			gateStatus = "failed"
			gateError = strings.TrimSpace(result.Error)
			if gateError == "" {
				gateError = "deterministic quality gate failed"
			}
		}
	}
	evidenceJSON, _ := json.Marshal(gateEvidence)
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO autonomous_project_quality_gate_run (
			workspace_id, project_id, plan_id, node_id, gate_type,
			status, required, evidence, attempt, last_error, started_at, completed_at
		)
		SELECT $1, $2, $3, $4, $5, $6, TRUE, $7, 1, NULLIF($8, ''), now(), now()
		WHERE NOT EXISTS (
			SELECT 1
			FROM autonomous_project_quality_gate_run
			WHERE workspace_id = $1
			  AND project_id = $2
			  AND node_id = $4
			  AND gate_type = $5
			  AND evidence ->> 'task_id' = $9
		)
	`, issue.WorkspaceID, issue.ProjectID, planID, nodeID, gateType, gateStatus,
		evidenceJSON, gateError, util.UUIDToString(task.ID)); err != nil {
		return false, fmt.Errorf("record project quality evidence: %w", err)
	}
	if gateStatus == "passed" {
		return false, nil
	}

	if r.projectStore == nil {
		return true, errors.New(gateError)
	}
	disposition, nodeKey, projectID, failErr := r.projectStore.FailNodeByIssue(
		ctx, issue.WorkspaceID, issue.ID, "quality gate "+gateType+" failed: "+gateError,
	)
	if failErr != nil {
		return true, failErr
	}
	if disposition == projectorchestration.FailureBlocked {
		_, _ = r.taskSvc.SetIssueStatusForWorkflow(ctx, issue.ID, issuestatus.Blocked)
		contextJSON, _ := json.Marshal(map[string]any{
			"node_key": nodeKey,
			"issue_id": util.UUIDToString(issue.ID),
			"gate_type": gateType,
			"error": gateError,
		})
		_, _ = r.pool.Exec(ctx, `
			INSERT INTO autonomous_project_escalation (
				workspace_id, project_id, category, severity, summary, context
			)
			VALUES ($1, $2, 'technical_failure', 'high', $3, $4)
		`, issue.WorkspaceID, projectID, "Deterministic quality gate exhausted retry budget: "+nodeKey, contextJSON)
	}
	return true, nil
}

func projectArtifactType(kind projectorchestration.NodeKind) string {
	switch kind {
	case projectorchestration.NodeProduct:
		return "product_spec"
	case projectorchestration.NodeArchitecture:
		return "architecture"
	case projectorchestration.NodeReview:
		return "review"
	case projectorchestration.NodeQA:
		return "qa_report"
	case projectorchestration.NodeSecurity:
		return "security_review"
	case projectorchestration.NodeIntegration:
		return "integration_report"
	case projectorchestration.NodeRelease:
		return "release_manifest"
	case projectorchestration.NodeDeploy:
		return "deployment_record"
	case projectorchestration.NodeIncident:
		return "incident_report"
	default:
		return "implementation_handoff"
	}
}

func projectBrainEntryType(kind projectorchestration.NodeKind) string {
	switch kind {
	case projectorchestration.NodeProduct:
		return "product_decision"
	case projectorchestration.NodeArchitecture:
		return "architecture_decision"
	case projectorchestration.NodeResearch:
		return "fact"
	case projectorchestration.NodeSecurity:
		return "risk"
	case projectorchestration.NodeReview, projectorchestration.NodeQA, projectorchestration.NodeIntegration:
		return "lesson"
	default:
		return ""
	}
}

func projectQualityGateType(kind projectorchestration.NodeKind) string {
	switch kind {
	case projectorchestration.NodeReview:
		return "review"
	case projectorchestration.NodeQA:
		return "acceptance"
	case projectorchestration.NodeSecurity:
		return "security"
	case projectorchestration.NodeIntegration:
		return "integration_test"
	default:
		return ""
	}
}


func (r *Runtime) failProjectNodeTask(
	ctx context.Context,
	task db.AgentTaskQueue,
	issue db.Issue,
) (bool, error) {
	if r.projectStore == nil {
		return false, nil
	}
	var kind string
	err := r.pool.QueryRow(ctx, `
		SELECT kind
		FROM autonomous_project_plan_node
		WHERE workspace_id = $1
		  AND materialized_issue_id = $2
		  AND status IN ('running', 'verification', 'ready')
		ORDER BY updated_at DESC
		LIMIT 1
	`, issue.WorkspaceID, issue.ID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if projectNodeUsesIssueWorkflow(projectorchestration.NodeKind(kind)) {
		return false, nil
	}

	reason := "autonomous project stage task failed"
	if task.Error.Valid && strings.TrimSpace(task.Error.String) != "" {
		reason += ": " + strings.TrimSpace(task.Error.String)
	}
	disposition, nodeKey, projectID, err := r.projectStore.FailNodeByIssue(
		ctx, issue.WorkspaceID, issue.ID, reason,
	)
	if err != nil {
		return true, err
	}
	switch disposition {
	case projectorchestration.FailureRetry:
		slog.Info("autonomous project node queued for bounded retry",
			"project_id", util.UUIDToString(projectID),
			"node", nodeKey,
			"issue_id", util.UUIDToString(issue.ID),
		)
		return true, nil
	case projectorchestration.FailureBlocked:
		if _, statusErr := r.taskSvc.SetIssueStatusForWorkflow(ctx, issue.ID, issuestatus.Blocked); statusErr != nil {
			return true, statusErr
		}
		contextJSON, _ := json.Marshal(map[string]any{
			"node_key": nodeKey,
			"issue_id": util.UUIDToString(issue.ID),
			"task_id": util.UUIDToString(task.ID),
			"error": reason,
		})
		_, escErr := r.pool.Exec(ctx, `
			INSERT INTO autonomous_project_escalation (
				workspace_id, project_id, category, severity, summary, context
			)
			VALUES ($1, $2, 'technical_failure', 'high', $3, $4)
		`, issue.WorkspaceID, projectID,
			"Project node exhausted its retry budget: "+nodeKey, contextJSON)
		return true, escErr
	default:
		return false, nil
	}
}

func (r *Runtime) syncBlockedProjectNode(
	ctx context.Context,
	issue db.Issue,
) error {
	if r.projectStore == nil || !issue.ProjectID.Valid {
		return nil
	}
	disposition, nodeKey, projectID, err := r.projectStore.FailNodeByIssue(
		ctx, issue.WorkspaceID, issue.ID, "materialized issue moved to Blocked",
	)
	if err != nil {
		return err
	}
	if disposition == projectorchestration.FailureRetry {
		// An explicit Blocked board transition is a durable local blocker, not
		// a reason to freeze unrelated branches of the whole project. The
		// conductor may resume this node after the issue/escalation is resolved.
		_, _ = r.pool.Exec(ctx, `
			UPDATE autonomous_project_plan_node
			SET status = 'blocked',
			    blocked_category = 'technical_failure',
			    blocked_reason = 'issue workflow blocked before project retry',
			    updated_at = now()
			WHERE workspace_id = $1 AND materialized_issue_id = $2
		`, issue.WorkspaceID, issue.ID)
	}
	if nodeKey == "" {
		return nil
	}
	contextJSON, _ := json.Marshal(map[string]any{
		"node_key": nodeKey,
		"issue_id": util.UUIDToString(issue.ID),
	})
	_, err = r.pool.Exec(ctx, `
		INSERT INTO autonomous_project_escalation (
			workspace_id, project_id, category, severity, summary, context
		)
		SELECT $1, $2, 'technical_failure', 'high', $3, $4
		WHERE NOT EXISTS (
			SELECT 1 FROM autonomous_project_escalation
			WHERE workspace_id = $1
			  AND project_id = $2
			  AND status IN ('open', 'acknowledged')
			  AND context ->> 'node_key' = $5
		)
	`, issue.WorkspaceID, projectID,
		"Implementation workflow blocked project node: "+nodeKey, contextJSON, nodeKey)
	return err
}

func (r *Runtime) completeNonImplementationProjectNode(ctx context.Context, task db.AgentTaskQueue, issue db.Issue) (bool, error) {
	if r.projectStore == nil {
		return false, nil
	}
	var kind string
	err := r.pool.QueryRow(ctx, `
		SELECT kind
		FROM autonomous_project_plan_node
		WHERE workspace_id = $1
		  AND materialized_issue_id = $2
		  AND status IN ('running', 'verification')
		ORDER BY updated_at DESC
		LIMIT 1
	`, issue.WorkspaceID, issue.ID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if projectNodeUsesIssueWorkflow(projectorchestration.NodeKind(kind)) {
		return false, nil
	}

	if _, err := r.taskSvc.SetIssueStatusForWorkflow(ctx, issue.ID, issuestatus.Done); err != nil {
		return true, err
	}
	if err := r.projectStore.CompleteNodeByIssue(ctx, issue.WorkspaceID, issue.ID); err != nil {
		return true, err
	}
	return true, nil
}
