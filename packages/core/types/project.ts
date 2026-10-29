
export type ProjectStatus = "planned" | "in_progress" | "paused" | "completed" | "cancelled";

export type ProjectPriority = "urgent" | "high" | "medium" | "low" | "none";

export type ProjectAutonomyMode = "standard" | "autonomous";
export type ProjectAutonomyLevel =
  | "assisted"
  | "development"
  | "delivery"
  | "closed_loop";

export interface ProjectBootstrapKnowledgeItem {
  kind:
    | "prd"
    | "requirements"
    | "architecture"
    | "design"
    | "api_spec"
    | "reference"
    | "note";
  title: string;
  content: string;
}

export interface ProjectBootstrapConfig {
  autonomy_mode: ProjectAutonomyMode;
  autonomy_level?: ProjectAutonomyLevel;
  brief?: string;
  knowledge?: ProjectBootstrapKnowledgeItem[];
  approvals?: {
    database_migration?: boolean;
    production_deploy?: boolean;
    major_dependency?: boolean;
    critical_risk?: boolean;
  };
  budget?: {
    max_parallel_nodes?: number;
    max_total_attempts?: number;
    token_limit?: number;
    runtime_seconds_limit?: number;
    cost_microunits_limit?: number;
  };
}

export interface ProjectWorkingDirectory {
  /** Absolute path reported by the daemon that executed the project task. */
  path: string;
  /** Privacy-safe display form for non-local surfaces. */
  relative_path: string;
  /** Machine identity: absolute paths are only valid on this daemon's host. */
  daemon_id: string;
  /** Loopback health/open-directory port reported by this daemon. */
  health_port?: number;
  runtime_id: string;
  task_id: string;
  status: string;
}

export interface Project {
  id: string;
  workspace_id: string;
  title: string;
  description: string | null;
  icon: string | null;
  status: ProjectStatus;
  priority: ProjectPriority;
  lead_type: "member" | "agent" | null;
  lead_id: string | null;
  // Calendar days ("YYYY-MM-DD"), no time-of-day or timezone — same contract as
  // issue.start_date / issue.due_date.
  start_date: string | null;
  due_date: string | null;
  created_at: string;
  updated_at: string;
  issue_count: number;
  done_count: number;
  resource_count: number;
  /** Detail-only daemon-owned working directories, newest per machine. */
  working_directories?: ProjectWorkingDirectory[];
  /**
   * The workflow this project uses; null for the workspace Default workflow.
   * Optional because older servers do not send it. (MUL-7420)
   */
  workflow_id?: string | null;
}

export interface CreateProjectRequest {
  title: string;
  description?: string;
  icon?: string;
  status?: ProjectStatus;
  priority?: ProjectPriority;
  lead_type?: "member" | "agent";
  lead_id?: string;
  start_date?: string;
  due_date?: string;
  /** Workflow to use from the start; omitted uses the Default workflow. */
  workflow_id?: string | null;
  // Resources to attach in the same transaction as the project. Server returns
  // 4xx (and rolls back) if any one is invalid or duplicate.
  resources?: CreateProjectResourceRequest[];
  bootstrap?: ProjectBootstrapConfig;
}

export interface UpdateProjectRequest {
  title?: string;
  description?: string | null;
  icon?: string | null;
  status?: ProjectStatus;
  priority?: ProjectPriority;
  lead_type?: "member" | "agent" | null;
  lead_id?: string | null;
  // Omit the key to leave the date untouched; send null (or "") to clear it.
  start_date?: string | null;
  due_date?: string | null;
}

export interface ListProjectsResponse {
  projects: Project[];
  total: number;
}

export interface PortableActorRef {
  type?: string;
  name: string;
  email?: string;
}

export interface PortableLabelRef {
  name: string;
  color?: string;
}

