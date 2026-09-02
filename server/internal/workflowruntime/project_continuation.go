package workflowruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/teamprovision"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

const autonomousCoordinatorSystemKeyPrefix = "autonomous_project_coordinator:"

const autonomousCoordinatorInstructions = `You are an internal Chief-of-Staff continuation agent for Multica autonomous software delivery.

Your job is orchestration only. Never implement product code, edit repository files, run a development server, or change the generated team configuration.

After a human confirms the Technology Team runtimes and skills, inspect the project and its existing issues, then create only the missing executable backlog needed to deliver the requested MVP. Reuse existing issues instead of duplicating them. Assign each issue to the most appropriate generated specialist and start only work that is ready to run.

Use the Multica CLI for project and issue operations. Do not create, archive, or reconfigure agents, squads, runtimes, or skills. The backend owns team composition and workflow state.`

func (r *Runtime) processProjectContinuations(ctx context.Context) error {
	if r == nil || r.team == nil || r.taskSvc == nil {
		return nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT project_id, workspace_id, confirmed_by, continuation_session_id,
		       continuation_task_id
		FROM autonomous_project_team_draft
		WHERE status = 'applied'
		  AND continuation_completed_at IS NULL
		ORDER BY updated_at ASC
		LIMIT 10
	`)
	if err != nil {
		return fmt.Errorf("query autonomous project continuations: %w", err)
	}
	type continuation struct {
		projectID pgtype.UUID
		workspaceID pgtype.UUID
		confirmedBy pgtype.UUID
		sessionID pgtype.UUID
		taskID pgtype.UUID
	}
	items := make([]continuation, 0, 10)
	for rows.Next() {
		var item continuation
		if err := rows.Scan(&item.projectID, &item.workspaceID, &item.confirmedBy, &item.sessionID, &item.taskID); err != nil {
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
		if item.taskID.Valid {
			task, err := r.taskSvc.Queries.GetAgentTask(ctx, item.taskID)
			if errors.Is(err, pgx.ErrNoRows) {
				_, _ = r.pool.Exec(ctx, `
					UPDATE autonomous_project_team_draft
					SET continuation_task_id = NULL, updated_at = now()
					WHERE workspace_id = $1 AND project_id = $2
				`, item.workspaceID, item.projectID)
				continue
			}
			if err != nil {
				return fmt.Errorf("read autonomous continuation task: %w", err)
			}
			switch task.Status {
			case "completed":
				_, err = r.pool.Exec(ctx, `
					UPDATE autonomous_project_team_draft
					SET continuation_completed_at = now(), updated_at = now()
					WHERE workspace_id = $1 AND project_id = $2
				`, item.workspaceID, item.projectID)
				if err != nil {
					return fmt.Errorf("mark autonomous project continuation completed: %w", err)
				}
				continue
			case "failed", "cancelled", "canceled":
				message := "Autonomous project backlog creation failed."
				if task.Error.Valid && strings.TrimSpace(task.Error.String) != "" {
					message += " " + strings.TrimSpace(task.Error.String)
				}
				if len(message) > 2000 {
					message = message[:2000]
				}
				_, _ = r.pool.Exec(ctx, `
					UPDATE autonomous_project_team_draft
					SET continuation_completed_at = now(), updated_at = now()
					WHERE workspace_id = $1 AND project_id = $2
				`, item.workspaceID, item.projectID)
				_, _ = r.pool.Exec(ctx, `
					INSERT INTO autonomous_project_control (project_id, workspace_id, last_error, updated_at)
					VALUES ($1, $2, $3, now())
					ON CONFLICT (project_id) DO UPDATE
					SET last_error = EXCLUDED.last_error, updated_at = now()
					WHERE autonomous_project_control.workspace_id = EXCLUDED.workspace_id
				`, item.projectID, item.workspaceID, message)
				continue
			default:
				continue
			}
		}

		if err := r.enqueueProjectContinuation(ctx, item.workspaceID, item.projectID, item.confirmedBy, item.sessionID); err != nil {
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
			`, item.projectID, item.workspaceID, message)
		}
	}
	return nil
}

