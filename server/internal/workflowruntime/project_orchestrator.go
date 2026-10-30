package workflowruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/issuestatus"
	"github.com/multica-ai/multica/server/internal/projectorchestration"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/teamprovision"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/internal/workflow"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	projectPlanningDeadline = 5 * time.Minute
	// The extra minute leaves time to persist completion or retry state after timeout.
	projectPlanningLease      = 6 * time.Minute
	projectPlanningClaimLimit = 1
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
			SELECT 1 FROM autonomous_project_control c
			WHERE c.workspace_id = b.workspace_id
			  AND c.project_id = b.project_id
			  AND c.cancelled_at IS NOT NULL
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
		tag, err := r.pool.Exec(ctx, `
			UPDATE autonomous_project_bootstrap
			SET status = 'started', updated_at = now()
			WHERE workspace_id = $1
			  AND project_id = $2
			  AND autonomy_mode = 'autonomous'
			  AND status IN ('ready', 'started')
			  AND NOT EXISTS (
				SELECT 1 FROM autonomous_project_control c
				WHERE c.workspace_id = $1 AND c.project_id = $2 AND c.cancelled_at IS NOT NULL
			  )
		`, item.workspaceID, item.projectID)
		if err != nil {
			return fmt.Errorf("mark project bootstrap started: %w", err)
		}
		if tag.RowsAffected() == 0 {
			continue
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
		  AND NOT EXISTS (
			SELECT 1 FROM autonomous_project_control c
			WHERE c.workspace_id = d.workspace_id
			  AND c.project_id = d.project_id
			  AND c.cancelled_at IS NOT NULL
		  )
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
			  AND NOT EXISTS (
				SELECT 1 FROM autonomous_project_control c
				WHERE c.workspace_id = $1 AND c.project_id = $2 AND c.cancelled_at IS NOT NULL
			  )
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

	if err := r.recoverExpiredProjectPlanningLeases(ctx); err != nil {
		return err
	}
	rows, err := r.pool.Query(ctx, `
		WITH candidates AS (
			SELECT d.project_id, d.workspace_id, d.confirmed_by, d.confirmed_at
			FROM autonomous_project_team_draft d
			WHERE d.status = 'applied'
			  AND d.confirmed_at IS NOT NULL
			  AND d.continuation_started_at IS NOT NULL
			  AND d.continuation_completed_at IS NULL
			  AND d.planning_attempts < 3
			  AND (d.next_planning_retry_at IS NULL OR d.next_planning_retry_at <= now())
			  AND (d.planning_lease_until IS NULL OR d.planning_lease_until <= now())
			  AND NOT EXISTS (
				SELECT 1
				FROM autonomous_project_plan p
				WHERE p.workspace_id = d.workspace_id
				  AND p.project_id = d.project_id
				  AND p.status IN ('active', 'blocked', 'awaiting_approval', 'completed')
			  )
			  AND NOT EXISTS (
				SELECT 1
				FROM autonomous_project_control c
				WHERE c.workspace_id = d.workspace_id
				  AND c.project_id = d.project_id
				  AND c.cancelled_at IS NOT NULL
			  )
			ORDER BY d.confirmed_at ASC
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		), claimed AS (
			UPDATE autonomous_project_team_draft d
			SET planning_lease_owner = gen_random_uuid()::text,
			    planning_lease_until = now() + ($1::double precision * interval '1 second'),
			    planning_attempts = d.planning_attempts + 1,
			    updated_at = now()
			FROM candidates c
			WHERE d.project_id = c.project_id AND d.workspace_id = c.workspace_id
			RETURNING d.project_id, d.workspace_id, d.confirmed_by, d.confirmed_at,
			          d.planning_attempts, d.planning_lease_owner
		)
		SELECT project_id, workspace_id, confirmed_by, confirmed_at,
		       planning_attempts, planning_lease_owner
		FROM claimed
	`, projectPlanningLease.Seconds(), projectPlanningClaimLimit)
	if err != nil {
		return fmt.Errorf("query projects requiring durable planning: %w", err)
	}
	type candidate struct {
		projectID   pgtype.UUID
		workspaceID pgtype.UUID
		confirmedBy pgtype.UUID
		confirmedAt time.Time
		attempts    int32
		leaseOwner  string
	}
	items := make([]candidate, 0, 1)
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.projectID, &item.workspaceID, &item.confirmedBy, &item.confirmedAt, &item.attempts, &item.leaseOwner); err != nil {
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
		planCtx, cancel := context.WithTimeout(ctx, projectPlanningDeadline)
		err := r.planProjectRevision(planCtx, item.workspaceID, item.projectID, item.confirmedAt.UTC().Format(time.RFC3339Nano))
		cancel()
		if err != nil {
			if updateErr := r.recordProjectPlanningFailure(ctx, item.projectID, item.workspaceID, item.attempts, item.leaseOwner, err); updateErr != nil {
				return updateErr
			}
			continue
		}
		tag, err := r.pool.Exec(ctx, `
			UPDATE autonomous_project_team_draft
			SET continuation_completed_at = now(), planning_lease_owner=NULL,
			    planning_lease_until=NULL, next_planning_retry_at=NULL, updated_at = now()
			WHERE workspace_id = $1
			  AND project_id = $2
			  AND continuation_started_at IS NOT NULL
			  AND continuation_completed_at IS NULL
			  AND planning_lease_owner = $3
		`, item.workspaceID, item.projectID, item.leaseOwner)
		if err != nil {
			return fmt.Errorf("complete durable project planning lease: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("complete durable project planning lease: lease lost for project %s", util.UUIDToString(item.projectID))
		}
		if _, err := r.pool.Exec(ctx, `
			INSERT INTO autonomous_project_control (project_id, workspace_id, last_error, updated_at)
			VALUES ($1, $2, NULL, now())
			ON CONFLICT (project_id) DO UPDATE
			SET last_error = NULL, updated_at = now()
			WHERE autonomous_project_control.workspace_id = EXCLUDED.workspace_id
		`, item.projectID, item.workspaceID); err != nil {
			return fmt.Errorf("clear completed project planning error: %w", err)
		}
	}
	return nil
}

func (r *Runtime) recoverExpiredProjectPlanningLeases(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `
		WITH expired AS (
			UPDATE autonomous_project_team_draft d
			SET status = CASE
			        WHEN EXISTS (
				        SELECT 1 FROM autonomous_project_plan p
				        WHERE p.workspace_id=d.workspace_id AND p.project_id=d.project_id
				          AND p.status IN ('active','blocked','awaiting_approval','completed')
			        ) THEN d.status
			        WHEN d.planning_attempts >= 3 THEN 'planning_failed'
			        ELSE d.status
			    END,
			    continuation_completed_at = CASE
			        WHEN EXISTS (
				        SELECT 1 FROM autonomous_project_plan p
				        WHERE p.workspace_id=d.workspace_id AND p.project_id=d.project_id
				          AND p.status IN ('active','blocked','awaiting_approval','completed')
			        ) THEN COALESCE(d.continuation_completed_at, now())
			        ELSE d.continuation_completed_at
			    END,
			    planning_lease_owner=NULL, planning_lease_until=NULL,
			    next_planning_retry_at = CASE
			        WHEN EXISTS (
				        SELECT 1 FROM autonomous_project_plan p
				        WHERE p.workspace_id=d.workspace_id AND p.project_id=d.project_id
				          AND p.status IN ('active','blocked','awaiting_approval','completed')
			        ) OR d.planning_attempts >= 3 THEN NULL
			        ELSE now() + (
				        30 * power(2, LEAST(d.planning_attempts,10)) + floor(random()*30)
			        ) * interval '1 second'
			    END,
			    updated_at=now()
			WHERE d.status='applied' AND d.continuation_completed_at IS NULL
		  AND d.planning_lease_until <= now()
		  AND NOT EXISTS (
			SELECT 1 FROM autonomous_project_control c
			WHERE c.workspace_id=d.workspace_id AND c.project_id=d.project_id
			  AND c.cancelled_at IS NOT NULL
		  )
			RETURNING d.project_id, d.workspace_id, d.planning_attempts, d.status,
			          d.continuation_completed_at
		), controls AS (
			INSERT INTO autonomous_project_control (project_id, workspace_id, last_error, updated_at)
			SELECT project_id, workspace_id,
			       CASE WHEN status='planning_failed'
			            THEN 'Project planning lease expired after 3 attempts'
			            ELSE 'Project planning lease expired before the attempt completed; retry scheduled'
			       END, now()
			FROM expired WHERE continuation_completed_at IS NULL
			ON CONFLICT (project_id) DO UPDATE
			SET last_error=EXCLUDED.last_error, updated_at=now()
			WHERE autonomous_project_control.workspace_id=EXCLUDED.workspace_id
		)
		INSERT INTO autonomous_project_escalation (
			workspace_id, project_id, category, severity, summary, context
		)
		SELECT e.workspace_id, e.project_id, 'technical_failure', 'high',
		       'Project planning lease expired after 3 attempts',
		       jsonb_build_object('scope','project_planning','attempts',e.planning_attempts)
		FROM expired e
		WHERE e.status='planning_failed'
		  AND NOT EXISTS (
			SELECT 1 FROM autonomous_project_escalation x
			WHERE x.workspace_id=e.workspace_id AND x.project_id=e.project_id
			  AND x.status IN ('open','acknowledged')
			  AND x.context ->> 'scope'='project_planning'
		)
	`)
	if err != nil {
		return fmt.Errorf("recover expired project planning leases: %w", err)
	}
	return nil
}

func (r *Runtime) recordProjectPlanningFailure(ctx context.Context, projectID, workspaceID pgtype.UUID, attempts int32, leaseOwner string, cause error) error {
	message := cause.Error()
	if len(message) > 2000 {
		message = message[:2000]
	}
	terminal := attempts >= 3
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin project planning failure update: %w", err)
	}
	defer tx.Rollback(ctx)
	status := "applied"
	if terminal {
		status = "planning_failed"
	}
	var delaySeconds float64
	if !terminal {
		delaySeconds = projectPlanningRetryDelay(attempts).Seconds()
	}
	tag, err := tx.Exec(ctx, `
		UPDATE autonomous_project_team_draft
		SET status=$4, planning_lease_owner=NULL, planning_lease_until=NULL,
		    next_planning_retry_at=CASE WHEN $5 THEN NULL ELSE now()+($6 * interval '1 second') END,
		    updated_at=now()
		WHERE workspace_id=$1 AND project_id=$2 AND planning_lease_owner=$3
	`, workspaceID, projectID, leaseOwner, status, terminal, delaySeconds)
	if err != nil {
		return fmt.Errorf("persist project planning failure: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("persist project planning failure: lease lost for project %s", util.UUIDToString(projectID))
	}
	lastError := "Project planning failed: " + message
	if _, err := tx.Exec(ctx, `
		INSERT INTO autonomous_project_control (project_id, workspace_id, last_error, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (project_id) DO UPDATE
		SET last_error=EXCLUDED.last_error, updated_at=now()
		WHERE autonomous_project_control.workspace_id=EXCLUDED.workspace_id
	`, projectID, workspaceID, lastError); err != nil {
		return fmt.Errorf("persist project planning error: %w", err)
	}
	if terminal {
		contextJSON, _ := json.Marshal(map[string]any{"scope": "project_planning", "attempts": attempts, "error": message})
		if _, err := tx.Exec(ctx, `
			INSERT INTO autonomous_project_escalation (
				workspace_id, project_id, category, severity, summary, context
			)
			SELECT $1, $2, 'technical_failure', 'high', $3, $4
			WHERE NOT EXISTS (
				SELECT 1 FROM autonomous_project_escalation
				WHERE workspace_id=$1 AND project_id=$2
				  AND status IN ('open','acknowledged')
				  AND context ->> 'scope'='project_planning'
			)
		`, workspaceID, projectID, "Project planning failed after 3 attempts", contextJSON); err != nil {
			return fmt.Errorf("open project planning escalation: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit project planning failure: %w", err)
	}
	return nil
}

func projectPlanningRetryDelay(attempts int32) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	if attempts > 10 {
		attempts = 10
	}
	return time.Duration(30*(1<<attempts)+rand.Intn(30)) * time.Second
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
		"revision":     snapshot.Revision,
		"modules":      snapshot.Modules,
		"test_targets": snapshot.TestTargets,
		"api_surfaces": snapshot.APISurfaces,
		"data_stores":  snapshot.DataStores,
		"dependencies": snapshot.Dependencies,
		"evidence":     snapshot.Evidence,
	})
	sourceID := strings.TrimSpace(snapshot.Revision)
	if sourceID == "" {
		sourceID = "latest"
	}
	const subject = "Repository snapshot"
	embedding, err := projectorchestration.PrepareBrainMemoryEmbedding(ctx, subject, content)
	if err != nil {
		return fmt.Errorf("prepare repository snapshot embedding: %w", err)
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO autonomous_project_brain_entry (
			workspace_id, project_id, entry_type, subject, content,
			source_type, source_id, confidence, created_by_type, embedding
		)
		SELECT $1, $2, 'repository_fact', $3, $4,
		       'repository_analyzer', $5, 1.0, 'system', NULLIF($6,'')::vector
		WHERE NOT EXISTS (
			SELECT 1
			FROM autonomous_project_brain_entry
			WHERE workspace_id = $1
			  AND project_id = $2
			  AND entry_type = 'repository_fact'
			  AND source_type = 'repository_analyzer'
			  AND source_id = $5
			  AND superseded_by IS NULL
		)
	`, workspaceID, projectID, subject, content, sourceID, embedding)
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
				Type:    strings.TrimSpace(item.Kind),
				Title:   strings.TrimSpace(item.Title),
				Content: strings.TrimSpace(item.Content),
				Source:  "bootstrap",
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
		ORDER BY importance DESC,
		         confirmation_count DESC,
		         confidence DESC NULLS LAST,
		         COALESCE(last_confirmed_at, created_at) DESC
		LIMIT 80
	`, workspaceID, projectID)
	if brainErr != nil {
		return "", nil, nil, requested, brainErr
	}
	const brainPlanningContextChars = 32000
	brainContextChars := 0
	for brainRows.Next() {
		var entryType, subject string
		var contentJSON []byte
		if err := brainRows.Scan(&entryType, &subject, &contentJSON); err != nil {
			brainRows.Close()
			return "", nil, nil, requested, err
		}
		remaining := brainPlanningContextChars - brainContextChars
		if remaining <= 0 {
			break
		}
		content := string(contentJSON)
		if len(content) > remaining {
			content = content[:remaining]
		}
		brainContextChars += len(content)
		contextItems = append(contextItems, projectorchestration.PlanningContextItem{
			Type:    entryType,
			Title:   subject,
			Content: content,
			Source:  "brain",
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
			Ref:  append(json.RawMessage(nil), ref...),
		})
	}
	if err := resourceRows.Err(); err != nil {
		resourceRows.Close()
		return "", nil, nil, requested, err
	}
	resourceRows.Close()

	return brief, contextItems, resources, requested, nil
}

func (r *Runtime) projectAutonomousCancelled(ctx context.Context, workspaceID, projectID pgtype.UUID) (bool, error) {
	var cancelled bool
	err := r.pool.QueryRow(ctx, `
		SELECT cancelled_at IS NOT NULL
		FROM autonomous_project_control
		WHERE workspace_id=$1 AND project_id=$2
	`, workspaceID, projectID).Scan(&cancelled)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return cancelled, err
}

func (r *Runtime) watchProjectAutonomousCancellation(ctx context.Context, workspaceID, projectID pgtype.UUID) (context.Context, context.CancelFunc) {
	planCtx, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-planCtx.Done():
				return
			case <-ticker.C:
				checkCtx, stop := context.WithTimeout(planCtx, 2*time.Second)
				cancelled, err := r.projectAutonomousCancelled(checkCtx, workspaceID, projectID)
				stop()
				if err != nil {
					slog.Warn("project planning cancellation watch failed",
						"project_id", util.UUIDToString(projectID), "error", err)
					cancel()
					return
				}
				if cancelled {
					cancel()
					return
				}
			}
		}
	}()
	return planCtx, cancel
}