export interface PortableComment {
  key: string;
  origin_id?: string;
  parent_key?: string | null;
  author_type?: string;
  author_name?: string;
  author_email?: string;
  type?: string;
  content: string;
  created_at?: string;
  metadata?: Record<string, unknown>;
}

export interface PortablePlanRequirement {
  id: string;
  text: string;
  rationale?: string;
}

export interface PortablePlanNode {
  key: string;
  title: string;
  description?: string;
  kind?: string;
  status?: string;
  priority?: number;
  acceptance_criteria?: string[];
}

export interface PortablePlanEdge {
  from: string;
  to: string;
  type?: string;
}

export interface PortablePlan {
  goal?: string;
  requirements?: PortablePlanRequirement[];
  specification?: PortableProjectSpecification;
  nodes?: PortablePlanNode[];
  edges?: PortablePlanEdge[];
}

export interface PortableSpecificationRequirement {
  id: string;
  text: string;
  rationale: string;
  source: string;
}

export interface PortableProjectSpecification {
  goal: string;
  summary: string;
  requirements: PortableSpecificationRequirement[];
  non_functional_requirements: string[];
  constraints: string[];
  non_goals: string[];
  definition_of_done: string[];
  acceptance_expectations: string[];
}

export interface ProjectContextFile {
  path: string;
  content: string;
}

export interface ProjectContextPayload {
  root_name: string;
  files: ProjectContextFile[];
  warnings?: string[];
  local_path?: string;
  daemon_id?: string;
}

export interface DiscoveredProject {
  title: string;
  purpose: string;
  goal: string;
  architecture_summary: string;
}

export interface DiscoveredProjectBoundaries {
  in_scope: string[];
  out_of_scope: string[];
  constraints: string[];
  definition_of_done: string[];
}

export interface DiscoveredFeature {
  key: string;
  title: string;
  description: string;
  evidence: string[];
}

export interface DiscoveredTask {
  key: string;
  title: string;
  description: string;
  priority: string;
  stage: number;
  parent_key: string;
  blocked_by_keys: string[];
  acceptance_criteria: string[];
}

export interface ProjectDiscoveryReport {
  project: DiscoveredProject;
  boundaries: DiscoveredProjectBoundaries;
  implemented_features: DiscoveredFeature[];
  next_tasks: DiscoveredTask[];
}

export interface PortableKnowledgeItem {
  kind: string;
  title: string;
  content: unknown;
}

export interface PortableBootstrap {
  autonomy_mode?: string;
  autonomy_level?: string;
  brief?: string;
  knowledge?: PortableKnowledgeItem[];
  policy?: unknown;
  budget?: unknown;
  plan?: PortablePlan;
}

export interface PortableProject {
  title?: string;
  description?: string | null;
  icon?: string | null;
  status?: string | null;
  priority?: string | null;
  start_date?: string | null;
  due_date?: string | null;
  lead?: PortableActorRef | null;
}

export interface PortableResource {
  resource_type: string;
  label?: string | null;
  resource_ref: Record<string, unknown>;
  position?: number;
}

export interface PortableTask {
  key: string;
  origin_id?: string;
  title?: string;
  description?: string | null;
  status?: string | null;
  status_category?: string | null;
  priority?: string | null;
  parent_key?: string | null;
  stage?: number;
  position?: number;
  blocked_by_keys?: string[];
  labels?: PortableLabelRef[];
  assignee?: PortableActorRef | null;
  acceptance_criteria?: string[];
  metadata?: Record<string, unknown>;
  comments?: PortableComment[];
}

export interface PortableProjectBundle {
  schema_version: "multica.project.v1";
  exported_at?: string;
  origin: {
    workspace_id?: string;
    project_id?: string;
  };
  project: PortableProject;
  resources?: PortableResource[];
  bootstrap?: PortableBootstrap | null;
  tasks: PortableTask[];
}

export interface PortableProjectExportResponse {
  bundle: PortableProjectBundle;
  tasks_markdown: string;
  agent_guide_markdown: string;
}

