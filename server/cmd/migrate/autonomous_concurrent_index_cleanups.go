package main

// Autonomous Project OS migrations use CREATE INDEX CONCURRENTLY so failed
// PostgreSQL builds may leave INVALID index shells behind. Register every
// concurrent autonomous index with the migrator's pre-flight cleanup hook so a
// retry is idempotent and safe.
func init() {
	cleanups := map[string]string{
		"461_autonomous_project_plan_identity":                     "idx_autonomous_project_plan_identity",
		"462_autonomous_project_plan_latest":                       "idx_autonomous_project_plan_latest",
		"463_autonomous_project_plan_node_identity":                "idx_autonomous_project_plan_node_identity",
		"464_autonomous_project_plan_node_ready":                   "idx_autonomous_project_plan_node_ready",
		"465_autonomous_project_plan_edge_identity":                "idx_autonomous_project_plan_edge_identity",
		"466_autonomous_project_brain_lookup":                      "idx_autonomous_project_brain_lookup",
		"467_autonomous_project_quality_pending":                   "idx_autonomous_project_quality_pending",
		"468_autonomous_project_escalation_open":                   "idx_autonomous_project_escalation_open",
		"470_autonomous_agent_performance_rank":                    "idx_autonomous_agent_performance_rank",
		"472_autonomous_project_bootstrap_workspace_idx":           "idx_autonomous_project_bootstrap_workspace",
		"474_autonomous_project_usage_accounting_lookup":           "idx_autonomous_project_usage_accounting_project",
		"477_autonomous_project_brain_canonical_index":             "autonomous_project_brain_canonical_active_idx",
		"478_autonomous_project_brain_learning_claim_index":        "autonomous_project_brain_learning_claim_idx",
		"479_autonomous_project_brain_learning_task_index":         "autonomous_project_brain_learning_task_idx",
		"483_autonomous_project_review_finding_open_index":         "idx_autonomous_project_review_finding_open",
		"484_autonomous_project_review_verdict_issue_index":        "idx_autonomous_project_review_verdict_issue",
		"485_autonomous_project_handoff_issue_index":               "idx_autonomous_project_handoff_issue",
		"486_autonomous_project_handoff_implementation_task_index": "idx_autonomous_project_handoff_implementation_task",
		"487_autonomous_project_handoff_target_task_index":         "idx_autonomous_project_handoff_target_task",
		"491_autonomous_project_brain_fts_index":                   "autonomous_project_brain_entry_fts_idx",
	}
	for version, indexName := range cleanups {
		concurrentIndexCleanups[version] = indexName
		preMigrationHooks[version] = cleanupInvalidConcurrentIndexHook(indexName)
	}
}
