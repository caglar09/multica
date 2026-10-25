export type DeploymentProvider = "vercel" | "render" | "heroku" | "railway";

export type DeploymentEnvironment = "preview" | "staging" | "production";

export type DeploymentOperationStatus =
  | "queued"
  | "dispatching"
  | "building"
  | "deploying"
  | "running"
  | "ready"
  | "succeeded"
  | "failed"
  | "canceled"
  | "retry_wait";

export interface CapabilitySet {
  trigger?: boolean;
  status?: boolean;
  cancel?: boolean;
  webhook?: boolean;
  evidence?: boolean;
  logs?: boolean;
  promote?: boolean;
  rollback?: boolean;
  target_discovery?: boolean;
  target_create?: boolean;
}

export const DEFAULT_VERCEL_CAPABILITIES: CapabilitySet = {
  trigger: true,
  status: true,
  cancel: true,
  webhook: true,
  evidence: true,
  logs: true,
  promote: true,
  rollback: true,
  target_discovery: true,
  target_create: true,
};

export interface DeploymentProviderConnection {
  id: string;
  workspace_id: string;
  provider: DeploymentProvider | string;
  name: string;
  enabled: boolean;
  has_credentials?: boolean;
  webhook_url?: string;
  webhook_configured?: boolean;
  webhook_status?: "configured" | "polling_only" | "unsupported";
  capabilities?: CapabilitySet;
  created_at: string;
  updated_at: string;
  config?: {
    team_id?: string;
    team_slug?: string;
    account_name?: string;
    user_id?: string;
    scope?: string;
  };
}

export interface CreateDeploymentConnectionInput {
  provider: "vercel";
  name: string;
  token: string;
  webhook_secret?: string;
  config?: Pick<VercelTargetConfig, "team_id" | "team_slug">;
}

export interface VercelTargetConfig {
  project_id?: string;
  project_name?: string;
  repository?: string;
  default_branch?: string;
  framework?: string;
  root_directory?: string;
  build_command?: string;
  output_directory?: string;
  install_command?: string;
  dev_command?: string;
  team_id?: string;
  team_slug?: string;
  /** Names only; secret values are never part of repository analysis. */
  environment_variable_names?: string[];
  environment_variables?: Record<string, string>;
}

export interface DeploymentTarget {
  id: string;
  workspace_id: string;
  project_id?: string;
  connection_id: string;
  name: string;
  environment: DeploymentEnvironment;
  external_id?: string | null;
  config: VercelTargetConfig;
  capabilities?: CapabilitySet;
  created_at: string;
  updated_at: string;
}

export interface DeploymentOperation {
  id: string;
  workspace_id: string;
  target_id: string;
  action: "deploy" | "promote" | "rollback" | "cancel";
  idempotency_key: string;
  status: DeploymentOperationStatus;
  attempt_count: number;
  max_attempts: number;
  provider_operation_id?: string | null;
  request: {
    repository?: string;
    git_ref?: string;
    commit_sha?: string;
    commit_message?: string;
    environment?: DeploymentEnvironment;
    target_name?: string;
    initiator?: string;
    approval_reference?: string;
  };
  evidence: {
    url?: string;
    deployment_url?: string;
    inspector_url?: string;
    deployment_id?: string;
    build_time_ms?: number;
    error?: string;
    diagnostics?: string;
  };
  last_error?: string | null;
  created_at: string;
  updated_at: string;
}

export interface DeploymentLogResult {
  logs: string[];
  next_token?: string;
  completed: boolean;
}

export interface DeploymentSourceRevision {
  repository: string;
  git_ref: string;
  commit_sha: string;
  commit_message?: string;
}

export interface DiscoveredVercelProject {
  id: string;
  name: string;
  framework?: string | null;
  updated_at?: number | string;
  account_id?: string;
  latest_deployment_url?: string | null;
}

export interface DeploymentRecipe {
  id: string;
  name: string;
  framework: string;
  description: string;
  defaultRootDir: string;
  defaultBuildCommand: string;
  defaultOutputDir: string;
  defaultInstallCommand: string;
  defaultDevCommand: string;
}

export interface CreateDeploymentTargetInput {
  workspace_id?: string;
  project_id?: string;
  connection_id: string;
  name: string;
  environment: DeploymentEnvironment;
  external_id?: string | null;
  /** Selects the generic provider target-create endpoint instead of local attach. */
  create_new?: boolean;
  confirmed?: boolean;
  config: VercelTargetConfig;
}

export interface TriggerDeploymentInput {
  target_id: string;
  action?: "deploy" | "promote" | "rollback";
  repository?: string;
  git_ref?: string;
  commit_sha?: string;
  commit_message?: string;
  environment: DeploymentEnvironment;
  approval_reference?: string;
}

export interface PromoteDeploymentInput {
  target_id: string;
  candidate_operation_id: string;
  approval_reference?: string;
}

export interface RollbackDeploymentInput {
  target_id: string;
  target_operation_id: string;
  approval_reference?: string;
}
