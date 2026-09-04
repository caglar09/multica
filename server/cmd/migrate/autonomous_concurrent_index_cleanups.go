package main

// Autonomous Project OS migrations use CREATE INDEX CONCURRENTLY so failed
// PostgreSQL builds may leave INVALID index shells behind. Register every
// concurrent autonomous index with the migrator's pre-flight cleanup hook so a
// retry is idempotent and safe.
func init() {
	cleanups := map[int64]string{
		461: "idx_autonomous_project_plan_identity",
		462: "idx_autonomous_project_plan_latest",
		463: "idx_autonomous_project_plan_node_identity",
		464: "idx_autonomous_project_plan_node_ready",
		465: "idx_autonomous_project_plan_edge_identity",
		466: "idx_autonomous_project_brain_lookup",
		467: "idx_autonomous_project_quality_pending",
		468: "idx_autonomous_project_escalation_open",
		470: "idx_autonomous_agent_performance_rank",
		472: "idx_autonomous_project_bootstrap_workspace",
		474: "idx_autonomous_project_usage_accounting_project",
		477: "autonomous_project_brain_canonical_active_idx",
		478: "autonomous_project_brain_learning_claim_idx",
		479: "autonomous_project_brain_learning_task_idx",
		483: "idx_autonomous_project_review_finding_open",
		484: "idx_autonomous_project_review_verdict_issue",
		485: "idx_autonomous_project_handoff_issue",
		486: "idx_autonomous_project_handoff_implementation_task",
		487: "idx_autonomous_project_handoff_target_task",
		491: "autonomous_project_brain_entry_fts_idx",
	}
	for version, indexName := range cleanups {
		concurrentIndexCleanups[version] = indexName
		preMigrationHooks[version] = cleanupInvalidConcurrentIndex(indexName)
	}
}
