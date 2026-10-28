package handler

import (
	"context"
	"sort"
	"testing"
)

type workspaceDeleteAction string

const (
	workspaceDelete       workspaceDeleteAction = "delete"
	workspaceDeleteDetach workspaceDeleteAction = "detach"
	workspaceDeleteKeep   workspaceDeleteAction = "keep"
	workspaceDeleteSettle workspaceDeleteAction = "settle"
)

// workspaceDeletionManifest is the schema coverage contract for workspace
// teardown. Adding a table requires an explicit ownership decision here; the
// handler deletion graph must then implement that decision before CI passes.
var workspaceDeletionManifest = map[string]workspaceDeleteAction{
	"cloud_agent_connection":                    workspaceDelete,
	"activity_log":                              workspaceDelete,
	"agent":                                     workspaceDelete,
	"agent_builder_draft":                       workspaceDelete,
	"agent_invocation_target":                   workspaceDelete,
	"agent_runtime":                             workspaceDelete,
	"agent_skill":                               workspaceDelete,
	"agent_task_queue":                          workspaceDelete,
	"agent_to_label":                            workspaceDelete,
	"attachment":                                workspaceDelete,
	"autopilot":                                 workspaceDelete,
	"autopilot_collaborator":                    workspaceDelete,
	"autopilot_quota_period":                    workspaceDelete,
	"autopilot_quota_reservation":               workspaceDelete,
	"autopilot_rule_version":                    workspaceDelete,
	"autopilot_run":                             workspaceDelete,
	"autopilot_subscriber":                      workspaceDelete,
	"autopilot_trigger":                         workspaceDelete,
	"autonomous_agent_performance":              workspaceDelete,
	"autonomous_control_plane_job":              workspaceDelete,
	"autonomous_project_artifact":               workspaceDelete,
	"autonomous_project_bootstrap":              workspaceDelete,
	"autonomous_project_brain_config":           workspaceDelete,
	"autonomous_project_brain_entry":            workspaceDelete,
	"autonomous_project_brain_impact_proposal":  workspaceDelete,
	"autonomous_project_brain_learning_job":     workspaceDelete,
	"autonomous_project_brain_snapshot":         workspaceDelete,
	"autonomous_project_brain_state":            workspaceDelete,
	"autonomous_project_budget":                 workspaceDelete,
	"autonomous_project_change_request":         workspaceDelete,
	"autonomous_project_change_request_event":   workspaceDelete,
	"autonomous_project_change_set":             workspaceDelete,
	"autonomous_project_context_compilation":    workspaceDelete,
	"autonomous_project_control":                workspaceDelete,
	"autonomous_project_deployment":             workspaceDelete,
	"autonomous_project_escalation":             workspaceDelete,
	"autonomous_project_handoff":                workspaceDelete,
	"autonomous_project_incident":               workspaceDelete,
	"autonomous_project_node_retirement":        workspaceDelete,
	"autonomous_project_plan":                   workspaceDelete,
	"autonomous_project_plan_edge":              workspaceDelete,
	"autonomous_project_plan_mutation":          workspaceDelete,
	"autonomous_project_plan_node":              workspaceDelete,
	"autonomous_project_quality_gate_run":       workspaceDelete,
	"autonomous_project_review_finding":         workspaceDelete,
	"autonomous_project_review_verdict":         workspaceDelete,
	"autonomous_project_specification_head":     workspaceDelete,
	"autonomous_project_specification_revision": workspaceDelete,
	"autonomous_project_team":                   workspaceDelete,
	"autonomous_project_team_analysis":          workspaceDelete,
	"autonomous_project_team_draft":             workspaceDelete,
	"autonomous_project_team_member":            workspaceDelete,
	"autonomous_project_usage_accounting":       workspaceDelete,
	"autonomous_workflow_action":                workspaceDelete,
	"autonomous_workflow_processed_event":       workspaceDelete,
	"autonomous_workflow_run":                   workspaceDelete,
	"channel_binding_token":                     workspaceDelete,
	"channel_chat_context_generation":           workspaceDelete,
	"channel_chat_session_binding":              workspaceDelete,
	"channel_inbound_audit":                     workspaceDelete,
	"channel_inbound_message_dedup":             workspaceDelete,
	"channel_installation":                      workspaceDelete,
	"channel_media_pending_object":              workspaceDeleteSettle,
	"channel_outbound_card_message":             workspaceDelete,
	"channel_outbound_message":                  workspaceDelete,
	"channel_reply_delivery":                    workspaceDelete,
	"channel_task_delivery":                     workspaceDelete,
	"channel_typing_reaction":                   workspaceDeleteSettle, // Source deletion makes retained anchors eligible for the cleanup worker.
	"channel_user_binding":                      workspaceDelete,
	"chat_draft_restore":                        workspaceDelete,
	"chat_message":                              workspaceDelete,
	"chat_pinned_agent":                         workspaceDelete,
	"chat_session":                              workspaceDelete,
	"client_usage_daily":                        workspaceDeleteDetach,
	"comment":                                   workspaceDelete,
	"comment_reaction":                          workspaceDelete,
	"contact_sales_inquiry":                     workspaceDeleteKeep,
	"daemon_connection":                         workspaceDelete,
	"daemon_token":                              workspaceDelete,
	"dingtalk_group_presence":                   workspaceDelete,
	"dingtalk_bot_identity":                     workspaceDelete,
	"dingtalk_group_route":                      workspaceDelete,
	"deployment_event":                          workspaceDelete,
	"deployment_operation":                      workspaceDelete,
	"deployment_provider_connection":            workspaceDelete,
	"deployment_source_revision":                workspaceDelete,
	"deployment_target":                         workspaceDelete,
	"deployment_vercel_oauth_state":             workspaceDelete,
	"feedback":                                  workspaceDeleteDetach,
	"github_installation":                       workspaceDelete,
	"github_pending_check_suite":                workspaceDelete,
	"github_pending_installation":               workspaceDeleteKeep,
	"github_pull_request":                       workspaceDelete,
	"github_pull_request_check_run":             workspaceDelete,
	"github_pull_request_check_suite":           workspaceDelete,
	"inbox_item":                                workspaceDelete,
	"instance_telemetry_state":                  workspaceDeleteKeep,
	"issue":                                     workspaceDelete,
	"issue_wakeup":                              workspaceDelete,
	"issue_wakeup_receipt":                      workspaceDelete,
	"issue_child_event":                         workspaceDelete,
	"issue_view":                                workspaceDelete,
	"issue_view_preference":                     workspaceDelete,
	"issue_dependency":                          workspaceDelete,
	"issue_label":                               workspaceDelete,
	"issue_property":                            workspaceDelete,
	"issue_pr_automation":                       workspaceDelete,
	"issue_pull_request":                        workspaceDelete,
	"issue_pull_request_exclusion":              workspaceDelete,
	"issue_reaction":                            workspaceDelete,
	"issue_source_context":                      workspaceDelete,
	"issue_source_context_object_intent":        workspaceDeleteSettle,
	"issue_status":                              workspaceDelete,
	"issue_workflow":                            workspaceDelete,
	"issue_subscriber":                          workspaceDelete,
	"issue_to_label":                            workspaceDelete,
	"issue_vcs_pull_request":                    workspaceDelete,
	"lark_binding_token":                        workspaceDelete,
	"lark_chat_session_binding":                 workspaceDelete,
	"lark_inbound_audit":                        workspaceDelete,
	"lark_inbound_message_dedup":                workspaceDelete,
	"lark_installation":                         workspaceDelete,
	"lark_outbound_card_message":                workspaceDelete,
	"lark_user_binding":                         workspaceDelete,
	"maintenance_job":                           workspaceDeleteKeep, // Global maintenance audit history, not workspace-owned.
	"member":                                    workspaceDelete,
	"member_delegation_day":                     workspaceDeleteKeep, // user-scoped telemetry ledger, no workspace_id
	"agent_mcp_server":                          workspaceDelete,
	"workspace_mcp_server":                      workspaceDelete,
	"notification_preference":                   workspaceDelete,
	"personal_access_token":                     workspaceDeleteKeep,
	"pinned_item":                               workspaceDelete,
	"plugin_installation":                       workspaceDelete,
	"plugin_hook_schedule":                      workspaceDelete,
	"plugin_invocation":                         workspaceDelete,
	"plugin_storage":                            workspaceDelete,
	"plugin_secret":                             workspaceDelete,
	"plugin_package":                            workspaceDelete,
	"plugin_package_version":                    workspaceDelete,
	"plugin_package_file":                       workspaceDelete,
	"project":                                   workspaceDelete,
	"project_resource":                          workspaceDelete,
	"quick_action":                              workspaceDelete,
	"runtime_profile":                           workspaceDelete,
	"schema_migrations":                         workspaceDeleteKeep,
	"search_index_change":                       workspaceDelete,
	"search_index_prune_mark":                   workspaceDeleteKeep,
	"seat_capacity_outbox":                      workspaceDeleteSettle,
	"skill":                                     workspaceDelete,
	"skill_file":                                workspaceDelete,
	"skill_to_label":                            workspaceDelete,
	"squad":                                     workspaceDelete,
	"squad_member":                              workspaceDelete,
	"sys_cron_executions":                       workspaceDeleteKeep,
	"task_message":                              workspaceDelete,
	"task_supplement":                           workspaceDelete,
	"task_supplement_capability":                workspaceDelete,
	"task_token":                                workspaceDelete,
	"task_usage":                                workspaceDelete,
	"task_usage_hourly":                         workspaceDelete,
	"task_usage_hourly_dirty":                   workspaceDelete,
	"task_usage_hourly_rollup_state":            workspaceDeleteKeep,
	"user":                                      workspaceDeleteKeep,
	"user_composio_connection":                  workspaceDeleteKeep,
	"vcs_commit_status":                         workspaceDelete,
	"vcs_connection":                            workspaceDelete,
	"vcs_pull_request":                          workspaceDelete,
	"verification_code":                         workspaceDeleteKeep,
	"webhook_delivery":                          workspaceDelete,
	"workspace":                                 workspaceDelete,
	"workspace_invitation":                      workspaceDelete,
	"workspace_share_link":                      workspaceDelete,
}

