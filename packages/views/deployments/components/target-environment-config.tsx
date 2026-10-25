"use client";

import { ShieldAlert, GitBranch, Layers } from "lucide-react";
import type { DeploymentEnvironment } from "@multica/core/types";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Button } from "@multica/ui/components/ui/button";
import {
  Alert,
  AlertDescription,
  AlertTitle,
} from "@multica/ui/components/ui/alert";
import { useT } from "../../i18n";

export interface TargetEnvironmentConfigProps {
  targetName: string;
  onChangeTargetName: (name: string) => void;
  environment: DeploymentEnvironment;
  onChangeEnvironment: (env: DeploymentEnvironment) => void;
  gitBranch: string;
  onChangeGitBranch: (branch: string) => void;
  disabled?: boolean;
}

export function TargetEnvironmentConfig({
  targetName,
  onChangeTargetName,
  environment,
  onChangeEnvironment,
  gitBranch,
  onChangeGitBranch,
  disabled = false,
}: TargetEnvironmentConfigProps) {
  const { t } = useT("projects");

  const environments: Array<{
    id: DeploymentEnvironment;
    label: string;
    description: string;
  }> = [
    {
      id: "preview",
      label: t(($) => $.deployments.env_preview),
      description: "Ephemeral deployments for feature branches and PR previews",
    },
    {
      id: "staging",
      label: t(($) => $.deployments.env_staging),
      description: "Pre-release environments for team integration testing",
    },
    {
      id: "production",
      label: t(($) => $.deployments.env_production),
      description: "Live environment serving external user traffic",
    },
  ];

  return (
    <div className="flex flex-col gap-4">
      {/* Target Name */}
      <div className="flex flex-col gap-1.5">
        <Label
          htmlFor="target-name-input"
          className="text-xs font-medium text-foreground"
        >
          {t(($) => $.deployments.target_name)}
        </Label>
        <Input
          id="target-name-input"
          value={targetName}
          onChange={(e) => onChangeTargetName(e.target.value)}
          placeholder={t(($) => $.deployments.target_name_placeholder)}
          disabled={disabled}
          className="text-sm"
          data-testid="target-name-input"
        />
      </div>

      {/* Environment Selection */}
      <div className="flex flex-col gap-2">
        <div className="flex items-center gap-1.5">
          <Layers className="size-3.5 text-muted-foreground" />
          <Label className="text-xs font-medium text-foreground">
            {t(($) => $.deployments.environment)}
          </Label>
        </div>
        <div
          className="grid grid-cols-1 gap-2 sm:grid-cols-3"
          data-testid="environment-selector-grid"
        >
          {environments.map((env) => {
            const isSelected = environment === env.id;
            return (
              <Button
                key={env.id}
                type="button"
                variant={isSelected ? "default" : "outline"}
                size="sm"
                disabled={disabled}
                onClick={() => onChangeEnvironment(env.id)}
                className={`flex h-auto flex-col items-start gap-1 p-2.5 text-left transition-all ${
                  isSelected ? "border-primary" : "hover:border-border/80"
                }`}
                data-testid={`env-card-${env.id}`}
              >
                <span className="text-xs font-semibold">{env.label}</span>
                <span className="text-[10px] text-muted-foreground font-normal line-clamp-2">
                  {env.description}
                </span>
              </Button>
            );
          })}
        </div>
      </div>

      {/* Git Branch / Reference */}
      <div className="flex flex-col gap-1.5">
        <div className="flex items-center gap-1.5">
          <GitBranch className="size-3.5 text-muted-foreground" />
          <Label
            htmlFor="target-git-branch-input"
            className="text-xs font-medium text-foreground"
          >
            {t(($) => $.deployments.git_branch)}
          </Label>
        </div>
        <Input
          id="target-git-branch-input"
          value={gitBranch}
          onChange={(e) => onChangeGitBranch(e.target.value)}
          placeholder={t(($) => $.deployments.git_branch_placeholder)}
          disabled={disabled}
          className="font-mono text-xs"
          data-testid="git-branch-input"
        />
      </div>

      {/* Production Guard Warning */}
      {environment === "production" ? (
        <Alert
          variant="destructive"
          className="border-red-500/30 bg-red-500/10 text-red-950 dark:text-red-200"
          data-testid="production-environment-guard-warning"
        >
          <ShieldAlert className="size-4 text-red-600 dark:text-red-400" />
          <div>
            <AlertTitle className="text-xs font-semibold">
              {t(($) => $.deployments.prod_auth_title)}
            </AlertTitle>
            <AlertDescription className="text-xs text-red-800 dark:text-red-300">
              {t(($) => $.deployments.prod_warning)}
            </AlertDescription>
          </div>
        </Alert>
      ) : null}
    </div>
  );
}