export interface ImportProjectRequest {
  bundle?: PortableProjectBundle;
  markdown?: string;
  project_title_hint?: string;
  instruction?: string;
  project_context?: ProjectContextPayload;
  discovery_report?: ProjectDiscoveryReport;
  runtime_id?: string;
  model?: string;
  dry_run?: boolean;
  create_missing_labels?: boolean;
}

export interface SyncProjectRequest {
  bundle?: PortableProjectBundle;
  markdown?: string;
  dry_run?: boolean;
  create_missing_labels?: boolean;
}

export type PortabilityAction = "create" | "update" | "unchanged";

export interface TaskSyncDiff {
  key: string;
  title: string;
  action: PortabilityAction;
  changes: string[];
}

export interface PortabilityPreview {
  project_action: PortabilityAction;
  project_changes: string[];
  tasks: TaskSyncDiff[];
  warnings: string[];
}

export interface PortabilityResult {
  project_id?: string | null;
  created_count: number;
  updated_count: number;
  unchanged_count: number;
  warnings: string[];
  preview?: PortabilityPreview | null;
  discovery_report?: ProjectDiscoveryReport | null;
}

// ProjectResource is a typed pointer from a project to an external resource.
// The resource_ref shape depends on resource_type. New types add a case in
// validateAndNormalizeResourceRef on the server and a renderer in the UI.
//
// Known types (UI must default-case unknown server-side additions):
//   - github_repo: cloud-side git checkout, ref = { url, ref?, default_branch_hint? }
//   - local_directory: agent execution on a specific daemon,
//     ref = { local_path, daemon_id, label?, execution_mode? }
export type ProjectResourceType = "github_repo" | "local_directory";

export interface GithubRepoResourceRef {
  url: string;
  ref?: string;
  default_branch_hint?: string;
}

/**
 * How tasks sharing one local directory are executed.
 *
 * - `in_place`: the agent works directly in the user's directory and tasks run
 *   one at a time — a second task waits in `waiting_local_directory`. Edits
 *   land in the user's working copy.
 * - `worktree`: each task gets its own git worktree of that repo inside the
 *   runtime's workspace, so tasks run concurrently and deliver their work as a
 *   branch instead of touching the working copy. Every task of one conversation
 *   shares that branch — `agent/<agent>/<issue>` — so a follow-up continues the
 *   previous turn's work; a task with no conversation behind it gets
 *   `agent/<agent>/<task>`. Continuation is decided by an ownership record in
 *   the repo, not by the branch name, so a same-named branch the user made is
 *   never adopted.
 *
 * Absent means `in_place`: resources created before the mode existed keep their
 * original behavior, so this is optional rather than defaulted on the server.
 */
export type LocalDirectoryExecutionMode = "in_place" | "worktree";

export interface LocalDirectoryResourceRef {
  local_path: string;
  daemon_id: string;
  label?: string;
  execution_mode?: LocalDirectoryExecutionMode;
}

export type ProjectResourceRef =
  | GithubRepoResourceRef
  | LocalDirectoryResourceRef
  | Record<string, unknown>;

export interface ProjectResource {
  id: string;
  project_id: string;
  workspace_id: string;
  resource_type: ProjectResourceType;
  resource_ref: ProjectResourceRef;
  label: string | null;
  position: number;
  created_at: string;
  created_by: string | null;
}

export interface CreateProjectResourceRequest {
  resource_type: ProjectResourceType;
  resource_ref: ProjectResourceRef;
  label?: string;
  position?: number;
}

// resource_type is immutable server-side; partial-update payload mirrors that.
// Sending only the field(s) you want to change is fine — the server merges
// the request body with the existing row, including resource_ref shortcuts.
export interface UpdateProjectResourceRequest {
  resource_ref?: ProjectResourceRef;
  label?: string | null;
  position?: number;
}

export interface ListProjectResourcesResponse {
  resources: ProjectResource[];
  total: number;
}