func (r *Runtime) planProjectRevision(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	sourceRevision string,
) (retErr error) {
	cancelled, err := r.projectAutonomousCancelled(ctx, workspaceID, projectID)
	if err != nil {
		return fmt.Errorf("check project cancellation before planning: %w", err)
	}
	if cancelled {
		return nil
	}
	ctx, stopWatching := r.watchProjectAutonomousCancellation(ctx, workspaceID, projectID)
	defer stopWatching()
	defer func() {
		if retErr == nil {
			return
		}
		checkCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		cancelled, checkErr := r.projectAutonomousCancelled(checkCtx, workspaceID, projectID)
		if checkErr == nil && cancelled {
			retErr = nil
		}
	}()
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
	var projectSpecification *projectorchestration.ProjectSpecification
	specificationRevisionID := ""
	if revision, exists, loadErr := r.projectStore.LoadLatestSpecification(ctx, workspaceID, projectID); loadErr != nil {
		return fmt.Errorf("load authoritative project specification: %w", loadErr)
	} else if exists {
		projectSpecification = &revision.Specification
		specificationRevisionID = revision.ID
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
		WorkspaceID:             workspaceID,
		ProjectID:               projectID,
		ProjectTitle:            project.Title,
		ProjectDescription:      description,
		BootstrapBrief:          brief,
		ProjectSpecification:    projectSpecification,
		SpecificationRevisionID: specificationRevisionID,
		Context:                 planningContext,
		Resources:               resources,
		Team:                    roles,
		CurrentPlan:             currentPlan,
		Policy:                  requestedPolicy,
	})
	if err != nil {
		return err
	}
	if projectSpecification == nil {
		specification, err := projectorchestration.ProjectSpecificationFromPlan(plan)
		if err != nil {
			return fmt.Errorf("initial plan cannot establish a versioned project specification: %w", err)
		}
		revision, err := r.projectStore.CreateSpecificationRevision(ctx, workspaceID, projectID, "project_bootstrap", sourceRevision, specification, true)
		if err != nil {
			return fmt.Errorf("persist initial project specification: %w", err)
		}
		plan.SpecificationRevisionID = revision.ID
	}
	_, err = r.projectStore.PersistPlan(
		ctx, workspaceID, projectID, sourceRevision,
		execution.Provider, execution.Model, plan,
	)
	if errors.Is(err, projectorchestration.ErrProjectAutonomousCancelled) {
		return nil
	}
	if err != nil {
		return err
	}
	// Replanning is a state migration, not just a new DAG insert. Retire any
	// materialized issues that belonged only to superseded revisions so the
	// board cannot retain backlog work that the current scheduler can never own.
	if err := r.reconcileSupersededProjectIssues(ctx, workspaceID, projectID); err != nil {
		slog.Warn("project plan persisted but issue projections need reconciliation",
			"workspace_id", util.UUIDToString(workspaceID),
			"project_id", util.UUIDToString(projectID),
			"error", err,
		)
	}
	return nil
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
		 AND tm.active = true
		 AND tm.agent_id = CASE
			WHEN i.assignee_type = 'agent' AND i.assignee_id IS NOT NULL THEN i.assignee_id
			ELSE i.creator_id
		 END
		WHERE i.creator_type = 'agent'
		  AND i.origin_type = 'agent_create'
		  AND i.project_id IS NOT NULL
		  AND i.status NOT IN ('done', 'cancelled')
		  AND EXISTS (
			SELECT 1
			FROM autonomous_project_plan p
			WHERE p.workspace_id = i.workspace_id
			  AND p.project_id = i.project_id
			  AND p.status IN ('active', 'blocked', 'completed')
			  AND NOT EXISTS (
			      SELECT 1
			      FROM autonomous_project_plan newer
			      WHERE newer.workspace_id = p.workspace_id
			        AND newer.project_id = p.project_id
			        AND newer.revision > p.revision
			  )
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
	if !exists {
		return errors.New("autonomous project plan unavailable")
	}
	planID, err := util.ParseUUID(stored.ID)
	if err != nil {
		return err
	}
	if stored.Status == "completed" {
		if err := r.projectStore.ResumeCompletedPlanForDiscoveredWork(
			ctx, issue.WorkspaceID, issue.ProjectID, planID,
		); err != nil {
			return fmt.Errorf("resume completed project for discovered work: %w", err)
		}
		stored.Status = "active"
	}
	if stored.Status != "active" && stored.Status != "blocked" {
		return errors.New("active autonomous project plan unavailable")
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
		Key:                  nodeKey,
		Kind:                 kind,
		Title:                issue.Title,
		Description:          description,
		Priority:             discoveredProjectPriority(issue.Priority),
		RequiredRoleFamily:   family,
		RequiredCapabilities: append([]string(nil), roleSpec.Capabilities...),
		AcceptanceCriteria: []string{
			"Requested runtime-discovered work is completed and its result is recorded on the issue.",
		},
		Risk:        projectorchestration.RiskMedium,
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

	blockSourceKey := ""
	sourceKey, sourceNodeStatus, sourceIssueStatus, sourceParentEpicKey, sourceIsEpic, sourceRequirementIDs, sourceFound, err := r.discoveredIssueSource(
		ctx, planID, issue,
	)
	if err != nil {
		return err
	}
	if err := inheritDiscoveredProjectNodeTraceability(
		&node, plan, sourceKey, sourceParentEpicKey, sourceIsEpic, sourceFound, sourceRequirementIDs,
	); err != nil {
		return err
	}
	if sourceFound {
		sourceBlocked := sourceNodeStatus == "blocked" ||
			issuestatus.Effective(ctx, r.taskSvc.Queries, issue.WorkspaceID, sourceIssueStatus) == issuestatus.Blocked
		if sourceBlocked {
			plan.Edges = append(plan.Edges, projectorchestration.EdgeSpec{
				From: nodeKey,
				To:   sourceKey,
				Type: projectorchestration.DependencyHard,
			})
			blockSourceKey = sourceKey
		} else {
			// Provenance edge only: runtime-discovered work may execute in
			// parallel unless an explicit blocker/dependency says otherwise.
			plan.Edges = append(plan.Edges, projectorchestration.EdgeSpec{
				From: sourceKey,
				To:   nodeKey,
				Type: projectorchestration.DependencySoft,
			})
		}
	}
	plan.Nodes = append(plan.Nodes, node)

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
		"issue_id":             util.UUIDToString(issue.ID),
		"title":                issue.Title,
		"description":          description,
		"origin_task_id":       util.UUIDToString(issue.OriginID),
		"source_node_key":      sourceKey,
		"source_blocked":       blockSourceKey != "",
		"assigned_role":        role,
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

func inheritDiscoveredProjectNodeTraceability(
	node *projectorchestration.NodeSpec,
	plan projectorchestration.Plan,
	sourceKey, parentEpicKey string,
	sourceIsEpic, sourceFound bool,
	requirementIDs []string,
) error {
	hasEpics := false
	for _, existing := range plan.Nodes {
		if existing.IsEpic {
			hasEpics = true
			break
		}
	}
	if !sourceFound {
		if hasEpics {
			return errors.New("runtime-discovered work has no source node to inherit epic membership from")
		}
		return nil
	}
	node.ParentEpicKey = parentEpicKey
	if sourceIsEpic {
		node.ParentEpicKey = sourceKey
	}
	node.RequirementIDs = append([]string(nil), requirementIDs...)
	if hasEpics && node.ParentEpicKey == "" {
		return errors.New("runtime-discovered source node has no epic membership to inherit")
	}
	return nil
}

func (r *Runtime) discoveredIssueSource(
	ctx context.Context,
	planID pgtype.UUID,
	issue db.Issue,
) (nodeKey, nodeStatus, issueStatus, parentEpicKey string, isEpic bool, requirementIDs []string, found bool, err error) {
	if !issue.OriginType.Valid || issue.OriginType.String != "agent_create" || !issue.OriginID.Valid {
		return "", "", "", "", false, nil, false, nil
	}
	err = r.pool.QueryRow(ctx, `
		SELECT n.node_key, n.status, source_issue.status,
		       COALESCE(n.parent_epic_key, ''), n.is_epic,
		       COALESCE(n.requirement_ids, ARRAY[]::text[])
		FROM agent_task_queue origin_task
		JOIN issue source_issue ON source_issue.id = origin_task.issue_id
		JOIN autonomous_project_plan_node n
		  ON n.plan_id = $1
		 AND n.materialized_issue_id = source_issue.id
		WHERE origin_task.id = $2
		LIMIT 1
	`, planID, issue.OriginID).Scan(&nodeKey, &nodeStatus, &issueStatus, &parentEpicKey, &isEpic, &requirementIDs)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", "", false, nil, false, nil
	}
	if err != nil {
		return "", "", "", "", false, nil, false, err
	}
	return nodeKey, nodeStatus, issueStatus, parentEpicKey, isEpic, requirementIDs, true, nil
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

// reconcileSupersededProjectIssues enforces the board/plan ownership invariant:
//
//	every open issue materialized by Project OS is owned by the latest plan.
//
// Stable node keys are carried forward by PersistPlan. Any remaining open issue
// that is referenced only by superseded revisions is therefore stale: either
// the node was removed by the replan or a newer revision replaced its issue.
// Stale work is explicitly cancelled (including active agent tasks) instead of
// being left in Backlog forever with no scheduler owner.
func (r *Runtime) reconcileSupersededProjectIssues(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
) error {
	if r == nil || r.pool == nil || r.taskSvc == nil {
		return nil
	}
	var latestPlanID pgtype.UUID
	var latestRevision int64
	if err := r.pool.QueryRow(ctx, `
		SELECT id, revision
		FROM autonomous_project_plan
		WHERE workspace_id = $1 AND project_id = $2
		ORDER BY revision DESC
		LIMIT 1
	`, workspaceID, projectID).Scan(&latestPlanID, &latestRevision); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}

	// Repair the other half of the durable state projection: a node can commit
	// completed/cancelled and the process can die before its issue status write.
	// Completed nodes are excluded from ListPlanNodes, so normal scheduling will
	// never revisit that gap. Repair it here from the authoritative latest plan.
	terminalRows, err := r.pool.Query(ctx, `
		SELECT n.materialized_issue_id, n.status, i.status
		FROM autonomous_project_plan_node n
		JOIN issue i ON i.id = n.materialized_issue_id
		WHERE n.plan_id = $1::uuid
		  AND n.materialized_issue_id IS NOT NULL
		  AND n.status IN ('completed','cancelled')
	`, latestPlanID)
	if err != nil {
		return fmt.Errorf("query terminal project issue projections: %w", err)
	}
	type terminalProjection struct {
		issueID    pgtype.UUID
		nodeState  string
		issueState string
	}
	terminal := make([]terminalProjection, 0, 16)
	for terminalRows.Next() {
		var item terminalProjection
		if err := terminalRows.Scan(&item.issueID, &item.nodeState, &item.issueState); err != nil {
			terminalRows.Close()
			return err
		}
		terminal = append(terminal, item)
	}
	if err := terminalRows.Err(); err != nil {
		terminalRows.Close()
		return err
	}
	terminalRows.Close()
	for _, item := range terminal {
		effective := issuestatus.Effective(ctx, r.taskSvc.Queries, workspaceID, item.issueState)
		switch item.nodeState {
		case "completed":
			if effective == issuestatus.Done || effective == issuestatus.Cancelled {
				continue
			}
			if _, err := r.taskSvc.SetIssueStatusForWorkflow(ctx, item.issueID, issuestatus.Done); err != nil {
				slog.Warn("autonomous repair could not project completed node to issue",
					"workspace_id", util.UUIDToString(workspaceID),
					"project_id", util.UUIDToString(projectID),
					"issue_id", util.UUIDToString(item.issueID),
					"error", err,
				)
				continue
			}
		case "cancelled":
			if effective == issuestatus.Cancelled {
				continue
			}
			if err := r.taskSvc.CancelTasksForIssue(ctx, item.issueID); err != nil {
				slog.Warn("autonomous repair could not cancel tasks for terminal node",
					"workspace_id", util.UUIDToString(workspaceID),
					"project_id", util.UUIDToString(projectID),
					"issue_id", util.UUIDToString(item.issueID),
					"error", err,
				)
				continue
			}
			if _, err := r.taskSvc.SetIssueStatusForWorkflow(ctx, item.issueID, issuestatus.Cancelled); err != nil {
				slog.Warn("autonomous repair could not project cancelled node to issue",
					"workspace_id", util.UUIDToString(workspaceID),
					"project_id", util.UUIDToString(projectID),
					"issue_id", util.UUIDToString(item.issueID),
					"error", err,
				)
				continue
			}
		}
	}

	// Escalations attached to a node that is already terminal are stale even
	// when they predate this repair code. Close them before evaluating plan
	// history so an old technical failure cannot keep a recovered project in
	// Attention forever.
	if _, err := r.pool.Exec(ctx, `
		UPDATE autonomous_project_escalation e
		SET status = 'resolved',
		    resolution = jsonb_build_object(
		        'decision', 'auto_resolved',
		        'reason', 'node_terminal'
		    ),
		    resolved_at = now()
		FROM autonomous_project_plan_node n
		WHERE e.node_id = n.id
		  AND e.workspace_id = $1::uuid
		  AND e.project_id = $2::uuid
		  AND n.status IN ('completed','cancelled')
		  AND e.status IN ('open','acknowledged')
	`, workspaceID, projectID); err != nil {
		slog.Warn("autonomous repair could not close terminal-node escalations",
			"workspace_id", util.UUIDToString(workspaceID),
			"project_id", util.UUIDToString(projectID),
			"error", err,
		)
	}

	// Escalations attached to superseded nodes are historical evidence, not
	// current operator work. Resolve them durably so recovered projects do not
	// stay in Attention because of a failure from an obsolete revision.
	if _, err := r.pool.Exec(ctx, `
		UPDATE autonomous_project_escalation e
		SET status = 'resolved',
		    resolution = jsonb_build_object(
		        'decision', 'auto_resolved',
		        'reason', 'plan_superseded',
		        'current_plan_revision', $4::bigint
		    ),
		    resolved_at = now()
		FROM autonomous_project_plan_node n
		JOIN autonomous_project_plan p ON p.id = n.plan_id
		WHERE e.node_id = n.id
		  AND e.workspace_id = $1::uuid
		  AND e.project_id = $2::uuid
		  AND p.id <> $3::uuid
		  AND p.status IN ('superseded','completed')
		  AND e.status IN ('open','acknowledged')
	`, workspaceID, projectID, latestPlanID, latestRevision); err != nil {
		slog.Warn("autonomous repair could not close historical escalations",
			"workspace_id", util.UUIDToString(workspaceID),
			"project_id", util.UUIDToString(projectID),
			"current_plan_revision", latestRevision,
			"error", err,
		)
	}

	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT ON (i.id) i.id, i.status, n.node_key, p.revision
		FROM autonomous_project_plan_node n
		JOIN autonomous_project_plan p ON p.id = n.plan_id
		JOIN issue i ON i.id = n.materialized_issue_id
		WHERE p.workspace_id = $1::uuid
		  AND p.project_id = $2::uuid
		  AND p.status IN ('superseded','completed')
		  AND p.id <> $3::uuid
		  AND NOT EXISTS (
		      SELECT 1
		      FROM autonomous_project_plan_node current_node
		      WHERE current_node.plan_id = $3
		        AND current_node.materialized_issue_id = i.id
		  )
		ORDER BY i.id, p.revision DESC
	`, workspaceID, projectID, latestPlanID)
	if err != nil {
		return fmt.Errorf("query stale superseded project issues: %w", err)
	}
	type staleIssue struct {
		id       pgtype.UUID
		status   string
		nodeKey  string
		revision int64
	}
	stale := make([]staleIssue, 0, 16)
	for rows.Next() {
		var item staleIssue
		if err := rows.Scan(&item.id, &item.status, &item.nodeKey, &item.revision); err != nil {
			rows.Close()
			return err
		}
		stale = append(stale, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, item := range stale {
		effective := issuestatus.Effective(ctx, r.taskSvc.Queries, workspaceID, item.status)
		if effective == issuestatus.Done || effective == issuestatus.Cancelled {
			continue
		}
		// Supersession is an explicit orchestration lifecycle cleanup. Stop any
		// still-running side effects before projecting the issue to Cancelled.
		if err := r.taskSvc.CancelTasksForIssue(ctx, item.id); err != nil {
			slog.Warn("autonomous repair could not cancel tasks for superseded issue",
				"workspace_id", util.UUIDToString(workspaceID),
				"project_id", util.UUIDToString(projectID),
				"issue_id", util.UUIDToString(item.id),
				"node_key", item.nodeKey,
				"error", err,
			)
			continue
		}
		if _, err := r.taskSvc.SetIssueStatusForWorkflow(ctx, item.id, issuestatus.Cancelled); err != nil {
			slog.Warn("autonomous repair could not retire superseded issue",
				"workspace_id", util.UUIDToString(workspaceID),
				"project_id", util.UUIDToString(projectID),
				"issue_id", util.UUIDToString(item.id),
				"node_key", item.nodeKey,
				"error", err,
			)
			continue
		}
		slog.Info("retired stale autonomous issue after replan",
			"workspace_id", util.UUIDToString(workspaceID),
			"project_id", util.UUIDToString(projectID),
			"issue_id", util.UUIDToString(item.id),
			"node_key", item.nodeKey,
			"superseded_revision", item.revision,
			"current_revision", latestRevision,
		)
	}
	return nil
}

func (r *Runtime) reconcileAllSupersededProjectIssues(ctx context.Context) error {
	if r == nil || r.pool == nil {
		return nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT workspace_id, project_id
		FROM autonomous_project_plan
		ORDER BY project_id
		LIMIT 200
	`)
	if err != nil {
		return err
	}
	type projectRef struct {
		workspaceID pgtype.UUID
		projectID   pgtype.UUID
	}
	projects := make([]projectRef, 0, 32)
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
		if err := r.reconcileSupersededProjectIssues(ctx, project.workspaceID, project.projectID); err != nil {
			slog.Warn("autonomous stale replan issue reconciliation failed",
				"project_id", util.UUIDToString(project.projectID),
				"error", err,
			)
		}
	}
	return nil
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
		if err := r.processProjectSchedulingForProject(ctx, project.workspaceID, project.projectID); err != nil {
			slog.Warn("project conductor scheduling pass failed",
				"project_id", util.UUIDToString(project.projectID),
				"error", err,
			)
		}
	}
	return nil
}