func TestWorkspaceDeletionManifestCoversPublicSchema(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}

	rows, err := testPool.Query(context.Background(), `
SELECT tablename
FROM pg_tables
WHERE schemaname = 'public'
ORDER BY tablename
`)
	if err != nil {
		t.Fatalf("list public tables: %v", err)
	}
	defer rows.Close()

	actual := make(map[string]struct{}, len(workspaceDeletionManifest))
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatalf("scan public table: %v", err)
		}
		actual[table] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate public tables: %v", err)
	}

	var unclassified, missing []string
	for table := range actual {
		if _, ok := workspaceDeletionManifest[table]; !ok {
			unclassified = append(unclassified, table)
		}
	}
	for table := range workspaceDeletionManifest {
		if _, ok := actual[table]; !ok {
			missing = append(missing, table)
		}
	}
	sort.Strings(unclassified)
	sort.Strings(missing)
	if len(unclassified) > 0 || len(missing) > 0 {
		t.Fatalf("workspace deletion manifest drift: unclassified=%v missing=%v", unclassified, missing)
	}

	workspaceColumns, err := testPool.Query(context.Background(), `
SELECT table_name
FROM information_schema.columns
WHERE table_schema = 'public'
  AND column_name = 'workspace_id'
`)
	if err != nil {
		t.Fatalf("list workspace_id columns: %v", err)
	}
	defer workspaceColumns.Close()

	withWorkspaceID := make(map[string]struct{})
	for workspaceColumns.Next() {
		var table string
		if err := workspaceColumns.Scan(&table); err != nil {
			t.Fatalf("scan workspace_id table: %v", err)
		}
		withWorkspaceID[table] = struct{}{}
	}
	if err := workspaceColumns.Err(); err != nil {
		t.Fatalf("iterate workspace_id tables: %v", err)
	}

	for table, action := range workspaceDeletionManifest {
		_, hasWorkspaceID := withWorkspaceID[table]
		switch action {
		case workspaceDeleteKeep:
			if hasWorkspaceID {
				t.Errorf("KEEP table %s gained workspace_id; classify its teardown behavior", table)
			}
		case workspaceDeleteDetach, workspaceDeleteSettle:
			if !hasWorkspaceID {
				t.Errorf("%s table %s lost workspace_id; update its teardown selector", action, table)
			}
		}
	}
}
