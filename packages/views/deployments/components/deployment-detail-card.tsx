"use client";

import {
  ExternalLink,
  GitCommit,
  GitBranch,
  CheckCircle2,
  XCircle,
  Clock,
  AlertTriangle,
  RefreshCw,
  Server,
  Globe,
  ArrowUpRight,
  RotateCcw,
  StopCircle,
} from "lucide-react";
import { Card, CardContent, CardHeader } from "@multica/ui/components/ui/card";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button, buttonVariants } from "@multica/ui/components/ui/button";
import { Spinner } from "@multica/ui/components/ui/spinner";
import { cn } from "@multica/ui/lib/utils";
import type {
  DeploymentOperation,
  DeploymentTarget,
  DeploymentOperationStatus,
  CapabilitySet,
} from "@multica/core/types";
import { DEFAULT_VERCEL_CAPABILITIES } from "@multica/core/types";
import { useT } from "../../i18n";

export function repositoryCommitUrl(
  repository: string | undefined,
  sha: string | undefined,
): string | undefined {
  if (!repository || !sha) return undefined;
  const value = repository.trim();
  const sshMatch = value.match(/^git@github\.com:(.+)$/i);
  const candidate = sshMatch
    ? `https://github.com/${sshMatch[1]}`
    : /^https?:\/\//i.test(value)
      ? value
      : `https://${value}`;
  try {
    const parsed = new URL(candidate);
    if (parsed.hostname.toLowerCase() !== "github.com" && parsed.hostname.toLowerCase() !== "www.github.com") {
      return undefined;
    }
    const path = parsed.pathname.replace(/^\/+|\/+$/g, "").replace(/\.git$/, "");
    if (path.split("/").length !== 2) return undefined;
    return `https://github.com/${path}/commit/${encodeURIComponent(sha)}`;
  } catch {
    return undefined;
  }
}

function vercelProjectDashboardUrl(inspectorUrl: string | undefined): string | undefined {
  if (!inspectorUrl) return undefined;
  try {
    const parsed = new URL(inspectorUrl);
    if (parsed.hostname !== "vercel.com") return undefined;
    const segments = parsed.pathname.split("/").filter(Boolean);
    if (segments.length < 2) return undefined;
    const projectName = decodeURIComponent(segments[1]!).replace(/\s+\((?:production|preview|staging)\)$/i, "");
    return `https://vercel.com/${segments[0]}/${encodeURIComponent(projectName)}`;
  } catch {
    return undefined;
  }
}

export interface DeploymentDetailCardProps {
  operation: DeploymentOperation;
  target?: DeploymentTarget;
  capabilities?: CapabilitySet;
  onTriggerRedeploy?: (target: DeploymentTarget) => void;
  onPromote?: (
    operation: DeploymentOperation,
    target?: DeploymentTarget,
  ) => void;
  onRollback?: (
    operation: DeploymentOperation,
    target?: DeploymentTarget,
  ) => void;
  onCancel?: (operation: DeploymentOperation) => void;
  onViewLogs?: (operation: DeploymentOperation) => void;
  className?: string;
}