func (r *Runtime) processProjectSchedulingForProject(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
) error {
	if r.projectStore == nil || r.issueSvc == nil || r.team == nil {
		return nil
	}
	paused, err := r.isProjectPaused(ctx, workspaceID, projectID)
	if err != nil {
		return fmt.Errorf("check project pause state: %w", err)
	}
	if paused {
		return nil
	}
	var schedulable bool
	if err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM autonomous_project_plan
			WHERE workspace_id = $1
			  AND project_id = $2
			  AND status IN ('active','blocked')
		)
	`, workspaceID, projectID).Scan(&schedulable); err != nil {
		return fmt.Errorf("check schedulable project plan: %w", err)
	}
	if !schedulable {
		return nil
	}

	// Evaluate unblock conditions before projecting node state back to the
	// board. Otherwise a human/condition resolver moving a technical block
	// out of Blocked would be overwritten before the conductor could see it.
	if err := r.reconcileProjectBlockedNodes(ctx, workspaceID, projectID); err != nil {
		slog.Warn("project conductor blocked-node reconciliation failed",
			"project_id", util.UUIDToString(projectID), "error", err)
	}

	// Board projection is intentionally eager: every issue-backed DAG node
	// becomes visible as soon as the plan exists. Execution is a separate
	// claim below, so pending dependencies stay parked in Backlog.
	planned, err := r.projectStore.ListPlanNodes(ctx, workspaceID, projectID, 200)
	if err != nil {
		return fmt.Errorf("list project plan nodes: %w", err)
	}
	materialized := false
	for _, node := range planned {
		if node.Kind == projectorchestration.NodeDeploy || node.Kind == projectorchestration.NodeObserve {
			continue
		}
		if node.MaterializedIssueID == "" {
			if err := r.ensureProjectNodeIssue(ctx, workspaceID, projectID, node); err != nil {
				if escalationErr := r.openProjectEscalation(ctx, workspaceID, projectID, node.ReadyNode, err); escalationErr != nil {
					slog.Warn("project conductor materialization escalation failed",
						"project_id", util.UUIDToString(projectID), "node", node.Key, "error", escalationErr)
				}
				if blockErr := r.blockProjectNodeForSchedulingCause(ctx, workspaceID, projectID, node.ReadyNode, err); blockErr != nil {
					slog.Warn("project conductor materialization block failed",
						"project_id", util.UUIDToString(projectID), "node", node.Key, "error", blockErr)
				}
				continue
			}
			materialized = true
			continue
		}
		if node.Status == "pending" || node.Status == "ready" {
			if _, err := r.ensureMaterializedProjectNodeAssignment(ctx, workspaceID, projectID, node); err != nil {
				if escalationErr := r.openProjectEscalation(ctx, workspaceID, projectID, node.ReadyNode, err); escalationErr != nil {
					slog.Warn("project conductor assignment escalation failed",
						"project_id", util.UUIDToString(projectID), "node", node.Key, "error", escalationErr)
				}
				if blockErr := r.blockProjectNodeForSchedulingCause(ctx, workspaceID, projectID, node.ReadyNode, err); blockErr != nil {
					slog.Warn("project conductor assignment block failed",
						"project_id", util.UUIDToString(projectID), "node", node.Key, "error", blockErr)
				}
				continue
			}
		}
		if err := r.syncProjectNodeBoardState(ctx, workspaceID, node); err != nil {
			slog.Warn("project conductor board projection failed",
				"project_id", util.UUIDToString(projectID), "node", node.Key, "error", err)
		}
	}
	if materialized {
		return nil
	}
	goalDriftAuditPending, err := r.enqueueGoalDriftAuditCheckpoints(ctx, workspaceID, projectID)
	if err != nil {
		return fmt.Errorf("queue goal drift audit checkpoint: %w", err)
	}
	if goalDriftAuditPending {
		return nil
	}
	goalDriftBlocked, err := r.goalDriftAuditBlocksProject(ctx, workspaceID, projectID)
	if err != nil {
		return fmt.Errorf("check goal drift audit result: %w", err)
	}
	if goalDriftBlocked {
		return nil
	}

	ready, err := r.projectStore.ListReadyNodes(ctx, workspaceID, projectID, 20)
	if err != nil {
		return fmt.Errorf("list project ready nodes: %w", err)
	}
	for _, node := range ready {
		paused, err := r.isProjectPaused(ctx, workspaceID, projectID)
		if err != nil {
			return fmt.Errorf("recheck project pause state before dispatch: %w", err)
		}
		if paused {
			break
		}
		if node.IsEpic {
			if err := r.expandReadyProjectEpic(ctx, workspaceID, projectID, node); err != nil {
				if errors.Is(err, projectorchestration.ErrEpicExpansionStale) {
					return nil
				}
				if escalationErr := r.openProjectEscalation(ctx, workspaceID, projectID, node, err); escalationErr != nil {
					slog.Warn("project Epic decomposition escalation failed", "project_id", util.UUIDToString(projectID), "node", node.Key, "error", escalationErr)
				}
				if blockErr := r.blockProjectNodeForSchedulingCause(ctx, workspaceID, projectID, node, err); blockErr != nil {
					slog.Warn("project Epic decomposition block projection failed", "project_id", util.UUIDToString(projectID), "node", node.Key, "error", blockErr)
				}
				continue
			}
			// PersistEpicExpansion creates a new approved revision. Reconcile it
			// on the next scheduling pass so no task is dispatched from the old DAG.
			return nil
		}
		if err := r.startReadyProjectNode(ctx, workspaceID, projectID, node); err != nil {
			if errors.Is(err, service.ErrProjectAutonomousPaused) || errors.Is(err, service.ErrProjectAutonomousCancelled) {
				break
			}
			if escalationErr := r.openProjectEscalation(ctx, workspaceID, projectID, node, err); escalationErr != nil {
				slog.Warn("project scheduler escalation failed", "project_id", util.UUIDToString(projectID), "node", node.Key, "error", escalationErr)
			}
			if blockErr := r.blockProjectNodeForSchedulingCause(ctx, workspaceID, projectID, node, err); blockErr != nil {
				slog.Warn("project scheduler block projection failed", "project_id", util.UUIDToString(projectID), "node", node.Key, "error", blockErr)
			}
		}
	}
	return nil
}

func (r *Runtime) ensureMaterializedProjectNodeAssignment(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	node projectorchestration.PlannedNode,
) (bool, error) {
	if node.MaterializedIssueID == "" || (node.Status != "pending" && node.Status != "ready") {
		return false, nil
	}
	if r == nil || r.taskSvc == nil || r.taskSvc.Queries == nil || r.team == nil || r.projectStore == nil {
		return false, errors.New("project assignment services are not configured")
	}
	issueID, err := util.ParseUUID(node.MaterializedIssueID)
	if err != nil {
		return false, fmt.Errorf("parse materialized issue id for node %q: %w", node.Key, err)
	}
	issue, err := r.taskSvc.Queries.GetIssue(ctx, issueID)
	if err != nil {
		return false, fmt.Errorf("load materialized issue for node %q: %w", node.Key, err)
	}
	team, ok, err := r.team.FindProject(ctx, workspaceID, projectID)
	if err != nil {
		return false, fmt.Errorf("load project team for node %q: %w", node.Key, err)
	}
	if !ok {
		return false, errors.New("project team unavailable")
	}

	var agentID pgtype.UUID
	role := ""
	if node.AssignedAgentID != "" {
		agentID, err = util.ParseUUID(node.AssignedAgentID)
		if err != nil {
			return false, fmt.Errorf("parse assigned agent for node %q: %w", node.Key, err)
		}
		var member bool
		role, member = team.RoleForAgent(agentID)
		if !member {
			return false, fmt.Errorf("node %q is assigned to an agent outside its project team", node.Key)
		}
	}
	issueAssigned := issue.AssigneeType.Valid || issue.AssigneeID.Valid
	if issueAssigned {
		if issue.AssigneeType.String != "agent" || !issue.AssigneeID.Valid {
			return false, fmt.Errorf("node %q has an issue assigned outside its project team", node.Key)
		}
		issueRole, member := team.RoleForAgent(issue.AssigneeID)
		if !member {
			return false, fmt.Errorf("node %q issue is assigned to an agent outside its project team", node.Key)
		}
		if agentID.Valid && agentID != issue.AssigneeID {
			return false, fmt.Errorf("node %q plan and issue assignments do not match", node.Key)
		}
		agentID, role = issue.AssigneeID, issueRole
	}
	if !agentID.Valid {
		agentID, role, _, err = r.selectProjectNodeAgent(ctx, team, node.ReadyNode)
		if err != nil {
			return false, fmt.Errorf("select project agent for node %q: %w", node.Key, err)
		}
	}
	if !issueAssigned {
		if r.taskSvc == nil {
			return false, errors.New("task service unavailable for project issue assignment")
		}
		if _, err := r.taskSvc.AssignIssueToAgentForWorkflow(ctx, issueID, agentID); err != nil {
			return false, fmt.Errorf("assign issue for node %q: %w", node.Key, err)
		}
	}
	if node.AssignedAgentID == util.UUIDToString(agentID) && node.AssignedRole == role {
		return false, nil
	}
	if err := r.projectStore.BindNodeIssue(ctx, workspaceID, projectID, node.Key, issueID, role, agentID); err != nil {
		return false, fmt.Errorf("persist project assignment for node %q: %w", node.Key, err)
	}
	return true, nil
}

func (r *Runtime) expandReadyProjectEpic(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	node projectorchestration.ReadyNode,
) error {
	if !node.IsEpic {
		return errors.New("ready plan node is not an Epic")
	}
	if r == nil || r.projectStore == nil || r.projectPlanner == nil || r.taskSvc == nil || r.team == nil {
		return projectorchestration.ErrPlannerUnavailable
	}
	stored, exists, err := r.projectStore.LoadLatestPlan(ctx, workspaceID, projectID)
	if err != nil {
		return fmt.Errorf("load approved plan for Epic %q: %w", node.Key, err)
	}
	if !exists || stored.Status != "active" || stored.PlanDigest == "" {
		return projectorchestration.ErrEpicExpansionStale
	}
	revision, exists, err := r.projectStore.LoadLatestSpecification(ctx, workspaceID, projectID)
	if err != nil {
		return fmt.Errorf("load current specification for Epic %q: %w", node.Key, err)
	}
	if !exists || revision.ID == "" || revision.ID != stored.Plan.SpecificationRevisionID {
		return fmt.Errorf("Epic %q plan is not bound to the current specification revision", node.Key)
	}
	project, err := r.taskSvc.Queries.GetProjectInWorkspace(ctx, dbGetProjectParams(projectID, workspaceID))
	if err != nil {
		return fmt.Errorf("load project for Epic %q: %w", node.Key, err)
	}
	team, ok, err := r.team.FindProject(ctx, workspaceID, projectID)
	if err != nil {
		return fmt.Errorf("load project team for Epic %q: %w", node.Key, err)
	}
	if !ok {
		return errors.New("project team is not provisioned for Epic decomposition")
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
	brief, planningContext, resources, policy, err := r.loadProjectPlanningBootstrap(ctx, workspaceID, projectID)
	if err != nil {
		return fmt.Errorf("load planning context for Epic %q: %w", node.Key, err)
	}
	description := ""
	if project.Description.Valid {
		description = project.Description.String
	}
	input := projectorchestration.PlanningInput{
		WorkspaceID: workspaceID, ProjectID: projectID,
		ProjectTitle: project.Title, ProjectDescription: description,
		BootstrapBrief: brief, ProjectSpecification: &revision.Specification,
		SpecificationRevisionID: revision.ID, Context: planningContext,
		Resources: resources, Team: roles, CurrentPlan: &stored.Plan, Policy: policy,
	}
	expanded, execution, err := r.projectPlanner.DecomposeEpic(ctx, input, stored.Plan, node.Key)
	if err != nil {
		return fmt.Errorf("decompose Epic %q into Tasks: %w", node.Key, err)
	}
	planID, err := util.ParseUUID(stored.ID)
	if err != nil {
		return fmt.Errorf("parse approved plan identity for Epic %q: %w", node.Key, err)
	}
	_, err = r.projectStore.PersistEpicExpansion(
		ctx, workspaceID, projectID, planID, stored.PlanDigest, node.Key,
		execution.Provider, execution.Model, expanded,
	)
	if err != nil {
		return fmt.Errorf("persist approved Task decomposition for Epic %q: %w", node.Key, err)
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
	goalContext, err := r.canonicalGoalReviewContextForNode(ctx, workspaceID, projectID, node.ID)
	if err != nil {
		return fmt.Errorf("load canonical goal review context for node %q: %w", node.Key, err)
	}
	goalContextText, err := goalContext.promptText()
	if err != nil {
		return err
	}
	description := fmt.Sprintf(
		"[autonomous-project-node:%s]\nStage: %s\nRisk: %s\nRequired role: %s (%s)\n\n%s%s\n\nCanonical project review context (authoritative):\n%s",
		node.Key, node.Kind, node.Risk, role, family, node.Description, criteria, goalContextText,
	)
	res, err := r.issueSvc.Create(ctx, service.IssueCreateParams{
		WorkspaceID:    workspaceID,
		Title:          node.Title,
		Description:    pgtype.Text{String: description, Valid: true},
		Status:         issuestatus.Backlog,
		Priority:       projectNodePriority(node.Priority),
		AssigneeType:   pgtype.Text{String: "agent", Valid: true},
		AssigneeID:     agentID,
		CreatorType:    "member",
		CreatorID:      ownerUserID,
		ProjectID:      projectID,
		AllowDuplicate: false,
	}, service.IssueCreateOpts{
		ActorID:          util.UUIDToString(ownerUserID),
		AnalyticsAgentID: util.UUIDToString(agentID),
		Platform:         "autonomous",
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

func (r *Runtime) canonicalGoalReviewContextForNode(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	nodeID string,
) (goalReviewContext, error) {
	if r == nil || r.projectStore == nil || r.pool == nil {
		return goalReviewContext{}, errors.New("canonical project review context is unavailable")
	}
	var planSpecificationRevisionID pgtype.UUID
	err := r.pool.QueryRow(ctx, `
		SELECT p.specification_revision_id
		FROM autonomous_project_plan_node n
		JOIN autonomous_project_plan p ON p.id=n.plan_id
		WHERE n.id=$1::uuid AND n.workspace_id=$2 AND n.project_id=$3
		  AND p.workspace_id=$2 AND p.project_id=$3
		  AND p.status IN ('active','blocked')
		ORDER BY p.revision DESC
		LIMIT 1
	`, nodeID, workspaceID, projectID).Scan(&planSpecificationRevisionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return goalReviewContext{}, errors.New("active plan node with a specification revision was not found")
	}
	if err != nil {
		return goalReviewContext{}, fmt.Errorf("load plan specification revision: %w", err)
	}
	if !planSpecificationRevisionID.Valid {
		return goalReviewContext{}, errors.New("active plan is missing its canonical specification revision")
	}

	revision, ok, err := r.projectStore.LoadLatestSpecification(ctx, workspaceID, projectID)
	if err != nil {
		return goalReviewContext{}, fmt.Errorf("load latest canonical specification: %w", err)
	}
	if !ok {
		return goalReviewContext{}, errors.New("canonical specification revision is unavailable")
	}
	if util.UUIDToString(planSpecificationRevisionID) != revision.ID {
		return goalReviewContext{}, fmt.Errorf("plan specification revision %s is stale; current revision is %s", util.UUIDToString(planSpecificationRevisionID), revision.ID)
	}
	return canonicalGoalReviewContext(revision.Specification)
}

func (r *Runtime) ensureCanonicalProjectReviewContext(ctx context.Context, issue db.Issue) (db.Issue, error) {
	if !issue.ProjectID.Valid {
		return issue, nil
	}
	quality, err := r.loadNodeQualityByIssue(ctx, issue.WorkspaceID, issue.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return issue, nil
	}
	if err != nil {
		return db.Issue{}, fmt.Errorf("load project review context target: %w", err)
	}
	if !projectNodeUsesIssueWorkflow(quality.Kind) {
		return issue, nil
	}
	const marker = "Canonical project review context (authoritative):\n"
	if strings.Contains(issue.Description.String, marker) {
		return issue, nil
	}
	goalContext, err := r.canonicalGoalReviewContextForNode(
		ctx, issue.WorkspaceID, issue.ProjectID, util.UUIDToString(quality.NodeID),
	)
	if err != nil {
		return db.Issue{}, err
	}
	text, err := goalContext.promptText()
	if err != nil {
		return db.Issue{}, err
	}
	description := strings.TrimRight(issue.Description.String, "\n") + "\n\n" + marker + text
	expectedRevision := issue.Revision
	updated, err := r.issueSvc.UpdateContent(ctx, issue, service.IssueContentPatch{
		Description:      &description,
		ExpectedRevision: &expectedRevision,
	})
	if err != nil {
		return db.Issue{}, fmt.Errorf("attach canonical project review context: %w", err)
	}
	return updated, nil
}

func (r *Runtime) validateProjectReview(ctx context.Context, issue db.Issue, verdict reviewVerdictOutput) error {
	if !issue.ProjectID.Valid {
		return nil
	}
	quality, err := r.loadNodeQualityByIssue(ctx, issue.WorkspaceID, issue.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load project review requirements: %w", err)
	}
	if !projectNodeUsesIssueWorkflow(quality.Kind) {
		return nil
	}
	goalContext, err := r.canonicalGoalReviewContextForNode(
		ctx, issue.WorkspaceID, issue.ProjectID, util.UUIDToString(quality.NodeID),
	)
	if err != nil {
		return err
	}
	return validateGoalDriftReview(verdict, goalContext.requirementIDs())
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
		if projectNodeUsesIssueWorkflow(node.Kind) {
			run, exists, runErr := r.store.FindRun(
				ctx,
				softwareDevelopmentWorkflow,
				util.UUIDToString(workspaceID),
				node.MaterializedIssueID,
			)
			if runErr != nil {
				return fmt.Errorf("resolve workflow-owned ready node status: %w", runErr)
			}
			if exists {
				switch run.State {
				case issuestatus.InProgress, issuestatus.InReview, issuestatus.Done:
					target = run.State
				case issuestatus.Blocked:
					// A node that the conductor just resumed must be schedulable.
					// Keep Blocked only while an explicit task retry is active;
					// otherwise the stale workflow state would overwrite Todo and
					// startReadyProjectNode would refuse to dispatch it forever.
					retryStatus, retryBoardState, _, retryErr := r.projectWorkflowRetry(ctx, workspaceID, issueID)
					if retryErr != nil {
						return fmt.Errorf("resolve workflow-owned ready retry status: %w", retryErr)
					}
					if retryStatus != "" && retryStatus != "completed" && retryBoardState != "" {
						target = retryBoardState
					} else if issuestatus.Effective(ctx, r.taskSvc.Queries, issue.WorkspaceID, issue.Status) != issuestatus.Blocked {
						target = issuestatus.Todo
					}
				}
			}
		}
		if target == "" && issuestatus.Effective(ctx, r.taskSvc.Queries, issue.WorkspaceID, issue.Status) != issuestatus.Blocked {
			target = issuestatus.Todo
		}
	case "running":
		target = issuestatus.InProgress
		if projectNodeUsesIssueWorkflow(node.Kind) {
			// Once the issue-level software-development workflow exists, it owns
			// the board status for a running implementation node. The Project OS
			// node intentionally remains "running" across implementation and
			// review, so blindly projecting running -> In Progress here races the
			// workflow's In Review state and fabricates review rejections.
			run, exists, runErr := r.store.FindRun(
				ctx,
				softwareDevelopmentWorkflow,
				util.UUIDToString(workspaceID),
				node.MaterializedIssueID,
			)
			if runErr != nil {
				return fmt.Errorf("resolve workflow-owned project node status: %w", runErr)
			}
			if exists {
				switch run.State {
				case issuestatus.InProgress, issuestatus.InReview, issuestatus.Done:
					target = run.State
				case issuestatus.Blocked:
					// A task-level retry can be running while the durable workflow
					// still waits in Blocked for that retry's structured result. Once
					// Project OS has re-attached the node, preserve the execution phase
					// instead of projecting Blocked back over the live retry.
					retryStatus, retryBoardState, _, retryErr := r.projectWorkflowRetry(ctx, workspaceID, issueID)
					if retryErr != nil {
						return fmt.Errorf("resolve workflow retry board state: %w", retryErr)
					}
					if retryStatus != "completed" && retryBoardState != "" {
						target = retryBoardState
					} else {
						target = issuestatus.Blocked
					}
				}
			}
		}
	case "verification":
		target = issuestatus.InReview
	case "completed":
		target = issuestatus.Done
	case "blocked":
		target = issuestatus.Blocked
	}
	if target == "" || issue.Status == target {
		return nil
	}
	_, err = r.taskSvc.SetIssueStatusForWorkflow(ctx, issue.ID, target)
	return err
}

func (r *Runtime) projectWorkflowRetry(
	ctx context.Context,
	workspaceID, issueID pgtype.UUID,
) (string, string, pgtype.UUID, error) {
	var status string
	var taskID, agentID, ownerID, reviewerID pgtype.UUID
	err := r.pool.QueryRow(ctx, `
		SELECT t.status, t.id, t.agent_id, wr.owner_agent_id, wr.reviewer_agent_id
		FROM agent_task_queue t
		JOIN autonomous_workflow_run wr
		  ON wr.workspace_id = $1
		 AND wr.issue_id = t.issue_id
		 AND wr.workflow_name = $3
		WHERE t.issue_id = $2
		  AND wr.state = 'blocked'
		  AND t.status IN ('queued', 'dispatched', 'running', 'waiting_local_directory', 'deferred', 'completed')
		  AND (t.retry_of_task_id IS NOT NULL OR t.rerun_of_task_id IS NOT NULL)
		  AND (t.agent_id = wr.owner_agent_id OR t.agent_id = wr.reviewer_agent_id)
		  AND COALESCE(t.completed_at, t.started_at, t.created_at) > wr.updated_at
		ORDER BY COALESCE(t.completed_at, t.started_at, t.created_at) DESC, t.created_at DESC, t.id DESC
		LIMIT 1
	`, workspaceID, issueID, softwareDevelopmentWorkflow).Scan(&status, &taskID, &agentID, &ownerID, &reviewerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", pgtype.UUID{}, nil
	}
	if err != nil {
		return "", "", pgtype.UUID{}, err
	}
	return status, workflowRetryBoardState(agentID, ownerID, reviewerID), taskID, nil
}

func workflowRetryBoardState(agentID, ownerID, reviewerID pgtype.UUID) string {
	switch util.UUIDToString(agentID) {
	case util.UUIDToString(ownerID):
		return issuestatus.InProgress
	case util.UUIDToString(reviewerID):
		return issuestatus.InReview
	default:
		return ""
	}
}

func (r *Runtime) startReadyProjectNode(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	node projectorchestration.ReadyNode,
) error {
	if err := r.ensurePredecessorBrainDecisionsCurrent(ctx, workspaceID, projectID, node.Key); err != nil {
		return err
	}
	if err := r.enforceProjectNodePolicy(ctx, workspaceID, projectID, node); err != nil {
		return err
	}
	if err := r.ensureNodeQualityPolicy(ctx, workspaceID, projectID, node); err != nil {
		return fmt.Errorf("seed node quality policy: %w", err)
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
	if !projectNodeUsesIssueWorkflow(node.Kind) {
		recovered, recoverErr := r.recoverCompletedDirectProjectRetry(ctx, workspaceID, issue.ID, agentID)
		if recoverErr != nil {
			return recoverErr
		}
		if recovered {
			return nil
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
		dispatchErr := err
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		var dispatched bool
		if projectNodeUsesIssueWorkflow(node.Kind) {
			_, dispatched, err = r.store.FindRun(
				cleanupCtx,
				softwareDevelopmentWorkflow,
				util.UUIDToString(workspaceID),
				util.UUIDToString(issue.ID),
			)
		} else {
			err = r.pool.QueryRow(cleanupCtx, `
				SELECT EXISTS (
					SELECT 1 FROM agent_task_queue
					WHERE issue_id = $1
					  AND agent_id = $2
					  AND status IN ('queued', 'dispatched', 'running', 'waiting_local_directory', 'deferred')
				)
			`, issue.ID, agentID).Scan(&dispatched)
		}
		if err != nil {
			slog.Warn("project node dispatch state could not be verified; keeping claim",
				"project_id", util.UUIDToString(projectID), "node", node.Key, "error", err)
			return fmt.Errorf("dispatch failed (%v) and durable state could not be verified: %w", dispatchErr, err)
		}
		if dispatched {
			slog.Warn("project node dispatch failed after durable work was created; keeping claim",
				"project_id", util.UUIDToString(projectID), "node", node.Key, "issue_id", util.UUIDToString(issue.ID))
			return dispatchErr
		}
		if releaseErr := r.projectStore.ReleaseNodeClaim(cleanupCtx, workspaceID, projectID, node.Key, "dispatch failed: "+dispatchErr.Error()); releaseErr != nil {
			slog.Warn("project node claim release failed after dispatch error",
				"project_id", util.UUIDToString(projectID), "node", node.Key, "error", releaseErr)
		}
		if _, resetErr := r.taskSvc.SetIssueStatusForWorkflow(cleanupCtx, issue.ID, issuestatus.Todo); resetErr != nil {
			slog.Warn("project issue reset failed after dispatch error",
				"project_id", util.UUIDToString(projectID), "node", node.Key, "issue_id", util.UUIDToString(issue.ID), "error", resetErr)
		}
		return dispatchErr
	}
	_ = assignedRole // retained for durable assignment/debug projection.
	return nil
}

func (r *Runtime) ensurePredecessorBrainDecisionsCurrent(
	ctx context.Context,
	workspaceID, projectID pgtype.UUID,
	nodeKey string,
) error {
	if r == nil || r.pool == nil {
		return nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT predecessor.id, predecessor.node_key, predecessor.status,
		       artifact.content ->> 'task_id', artifact.content -> 'result'
		FROM autonomous_project_plan_edge edge
		JOIN autonomous_project_plan plan ON plan.id=edge.plan_id
		JOIN autonomous_project_plan_node predecessor
		  ON predecessor.plan_id=edge.plan_id AND predecessor.node_key=edge.from_node_key
		LEFT JOIN autonomous_project_artifact artifact
		  ON artifact.plan_id=edge.plan_id AND artifact.node_id=predecessor.id
		 AND artifact.status='active'
		 AND artifact.valid=TRUE AND artifact.artifact_revision=predecessor.spec_revision
		WHERE edge.workspace_id=$1 AND edge.project_id=$2 AND edge.to_node_key=$3
		  AND edge.dependency_type IN ('hard','artifact')
		  AND plan.status='active'
		  AND predecessor.kind IN ('architecture','implementation','migration','integration')
		ORDER BY plan.revision DESC, artifact.created_at DESC
	`, workspaceID, projectID, nodeKey)
	if err != nil {
		return fmt.Errorf("load predecessor decision artifacts: %w", err)
	}
	type predecessorArtifact struct {
		nodeID  pgtype.UUID
		nodeKey string
		status  string
		taskID  string
		result  []byte
	}
	artifacts := make([]predecessorArtifact, 0)
	for rows.Next() {
		var artifact predecessorArtifact
		if err := rows.Scan(&artifact.nodeID, &artifact.nodeKey, &artifact.status, &artifact.taskID, &artifact.result); err != nil {
			rows.Close()
			return err
		}
		artifacts = append(artifacts, artifact)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, artifact := range artifacts {
		if artifact.status != "completed" {
			return fmt.Errorf("dependent project node %q is blocked until predecessor %q is completed", nodeKey, artifact.nodeKey)
		}
		if strings.TrimSpace(artifact.taskID) == "" {
			continue
		}
		handoff, err := parseImplementationHandoff(artifact.result)
		if err != nil || len(handoff.Decisions)+len(handoff.ArchitectureChoices) == 0 {
			continue
		}
		var brainRevision int64
		err = r.pool.QueryRow(ctx, `
			SELECT brain_revision
			FROM autonomous_project_brain_entry
			WHERE workspace_id=$1 AND project_id=$2 AND node_id=$3
			  AND source_type='task_completion_decision'
			  AND source_id=$4
			  AND entry_type='architecture_decision'
			  AND status='active' AND governance_state='current'
			ORDER BY brain_revision DESC LIMIT 1
		`, workspaceID, projectID, artifact.nodeID,
			"task:"+artifact.taskID+":architecture-decisions").Scan(&brainRevision)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("dependent project node %q is blocked until predecessor %q decisions reach the project brain", nodeKey, artifact.nodeKey)
		}
		if err != nil {
			return fmt.Errorf("verify predecessor project brain revision: %w", err)
		}
		var currentRevision int64
		err = r.pool.QueryRow(ctx, `
			SELECT revision FROM autonomous_project_brain_state
			WHERE workspace_id=$1 AND project_id=$2
		`, workspaceID, projectID).Scan(&currentRevision)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && (brainRevision <= 0 || currentRevision < brainRevision)) {
			return fmt.Errorf("dependent project node %q is blocked by a stale predecessor brain revision", nodeKey)
		}
		if err != nil {
			return fmt.Errorf("load current project brain revision: %w", err)
		}
	}
	return nil
}

