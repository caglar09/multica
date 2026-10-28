"use client";

import { useState } from "react";
import {
  Plus,
  Server,
  Layers,
  Rocket,
  GitCommit,
  ExternalLink,
  Unlink,
} from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { Badge } from "@multica/ui/components/ui/badge";
import { useQueries, useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "@multica/core/hooks";
import type {
  DeploymentOperation,
  DeploymentTarget,
  DeploymentProviderConnection,
  DiscoveredVercelProject,
  CreateDeploymentTargetInput,
  GithubRepoResourceRef,
} from "@multica/core/types";
import type { RepositoryMetadata } from "@multica/core/deployments/recipes";
import {
  useCreateDeploymentTarget,
  useCreateDeploymentProviderTarget,
  useTriggerDeployment,
  usePromoteDeployment,
  useRollbackDeployment,
  useCancelDeployment,
  useDeleteDeploymentTarget,
} from "@multica/core/deployments/mutations";
import {
  deploymentTargetsOptions,
  deploymentConnectionsOptions,
  discoveredVercelProjectsOptions,
  deploymentSourceRevisionOptions,
  deploymentOperationsOptions,
} from "@multica/core/deployments/queries";
import { projectResourcesOptions } from "@multica/core/projects";
import { useT } from "../../i18n";
import { DeploymentDetailCard, repositoryCommitUrl } from "./deployment-detail-card";
import { VercelTargetAttachDialog } from "./vercel-target-attach-dialog";
import { ProductionDeployConfirmDialog } from "./production-deploy-confirm-dialog";
import { PromoteDeploymentConfirmDialog } from "./promote-deployment-confirm-dialog";
import { RollbackDeploymentConfirmDialog } from "./rollback-deployment-confirm-dialog";
import { DeploymentLogsDialog } from "./deployment-logs-dialog";

export interface ProjectDeploymentsSectionProps {
  projectId: string;
  targets?: DeploymentTarget[];
  operations?: DeploymentOperation[];
  connections?: DeploymentProviderConnection[];
  discoveredProjects?: DiscoveredVercelProject[];
  repository?: string;
  repositoryMetadata?: RepositoryMetadata;
  currentCommitSha?: string;
  currentGitRef?: string;
  currentCommitMessage?: string;
  className?: string;
}

export function ProjectDeploymentsSection({
  projectId,
  targets: propTargets,
  operations: propOperations,
  connections: propConnections,
  discoveredProjects: propDiscoveredProjects,
  repository: propRepository,
  repositoryMetadata,
  currentCommitSha,
  currentGitRef,
  currentCommitMessage,
  className,
}: ProjectDeploymentsSectionProps) {
  const { t } = useT("projects");
  const wsId = useWorkspaceId();

  const queriedTargets = useQuery({
    ...deploymentTargetsOptions(wsId, projectId),
    enabled: !propTargets && !!wsId && !!projectId,
  });
  const targets = propTargets ?? queriedTargets.data ?? [];

  const projectResources = useQuery({
    ...projectResourcesOptions(wsId, projectId),
    enabled: !!wsId && !!projectId,
  });
  const githubResource = projectResources.data?.find(
    (resource) => resource.resource_type === "github_repo",
  );
  const githubRef = githubResource?.resource_ref as
    | GithubRepoResourceRef
    | undefined;
  const sourceRevisionQuery = useQuery({
    ...deploymentSourceRevisionOptions(wsId, projectId),
    enabled: !!wsId && !!projectId && (!currentCommitSha || !currentGitRef),
  });
  const sourceRevision = sourceRevisionQuery.data;
  const repository = propRepository ?? githubRef?.url ?? sourceRevision?.repository;
  const defaultBranch =
    githubRef?.ref ?? githubRef?.default_branch_hint ?? sourceRevision?.git_ref;
  const sourceCommitSha = currentCommitSha ?? sourceRevision?.commit_sha;
  const sourceGitRef = currentGitRef ?? sourceRevision?.git_ref;
  const sourceCommitMessage =
    currentCommitMessage ?? sourceRevision?.commit_message;
  const deploymentSource = (target: DeploymentTarget) => ({
    repository: repository?.trim() || target.config?.repository?.trim(),
    gitRef: sourceGitRef?.trim() || target.config?.default_branch?.trim(),
    commitSha: sourceCommitSha?.trim(),
    commitMessage: sourceCommitMessage,
  });
  const deploymentSourceAvailable = (target: DeploymentTarget) => {
    const source = deploymentSource(target);
    return Boolean(source.repository && source.gitRef);
  };

  const queriedConnections = useQuery({
    ...deploymentConnectionsOptions(wsId),
    enabled: !propConnections && !!wsId,
  });
  const connections = propConnections ?? queriedConnections.data ?? [];

  const operationQueries = useQueries({
    queries: propOperations
      ? []
      : targets.map((target) => ({
          ...deploymentOperationsOptions(wsId, target.id),
          refetchInterval: (query: { state: { data?: DeploymentOperation[] } }) =>
            query.state.data?.some((operation) =>
              ["pending", "queued", "dispatching", "building", "deploying", "running", "retry_wait"].includes(
                operation.status,
              ),
            )
              ? 3_000
              : false,
        })),
  });
  const operations = (
    propOperations ?? operationQueries.flatMap((query) => query.data ?? [])
  ).toSorted(
    (a, b) =>
      new Date(b.created_at).getTime() - new Date(a.created_at).getTime(),
  );

  const [attachOpen, setAttachOpen] = useState(false);
  const [confirmTarget, setConfirmTarget] = useState<DeploymentTarget | null>(
    null,
  );
  const [promoteCandidate, setPromoteCandidate] = useState<{
    operation: DeploymentOperation;
    target?: DeploymentTarget;
  } | null>(null);
  const [rollbackCandidate, setRollbackCandidate] = useState<{
    operation: DeploymentOperation;
    target?: DeploymentTarget;
  } | null>(null);
  const [logsOperation, setLogsOperation] = useState<DeploymentOperation | null>(
    null,
  );

  const createTargetMutation = useCreateDeploymentTarget();
  const createProviderTargetMutation = useCreateDeploymentProviderTarget();
  const triggerDeployMutation = useTriggerDeployment();
  const promoteMutation = usePromoteDeployment();
  const rollbackMutation = useRollbackDeployment();
  const cancelMutation = useCancelDeployment();
  const deleteTargetMutation = useDeleteDeploymentTarget();

  const handleUnlinkTarget = (target: DeploymentTarget) => {
    if (!window.confirm(t(($) => $.deployments.unlink_target_confirm))) return;
    deleteTargetMutation.mutate(target.id);
  };

  const vercelConnection =
    connections.find((c) => c.provider === "vercel") || connections[0];
  const connectionId = vercelConnection?.id ?? "";
  const queriedDiscoveredProjects = useQuery({
    ...discoveredVercelProjectsOptions(wsId, connectionId),
    enabled: !propDiscoveredProjects && !!connectionId,
  });
  const discoveredProjects =
    propDiscoveredProjects ?? queriedDiscoveredProjects.data ?? [];

  const handleTriggerDeploy = (target: DeploymentTarget) => {
    const source = deploymentSource(target);
    if (!source.repository || !source.gitRef) return;
    if (target.environment === "production") {
      setConfirmTarget(target);
    } else {
      triggerDeployMutation.mutate({
        target_id: target.id,
        repository: source.repository,
        commit_sha: source.commitSha,
        git_ref: source.gitRef,
        commit_message: source.commitMessage,
        environment: target.environment,
      });
    }
  };

  const handleConfirmProductionDeploy = async (approvalReference: string) => {
    if (!confirmTarget) return;
    const source = deploymentSource(confirmTarget);
    if (!source.repository || !source.gitRef) return;
    await triggerDeployMutation.mutateAsync({
      target_id: confirmTarget.id,
      repository: source.repository,
      commit_sha: source.commitSha,
      git_ref: source.gitRef,
      commit_message: source.commitMessage,
      environment: "production",
      approval_reference: approvalReference,
    });
    setConfirmTarget(null);
  };

  const handleAttachSubmit = async (input: CreateDeploymentTargetInput) => {
    const data = { ...input, project_id: projectId };
    if (input.create_new) {
      await createProviderTargetMutation.mutateAsync(data);
      return;
    }
    await createTargetMutation.mutateAsync(data);
  };

  return (
    <div
      className={`flex flex-col gap-6 ${className ?? ""}`}
      data-testid="project-deployments-section"
    >
      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <Layers className="size-5 text-muted-foreground" />
          <h2 className="text-lg font-semibold tracking-tight text-foreground">
            {t(($) => $.deployments.title)}
          </h2>
        </div>
        <Button
          size="sm"
          onClick={() => setAttachOpen(true)}
          className="gap-1.5"
          data-testid="attach-target-button"
        >
          <Plus className="size-4" />
          <span>{t(($) => $.deployments.attach_vercel)}</span>
        </Button>
      </div>
      {!targets.some(deploymentSourceAvailable) ? (
        <p
          className="text-sm text-muted-foreground"
          data-testid="source-revision-unavailable"
        >
          {t(($) => $.deployments.source_revision_unavailable)}
        </p>
      ) : null}

      {/* Connected Targets */}
      <div className="flex flex-col gap-3">
        <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          {t(($) => $.deployments.connected_targets)} ({targets.length})
        </h3>
        {targets.length === 0 ? (
          <div
            className="flex flex-col items-center justify-center rounded-lg border border-dashed p-8 text-center"
            data-testid="empty-targets-placeholder"
          >
            <Server className="mb-2 size-8 text-muted-foreground" />
            <p className="text-sm font-medium text-foreground">
              {t(($) => $.deployments.no_targets)}
            </p>
            <p className="mt-1 text-xs text-muted-foreground">
              {t(($) => $.deployments.recipe_subtitle)}
            </p>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setAttachOpen(true)}
              className="mt-4 gap-1.5"
            >
              <Plus className="size-3.5" />
              <span>{t(($) => $.deployments.attach_vercel)}</span>
            </Button>
          </div>
        ) : (
          <div
            className="grid grid-cols-1 gap-3 sm:grid-cols-2"
            data-testid="targets-grid"
          >
            {targets.map((target) => {
              const isProd = target.environment === "production";
              const targetCapabilities = connections.find(
                (connection) => connection.id === target.connection_id,
              )?.capabilities;
              const targetOps = operations.filter(
                (op) => op.target_id === target.id,
              );
              const latestOp = targetOps[0];
              const targetStatus = latestOp?.status || "ready";

              return (
                <div
                  key={target.id}
                  className="flex flex-col justify-between rounded-lg border bg-card p-4 transition-all hover:border-border/80"
                  data-testid={`target-card-${target.id}`}
                >
                  <div className="flex items-start justify-between gap-2">
                    <div className="flex flex-col gap-1.5">
                      <div className="flex items-center gap-2 flex-wrap">
                        <span className="font-semibold text-foreground text-sm">
                          {target.name}
                        </span>
                        <Badge
                          variant={isProd ? "secondary" : "outline"}
                          className={
                            isProd
                              ? "border-red-500/30 bg-red-500/10 text-red-700 dark:text-red-300 font-semibold text-[10px]"
                              : "text-[10px]"
                          }
                          data-testid={`target-environment-badge-${target.id}`}
                        >
                          {target.environment}
                        </Badge>
                        <Badge
                          variant="outline"
                          className="text-[10px] capitalize text-muted-foreground"
                          data-testid={`target-status-badge-${target.id}`}
                        >
                          {targetStatus}
                        </Badge>
                      </div>

                      <span className="text-xs text-muted-foreground font-mono">
                        {target.config?.project_name ||
                          target.name ||
                          t(($) => $.deployments.default_project_name)}
                        {target.config?.framework
                          ? ` • ${target.config.framework}`
                          : ""}
                      </span>

                      {/* Source revision link if available */}
                      {latestOp?.request.commit_sha ? (
                        <a
                          href={
                            repositoryCommitUrl(
                              target.config?.repository,
                              latestOp.request.commit_sha,
                            ) || latestOp.evidence?.inspector_url
                          }
                          target="_blank"
                          rel="noopener noreferrer"
                          className="flex items-center gap-1 font-mono text-[11px] text-muted-foreground hover:text-foreground transition-colors w-fit pt-0.5"
                          data-testid={`target-source-revision-${target.id}`}
                          title={latestOp.request.commit_sha}
                        >
                          <GitCommit className="size-3" />
                          <span>{latestOp.request.commit_sha.slice(0, 7)}</span>
                          <ExternalLink className="size-2.5" />
                        </a>
                      ) : null}
                    </div>

                    <div className="flex shrink-0 items-center gap-2">
                      <Button
                        size="sm"
                        variant={isProd ? "destructive" : "default"}
                        onClick={() => handleTriggerDeploy(target)}
                        disabled={
                          !deploymentSourceAvailable(target) ||
                          targetCapabilities?.trigger === false ||
                          triggerDeployMutation.isPending
                        }
                        className="gap-1.5 h-8 text-xs font-medium"
                        data-testid={`trigger-deploy-${target.id}`}
                      >
                        <Rocket className="size-3" />
                        <span>
                          {isProd
                            ? t(($) => $.deployments.deploy_prod)
                            : t(($) => $.deployments.deploy_preview)}
                        </span>
                      </Button>
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => handleUnlinkTarget(target)}
                        disabled={deleteTargetMutation.isPending}
                        className="gap-1.5 h-8 text-xs font-medium"
                        data-testid={`unlink-target-${target.id}`}
                      >
                        <Unlink className="size-3" />
                        <span>{t(($) => $.deployments.unlink_target)}</span>
                      </Button>
                    </div>
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </div>

      {/* Deployment History / Operations */}
      <div className="flex flex-col gap-3">
        <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          {t(($) => $.deployments.recent_deployments)}
        </h3>
        {operations.length === 0 ? (
          <div
            className="rounded-lg border border-dashed py-8 text-center text-xs text-muted-foreground"
            data-testid="empty-operations-placeholder"
          >
            {t(($) => $.deployments.no_deployments)}
          </div>
        ) : (
          <div
            className="flex flex-col gap-3"
            data-testid="deployment-operations-list"
          >
            {operations.map((op) => {
              const matchedTarget = targets.find((t) => t.id === op.target_id);
              const capabilities = connections.find(
                (connection) =>
                  connection.id === matchedTarget?.connection_id,
              )?.capabilities;
              return (
                <DeploymentDetailCard
                  key={op.id}
                  operation={op}
                  target={matchedTarget}
                  capabilities={capabilities}
                  onTriggerRedeploy={handleTriggerDeploy}
                  onPromote={(candidateOp, t) =>
                    setPromoteCandidate({ operation: candidateOp, target: t })
                  }
                  onRollback={(targetOp, t) =>
                    setRollbackCandidate({ operation: targetOp, target: t })
                  }
                  onCancel={(cancelOp) => cancelMutation.mutate(cancelOp.id)}
                  onViewLogs={setLogsOperation}
                />
              );
            })}
          </div>
        )}
      </div>

      {/* Attach Modal */}
      <VercelTargetAttachDialog
        open={attachOpen}
        onOpenChange={setAttachOpen}
        connectionId={connectionId}
        projectId={projectId}
        repository={repository}
        repositoryMetadata={repositoryMetadata}
        discoveredProjects={discoveredProjects}
        defaultBranch={defaultBranch}
        onSubmitTarget={handleAttachSubmit}
      />

      {/* Production Confirmation Modal */}
      {confirmTarget && deploymentSourceAvailable(confirmTarget) ? (
        <ProductionDeployConfirmDialog
          open={!!confirmTarget}
          onOpenChange={(open) => {
            if (!open) setConfirmTarget(null);
          }}
          targetName={confirmTarget.name}
          providerName={t(($) => $.deployments.default_provider_name)}
          teamSlug={confirmTarget.config?.team_slug}
          commitSha={deploymentSource(confirmTarget).commitSha ?? ""}
          gitRef={deploymentSource(confirmTarget).gitRef ?? ""}
          commitMessage={deploymentSource(confirmTarget).commitMessage}
          environment="production"
          onConfirm={handleConfirmProductionDeploy}
        />
      ) : null}

      {/* Promote Confirmation Modal */}
      {promoteCandidate ? (
        <PromoteDeploymentConfirmDialog
          open={!!promoteCandidate}
          onOpenChange={(open) => {
            if (!open) setPromoteCandidate(null);
          }}
          candidateOperation={promoteCandidate.operation}
          target={promoteCandidate.target}
          targetName={promoteCandidate.target?.name}
          onConfirm={async (approvalReference) => {
            await promoteMutation.mutateAsync({
              target_id:
                promoteCandidate.target?.id ||
                promoteCandidate.operation.target_id,
              candidate_operation_id: promoteCandidate.operation.id,
              approval_reference: approvalReference,
            });
            setPromoteCandidate(null);
          }}
        />
      ) : null}

      {/* Rollback Confirmation Modal */}
      {rollbackCandidate ? (
        <RollbackDeploymentConfirmDialog
          open={!!rollbackCandidate}
          onOpenChange={(open) => {
            if (!open) setRollbackCandidate(null);
          }}
          targetOperation={rollbackCandidate.operation}
          target={rollbackCandidate.target}
          targetName={rollbackCandidate.target?.name}
          onConfirm={async (approvalReference) => {
            await rollbackMutation.mutateAsync({
              target_id:
                rollbackCandidate.target?.id ||
                rollbackCandidate.operation.target_id,
              target_operation_id: rollbackCandidate.operation.id,
              approval_reference: approvalReference,
            });
            setRollbackCandidate(null);
          }}
        />
      ) : null}

      {logsOperation ? (
        <DeploymentLogsDialog
          operation={logsOperation}
          onClose={() => setLogsOperation(null)}
        />
      ) : null}
    </div>
  );
}