export function DeploymentDetailCard({
  operation,
  target,
  capabilities,
  onTriggerRedeploy,
  onPromote,
  onRollback,
  onCancel,
  onViewLogs,
  className,
}: DeploymentDetailCardProps) {
  const { t } = useT("projects");

  const status = operation.status;
  const shortSha = operation.request.commit_sha
    ? operation.request.commit_sha.slice(0, 7)
    : undefined;
  const gitRef = operation.request.git_ref;
  const commitMessage = operation.request.commit_message;
  const environment =
    operation.request.environment || target?.environment || "preview";
  const targetName =
    operation.request.target_name ||
    target?.name ||
    t(($) => $.deployments.default_target_name);

  // Effective capabilities (fallback to DEFAULT_VERCEL_CAPABILITIES)
  const effectiveCapabilities: CapabilitySet =
    capabilities ?? target?.capabilities ?? DEFAULT_VERCEL_CAPABILITIES;

  const isSuccessful = status === "ready" || status === "succeeded";
  const isActive = [
    "queued",
    "dispatching",
    "building",
    "deploying",
    "running",
  ].includes(status);

  const canPromote =
    effectiveCapabilities.promote !== false &&
    environment !== "production" &&
    isSuccessful;

  const canRollback =
    effectiveCapabilities.rollback !== false &&
    environment === "production" &&
    isSuccessful;

  const canCancel = effectiveCapabilities.cancel !== false && isActive;

  // External Links & Source Revision
  const rawDeploymentUrl =
    operation.evidence?.deployment_url || operation.evidence?.url;
  const deploymentUrl = rawDeploymentUrl
    ? /^https?:\/\//i.test(rawDeploymentUrl)
      ? rawDeploymentUrl
      : `https://${rawDeploymentUrl}`
    : undefined;
  const dashboardUrl =
    vercelProjectDashboardUrl(operation.evidence?.inspector_url) ||
    (target?.config?.project_name
      ? target.config.team_slug
        ? `https://vercel.com/${target.config.team_slug}/${target.config.project_name}`
        : undefined
      : undefined);
  const sourceRevisionUrl =
    repositoryCommitUrl(target?.config?.repository, operation.request.commit_sha) ||
    operation.evidence?.inspector_url;

  const getStatusBadge = (s: DeploymentOperationStatus) => {
    switch (s) {
      case "ready":
      case "succeeded":
        return (
          <Badge
            variant="secondary"
            className="flex items-center gap-1 border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300"
            data-testid="status-badge-succeeded"
          >
            <CheckCircle2 className="size-3 text-emerald-600 dark:text-emerald-400" />
            <span>
              {s === "ready"
                ? t(($) => $.deployments.status_ready)
                : t(($) => $.deployments.status_succeeded)}
            </span>
          </Badge>
        );
      case "failed":
        return (
          <Badge
            variant="destructive"
            className="flex items-center gap-1"
            data-testid="status-badge-failed"
          >
            <XCircle className="size-3" />
            <span>{t(($) => $.deployments.status_failed)}</span>
          </Badge>
        );
      case "canceled":
        return (
          <Badge
            variant="outline"
            className="flex items-center gap-1 text-muted-foreground"
            data-testid="status-badge-canceled"
          >
            <AlertTriangle className="size-3" />
            <span>{t(($) => $.deployments.status_canceled)}</span>
          </Badge>
        );
      case "building":
      case "deploying":
      case "dispatching":
      case "running":
        return (
          <Badge
            variant="secondary"
            className="flex items-center gap-1 border-blue-500/30 bg-blue-500/10 text-blue-700 dark:text-blue-300"
            data-testid="status-badge-running"
          >
            <Spinner className="size-3 text-blue-600 dark:text-blue-400" />
            <span>
              {s === "building"
                ? t(($) => $.deployments.status_building)
                : s === "deploying"
                  ? t(($) => $.deployments.status_deploying)
                  : s === "dispatching"
                    ? t(($) => $.deployments.status_dispatching)
                    : t(($) => $.deployments.status_running)}
            </span>
          </Badge>
        );
      case "queued":
      case "retry_wait":
      default:
        return (
          <Badge
            variant="outline"
            className="flex items-center gap-1 text-muted-foreground"
            data-testid="status-badge-queued"
          >
            <Clock className="size-3" />
            <span>{t(($) => $.deployments.status_queued)}</span>
          </Badge>
        );
    }
  };

  const getEnvironmentBadge = (env: string) => {
    switch (env) {
      case "production":
        return (
          <Badge
            variant="secondary"
            className="border-red-500/30 bg-red-500/10 text-red-700 dark:text-red-300 font-semibold"
            data-testid="environment-badge-production"
          >
            {t(($) => $.deployments.env_production)}
          </Badge>
        );
      case "staging":
        return (
          <Badge
            variant="secondary"
            className="border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300"
            data-testid="environment-badge-staging"
          >
            {t(($) => $.deployments.env_staging)}
          </Badge>
        );
      case "preview":
      default:
        return (
          <Badge
            variant="outline"
            className="text-muted-foreground"
            data-testid="environment-badge-preview"
          >
            {t(($) => $.deployments.env_preview)}
          </Badge>
        );
    }
  };

  return (
    <Card className={className} data-testid="deployment-detail-card">
      <CardHeader className="flex flex-row items-center justify-between pb-2">
        <div className="flex items-center gap-2">
          <Server className="size-4 text-muted-foreground" />
          <span className="text-sm font-semibold text-foreground">
            {targetName}
          </span>
          {getEnvironmentBadge(environment)}
        </div>
        <div className="flex items-center gap-2">{getStatusBadge(status)}</div>
      </CardHeader>

      <CardContent className="flex flex-col gap-3 pt-0">
        {/* Commit & Branch Details */}
        <div className="flex flex-wrap items-center gap-3 text-xs text-muted-foreground">
          {shortSha ? (
            <a
              href={sourceRevisionUrl}
              target="_blank"
              rel="noopener noreferrer"
              className="flex items-center gap-1 font-mono font-medium text-foreground bg-muted hover:bg-muted/80 px-1.5 py-0.5 rounded transition-colors"
              data-testid="deployment-source-revision-link"
              title={operation.request.commit_sha}
            >
              <GitCommit className="size-3 text-muted-foreground" />
              <span data-testid="deployment-commit-sha">{shortSha}</span>
              <ExternalLink className="size-2.5 text-muted-foreground ml-0.5" />
            </a>
          ) : null}

          {gitRef ? (
            <span
              className="flex items-center gap-1 font-mono text-muted-foreground"
              data-testid="deployment-git-ref"
            >
              <GitBranch className="size-3" />
              {gitRef}
            </span>
          ) : null}

          {commitMessage ? (
            <span
              className="line-clamp-1 italic text-muted-foreground max-w-xs"
              data-testid="deployment-commit-message"
            >
              &ldquo;{commitMessage}&rdquo;
            </span>
          ) : null}
        </div>

        {/* Error Details if Failed */}
        {operation.last_error || operation.evidence?.error ? (
          <div
            className="rounded-md border border-destructive/30 bg-destructive/10 p-2.5 text-xs text-destructive"
            data-testid="deployment-error-message"
          >
            {operation.last_error || operation.evidence?.error}
          </div>
        ) : null}

        {/* External Links & Actions */}
        <div className="flex flex-wrap items-center justify-between gap-2 border-t pt-3">
          <div className="flex flex-wrap items-center gap-2">
            {/* External Vercel Deployment URL */}
            {deploymentUrl ? (
              <a
                href={deploymentUrl}
                target="_blank"
                rel="noopener noreferrer"
                data-testid="external-deployment-url"
                className={cn(
                  buttonVariants({ variant: "outline", size: "sm" }),
                  "h-7 gap-1.5 text-xs font-normal",
                )}
              >
                <Globe className="size-3.5 text-primary" />
                <span>{t(($) => $.deployments.view_deployment)}</span>
                <ExternalLink className="size-3 text-muted-foreground" />
              </a>
            ) : null}

            {/* External Vercel Dashboard / Inspector URL */}
            {dashboardUrl ? (
              <a
                href={dashboardUrl}
                target="_blank"
                rel="noopener noreferrer"
                data-testid="external-dashboard-url"
                className={cn(
                  buttonVariants({ variant: "ghost", size: "sm" }),
                  "h-7 gap-1.5 text-xs font-normal text-muted-foreground hover:text-foreground",
                )}
              >
                <ExternalLink className="size-3" />
                <span>{t(($) => $.deployments.view_dashboard)}</span>
              </a>
            ) : null}
          </div>

          <div className="flex flex-wrap items-center gap-2">
            {effectiveCapabilities.logs === true &&
            operation.provider_operation_id &&
            onViewLogs ? (
              <Button
                variant="ghost"
                size="sm"
                onClick={() => onViewLogs(operation)}
                className="h-7 gap-1.5 text-xs font-normal text-muted-foreground hover:text-foreground"
                data-testid="deployment-logs-button"
              >
                <Server className="size-3" />
                <span>{t(($) => $.deployments.view_logs)}</span>
              </Button>
            ) : null}

            {/* Cancel Action (if active and supported) */}
            {canCancel ? (
              <Button
                variant="destructive"
                size="sm"
                onClick={() => onCancel?.(operation)}
                className="h-7 text-xs gap-1.5"
                data-testid="deployment-cancel-button"
              >
                <StopCircle className="size-3" />
                <span>{t(($) => $.deployments.cancel) ?? "Cancel"}</span>
              </Button>
            ) : null}

            {/* Promote Action (if preview/staging succeeded and supported) */}
            {canPromote ? (
              <Button
                variant="outline"
                size="sm"
                onClick={() => onPromote?.(operation, target)}
                className="h-7 text-xs gap-1.5"
                data-testid="deployment-promote-button"
              >
                <ArrowUpRight className="size-3 text-primary" />
                <span>
                  {t(($) => $.deployments.promote) ?? "Promote to Production"}
                </span>
              </Button>
            ) : null}

            {/* Rollback Action (if production succeeded and supported) */}
            {canRollback ? (
              <Button
                variant="outline"
                size="sm"
                onClick={() => onRollback?.(operation, target)}
                className="h-7 text-xs gap-1.5 text-amber-700 dark:text-amber-300 border-amber-500/30 hover:bg-amber-500/10"
                data-testid="deployment-rollback-button"
              >
                <RotateCcw className="size-3" />
                <span>{t(($) => $.deployments.rollback) ?? "Rollback"}</span>
              </Button>
            ) : null}

            {/* Trigger / Redeploy Action */}
            {target && onTriggerRedeploy ? (
              <Button
                variant="outline"
                size="sm"
                onClick={() => onTriggerRedeploy(target)}
                className="h-7 text-xs"
                data-testid="trigger-redeploy-button"
              >
                <RefreshCw className="mr-1.5 size-3" />
                {environment === "production"
                  ? t(($) => $.deployments.deploy_prod)
                  : t(($) => $.deployments.deploy_preview)}
              </Button>
            ) : null}
          </div>
        </div>
      </CardContent>
    </Card>
  );
}