func (r *Runtime) enqueueProjectContinuation(ctx context.Context, workspaceID, projectID, confirmedBy, knownSessionID pgtype.UUID) error {
	team, ok, err := r.team.FindProject(ctx, workspaceID, projectID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("autonomous project team is not available for backlog continuation")
	}
	project, err := r.taskSvc.Queries.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{
		ID: projectID, WorkspaceID: workspaceID,
	})
	if err != nil {
		return fmt.Errorf("load project for autonomous backlog continuation: %w", err)
	}
	carrier, err := r.ensureProjectCoordinator(ctx, workspaceID, projectID)
	if err != nil {
		return err
	}

	sessionID := knownSessionID
	if !sessionID.Valid {
		session, err := r.taskSvc.Queries.CreateChatSession(ctx, db.CreateChatSessionParams{
			ID: dbid.NewV7(),
			WorkspaceID: workspaceID,
			AgentID: carrier.ID,
			CreatorID: confirmedBy,
			Title: "Autonomous Project Backlog",
			ProjectID: projectID,
		})
		if err != nil {
			return fmt.Errorf("create hidden autonomous project continuation session: %w", err)
		}
		sessionID = session.ID
		if _, err := r.pool.Exec(ctx, `
			UPDATE autonomous_project_team_draft
			SET continuation_session_id = $3, continuation_started_at = COALESCE(continuation_started_at, now()), updated_at = now()
			WHERE workspace_id = $1 AND project_id = $2 AND continuation_session_id IS NULL
		`, workspaceID, projectID, sessionID); err != nil {
			return fmt.Errorf("persist autonomous project continuation session: %w", err)
		}
	}

	var existingTaskID pgtype.UUID
	err = r.pool.QueryRow(ctx, `
		SELECT id
		FROM agent_task_queue
		WHERE chat_session_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, sessionID).Scan(&existingTaskID)
	if err == nil {
		_, err = r.pool.Exec(ctx, `
			UPDATE autonomous_project_team_draft
			SET continuation_task_id = $3, continuation_started_at = COALESCE(continuation_started_at, now()), updated_at = now()
			WHERE workspace_id = $1 AND project_id = $2 AND continuation_task_id IS NULL
		`, workspaceID, projectID, existingTaskID)
		return err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("recover autonomous project continuation task: %w", err)
	}

	session, err := r.taskSvc.Queries.GetChatSession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("reload autonomous project continuation session: %w", err)
	}
	roster := make([]string, 0, len(team.Plan.Roles))
	for _, role := range team.Plan.Roles {
		if agentID, exists := team.Agent(role.Role); exists {
			roster = append(roster, fmt.Sprintf("- %s (%s): agent %s", role.DisplayName, role.Role, util.UUIDToString(agentID)))
		}
	}
	prompt := fmt.Sprintf(`The Technology Team for this project has now been approved and provisioned.

Continue the autonomous delivery setup by creating the executable issue backlog for the project. This is an orchestration task only: do not edit repository files or implement code.

Project:
- ID: %s
- Title: %s
- Technology Team squad ID: %s

Generated specialists:
%s

Required procedure:
1. Run `+"`multica project get %s --output json`"+` and `+"`multica issue list --project %s --limit 100 --output json`"+` first.
2. Reuse and refine existing project issues when they already cover required work; do not create duplicates.
3. Create only missing, implementation-ready issues needed for the requested MVP. Give every issue a concrete description and acceptance criteria.
4. Assign each issue to the most appropriate generated specialist by exact UUID using `+"`--assignee-id`"+`.
5. Set ready work to `+"`in_progress`"+` so the autonomous workflow can dispatch it. Keep work that truly depends on unfinished prerequisites out of In Progress.
6. Do not create, archive, or reconfigure agents, squads, runtimes, or skills. Do not change the team plan.
7. Stop after the project backlog is created or updated. Do not perform implementation yourself.
`,
		util.UUIDToString(projectID),
		project.Title,
		util.UUIDToString(team.SquadID),
		strings.Join(roster, "\n"),
		util.UUIDToString(projectID),
		util.UUIDToString(projectID),
	)

	sent, err := r.taskSvc.SendDirectChatMessage(
		ctx, session, carrier, confirmedBy, prompt, nil, "member", confirmedBy,
	)
	if err != nil {
		return fmt.Errorf("enqueue autonomous project backlog continuation: %w", err)
	}
	_, err = r.pool.Exec(ctx, `
		UPDATE autonomous_project_team_draft
		SET continuation_task_id = $3, continuation_started_at = COALESCE(continuation_started_at, now()), updated_at = now()
		WHERE workspace_id = $1 AND project_id = $2
	`, workspaceID, projectID, sent.Task.ID)
	if err != nil {
		return fmt.Errorf("persist autonomous project continuation task: %w", err)
	}
	return nil
}

func (r *Runtime) ensureProjectCoordinator(ctx context.Context, workspaceID, projectID pgtype.UUID) (db.Agent, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return db.Agent{}, fmt.Errorf("begin autonomous project coordinator tx: %w", err)
	}
	defer tx.Rollback(ctx)
	qtx := r.taskSvc.Queries.WithTx(tx)
	lockKey := "autonomous-project-coordinator:" + util.UUIDToString(workspaceID) + ":" + util.UUIDToString(projectID)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", lockKey); err != nil {
		return db.Agent{}, fmt.Errorf("lock autonomous project coordinator: %w", err)
	}
	mika, err := qtx.GetAgentBySystemKey(ctx, db.GetAgentBySystemKeyParams{
		WorkspaceID: workspaceID,
		SystemKey: pgtype.Text{String: service.MikaSystemKey, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Agent{}, teamprovision.ErrMikaUnavailable
	}
	if err != nil {
		return db.Agent{}, fmt.Errorf("load Mika for autonomous project coordinator: %w", err)
	}
	if !mika.RuntimeID.Valid {
		return db.Agent{}, fmt.Errorf("%w: Mika has no runtime", teamprovision.ErrMikaUnavailable)
	}
	var runtimeStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM agent_runtime WHERE id = $1 AND workspace_id = $2`, mika.RuntimeID, workspaceID).Scan(&runtimeStatus); err != nil {
		return db.Agent{}, fmt.Errorf("load Mika runtime for autonomous project coordinator: %w", err)
	}
	if runtimeStatus != "online" {
		return db.Agent{}, fmt.Errorf("Mika runtime is %s; autonomous backlog continuation requires an online runtime", runtimeStatus)
	}

	systemKey := autonomousCoordinatorSystemKeyPrefix + util.UUIDToString(projectID)
	carrier, err := qtx.GetAgentBySystemKey(ctx, db.GetAgentBySystemKeyParams{
		WorkspaceID: workspaceID,
		SystemKey: pgtype.Text{String: systemKey, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		carrier, err = qtx.CreateAgentBuilder(ctx, db.CreateAgentBuilderParams{
			WorkspaceID: workspaceID,
			Name: "Autonomous Project Coordinator",
			RuntimeMode: mika.RuntimeMode,
			RuntimeID: mika.RuntimeID,
			OwnerID: mika.OwnerID,
			Instructions: autonomousCoordinatorInstructions,
			Model: mika.Model,
			SystemKey: pgtype.Text{String: systemKey, Valid: true},
		})
	}
	if err != nil {
		return db.Agent{}, fmt.Errorf("ensure autonomous project coordinator: %w", err)
	}
	runtimeConfig := mika.RuntimeConfig
	if len(runtimeConfig) == 0 { runtimeConfig = []byte("{}") }
	customEnv := mika.CustomEnv
	if len(customEnv) == 0 { customEnv = []byte("{}") }
	customArgs := mika.CustomArgs
	if len(customArgs) == 0 { customArgs = []byte("[]") }
	if _, err := tx.Exec(ctx, `
		UPDATE agent
		SET runtime_mode = $2, runtime_config = $3, runtime_id = $4, model = $5,
		    thinking_level = $6, service_tier = $7, custom_env = $8, custom_args = $9,
		    instructions = $10, mcp_config = NULL, composio_toolkit_allowlist = NULL,
		    max_concurrent_tasks = 1, updated_at = now()
		WHERE id = $1 AND kind = 'system'
	`, carrier.ID, mika.RuntimeMode, runtimeConfig, mika.RuntimeID, nullableText(mika.Model),
		nullableText(mika.ThinkingLevel), nullableText(mika.ServiceTier), customEnv, customArgs, autonomousCoordinatorInstructions); err != nil {
		return db.Agent{}, fmt.Errorf("inherit Mika execution profile for autonomous project coordinator: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM agent_skill WHERE agent_id = $1`, carrier.ID); err != nil {
		return db.Agent{}, fmt.Errorf("clear autonomous project coordinator workspace skills: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_skill (agent_id, skill_id, enabled)
		SELECT $1, skill_id, enabled
		FROM agent_skill
		WHERE agent_id = $2 AND enabled = TRUE
		ON CONFLICT (agent_id, skill_id) DO UPDATE SET enabled = EXCLUDED.enabled
	`, carrier.ID, mika.ID); err != nil {
		return db.Agent{}, fmt.Errorf("inherit Mika workspace skills for autonomous project coordinator: %w", err)
	}
	carrier, err = qtx.GetAgentBySystemKey(ctx, db.GetAgentBySystemKeyParams{
		WorkspaceID: workspaceID,
		SystemKey: pgtype.Text{String: systemKey, Valid: true},
	})
	if err != nil {
		return db.Agent{}, fmt.Errorf("reload autonomous project coordinator: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return db.Agent{}, fmt.Errorf("commit autonomous project coordinator: %w", err)
	}
	return carrier, nil
}