func (r *Runtime) recoverCompletedDirectProjectRetry(
	ctx context.Context,
	workspaceID, issueID, agentID pgtype.UUID,
) (bool, error) {
	var taskID pgtype.UUID
	err := r.pool.QueryRow(ctx, `
		SELECT t.id
		FROM agent_task_queue t
		WHERE t.issue_id = $1
		  AND t.agent_id = $2
		  AND t.status = 'completed'
		  AND (t.retry_of_task_id IS NOT NULL OR t.rerun_of_task_id IS NOT NULL)
		  AND NOT EXISTS (
			SELECT 1
			FROM agent_task_queue newer
			WHERE newer.issue_id = t.issue_id
			  AND newer.id <> t.id
			  AND newer.created_at > t.created_at
		  )
		ORDER BY COALESCE(t.completed_at, t.created_at) DESC, t.created_at DESC, t.id DESC
		LIMIT 1
	`, issueID, agentID).Scan(&taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	if err := r.handleTaskCompleted(ctx, events.Event{
		Type:        protocol.EventTaskCompleted,
		WorkspaceID: util.UUIDToString(workspaceID),
		TaskID:      util.UUIDToString(taskID),
	}); err != nil {
		return false, fmt.Errorf("recover completed direct project retry: %w", err)
	}

	var status string
	err = r.pool.QueryRow(ctx, `
		SELECT n.status
		FROM autonomous_project_plan_node n
		JOIN autonomous_project_plan p ON p.id = n.plan_id
		WHERE n.workspace_id = $1
		  AND n.materialized_issue_id = $2
		  AND p.status IN ('active', 'blocked')
		ORDER BY p.revision DESC, n.updated_at DESC
		LIMIT 1
	`, workspaceID, issueID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return status != "ready", nil
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
	case errors.Is(cause, projectorchestration.ErrNoEligibleAgent):
		category = "no_eligible_agent"
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
blockedNodeLoop:
	for _, node := range blocked {
		resolved := false
		var retryIssueID pgtype.UUID
		switch node.Category {
		case "dependency":
			resolved = true // ResumeBlockedNode performs the authoritative dependency check.
		case "approval":
			if node.Kind == projectorchestration.NodeDeploy {
				nodeID, parseErr := util.ParseUUID(node.ID)
				if parseErr != nil {
					return fmt.Errorf("load blocked production deployment node identity: %w", parseErr)
				}
				var planID pgtype.UUID
				var planRevision int64
				if err := r.pool.QueryRow(ctx, `
					SELECT p.id, p.revision
					FROM autonomous_project_plan p
					JOIN autonomous_project_plan_node n
					  ON n.plan_id=p.id AND n.workspace_id=p.workspace_id AND n.project_id=p.project_id
					WHERE p.workspace_id=$1 AND p.project_id=$2
					  AND p.status IN ('active','blocked') AND n.id=$3 AND n.node_key=$4
				`, workspaceID, projectID, nodeID, node.Key).Scan(&planID, &planRevision); err != nil {
					return fmt.Errorf("load blocked production deployment plan: %w", err)
				}
				manifest, err := projectorchestration.LoadPlanReleaseManifest(ctx, r.pool, workspaceID, projectID, planID)
				if err != nil {
					return err
				}
				if manifest.Environment == "production" {
					staged, err := r.hasHealthyStageDeployment(ctx, workspaceID, projectID, planID, nodeID, manifest)
					if err != nil {
						return err
					}
					if staged {
						resolved, err = r.hasExactProductionApproval(ctx, workspaceID, projectID, planID, planRevision, nodeID, manifest)
						if err != nil {
							return err
						}
					}
				}
			} else {
				nodeID, parseErr := util.ParseUUID(node.ID)
				if parseErr != nil {
					return fmt.Errorf("load blocked approval node identity: %w", parseErr)
				}
				var planID pgtype.UUID
				var planRevision int64
				if err := r.pool.QueryRow(ctx, `
					SELECT p.id, p.revision
					FROM autonomous_project_plan p
					JOIN autonomous_project_plan_node n
					  ON n.plan_id=p.id AND n.workspace_id=p.workspace_id AND n.project_id=p.project_id
					WHERE p.workspace_id=$1 AND p.project_id=$2
					  AND p.status IN ('active','blocked') AND n.id=$3 AND n.node_key=$4
				`, workspaceID, projectID, nodeID, node.Key).Scan(&planID, &planRevision); err != nil {
					return fmt.Errorf("load blocked approval plan: %w", err)
				}
				resolved, err = r.hasExactPlanApproval(ctx, workspaceID, projectID, planID, planRevision, nodeID)
				if err != nil {
					return err
				}
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
		case "no_eligible_agent":
			team, ok, teamErr := r.team.FindProject(ctx, workspaceID, projectID)
			if teamErr != nil {
				return teamErr
			}
			if ok {
				_, _, _, selectErr := r.selectProjectNodeAgent(ctx, team, node.ReadyNode)
				resolved = selectErr == nil
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
		case "quality_policy":
			// Repair & continue is an explicit request to rerun a failed
			// contract/quality check. The node itself must be released; merely
			// resolving the escalation leaves it permanently blocked. A resolved
			// escalation is only a retry signal for the block that preceded it;
			// historical resolutions must not resurrect a newer quality failure.
			if node.MaterializedIssueID != "" {
				issueID, err := util.ParseUUID(node.MaterializedIssueID)
				if err != nil {
					return err
				}
				if err := r.reconcileCompletedProjectQuality(ctx, db.Issue{ID: issueID, WorkspaceID: workspaceID}); err != nil {
					return err
				}
			}
			if node.ID != "" {
				var nodeID pgtype.UUID
				if parsed, parseErr := util.ParseUUID(node.ID); parseErr == nil {
					nodeID = parsed
					if err := r.pool.QueryRow(ctx, `
						SELECT EXISTS (
							SELECT 1 FROM autonomous_project_quality_gate_run
							WHERE workspace_id=$1 AND project_id=$2 AND node_id=$3 AND required=TRUE
						)
						AND NOT EXISTS (
							SELECT 1 FROM autonomous_project_quality_gate_run
							WHERE workspace_id=$1 AND project_id=$2 AND node_id=$3
							  AND required=TRUE AND status <> 'passed'
						)
					`, workspaceID, projectID, nodeID).Scan(&resolved); err != nil {
						return err
					}
					if resolved {
						_, err := r.pool.Exec(ctx, `
							UPDATE autonomous_project_escalation
							SET status='resolved', resolution=jsonb_build_object('decision','quality_evidence_passed'), resolved_at=now()
							WHERE workspace_id=$1 AND project_id=$2 AND node_id=$3
							  AND category='quality_policy' AND status IN ('open','acknowledged')
						`, workspaceID, projectID, nodeID)
						if err != nil {
							return err
						}
					}
					if resolved {
						break
					}
					if err := r.pool.QueryRow(ctx, `
						SELECT EXISTS (
							SELECT 1
							FROM autonomous_project_escalation e
							JOIN autonomous_project_plan_node n ON n.id = e.node_id
							WHERE e.workspace_id=$1 AND e.project_id=$2
							  AND e.node_id=$3 AND e.status='resolved'
							  AND e.resolved_at > n.updated_at
							  AND e.category IN ('contract_violation','quality_policy','technical_failure')
						)
					`, workspaceID, projectID, nodeID).Scan(&resolved); err != nil {
						return err
					}
				}
			}
		case "technical_failure", "manual":
			if node.MaterializedIssueID != "" {
				issueID, parseErr := util.ParseUUID(node.MaterializedIssueID)
				if parseErr != nil {
					return parseErr
				}
				retryIssueID = issueID
				issue, loadErr := r.taskSvc.Queries.GetIssue(ctx, issueID)
				if errors.Is(loadErr, pgx.ErrNoRows) {
					resolved = true
				} else if loadErr != nil {
					return loadErr
				} else {
					effective := issuestatus.Effective(ctx, r.taskSvc.Queries, issue.WorkspaceID, issue.Status)
					workflowRun, workflowExists, runErr := r.store.FindRun(
						ctx, softwareDevelopmentWorkflow, util.UUIDToString(workspaceID), util.UUIDToString(issueID),
					)
					if runErr != nil {
						return runErr
					}
					if workflowExists && workflowRun.State == issuestatus.Done && effective != issuestatus.Done {
						if err := r.reconcileCompletedProjectQuality(ctx, issue); err != nil {
							return err
						}
						nodeID, parseErr := util.ParseUUID(node.ID)
						if parseErr != nil {
							return parseErr
						}
						var qualityPassed bool
						if err := r.pool.QueryRow(ctx, `
							SELECT NOT EXISTS (
								SELECT 1 FROM autonomous_project_quality_gate_run
								WHERE workspace_id=$1 AND project_id=$2 AND node_id=$3
								  AND required=TRUE AND evidence ->> 'policy_requirement' = 'true'
								  AND status <> 'passed'
							)
						`, workspaceID, projectID, nodeID).Scan(&qualityPassed); err != nil {
							return err
						}
						if qualityPassed {
							if _, err := r.taskSvc.SetIssueStatusForWorkflow(ctx, issueID, issuestatus.Done); err != nil {
								return err
							}
							if err := r.projectStore.CompleteNodeByIssue(ctx, workspaceID, issueID); err != nil {
								return err
							}
							slog.Info("project conductor restored issue status from completed workflow",
								"project_id", util.UUIDToString(projectID),
								"node", node.Key,
								"issue_id", node.MaterializedIssueID,
							)
							continue blockedNodeLoop
						}
					}
					if effective == issuestatus.Done {
						// Done is terminal evidence, not an "unblocked" retry
						// request. The old path resumed the node and immediately
						// overwrote Done with Todo, which resurrected completed
						// reviewer work and left the durable workflow Blocked.
						if err := r.projectStore.CompleteNodeByIssue(ctx, issue.WorkspaceID, issue.ID); err != nil {
							return err
						}
						slog.Info("project conductor completed blocked node from terminal issue",
							"project_id", util.UUIDToString(projectID),
							"node", node.Key,
							"issue_id", node.MaterializedIssueID,
						)
						continue blockedNodeLoop
					}
					if canResumeManualDirectNode(node, effective) {
						// Manual direct nodes were blocked when no shared source was
						// available. A configured local project resource is now an
						// explicit repair signal; issue-workflow nodes use their own
						// contract recovery path above.
						if err := r.pool.QueryRow(ctx, `
							SELECT EXISTS (
								SELECT 1
								FROM project_resource
								WHERE workspace_id = $1
								  AND project_id = $2
								  AND resource_type = 'local_directory'
							)
						`, workspaceID, projectID).Scan(&resolved); err != nil {
							return err
						}
					}
					if !resolved {
						resolved = effective != issuestatus.Blocked
					}
					if resolved && projectNodeUsesIssueWorkflow(node.Kind) {
						// A task-level retry may already be executing while the
						// Project OS node still carries the old technical block.
						// A just-completed retry is also owned by the blocked workflow:
						// leave the node blocked until handleTaskCompleted validates its
						// contract. Otherwise the conductor can win the completion-event
						// race, move the node to Ready, and overwrite the issue with Todo.
						retryStatus, retryBoardState, retryTaskID, retryErr := r.projectWorkflowRetry(ctx, workspaceID, issueID)
						if retryErr != nil {
							return retryErr
						}
						if retryStatus == "completed" {
							// Completion events are in-process notifications and can be lost
							// across a backend restart. The conductor already has durable proof
							// of the completed retry here, so feed it through the same idempotent
							// completion path instead of waiting forever for an event that will
							// never be replayed.
							if err := r.handleTaskCompleted(ctx, events.Event{
								Type:        protocol.EventTaskCompleted,
								WorkspaceID: util.UUIDToString(workspaceID),
								TaskID:      util.UUIDToString(retryTaskID),
							}); err != nil {
								return fmt.Errorf("recover completed project workflow retry: %w", err)
							}
							continue blockedNodeLoop
						}
						if retryStatus != "" {
							attached, err := r.projectStore.ResumeNodeForWorkflowRetry(ctx, workspaceID, issueID)
							if err != nil {
								return err
							}
							if attached {
								if retryBoardState != "" && effective != retryBoardState {
									if _, err := r.taskSvc.SetIssueStatusForWorkflow(ctx, issueID, retryBoardState); err != nil {
										return err
									}
								}
								slog.Info("project conductor attached active task retry to blocked node",
									"project_id", util.UUIDToString(projectID),
									"node", node.Key,
									"issue_id", node.MaterializedIssueID,
								)
							}
							continue blockedNodeLoop
						}
					}
				}
			}
			if !resolved {
				// A resolved technical escalation is an explicit retry signal.
				err := r.pool.QueryRow(ctx, `
					SELECT EXISTS (
						SELECT 1
						FROM autonomous_project_escalation e
						WHERE e.workspace_id = $1
						  AND e.project_id = $2
						  AND e.status = 'resolved'
						  AND (e.context ->> 'node_key' = $3 OR e.context ->> 'node_id' = $5::text)
						  AND e.category IN ('technical_failure', 'external_dependency')
						  AND e.resolved_at > COALESCE(
							  (SELECT max(t.created_at) FROM agent_task_queue t WHERE t.issue_id = $4),
							  '-infinity'::timestamptz
						  )
					)
				`, workspaceID, projectID, node.Key, retryIssueID, node.ID).Scan(&resolved)
				if err != nil {
					return err
				}
			}
		}
		if !resolved {
			continue
		}
		if node.Category == "technical_failure" && projectNodeUsesIssueWorkflow(node.Kind) && node.MaterializedIssueID != "" {
			issueID, err := util.ParseUUID(node.MaterializedIssueID)
			if err != nil {
				return err
			}
			run, exists, err := r.store.FindRun(
				ctx, softwareDevelopmentWorkflow, util.UUIDToString(workspaceID), node.MaterializedIssueID,
			)
			if err != nil {
				return err
			}
			if exists && (run.State == issuestatus.Blocked || run.State == issuestatus.Done) {
				resumed, err := r.projectStore.ResumeNodeForWorkflowRetry(ctx, workspaceID, issueID)
				if err != nil {
					return fmt.Errorf("resume blocked project node for explicit workflow retry: %w", err)
				}
				if !resumed {
					continue blockedNodeLoop
				}
				if r.engine == nil {
					_ = r.projectStore.SetNodeBlocked(ctx, workspaceID, projectID, node.Key, "technical_failure", "workflow engine is unavailable for explicit retry")
					return errors.New("autonomous workflow engine is unavailable for explicit retry")
				}
				result, err := r.engine.Handle(softwareDevelopmentWorkflow, workflow.Event{
					ID:                fmt.Sprintf("project-repair-retry:%s:%d", run.ID, run.Revision),
					Type:              "issue.retry_requested",
					WorkspaceID:       util.UUIDToString(workspaceID),
					ProjectID:         util.UUIDToString(projectID),
					IssueID:           node.MaterializedIssueID,
					ActorType:         "system",
					AccountableUserID: run.AccountableUserID,
					Payload:           map[string]any{"source": "project_repair"},
				})
				if err != nil {
					_ = r.projectStore.SetNodeBlocked(ctx, workspaceID, projectID, node.Key, "technical_failure", "workflow retry transition failed: "+err.Error())
					return fmt.Errorf("transition blocked project workflow for explicit retry: %w", err)
				}
				if result.Applied || result.Duplicate {
					slog.Info("project conductor queued explicit retry for blocked workflow",
						"project_id", util.UUIDToString(projectID),
						"node", node.Key,
						"issue_id", node.MaterializedIssueID,
						"run_id", run.ID,
					)
				}
				continue blockedNodeLoop
			}
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

func canResumeManualDirectNode(node projectorchestration.BlockedNode, effectiveStatus string) bool {
	return node.Category == "manual" &&
		effectiveStatus == issuestatus.Blocked &&
		!projectNodeUsesIssueWorkflow(node.Kind)
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

	var policyJSON []byte
	if err := r.pool.QueryRow(ctx, `
		SELECT policy
		FROM autonomous_project_plan
		WHERE id = $1 AND workspace_id = $2 AND project_id = $3
	`, planID, workspaceID, projectID).Scan(&policyJSON); err != nil {
		return fail(err)
	}
	var policy projectorchestration.Policy
	if err := json.Unmarshal(policyJSON, &policy); err != nil {
		return fail(fmt.Errorf("decode deployment policy: %w", err))
	}
	if r.releaseArtifactStore == nil {
		return fail(errors.New("private release artifact storage is not configured"))
	}
	manifest, err := projectorchestration.LoadPlanReleaseManifest(ctx, r.pool, workspaceID, projectID, planID)
	if err != nil {
		return fail(err)
	}
	staged, err := r.hasHealthyStageDeployment(ctx, workspaceID, projectID, planID, nodeID, manifest)
	if err != nil {
		return fail(err)
	}
	if manifest.Environment == "production" && !staged {
		manifest, err = projectorchestration.PinPlanReleaseManifestEnvironment(ctx, r.pool, workspaceID, projectID, planID, "staging")
		if err != nil {
			return fail(err)
		}
	}
	if manifest.Environment == "staging" && staged {
		manifest, err = projectorchestration.PinPlanReleaseManifestEnvironment(ctx, r.pool, workspaceID, projectID, planID, "production")
		if err != nil {
			return fail(err)
		}
	}
	if manifest.Environment == "production" && !staged {
		return fail(errors.New("production release is blocked until the matching staging deployment passes health checks"))
	}
	if err := manifest.Validate(); err != nil {
		return fail(fmt.Errorf("validate deployment release manifest: %w", err))
	}
	releaseRef := "sha256:" + manifest.ManifestDigest
	if _, err := projectorchestration.ParseReleaseReference(releaseRef); err != nil {
		return fail(err)
	}
	manifestID, err := util.ParseUUID(manifest.ManifestID)
	if err != nil {
		return fail(errors.New("persisted release manifest has an invalid UUID identity"))
	}

	var deploymentID pgtype.UUID
	if err := r.pool.QueryRow(ctx, `
		INSERT INTO autonomous_project_deployment (
			workspace_id, project_id, plan_id, node_id, environment, provider,
			status, policy_snapshot, release_manifest_id, release_manifest_digest, started_at
		)
		VALUES ($1, $2, $3, $4, $5, 'generic', 'running', $6, $7, $8, now())
		RETURNING id
	`, workspaceID, projectID, planID, nodeID, manifest.Environment, policyJSON, manifestID, manifest.ManifestDigest).Scan(&deploymentID); err != nil {
		return fail(fmt.Errorf("create deployment record: %w", err))
	}

	deploymentRequest := projectorchestration.DeploymentRequest{
		WorkspaceID:    workspaceID,
		ProjectID:      projectID,
		PlanID:         planID,
		Environment:    manifest.Environment,
		ReleaseRef:     releaseRef,
		Manifest:       manifest.ProviderView(),
		ArtifactDigest: manifest.ArtifactDigest,
		CommitSHA:      manifest.CommitSHA,
	}
	bridge, ok := r.deploymentAdapter.(interface {
		DeployForRecord(context.Context, projectorchestration.DeploymentRequest, uuid.UUID, uuid.UUID) (projectorchestration.DeploymentResult, error)
	})
	if !ok {
		return fail(errors.New("deployment adapter cannot bind a generic operation to its autonomous deployment record"))
	}
	result, err := bridge.DeployForRecord(ctx, deploymentRequest, uuid.UUID(deploymentID.Bytes), uuid.UUID(nodeID.Bytes))
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
	operationID, err := uuid.Parse(fmt.Sprint(result.Evidence["generic_operation_id"]))
	if err != nil || operationID == uuid.Nil {
		return fail(errors.New("deployment bridge did not return a valid generic operation identity"))
	}
	updated, err := r.pool.Exec(ctx, `
		UPDATE autonomous_project_deployment
		SET generic_operation_id = $2,
		    evidence = jsonb_build_object('generic_operation_id', $3::text)
		WHERE id = $1 AND status = 'running' AND generic_operation_id IS NULL
	`, deploymentID, operationID, operationID.String())
	if err != nil {
		return fail(fmt.Errorf("correlate autonomous deployment with generic operation: %w", err))
	}
	if updated.RowsAffected() != 1 {
		return fail(errors.New("autonomous deployment operation correlation was not persisted"))
	}
	return nil
}

func (r *Runtime) hasHealthyStageDeployment(
	ctx context.Context,
	workspaceID, projectID, planID, nodeID pgtype.UUID,
	productionManifest projectorchestration.ReleaseManifest,
) (bool, error) {
	var staged bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM autonomous_project_deployment d
			JOIN autonomous_project_release_manifest m
			  ON m.manifest_id=d.release_manifest_id
			 AND m.manifest_digest=d.release_manifest_digest
			 AND m.workspace_id=d.workspace_id
			 AND m.project_id=d.project_id
			 AND m.plan_id=d.plan_id
			WHERE d.workspace_id=$1 AND d.project_id=$2 AND d.plan_id=$3 AND d.node_id=$4
			  AND d.environment='staging' AND d.status='succeeded'
			  AND d.evidence->'stage_smoke'->>'passed'='true'
			  AND d.evidence->'stage_health'->>'healthy'='true'
			  AND m.environment='staging'
			  AND m.plan_revision=$5 AND m.plan_digest=$6
			  AND m.change_set_id=$7 AND m.commit_sha=$8 AND m.tree_sha=$9
			  AND m.builder=$10 AND m.outputs=$11
			  AND m.artifact_digest=$12 AND m.artifact_storage_key=$13
			  AND m.quality_gate_run_ids=$14::uuid[]
		)
	`, workspaceID, projectID, planID, nodeID, productionManifest.PlanRevision,
		productionManifest.PlanDigest, productionManifest.ChangeSetID, productionManifest.CommitSHA,
		productionManifest.TreeSHA, productionManifest.Builder, productionManifest.Outputs,
		productionManifest.ArtifactDigest, productionManifest.ArtifactStorageKey,
		releaseRunIDsAsUUIDs(productionManifest.QualityGateRunIDs)).Scan(&staged)
	if err != nil {
		return false, fmt.Errorf("check matching healthy staging deployment: %w", err)
	}
	return staged, nil
}

func releaseRunIDsAsUUIDs(runIDs []string) []pgtype.UUID {
	ids := make([]pgtype.UUID, 0, len(runIDs))
	for _, runID := range runIDs {
		id, err := util.ParseUUID(runID)
		if err != nil {
			return nil
		}
		ids = append(ids, id)
	}
	return ids
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
	if r.observationAdapter == nil {
		return fail(projectorchestration.ErrAdapterNotConfigured)
	}

	var deploymentID pgtype.UUID
	if err := r.pool.QueryRow(ctx, `
		SELECT id
		FROM autonomous_project_deployment
		WHERE workspace_id = $1
		  AND project_id = $2
		  AND environment = 'production'
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
		WorkspaceID:   workspaceID,
		ProjectID:     projectID,
		DeploymentID:  deploymentID,
		WindowSeconds: 300,
	})
	if err != nil {
		return fail(err)
	}

	contentFields := map[string]any{
		"deployment_id": util.UUIDToString(deploymentID),
		"healthy":       result.Healthy,
		"error_rate":    result.ErrorRate,
		"latency_p95":   result.LatencyP95,
		"signals":       result.Signals,
		"evidence":      result.Evidence,
	}
	var rollbackErr error
	if !result.Healthy {
		rollbackOutcome, err := r.rollbackUnhealthyDeployment(ctx, workspaceID, projectID, deploymentID,
			"production", "production health check failed: "+node.Title)
		contentFields["rollback"] = rollbackOutcome
		rollbackErr = err
	}
	content, err := json.Marshal(contentFields)
	if err != nil {
		return fail(fmt.Errorf("encode deployment observation evidence: %w", err))
	}
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
	if rollbackErr != nil {
		return fail(fmt.Errorf("roll back unhealthy production release: %w", rollbackErr))
	}

	return r.projectStore.CompleteExternalNode(ctx, workspaceID, projectID, node.Key)
}

func (r *Runtime) rollbackUnhealthyDeployment(
	ctx context.Context,
	workspaceID, projectID, currentDeploymentID pgtype.UUID,
	environment, reason string,
) (map[string]any, error) {
	if environment != "staging" && environment != "production" {
		return nil, errors.New("health rollback has an unsupported target environment")
	}
	var currentEnvironment string
	if err := r.pool.QueryRow(ctx, `
		SELECT environment
		FROM autonomous_project_deployment
		WHERE id=$1 AND workspace_id=$2 AND project_id=$3
	`, currentDeploymentID, workspaceID, projectID).Scan(&currentEnvironment); err != nil {
		return nil, fmt.Errorf("load unhealthy deployment identity: %w", err)
	}
	if currentEnvironment != environment {
		return nil, errors.New("health rollback environment does not match the unhealthy deployment")
	}

	var targetDeploymentID, targetManifestID pgtype.UUID
	var targetManifestDigest string
	var encodedManifest []byte
	err := r.pool.QueryRow(ctx, `
		SELECT d.id, m.manifest_id, m.manifest_digest, m.manifest_json
		FROM autonomous_project_deployment d
		JOIN autonomous_project_release_manifest m
		  ON m.manifest_id=d.release_manifest_id AND m.manifest_digest=d.release_manifest_digest
		 AND m.workspace_id=d.workspace_id AND m.project_id=d.project_id AND m.plan_id=d.plan_id
		WHERE d.workspace_id=$1 AND d.project_id=$2 AND d.environment=$3 AND d.id<>$4
		  AND d.status IN ('succeeded','rolled_back')
		  AND m.environment=$3
		  AND (
			 d.evidence->'stage_health'->>'healthy'='true'
			 OR EXISTS (
				SELECT 1 FROM autonomous_project_artifact a
				WHERE a.workspace_id=d.workspace_id AND a.project_id=d.project_id
				  AND a.content->>'deployment_id'=d.id::text AND a.content->>'healthy'='true'
			 )
		  )
		ORDER BY d.completed_at DESC NULLS LAST, d.created_at DESC
		LIMIT 1
	`, workspaceID, projectID, environment, currentDeploymentID).Scan(
		&targetDeploymentID, &targetManifestID, &targetManifestDigest, &encodedManifest,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			outcome := map[string]any{"status": "unavailable", "reason": "no previous healthy manifest exists"}
			rollbackErr := errors.New("no previous healthy manifest is available for rollback")
			if persistErr := r.persistHealthRollbackOutcome(ctx, workspaceID, projectID, currentDeploymentID, outcome, ""); persistErr != nil {
				return outcome, errors.Join(rollbackErr, fmt.Errorf("persist unavailable rollback outcome: %w", persistErr))
			}
			return outcome, rollbackErr
		}
		return nil, fmt.Errorf("load most recent healthy release for rollback: %w", err)
	}
	var targetManifest projectorchestration.ReleaseManifest
	if err := json.Unmarshal(encodedManifest, &targetManifest); err != nil {
		return nil, fmt.Errorf("decode known-good rollback manifest: %w", err)
	}
	if err := targetManifest.Validate(); err != nil {
		return nil, fmt.Errorf("validate known-good rollback manifest: %w", err)
	}
	if targetManifest.ManifestID != util.UUIDToString(targetManifestID) || targetManifest.ManifestDigest != targetManifestDigest ||
		targetManifest.WorkspaceID != util.UUIDToString(workspaceID) || targetManifest.ProjectID != util.UUIDToString(projectID) ||
		targetManifest.Environment != environment {
		return nil, errors.New("known-good rollback manifest does not match its deployment record")
	}

	outcome := map[string]any{
		"status": "pending", "reason": reason,
		"target_deployment_id":   util.UUIDToString(targetDeploymentID),
		"target_manifest_id":     util.UUIDToString(targetManifestID),
		"target_manifest_digest": targetManifestDigest,
		"target_environment":     environment,
	}
	if err := r.persistHealthRollbackOutcome(ctx, workspaceID, projectID, currentDeploymentID, outcome, ""); err != nil {
		return nil, err
	}
	if r.deploymentAdapter == nil {
		outcome["status"] = "failed"
		outcome["error"] = projectorchestration.ErrAdapterNotConfigured.Error()
		if persistErr := r.persistHealthRollbackOutcome(ctx, workspaceID, projectID, currentDeploymentID, outcome, "failed"); persistErr != nil {
			return outcome, errors.Join(projectorchestration.ErrAdapterNotConfigured, fmt.Errorf("persist failed rollback outcome: %w", persistErr))
		}
		return outcome, projectorchestration.ErrAdapterNotConfigured
	}
	result, err := r.deploymentAdapter.Rollback(ctx, projectorchestration.RollbackRequest{
		WorkspaceID: workspaceID, ProjectID: projectID, DeploymentID: targetDeploymentID,
		TargetManifestID: targetManifest.ManifestID, TargetManifestDigest: targetManifest.ManifestDigest,
		TargetEnvironment: environment, Reason: reason,
	})
	if err != nil {
		outcome["status"] = "failed"
		outcome["error"] = err.Error()
		callErr := fmt.Errorf("call rollback adapter: %w", err)
		if persistErr := r.persistHealthRollbackOutcome(ctx, workspaceID, projectID, currentDeploymentID, outcome, "failed"); persistErr != nil {
			return outcome, errors.Join(callErr, fmt.Errorf("persist failed rollback outcome: %w", persistErr))
		}
		return outcome, callErr
	}
	if result.Status != "succeeded" && result.Status != "rolled_back" {
		outcome["status"] = "failed"
		outcome["error"] = "rollback provider did not confirm a terminal rollback"
		outcome["provider_status"] = result.Status
		rollbackErr := errors.New("rollback provider did not confirm a terminal rollback")
		if persistErr := r.persistHealthRollbackOutcome(ctx, workspaceID, projectID, currentDeploymentID, outcome, "failed"); persistErr != nil {
			return outcome, errors.Join(rollbackErr, fmt.Errorf("persist failed rollback outcome: %w", persistErr))
		}
		return outcome, rollbackErr
	}
	if result.Evidence == nil || result.Evidence["target_manifest_id"] != targetManifest.ManifestID ||
		result.Evidence["target_manifest_digest"] != targetManifest.ManifestDigest {
		outcome["status"] = "failed"
		outcome["error"] = "rollback provider acknowledged a different target manifest"
		rollbackErr := errors.New("rollback provider acknowledged a different target manifest")
		if persistErr := r.persistHealthRollbackOutcome(ctx, workspaceID, projectID, currentDeploymentID, outcome, "failed"); persistErr != nil {
			return outcome, errors.Join(rollbackErr, fmt.Errorf("persist failed rollback outcome: %w", persistErr))
		}
		return outcome, rollbackErr
	}
	outcome["status"] = result.Status
	outcome["provider"] = result.Provider
	outcome["external_ref"] = result.ExternalRef
	outcome["provider_evidence"] = result.Evidence
	if err := r.persistHealthRollbackOutcome(ctx, workspaceID, projectID, currentDeploymentID, outcome, "rolled_back"); err != nil {
		return outcome, err
	}
	return outcome, nil
}

func (r *Runtime) persistHealthRollbackOutcome(
	ctx context.Context,
	workspaceID, projectID, deploymentID pgtype.UUID,
	outcome map[string]any,
	deploymentStatus string,
) error {
	encoded, err := json.Marshal(outcome)
	if err != nil {
		return fmt.Errorf("encode health rollback outcome: %w", err)
	}
	var statusValue any
	if deploymentStatus != "" {
		statusValue = deploymentStatus
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE autonomous_project_deployment
		SET status=COALESCE($4, status),
		    evidence=COALESCE(evidence,'{}'::jsonb) || jsonb_build_object('health_rollback',$5::jsonb)
		WHERE id=$1 AND workspace_id=$2 AND project_id=$3
	`, deploymentID, workspaceID, projectID, statusValue, encoded)
	if err != nil {
		return fmt.Errorf("persist health rollback outcome: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errors.New("unhealthy deployment disappeared before rollback outcome was persisted")
	}
	return nil
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
		event := events.Event{
			Type:        protocol.EventIssueUpdated,
			WorkspaceID: util.UUIDToString(updated.WorkspaceID),
			ActorType:   "system",
			Payload: map[string]any{
				"issue": map[string]any{
					"id":       util.UUIDToString(updated.ID),
					"status":   updated.Status,
					"revision": updated.Revision,
				},
				"prev_status": issue.Status,
			},
		}
		if err := r.handleIssueEvent(ctx, event); err != nil {
			return fmt.Errorf("start issue workflow for project node %q: %w", node.Key, err)
		}
		if _, exists, err := r.store.FindRun(ctx, softwareDevelopmentWorkflow, util.UUIDToString(updated.WorkspaceID), util.UUIDToString(updated.ID)); err != nil {
			return fmt.Errorf("verify issue workflow for project node %q: %w", node.Key, err)
		} else if !exists {
			return fmt.Errorf("issue workflow for project node %q was not created", node.Key)
		}
		return nil
	}
	handoff := fmt.Sprintf(
		"Autonomous project stage %s (%s). Complete the stage against the issue acceptance criteria. "+
			"Do not create or dispatch follow-up work and do not manage dependency state; the Project OS scheduler owns orchestration. "+
			"Your FINAL response must be exactly one JSON object with keys: summary (string), decisions (string[]), artifacts ({type,ref,description}[]), changed_files (string[]), commit_sha (string), diff (string), tests ({name,status,evidence}[] where status is passed|failed|skipped|not_run), findings ({id,severity,category,description,evidence,blocking}[]), risks (string[]), blockers (string[]). No markdown fence or surrounding prose.",
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
		addFamily("design")
		addFamily("frontend")
	case projectorchestration.NodeReview:
		addFamily("review")
	case projectorchestration.NodeQA:
		addFamily("qa")
		addFamily("review")
	case projectorchestration.NodeSecurity:
		addFamily("security")
	case projectorchestration.NodeRelease:
		addFamily("release")
		addFamily("devops")
	case projectorchestration.NodeDeploy:
		addFamily("devops")
		addFamily("release")
		addFamily("sre")
	case projectorchestration.NodeObserve, projectorchestration.NodeIncident:
		addFamily("sre")
		addFamily("devops")
	case projectorchestration.NodeImplementation, projectorchestration.NodeMigration, projectorchestration.NodeIntegration:
		// When the planner omitted a family, implementation-capable roles are
		// considered below. RequiredCapabilities remains a hard filter.
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
			if !ok || !projectorchestration.CapabilitiesCover(spec.Capabilities, node.RequiredCapabilities) {
				continue
			}
			if projectNodeUsesIssueWorkflow(node.Kind) && !teamprovision.IsImplementationFamily(spec.Family) {
				continue
			}
			var online bool
			if err := r.pool.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1
					FROM agent a
					JOIN agent_runtime ar ON ar.id = a.runtime_id
					WHERE a.id = $1
					  AND a.archived_at IS NULL
					  -- agent.status is a task-presence projection (idle, working,
					  -- blocked, error, or offline); it has no "active" value.
					  -- Dispatch eligibility is determined by the bound runtime's
					  -- current liveness below.
					  AND ar.status = 'online'
					  AND ar.last_seen_at > now() - interval '2 minutes'
				)
			`, id).Scan(&online); err != nil {
				return pgtype.UUID{}, "", "", err
			}
			if !online {
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
			specialization := projectorchestration.SpecializationConfidence(spec.Capabilities, node.RequiredCapabilities)
			latencyPenalty := 0.0
			if performance.TasksCompleted > 0 && performance.TotalRuntimeSeconds > 0 {
				avgSeconds := float64(performance.TotalRuntimeSeconds) / float64(performance.TasksCompleted)
				latencyPenalty = min(avgSeconds/60.0, 20.0)
			}
			score := performance.Score() +
				(specialization * 20.0) -
				float64(active*25) -
				latencyPenalty -
				float64(familyIndex)
			candidates = append(candidates, candidate{id: id, role: spec.Role, family: spec.Family, score: score})
		}
	}
	if len(families) == 0 && projectNodeUsesIssueWorkflow(node.Kind) {
		for _, spec := range team.Plan.Roles {
			if !teamprovision.IsImplementationFamily(spec.Family) ||
				!projectorchestration.CapabilitiesCover(spec.Capabilities, node.RequiredCapabilities) {
				continue
			}
			id, ok := team.Agent(spec.Role)
			if !ok {
				continue
			}
			var online bool
			if err := r.pool.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM agent a
					JOIN agent_runtime ar ON ar.id = a.runtime_id
					WHERE a.id = $1 AND a.archived_at IS NULL
					  AND ar.status = 'online'
					  AND ar.last_seen_at > now() - interval '2 minutes'
				)
			`, id).Scan(&online); err != nil {
				return pgtype.UUID{}, "", "", err
			}
			if !online {
				continue
			}
			performance, err := r.projectStore.AgentPerformance(ctx, team.WorkspaceID, id, spec.Family)
			if err != nil {
				return pgtype.UUID{}, "", "", err
			}
			var active int
			if err := r.pool.QueryRow(ctx, `
				SELECT COUNT(*) FROM agent_task_queue
				WHERE agent_id = $1
				  AND status IN ('queued','dispatched','running','waiting_local_directory','deferred')
			`, id).Scan(&active); err != nil {
				return pgtype.UUID{}, "", "", err
			}
			specialization := projectorchestration.SpecializationConfidence(spec.Capabilities, node.RequiredCapabilities)
			latencyPenalty := 0.0
			if performance.TasksCompleted > 0 && performance.TotalRuntimeSeconds > 0 {
				latencyPenalty = min((float64(performance.TotalRuntimeSeconds)/float64(performance.TasksCompleted))/60.0, 20.0)
			}
			candidates = append(candidates, candidate{
				id: id, role: spec.Role, family: spec.Family,
				score: performance.Score() + specialization*20.0 - float64(active*25) - latencyPenalty,
			})
		}
	}
	if len(candidates) == 0 {
		return pgtype.UUID{}, "", "", fmt.Errorf(
			"%w: node %q kind=%s family=%q capabilities=%v has no online capability-complete candidate",
			projectorchestration.ErrNoEligibleAgent,
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
	var planID, nodeID pgtype.UUID
	var planRevision int64
	err := r.pool.QueryRow(ctx, `
		SELECT p.id, p.revision, p.policy, n.id
		FROM autonomous_project_plan p
		JOIN autonomous_project_plan_node n
		  ON n.plan_id=p.id AND n.workspace_id=p.workspace_id AND n.project_id=p.project_id
		WHERE p.workspace_id = $1 AND p.project_id = $2 AND p.status IN ('active', 'blocked')
		  AND n.node_key=$3
		ORDER BY p.revision DESC
		LIMIT 1
	`, workspaceID, projectID, node.Key).Scan(&planID, &planRevision, &raw, &nodeID)
	if err != nil {
		return err
	}
	var policy projectorchestration.Policy
	if err := json.Unmarshal(raw, &policy); err != nil {
		return fmt.Errorf("decode autonomous project policy: %w", err)
	}
	if node.Kind == projectorchestration.NodeDeploy {
		manifest, err := projectorchestration.LoadPlanReleaseManifest(ctx, r.pool, workspaceID, projectID, planID)
		if err != nil {
			return err
		}
		staged, err := r.hasHealthyStageDeployment(ctx, workspaceID, projectID, planID, nodeID, manifest)
		if err != nil {
			return err
		}
		if manifest.Environment == "staging" && staged {
			manifest, err = projectorchestration.PinPlanReleaseManifestEnvironment(ctx, r.pool, workspaceID, projectID, planID, "production")
			if err != nil {
				return err
			}
		}
		if manifest.Environment == "staging" {
			return nil
		}
		if manifest.Environment != "production" {
			return errors.New("deployment target environment must be staging or production")
		}
		if !staged {
			return nil
		}
		approved, err := r.hasExactProductionApproval(ctx, workspaceID, projectID, planID, planRevision, nodeID, manifest)
		if err != nil {
			return err
		}
		if !approved {
			return errProjectApprovalRequired
		}
		return nil
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
	approved, err := r.hasExactPlanApproval(ctx, workspaceID, projectID, planID, planRevision, nodeID)
	if err != nil {
		return err
	}
	if !approved {
		return errProjectApprovalRequired
	}
	return nil
}

func (r *Runtime) hasExactPlanApproval(
	ctx context.Context,
	workspaceID, projectID, planID pgtype.UUID,
	planRevision int64,
	nodeID pgtype.UUID,
) (bool, error) {
	var approved bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM autonomous_project_escalation
			WHERE workspace_id = $1
			  AND project_id = $2
			  AND plan_id = $3
			  AND node_id = $4
			  AND category = 'approval_required'
			  AND status = 'resolved'
			  AND context ->> 'plan_revision' = $5::bigint::text
			  AND resolution ->> 'decision' = 'approved'
		)
	`, workspaceID, projectID, planID, nodeID, planRevision).Scan(&approved)
	if err != nil {
		return false, err
	}
	return approved, nil
}

func (r *Runtime) hasExactProductionApproval(
	ctx context.Context,
	workspaceID, projectID, planID pgtype.UUID,
	planRevision int64,
	nodeID pgtype.UUID,
	manifest projectorchestration.ReleaseManifest,
) (bool, error) {
	var approved bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM autonomous_project_escalation e
			WHERE e.workspace_id=$1 AND e.project_id=$2
			  AND e.plan_id=$3 AND e.node_id=$4
			  AND e.category='approval_required' AND e.status='resolved'
			  AND e.context->>'approval_scope'='production_release'
			  AND e.context->>'workspace_id'=$1::text
			  AND e.context->>'project_id'=$2::text
			  AND e.context->>'plan_id'=$3::text
			  AND e.context->>'plan_revision'=$5::bigint::text
			  AND e.context->>'node_id'=$4::text
			  AND e.context->>'target_environment'='production'
			  AND e.context->>'manifest_id'=$6
			  AND e.context->>'manifest_digest'=$7
			  AND e.resolution->>'decision'='approved'
			  AND e.resolution->>'approval_scope'='production_release'
			  AND e.resolution->>'workspace_id'=e.context->>'workspace_id'
			  AND e.resolution->>'project_id'=e.context->>'project_id'
			  AND e.resolution->>'plan_id'=e.context->>'plan_id'
			  AND e.resolution->>'plan_revision'=e.context->>'plan_revision'
			  AND e.resolution->>'node_id'=e.context->>'node_id'
			  AND e.resolution->>'target_environment'=e.context->>'target_environment'
			  AND e.resolution->>'manifest_id'=e.context->>'manifest_id'
			  AND e.resolution->>'manifest_digest'=e.context->>'manifest_digest'
		)
	`, workspaceID, projectID, planID, nodeID, planRevision, manifest.ManifestID, manifest.ManifestDigest).Scan(&approved)
	if err != nil {
		return false, fmt.Errorf("verify exact production manifest approval: %w", err)
	}
	return approved, nil
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
	} else if errors.Is(cause, projectorchestration.ErrNoEligibleAgent) {
		category = "no_eligible_agent"
		summary = "No eligible online agent can execute: " + node.Title
	}
	contextFields := map[string]any{
		"node_key":              node.Key,
		"kind":                  node.Kind,
		"required_role_family":  node.RequiredRoleFamily,
		"required_capabilities": node.RequiredCapabilities,
		"error":                 cause.Error(),
		"suggested_team_delta": map[string]any{
			"mode":                  "add_only",
			"required_role_family":  node.RequiredRoleFamily,
			"required_capabilities": node.RequiredCapabilities,
		},
	}
	var planID, planNodeID pgtype.UUID
	if category == "approval_required" {
		var revision int64
		if err := r.pool.QueryRow(ctx, `
			SELECT p.id, p.revision, n.id
			FROM autonomous_project_plan p
			JOIN autonomous_project_plan_node n ON n.plan_id=p.id AND n.workspace_id=p.workspace_id AND n.project_id=p.project_id
			WHERE p.workspace_id=$1 AND p.project_id=$2 AND p.status IN ('active','blocked') AND n.node_key=$3
			ORDER BY p.revision DESC LIMIT 1
		`, workspaceID, projectID, node.Key).Scan(&planID, &revision, &planNodeID); err != nil {
			return fmt.Errorf("load project approval plan scope: %w", err)
		}
		contextFields["plan_revision"] = revision
		if node.Kind == projectorchestration.NodeDeploy {
			manifest, err := projectorchestration.LoadPlanReleaseManifest(ctx, r.pool, workspaceID, projectID, planID)
			if err != nil {
				return fmt.Errorf("load production approval manifest: %w", err)
			}
			if manifest.Environment != "production" {
				return errors.New("production approval escalation requires a production manifest")
			}
			contextFields["approval_scope"] = "production_release"
			contextFields["workspace_id"] = util.UUIDToString(workspaceID)
			contextFields["project_id"] = util.UUIDToString(projectID)
			contextFields["plan_id"] = util.UUIDToString(planID)
			contextFields["node_id"] = util.UUIDToString(planNodeID)
			contextFields["target_environment"] = "production"
			contextFields["manifest_id"] = manifest.ManifestID
			contextFields["manifest_digest"] = manifest.ManifestDigest
		}
	}
	contextJSON, err := json.Marshal(contextFields)
	if err != nil {
		return fmt.Errorf("encode project escalation context: %w", err)
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO autonomous_project_escalation (
			workspace_id, project_id, plan_id, node_id, category, severity, summary, context
		)
		SELECT $1, $2, $3, $4, $9,
		       CASE WHEN $5 IN ('high', 'critical') THEN $5 ELSE 'medium' END,
		       $6, $7
		WHERE NOT EXISTS (
			SELECT 1
			FROM autonomous_project_escalation
			WHERE workspace_id = $1
			  AND project_id = $2
			  AND status IN ('open', 'acknowledged')
			  AND (
				($9='approval_required' AND $7::jsonb ? 'approval_scope'
				 AND context->>'approval_scope'=$7::jsonb->>'approval_scope'
				 AND context->>'plan_revision'=$7::jsonb->>'plan_revision'
				 AND context->>'node_id'=$7::jsonb->>'node_id'
				 AND context->>'manifest_digest'=$7::jsonb->>'manifest_digest')
				OR (NOT ($7::jsonb ? 'approval_scope') AND context->>'node_key'=$8
				 AND context->>'plan_revision'=$7::jsonb->>'plan_revision')
			  )
		)
	`, workspaceID, projectID, planID, planNodeID, string(node.Risk),
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
	var nodeKey, nodeKind string
	err := r.pool.QueryRow(ctx, `
		SELECT node_key, kind
		FROM autonomous_project_plan_node
		WHERE workspace_id = $1
		  AND project_id = $2
		  AND materialized_issue_id = $3
		ORDER BY updated_at DESC
		LIMIT 1
	`, issue.WorkspaceID, issue.ProjectID, issue.ID).Scan(&nodeKey, &nodeKind)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	category := usageCategoryForNodeKind(nodeKind)
	var reviewerTask bool
	if err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM autonomous_workflow_run
			WHERE workspace_id = $1
			  AND project_id = $2
			  AND issue_id = $3
			  AND reviewer_agent_id = $4
		)
	`, issue.WorkspaceID, issue.ProjectID, issue.ID, task.AgentID).Scan(&reviewerTask); err != nil {
		return false, err
	}
	if reviewerTask {
		category = projectorchestration.UsageReview
	}

	usage, err := loadTaskUsageSnapshot(ctx, r.pool, task.ID)
	if err != nil {
		return false, err
	}
	brainContextTokens, brainContextEstimated, err := r.brainContextUsageForTask(ctx, task.ID)
	if err != nil {
		return false, err
	}
	err = r.projectStore.AccountTaskUsageDetailed(
		ctx,
		issue.WorkspaceID,
		issue.ProjectID,
		task.ID,
		projectorchestration.UsageAttribution{
			Category:              category,
			InputTokens:           usage.InputTokens,
			OutputTokens:          usage.OutputTokens,
			CacheReadTokens:       usage.CacheReadTokens,
			CacheWriteTokens:      usage.CacheWriteTokens,
			RuntimeSeconds:        usage.RuntimeSeconds,
			CostUsdTicks:          usage.CostUsdTicks,
			CostComplete:          usage.CostComplete,
			BrainContextTokens:    brainContextTokens,
			BrainContextEstimated: brainContextEstimated,
		},
	)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, projectorchestration.ErrBudgetExceeded) {
		return false, err
	}

	tokens := usage.TotalTokens()
	costMicrounits := usage.CostMicrounits()
	runtimeSeconds := usage.RuntimeSeconds
	reason := "project token/runtime/cost budget exceeded after task " + util.UUIDToString(task.ID)
	_, _ = r.pool.Exec(ctx, `
		UPDATE autonomous_project_plan_node
		SET status = 'blocked',
		    blocked_category = 'budget',
		    blocked_reason = $4,
		    updated_at = now()
		WHERE id = (
			SELECT n.id
			FROM autonomous_project_plan_node n
			JOIN autonomous_project_plan p ON p.id = n.plan_id
			WHERE n.workspace_id = $1
			  AND n.project_id = $2
			  AND n.materialized_issue_id = $3
			  AND n.status NOT IN ('completed', 'cancelled')
			  AND p.status IN ('active', 'blocked')
			ORDER BY p.revision DESC, n.updated_at DESC
			LIMIT 1
		)
	`, issue.WorkspaceID, issue.ProjectID, issue.ID, reason)
	_, _ = r.pool.Exec(ctx, `
		UPDATE autonomous_project_plan
		SET status = 'blocked', updated_at = now()
		WHERE workspace_id = $1 AND project_id = $2 AND status = 'active'
	`, issue.WorkspaceID, issue.ProjectID)
	contextJSON, _ := json.Marshal(map[string]any{
		"node_key":        nodeKey,
		"task_id":         util.UUIDToString(task.ID),
		"usage_category":  category,
		"tokens":          tokens,
		"runtime_seconds": runtimeSeconds,
		"cost_microunits": costMicrounits,
	})
	_, _ = r.pool.Exec(ctx, `
		INSERT INTO autonomous_project_escalation (
			workspace_id, project_id, category, severity, summary, context
		)
		VALUES ($1, $2, 'budget_exceeded', 'high', $3, $4)
	`, issue.WorkspaceID, issue.ProjectID, "Project usage budget exceeded: "+nodeKey, contextJSON)

	if r.store != nil && r.engine != nil {
		run, exists, findErr := r.store.FindRun(
			ctx, softwareDevelopmentWorkflow, util.UUIDToString(issue.WorkspaceID), util.UUIDToString(issue.ID),
		)
		if findErr != nil {
			return true, findErr
		}
		if exists {
			failureEvent := ""
			switch run.State {
			case issuestatus.InProgress:
				failureEvent = "implementation.failed"
			case issuestatus.InReview:
				failureEvent = "review.failed"
			}
			if failureEvent != "" {
				if _, handleErr := r.engine.Handle(softwareDevelopmentWorkflow, workflow.Event{
					ID:                "budget-exceeded:" + util.UUIDToString(task.ID),
					Type:              failureEvent,
					WorkspaceID:       util.UUIDToString(issue.WorkspaceID),
					ProjectID:         util.UUIDToString(issue.ProjectID),
					IssueID:           util.UUIDToString(issue.ID),
					AccountableUserID: util.UUIDToString(task.AccountableUserID),
					Payload:           map[string]any{"task_id": util.UUIDToString(task.ID), "budget_exceeded": true},
				}); handleErr != nil && !errors.Is(handleErr, workflow.ErrRevisionConflict) {
					return true, handleErr
				}
			}
		}
	}
	_, _ = r.taskSvc.SetIssueStatusForWorkflow(ctx, issue.ID, issuestatus.Blocked)
	return true, nil
}

func (r *Runtime) recordProjectTaskArtifact(ctx context.Context, task db.AgentTaskQueue, issue db.Issue) (bool, error) {
	var nodeID, planID pgtype.UUID
	var kind, nodeKey string
	var specRevision int64
	err := r.pool.QueryRow(ctx, `
		SELECT n.id, n.plan_id, n.node_key, n.kind, n.spec_revision
		FROM autonomous_project_plan_node n
		JOIN autonomous_project_plan p ON p.id = n.plan_id
		WHERE n.workspace_id = $1
		  AND n.materialized_issue_id = $2
		  AND p.status IN ('active', 'blocked')
		ORDER BY p.revision DESC, n.updated_at DESC
		LIMIT 1
	`, issue.WorkspaceID, issue.ID).Scan(&nodeID, &planID, &nodeKey, &kind, &specRevision)
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
	artifactType := projectArtifactType(projectorchestration.NodeKind(kind))
	var reviewerTask bool
	_ = r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM autonomous_workflow_run
			WHERE workspace_id=$1 AND project_id=$2 AND issue_id=$3
			  AND reviewer_agent_id=$4
		)
	`, issue.WorkspaceID, issue.ProjectID, issue.ID, task.AgentID).Scan(&reviewerTask)
	if reviewerTask {
		artifactType = "review"
	}

	valid := len(task.Result) > 0
	validationError := ""
	var changeSet *projectorchestration.TaskCommit
	var handoff implementationHandoffOutput
	changeSetMetadataError := ""
	if !valid {
		validationError = "task result is empty"
	} else if reviewerTask || artifactType == "review" {
		verdict, parseErr := parseReviewVerdict(task.Result)
		if parseErr != nil {
			valid = false
			validationError = parseErr.Error()
		} else {
			goalContext, contextErr := r.canonicalGoalReviewContextForNode(
				ctx, issue.WorkspaceID, issue.ProjectID, util.UUIDToString(nodeID),
			)
			if contextErr != nil {
				valid = false
				validationError = contextErr.Error()
			} else if goalErr := validateGoalDriftReview(verdict, goalContext.requirementIDs()); goalErr != nil {
				valid = false
				validationError = goalErr.Error()
			}
		}
	} else if projectorchestration.NodeKind(kind) == projectorchestration.NodeQA {
		if !structuredQAHandoff(task.Result) {
			valid = false
			validationError = "qa_handoff must contain a passing verdict and completed acceptance evidence"
		}
	} else if projectNodeUsesIssueWorkflow(projectorchestration.NodeKind(kind)) {
		if handoff, err = parseImplementationHandoff(task.Result); err != nil {
			valid = false
			validationError = err.Error()
		}
	}
	if valid && !reviewerTask && projectNodeUsesIssueWorkflow(projectorchestration.NodeKind(kind)) {
		changeSet, err = taskCommitFromResult(task.Result, issue.WorkspaceID, issue.ProjectID, planID, nodeID, issue.ID, task.ID, task.AgentID)
		if err != nil {
			commitErr := err
			var reused bool
			changeSet, reused, err = r.reusePreviousTaskCommitForNoop(ctx, task, issue, planID, nodeID, handoff)
			if err != nil {
				changeSetMetadataError = err.Error()
			} else if !reused {
				changeSetMetadataError = commitErr.Error()
			}
		}
		if changeSetMetadataError != "" {
			valid = false
			validationError = changeSetMetadataError
		}
	}

	artifactPayload := map[string]any{
		"task_id":       util.UUIDToString(task.ID),
		"agent_id":      util.UUIDToString(task.AgentID),
		"issue_id":      util.UUIDToString(issue.ID),
		"spec_revision": specRevision,
		"result":        taskResult,
	}
	content, _ := json.Marshal(artifactPayload)
	var synchronousDecisions []projectorchestration.SynchronousBrainDecision
	if valid && !reviewerTask {
		synchronousDecisions, err = prepareSynchronousCompletionDecisions(
			ctx, task, issue, projectorchestration.NodeKind(kind), nodeKey,
		)
		if err != nil {
			return false, err
		}
	}
	brainType := ""
	brainEmbedding := ""
	if valid {
		brainType = projectBrainEntryType(projectorchestration.NodeKind(kind))
		if brainType != "" && brainType != "architecture_decision" {
			// The generated artifact UUID is metadata, not semantic memory input.
			// Prepare this before the artifact transaction so provider calls never
			// hold a database transaction open.
			brainEmbeddingContent, _ := json.Marshal(map[string]any{
				"artifact_type": artifactType,
				"task_id":       util.UUIDToString(task.ID),
				"issue_id":      util.UUIDToString(issue.ID),
				"result":        taskResult,
			})
			brainEmbedding, err = projectorchestration.PrepareBrainMemoryEmbedding(ctx, issue.Title, brainEmbeddingContent)
			if err != nil {
				return false, fmt.Errorf("prepare project task artifact embedding: %w", err)
			}
		}
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	var artifactID pgtype.UUID
	status := "active"
	if !valid {
		status = "invalid"
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO autonomous_project_artifact (
			workspace_id, project_id, plan_id, node_id, artifact_type,
			name, content, producer_agent_id, status, valid, validation_error,
			artifact_revision
		)
		SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,''),$12
		WHERE NOT EXISTS (
			SELECT 1 FROM autonomous_project_artifact
			WHERE workspace_id=$1 AND project_id=$2 AND node_id=$4
			  AND content ->> 'task_id' = $13
		)
		RETURNING id
	`, issue.WorkspaceID, issue.ProjectID, planID, nodeID, artifactType,
		"Task result: "+issue.Title, content, task.AgentID, status, valid,
		validationError, specRevision, util.UUIDToString(task.ID)).Scan(&artifactID)
	if errors.Is(err, pgx.ErrNoRows) {
		if len(synchronousDecisions) > 0 {
			if _, err := projectorchestration.PersistSynchronousBrainDecisions(
				ctx, tx, issue.WorkspaceID, issue.ProjectID, planID, nodeID, task.ID, task.AgentID,
				synchronousDecisions,
			); err != nil {
				return false, err
			}
		}
		if changeSet != nil {
			if err := projectorchestration.RecordTaskCommit(ctx, tx, *changeSet); err != nil {
				return false, err
			}
			if err := tx.Commit(ctx); err != nil {
				return false, err
			}
			return r.finalizeProjectChangeSetQuality(ctx, task, issue, artifactType, artifactPayload, true, changeSet.TaskID)
		}
		if changeSetMetadataError != "" {
			if err := tx.Commit(ctx); err != nil {
				return false, err
			}
			return r.blockProjectChangeSetEvidence(ctx, task, issue, planID, nodeID, nodeKey, validationError)
		}
		if projectorchestration.NodeKind(kind) == projectorchestration.NodeQA {
			var existingID pgtype.UUID
			var existingStatus string
			var existingValid bool
			err := tx.QueryRow(ctx, `
				SELECT id, status, valid
				FROM autonomous_project_artifact
				WHERE workspace_id=$1 AND project_id=$2 AND plan_id=$3 AND node_id=$4
				  AND artifact_type='qa_handoff' AND content ->> 'task_id'=$5
				ORDER BY created_at DESC
				LIMIT 1
			`, issue.WorkspaceID, issue.ProjectID, planID, nodeID, util.UUIDToString(task.ID)).Scan(&existingID, &existingStatus, &existingValid)
			if errors.Is(err, pgx.ErrNoRows) {
				return false, tx.Commit(ctx)
			}
			if err != nil {
				return false, fmt.Errorf("load existing QA handoff artifact: %w", err)
			}
			if err := tx.Commit(ctx); err != nil {
				return false, err
			}
			artifactPayload["artifact_id"] = util.UUIDToString(existingID)
			artifactPayload["valid"] = existingValid && existingStatus == "active"
			return r.finalizeProjectChangeSetQuality(ctx, task, issue, artifactType, artifactPayload, false, task.ID)
		}
		return false, tx.Commit(ctx)
	}
	if err != nil {
		return false, fmt.Errorf("record project task artifact: %w", err)
	}
	if changeSet != nil {
		if err := projectorchestration.RecordTaskCommit(ctx, tx, *changeSet); err != nil {
			return false, err
		}
	}
	if len(synchronousDecisions) > 0 {
		if _, err := projectorchestration.PersistSynchronousBrainDecisions(
			ctx, tx, issue.WorkspaceID, issue.ProjectID, planID, nodeID, task.ID, task.AgentID,
			synchronousDecisions,
		); err != nil {
			return false, err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE autonomous_project_artifact
		SET status='superseded', superseded_by=$5
		WHERE workspace_id=$1 AND project_id=$2 AND node_id=$3
		  AND artifact_type=$4 AND id<>$5 AND status='active'
	`, issue.WorkspaceID, issue.ProjectID, nodeID, artifactType, artifactID); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}

	if !valid {
		if changeSetMetadataError != "" {
			return r.blockProjectChangeSetEvidence(ctx, task, issue, planID, nodeID, nodeKey, validationError)
		}
		if !projectNodeUsesIssueWorkflow(projectorchestration.NodeKind(kind)) || projectorchestration.NodeKind(kind) == projectorchestration.NodeQA {
			if r.projectStore != nil {
				_ = r.projectStore.SetNodeBlocked(ctx, issue.WorkspaceID, issue.ProjectID, nodeKey, "quality_policy", validationError)
			}
			_, _ = r.taskSvc.SetIssueStatusForWorkflow(ctx, issue.ID, issuestatus.Blocked)
			contextJSON, _ := json.Marshal(map[string]any{
				"node_id": util.UUIDToString(nodeID), "issue_id": util.UUIDToString(issue.ID),
				"artifact_type": artifactType, "error": validationError,
			})
			_, _ = r.pool.Exec(ctx, `
				INSERT INTO autonomous_project_escalation (
					workspace_id, project_id, plan_id, node_id, category, severity, summary, context
				) VALUES ($1,$2,$3,$4,'contract_violation','high',$5,$6)
			`, issue.WorkspaceID, issue.ProjectID, planID, nodeID,
				"Artifact contract validation failed: "+issue.Title, contextJSON)
			return true, nil
		}
		// Issue-workflow structured contracts are handled by the workflow
		// completion path; keep the invalid artifact as durable evidence.
		return false, nil
	}

	if brainType != "" && brainType != "architecture_decision" {
		brainContent, _ := json.Marshal(map[string]any{
			"artifact_type": artifactType, "artifact_id": util.UUIDToString(artifactID),
			"task_id": util.UUIDToString(task.ID), "issue_id": util.UUIDToString(issue.ID),
			"result": taskResult,
		})
		if _, err := r.pool.Exec(ctx, `
			INSERT INTO autonomous_project_brain_entry (
				workspace_id, project_id, plan_id, node_id, entry_type,
				subject, content, source_type, source_id, confidence,
				created_by_type, created_by_id, embedding
			)
			SELECT $1,$2,$3,$4,$5,$6,$7,'artifact',$8,0.9,'agent',$9,NULLIF($10,'')::vector
			WHERE NOT EXISTS (
				SELECT 1 FROM autonomous_project_brain_entry
				WHERE workspace_id=$1 AND project_id=$2
				  AND source_type='artifact' AND source_id=$8 AND entry_type=$5
			)
		`, issue.WorkspaceID, issue.ProjectID, planID, nodeID, brainType,
			issue.Title, brainContent, util.UUIDToString(artifactID), task.AgentID, brainEmbedding); err != nil {
			return false, fmt.Errorf("record project brain artifact knowledge: %w", err)
		}
	}
	artifactPayload["artifact_id"] = util.UUIDToString(artifactID)
	artifactPayload["valid"] = valid

	changeSetTaskID := task.ID
	if changeSet != nil {
		changeSetTaskID = changeSet.TaskID
	}
	return r.finalizeProjectChangeSetQuality(ctx, task, issue, artifactType, artifactPayload, changeSet != nil, changeSetTaskID)
}

