export interface ProjectReportSummary {
  task_count: number;
  completed_tasks: number;
  failed_tasks: number;
  cancelled_tasks: number;
  active_tasks: number;
  elapsed_seconds: number;
  total_runtime_seconds: number;
  average_runtime_seconds: number;
  p95_runtime_seconds: number;
  average_queue_seconds: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  total_tokens: number;
  authoritative_cost_usd_ticks: number;
  usage_rows: number;
  costed_usage_rows: number;
  review_rejects: number;
  review_cycles: number;
  reviewed_issues: number;
  approved_issues: number;
  first_pass_approved_issues: number;
  first_pass_approval_rate: number;
  blocked_workflows: number;
  quality_gate_total: number;
  quality_gate_passed: number;
  quality_gate_pass_rate: number;
}

export interface ProjectReportTask {
  id: string;
  issue_id: string | null;
  issue_title: string | null;
  agent_id: string;
  agent_name: string;
  stage: "implementation" | "review" | "control_plane" | "project" | string;
  status: string;
  failure_reason: string | null;
  runtime_id: string | null;
  runtime_name: string | null;
  runtime_provider: string | null;
  runtime_mode: string | null;
  models: string;
  created_at: string;
  started_at: string | null;
  completed_at: string | null;
  queue_seconds: number | null;
  runtime_seconds: number | null;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  total_tokens: number;
  cost_usd_ticks: number;
  cost_complete: boolean;
  review_rejects: number;
}

export interface ProjectReportAgent {
  agent_id: string;
  agent_name: string;
  task_count: number;
  completed_tasks: number;
  failed_tasks: number;
  total_runtime_seconds: number;
  average_runtime_seconds: number;
  total_tokens: number;
  cost_usd_ticks: number;
}

export interface ProjectReportRuntime {
  provider: string;
  runtime_name: string;
  runtime_mode: string;
  task_count: number;
  failed_tasks: number;
  total_runtime_seconds: number;
  total_tokens: number;
  cost_usd_ticks: number;
}

export interface ProjectReportDay {
  date: string;
  task_count: number;
  completed_tasks: number;
  failed_tasks: number;
  total_runtime_seconds: number;
  total_tokens: number;
}

export interface ProjectReportSnapshot {
  generated_at: string;
  task_limit: number;
  task_truncated: boolean;
  summary: ProjectReportSummary;
  tasks: ProjectReportTask[];
  agents: ProjectReportAgent[];
  runtimes: ProjectReportRuntime[];
  daily: ProjectReportDay[];
}
