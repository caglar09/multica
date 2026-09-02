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
	plan, execution, err := r.projectPlanner.Plan(ctx, projectorchestration.PlanningInput{
		WorkspaceID: workspaceID,
		ProjectID: projectID,
		ProjectTitle: project.Title,
		ProjectDescription: description,
		Team: roles,
		CurrentPlan: currentPlan,
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

func (r *Runtime) processProjectScheduling(ctx context.Context) error {
	if r.projectStore == nil || r.issueSvc == nil || r.team == nil {
		return nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT p.workspace_id, p.project_id
		FROM autonomous_project_plan p
		LEFT JOIN autonomous_project_control c
		  ON c.project_id = p.project_id AND c.workspace_id = p.workspace_id
		WHERE p.status = 'active'
		  AND COALESCE(c.paused, FALSE) = FALSE
		ORDER BY p.project_id
		LIMIT 20
	`)
	if err != nil {
		return fmt.Errorf("query active project plans: %w", err)
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
		ready, err := r.projectStore.ListReadyNodes(ctx, project.workspaceID, project.projectID, 20)
		if err != nil {
			slog.Warn("project scheduler readiness failed", "project_id", util.UUIDToString(project.projectID), "error", err)
			continue
		}
		for _, node := range ready {
			if err := r.materializeProjectNode(ctx, project.workspaceID, project.projectID, node); err != nil {
				if escalationErr := r.openProjectEscalation(ctx, project.workspaceID, project.projectID, node, err); escalationErr != nil {
					slog.Warn("project scheduler escalation failed", "project_id", util.UUIDToString(project.projectID), "node", node.Key, "error", escalationErr)
				}
			}
		}
	}
	return nil
}

var errProjectApprovalRequired = errors.New("autonomous project node requires approval")

func (r *Runtime) materializeProjectNode(
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
	team, ok, err := r.team.FindProject(ctx, workspaceID, projectID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("project team unavailable")
	}
	agentID, role, family, err := r.selectProjectNodeAgent(ctx, team, node)
	if err != nil {
		return err
	}

	if _, err := r.pool.Exec(ctx, `
		UPDATE autonomous_project_plan_node n
		SET assigned_role = $4,
		    assigned_agent_id = $5,
		    updated_at = now()
		FROM autonomous_project_plan p
		WHERE n.plan_id = p.id
		  AND n.workspace_id = $1
		  AND n.project_id = $2
		  AND n.node_key = $3
		  AND p.status = 'active'
		  AND n.status = 'ready'
	`, workspaceID, projectID, node.Key, role, agentID); err != nil {
		return fmt.Errorf("stamp project node assignment: %w", err)
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
		Status: "backlog",
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
	if errors.Is(err, service.ErrActiveDuplicate) && res.DuplicateIssue != nil {
		if err := r.projectStore.MarkNodeMaterialized(ctx, workspaceID, projectID, node.Key, res.DuplicateIssue.ID); err != nil {
			return err
		}
		return r.startMaterializedNode(ctx, *res.DuplicateIssue, node, agentID, ownerUserID)
	}
	if err != nil {
		return fmt.Errorf("materialize project node %q: %w", node.Key, err)
	}
	if err := r.projectStore.MarkNodeMaterialized(ctx, workspaceID, projectID, node.Key, res.Issue.ID); err != nil {
		return err
	}
	return r.startMaterializedNode(ctx, res.Issue, node, agentID, ownerUserID)
}

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
		WHERE workspace_id = $1 AND project_id = $2 AND status = 'active'
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

func (r *Runtime) recordProjectTaskArtifact(ctx context.Context, task db.AgentTaskQueue, issue db.Issue) error {
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
		return nil
	}
	if err != nil {
		return err
	}

	var taskResult any = map[string]any{}
	if len(task.Result) > 0 {
		if err := json.Unmarshal(task.Result, &taskResult); err != nil {
			taskResult = map[string]any{"raw": string(task.Result)}
		}
	}
	content, _ := json.Marshal(map[string]any{
		"task_id": util.UUIDToString(task.ID),
		"agent_id": util.UUIDToString(task.AgentID),
		"issue_id": util.UUIDToString(issue.ID),
		"result": taskResult,
	})
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
		return fmt.Errorf("record project task artifact: %w", err)
	}

	gateType := projectQualityGateType(projectorchestration.NodeKind(kind))
	if gateType != "" {
		evidence, _ := json.Marshal(map[string]any{
			"task_id": util.UUIDToString(task.ID),
			"artifact_type": artifactType,
		})
		if _, err := r.pool.Exec(ctx, `
			INSERT INTO autonomous_project_quality_gate_run (
				workspace_id, project_id, plan_id, node_id, gate_type,
				status, required, evidence, attempt, started_at, completed_at
			)
			SELECT $1, $2, $3, $4, $5, 'passed', TRUE, $6, 1, now(), now()
			WHERE NOT EXISTS (
				SELECT 1
				FROM autonomous_project_quality_gate_run
				WHERE workspace_id = $1
				  AND project_id = $2
				  AND node_id = $4
				  AND gate_type = $5
				  AND evidence ->> 'task_id' = $7
			)
		`, issue.WorkspaceID, issue.ProjectID, planID, nodeID, gateType, evidence, util.UUIDToString(task.ID)); err != nil {
			return fmt.Errorf("record project quality evidence: %w", err)
		}
	}
	return nil
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
		// Existing issue-level workflow exhaustion is authoritative: a Blocked
		// implementation issue is not silently restarted. Convert the retryable
		// node into a durable escalation so a replan/human can decide.
		_, _ = r.pool.Exec(ctx, `
			UPDATE autonomous_project_plan_node
			SET status = 'blocked',
			    blocked_reason = 'issue workflow blocked before project retry',
			    updated_at = now()
			WHERE workspace_id = $1 AND materialized_issue_id = $2
		`, issue.WorkspaceID, issue.ID)
	}
	_, _ = r.pool.Exec(ctx, `
		UPDATE autonomous_project_plan
		SET status = 'blocked', updated_at = now()
		WHERE workspace_id = $1 AND project_id = $2 AND status = 'active'
	`, issue.WorkspaceID, projectID)
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