func (r *Runtime) finalizeProjectChangeSetQuality(ctx context.Context, task db.AgentTaskQueue, issue db.Issue, artifactType string, artifact map[string]any, hasChangeSet bool, changeSetTaskID pgtype.UUID) (bool, error) {
	blocked, err := r.runTaskQualityIfRequired(ctx, task, issue, artifactType, artifact)
	if err != nil {
		return blocked, err
	}
	if hasChangeSet && !blocked {
		q, err := r.loadNodeQualityByIssue(ctx, issue.WorkspaceID, issue.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("load project quality evidence for change set: %w", err)
		}
		ready, err := r.hasCurrentDeterministicQualityEvidence(ctx, q, util.UUIDToString(task.ID))
		if err != nil {
			return false, err
		}
		if !ready {
			return false, nil
		}
		if _, err := r.pool.Exec(ctx, `
			UPDATE autonomous_project_change_set
			SET merge_status='ready', ready_at=now(), updated_at=now()
			WHERE source_task_id=$1 AND merge_status='changes_requested'
		`, changeSetTaskID); err != nil {
			return false, fmt.Errorf("mark project change set ready after quality gates: %w", err)
		}
	}
	return blocked, nil
}

func (r *Runtime) reusePreviousTaskCommitForNoop(
	ctx context.Context,
	task db.AgentTaskQueue,
	issue db.Issue,
	planID, nodeID pgtype.UUID,
	handoff implementationHandoffOutput,
) (*projectorchestration.TaskCommit, bool, error) {
	var current struct {
		BranchName           string   `json:"branch_name"`
		WorktreeBaseSHA      string   `json:"worktree_base_sha"`
		WorktreeCommitSHA    string   `json:"worktree_commit_sha"`
		WorktreeChangedFiles []string `json:"worktree_changed_files"`
		WorktreePath         string   `json:"worktree_path"`
		WorktreeRepoPath     string   `json:"worktree_repo_path"`
	}
	if json.Unmarshal(task.Result, &current) != nil || !task.CreatedAt.Valid ||
		len(current.WorktreeChangedFiles) != 0 || strings.TrimSpace(current.BranchName) == "" ||
		strings.TrimSpace(current.WorktreeBaseSHA) == "" || current.WorktreeBaseSHA != current.WorktreeCommitSHA ||
		strings.TrimSpace(current.WorktreePath) == "" || strings.TrimSpace(current.WorktreeRepoPath) == "" {
		return nil, false, nil
	}

	var previousTaskID pgtype.UUID
	var previousResult []byte
	err := r.pool.QueryRow(ctx, `
		SELECT id, result
		FROM agent_task_queue
		WHERE issue_id=$1 AND agent_id=$2 AND status='completed' AND created_at<$3
		  AND result->>'worktree_commit_sha'=$4
		  AND CASE WHEN jsonb_typeof(result->'worktree_changed_files')='array'
		           THEN jsonb_array_length(result->'worktree_changed_files')>0 ELSE FALSE END
		ORDER BY created_at DESC
		LIMIT 1
	`, issue.ID, task.AgentID, task.CreatedAt, current.WorktreeBaseSHA).Scan(&previousTaskID, &previousResult)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	previous, err := taskCommitFromResult(
		previousResult, issue.WorkspaceID, issue.ProjectID, planID, nodeID, issue.ID, previousTaskID, task.AgentID,
	)
	if err != nil || previous.BaseSHA == previous.CommitSHA ||
		previous.CommitSHA != current.WorktreeBaseSHA || previous.BranchName != current.BranchName ||
		previous.RepoPath != current.WorktreeRepoPath ||
		!sameFileSet(handoff.ChangedFiles, previous.ChangedFiles) {
		return nil, false, nil
	}
	return previous, true, nil
}

func sameFileSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	leftSorted := append([]string(nil), left...)
	rightSorted := append([]string(nil), right...)
	sort.Strings(leftSorted)
	sort.Strings(rightSorted)
	for i := range leftSorted {
		if leftSorted[i] != rightSorted[i] {
			return false
		}
	}
	return true
}

func (r *Runtime) blockProjectChangeSetEvidence(ctx context.Context, task db.AgentTaskQueue, issue db.Issue, planID, nodeID pgtype.UUID, nodeKey, message string) (bool, error) {
	if r.store != nil {
		run, exists, err := r.store.FindRun(
			ctx, softwareDevelopmentWorkflow, util.UUIDToString(issue.WorkspaceID), util.UUIDToString(issue.ID),
		)
		if err != nil {
			return true, fmt.Errorf("load workflow before blocking incomplete worktree evidence: %w", err)
		}
		if exists {
			switch {
			case run.State == issuestatus.InProgress && run.OwnerAgentID == util.UUIDToString(task.AgentID):
				if r.engine == nil {
					return true, errors.New("autonomous workflow engine is unavailable for invalid implementation evidence")
				}
				_, err := r.engine.Handle(softwareDevelopmentWorkflow, workflow.Event{
					ID:                "implementation-evidence-failed:" + util.UUIDToString(task.ID),
					Type:              "implementation.failed",
					WorkspaceID:       util.UUIDToString(issue.WorkspaceID),
					ProjectID:         util.UUIDToString(issue.ProjectID),
					IssueID:           util.UUIDToString(issue.ID),
					AccountableUserID: util.UUIDToString(task.AccountableUserID),
					Payload:           map[string]any{"task_id": util.UUIDToString(task.ID), "contract_error": message},
				})
				if err != nil {
					return true, fmt.Errorf("block workflow after incomplete worktree evidence: %w", err)
				}
			case run.State == issuestatus.Blocked:
			default:
				return true, fmt.Errorf("cannot block incomplete worktree evidence for workflow in %q owned by another task", run.State)
			}
		}
	}
	if r.projectStore != nil {
		if err := r.projectStore.SetNodeBlocked(ctx, issue.WorkspaceID, issue.ProjectID, nodeKey, "technical_failure", message); err != nil {
			return true, fmt.Errorf("block project node after incomplete worktree evidence: %w", err)
		}
	}
	if _, err := r.taskSvc.SetIssueStatusForWorkflow(ctx, issue.ID, issuestatus.Blocked); err != nil {
		return true, fmt.Errorf("block issue after incomplete worktree evidence: %w", err)
	}
	contextJSON, _ := json.Marshal(map[string]any{
		"node_key": nodeKey,
		"node_id":  util.UUIDToString(nodeID), "issue_id": util.UUIDToString(issue.ID),
		"task_id": util.UUIDToString(task.ID), "error": message,
	})
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO autonomous_project_escalation (
			workspace_id, project_id, plan_id, node_id, category, severity, summary, context
		)
		SELECT $1,$2,$3,$4,'technical_failure','high',$5,$6
		WHERE NOT EXISTS (
			SELECT 1 FROM autonomous_project_escalation
			WHERE workspace_id=$1 AND project_id=$2 AND category='technical_failure'
			  AND context ->> 'task_id' = $7
		)
	`, issue.WorkspaceID, issue.ProjectID, planID, nodeID,
		"Daemon commit evidence is incomplete: "+issue.Title, contextJSON, util.UUIDToString(task.ID)); err != nil {
		return true, fmt.Errorf("record incomplete project worktree evidence: %w", err)
	}
	return true, nil
}

func taskCommitFromResult(result []byte, workspaceID, projectID, planID, nodeID, issueID, taskID, agentID pgtype.UUID) (*projectorchestration.TaskCommit, error) {
	var evidence struct {
		BranchName           string   `json:"branch_name"`
		WorktreeBaseSHA      string   `json:"worktree_base_sha"`
		WorktreeCommitSHA    string   `json:"worktree_commit_sha"`
		WorktreeChangedFiles []string `json:"worktree_changed_files"`
		WorktreePath         string   `json:"worktree_path"`
		WorktreeRepoPath     string   `json:"worktree_repo_path"`
	}
	if err := json.Unmarshal(result, &evidence); err != nil {
		return nil, fmt.Errorf("decode daemon worktree evidence: %w", err)
	}
	hasWorktreeEvidence := evidence.BranchName != "" || evidence.WorktreeBaseSHA != "" ||
		evidence.WorktreeCommitSHA != "" || len(evidence.WorktreeChangedFiles) > 0 ||
		evidence.WorktreePath != "" || evidence.WorktreeRepoPath != ""
	if !hasWorktreeEvidence {
		return nil, fmt.Errorf("implementation completion did not include daemon-observed worktree commit evidence")
	}
	if strings.TrimSpace(evidence.BranchName) == "" || strings.TrimSpace(evidence.WorktreeBaseSHA) == "" ||
		strings.TrimSpace(evidence.WorktreeCommitSHA) == "" || strings.TrimSpace(evidence.WorktreePath) == "" ||
		strings.TrimSpace(evidence.WorktreeRepoPath) == "" || len(evidence.WorktreeChangedFiles) == 0 {
		return nil, fmt.Errorf("daemon worktree evidence is incomplete; commit, branch, paths, and changed files are required")
	}
	return &projectorchestration.TaskCommit{
		WorkspaceID: workspaceID, ProjectID: projectID, PlanID: planID, NodeID: nodeID,
		IssueID: issueID, TaskID: taskID, AgentID: agentID, BaseSHA: evidence.WorktreeBaseSHA,
		CommitSHA: evidence.WorktreeCommitSHA, BranchName: evidence.BranchName,
		Worktree: evidence.WorktreePath, RepoPath: evidence.WorktreeRepoPath,
		ChangedFiles: evidence.WorktreeChangedFiles,
	}, nil
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
		return "qa_handoff"
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
			"task_id":  util.UUIDToString(task.ID),
			"error":    reason,
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
			WHERE id = (
				SELECT n.id
				FROM autonomous_project_plan_node n
				JOIN autonomous_project_plan p ON p.id = n.plan_id
				WHERE n.workspace_id = $1
				  AND n.materialized_issue_id = $2
				  AND p.status IN ('active', 'blocked')
				ORDER BY p.revision DESC, n.updated_at DESC
				LIMIT 1
			)
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
	var kind, status string
	err := r.pool.QueryRow(ctx, `
		SELECT kind, status
		FROM autonomous_project_plan_node
		WHERE workspace_id = $1
		  AND materialized_issue_id = $2
		  AND status IN ('running', 'verification', 'ready', 'blocked')
		ORDER BY updated_at DESC
		LIMIT 1
	`, issue.WorkspaceID, issue.ID).Scan(&kind, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if projectNodeUsesIssueWorkflow(projectorchestration.NodeKind(kind)) {
		return false, nil
	}
	if status != "running" && status != "verification" {
		if !task.RetryOfTaskID.Valid && !task.RerunOfTaskID.Valid {
			return false, nil
		}
		var latest bool
		if err := r.pool.QueryRow(ctx, `
			SELECT NOT EXISTS (
				SELECT 1
				FROM agent_task_queue newer
				WHERE newer.issue_id = $1
				  AND newer.id <> $2
				  AND newer.created_at > (
					SELECT created_at FROM agent_task_queue WHERE id = $2
				  )
			)
		`, issue.ID, task.ID).Scan(&latest); err != nil {
			return true, err
		}
		if !latest {
			return true, nil
		}
		resumed, err := r.projectStore.ResumeNodeForWorkflowRetry(ctx, issue.WorkspaceID, issue.ID)
		if err != nil {
			return true, err
		}
		if !resumed {
			return true, nil
		}
	}

	if _, err := r.taskSvc.SetIssueStatusForWorkflow(ctx, issue.ID, issuestatus.Done); err != nil {
		return true, err
	}
	if err := r.projectStore.CompleteNodeByIssue(ctx, issue.WorkspaceID, issue.ID); err != nil {
		return true, err
	}
	return true, nil
}
