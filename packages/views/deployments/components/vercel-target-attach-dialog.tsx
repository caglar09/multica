"use client";

import { useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Button } from "@multica/ui/components/ui/button";
import { Spinner } from "@multica/ui/components/ui/spinner";
import type {
  CreateDeploymentTargetInput,
  DeploymentEnvironment,
  DeploymentRecipe,
  DiscoveredVercelProject,
  VercelTargetConfig,
} from "@multica/core/types";
import { analyzeRepository } from "@multica/core/deployments/recipes";
import type { RepositoryMetadata } from "@multica/core/deployments/recipes";
import { useT } from "../../i18n";
import { VercelProjectSelect } from "./vercel-project-select";
import { DeploymentRecipeSuggestions } from "./deployment-recipe-suggestions";
import { TargetEnvironmentConfig } from "./target-environment-config";

export interface VercelTargetAttachDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  connectionId: string;
  projectId?: string;
  repository?: string;
  repositoryMetadata?: RepositoryMetadata;
  discoveredProjects?: DiscoveredVercelProject[];
  defaultEnvironment?: DeploymentEnvironment;
  defaultBranch?: string;
  onSubmitTarget: (input: CreateDeploymentTargetInput) => Promise<void> | void;
}

export function VercelTargetAttachDialog({
  open,
  onOpenChange,
  connectionId,
  projectId,
  repository,
  repositoryMetadata,
  discoveredProjects = [],
  defaultEnvironment = "production",
  defaultBranch,
  onSubmitTarget,
}: VercelTargetAttachDialogProps) {
  const { t } = useT("projects");

  // Project Selection State
  const [selectedProjectId, setSelectedProjectId] = useState<string | null>(
    discoveredProjects.length > 0 ? (discoveredProjects[0]?.id ?? null) : null,
  );
  const [newProjectName, setNewProjectName] = useState("");
  const [isCreatingNew, setIsCreatingNew] = useState(false);
  const [isConfirmedNew, setIsConfirmedNew] = useState(false);

  // Recipe & Build Settings State. Analysis is pure and provider-independent.
  const repositoryAnalysis = useMemo(
    () => analyzeRepository(repositoryMetadata),
    [repositoryMetadata],
  );
  const initialRecipe = repositoryAnalysis.recipe;
  const [selectedFramework, setSelectedFramework] = useState<string>(
    initialRecipe.framework,
  );
  const [config, setConfig] = useState<VercelTargetConfig>({
    framework: initialRecipe.framework,
    root_directory: repositoryAnalysis.rootDirectory,
    build_command: initialRecipe.defaultBuildCommand,
    output_directory: initialRecipe.defaultOutputDir,
    install_command: initialRecipe.defaultInstallCommand,
    dev_command: initialRecipe.defaultDevCommand,
    environment_variable_names: [
      ...repositoryAnalysis.environmentVariableNames,
    ],
  });

  // Target & Environment State
  const [targetName, setTargetName] = useState("");
  const [environment, setEnvironment] =
    useState<DeploymentEnvironment>(defaultEnvironment);
  const [gitBranch, setGitBranch] = useState(defaultBranch ?? "");

  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    setSelectedFramework(repositoryAnalysis.recipe.framework);
    setConfig((previous) => ({
      ...previous,
      framework: repositoryAnalysis.recipe.framework,
      root_directory: repositoryAnalysis.rootDirectory,
      build_command: repositoryAnalysis.recipe.defaultBuildCommand,
      output_directory: repositoryAnalysis.recipe.defaultOutputDir,
      install_command: repositoryAnalysis.recipe.defaultInstallCommand,
      dev_command: repositoryAnalysis.recipe.defaultDevCommand,
      environment_variable_names: [
        ...repositoryAnalysis.environmentVariableNames,
      ],
    }));
  }, [repositoryAnalysis]);

  // Selected project helper
  const selectedProject = discoveredProjects.find(
    (p) => p.id === selectedProjectId,
  );

  const handleSelectProject = (project: DiscoveredVercelProject | null) => {
    setSelectedProjectId(project?.id ?? null);
    if (project) {
      if (!targetName) {
        setTargetName(`${project.name} (${environment})`);
      }
    }
  };

  const handleSelectRecipe = (recipe: DeploymentRecipe) => {
    setSelectedFramework(recipe.framework);
  };

  const isFormValid = () => {
    if (!targetName.trim()) return false;
    if (isCreatingNew) {
      return newProjectName.trim().length > 0 && isConfirmedNew;
    }
    return selectedProjectId !== null;
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!isFormValid() || submitting) return;

    setSubmitting(true);
    try {
      const projectName = isCreatingNew
        ? newProjectName.trim()
        : selectedProject?.name || targetName;

      const targetInput: CreateDeploymentTargetInput = {
        connection_id: connectionId,
        project_id: projectId,
        name: targetName.trim(),
        environment,
        external_id: isCreatingNew ? null : selectedProjectId,
        create_new: isCreatingNew,
        confirmed: isCreatingNew && isConfirmedNew,
        config: {
          ...config,
          project_name: projectName,
          repository: repository || config.repository,
          default_branch: gitBranch.trim(),
          project_id: isCreatingNew
            ? undefined
            : (selectedProjectId ?? undefined),
        },
      };

      await onSubmitTarget(targetInput);
      onOpenChange(false);
    } catch (err) {
      toast.error(
        err instanceof Error
          ? err.message
          : t(($) => $.deployments.attach_error),
      );
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (!submitting) onOpenChange(v);
      }}
    >
      <DialogContent
        className="max-h-[90vh] overflow-y-auto sm:max-w-2xl"
        data-testid="vercel-target-attach-dialog"
      >
        <form onSubmit={handleSubmit} className="flex flex-col gap-6">
          <DialogHeader>
            <DialogTitle data-testid="attach-dialog-title">
              {t(($) => $.deployments.attach_vercel)}
            </DialogTitle>
            <DialogDescription>
              {t(($) => $.deployments.recipe_subtitle)}
            </DialogDescription>
          </DialogHeader>

          {/* Section 1: Project Selection */}
          <div className="flex flex-col gap-2">
            <VercelProjectSelect
              projects={discoveredProjects}
              selectedProjectId={selectedProjectId}
              onSelectProject={handleSelectProject}
              newProjectName={newProjectName}
              onChangeNewProjectName={setNewProjectName}
              isCreatingNew={isCreatingNew}
              onToggleCreatingNew={setIsCreatingNew}
              isConfirmedNew={isConfirmedNew}
              onConfirmNew={setIsConfirmedNew}
              disabled={submitting}
            />
          </div>

          {/* Section 2: Recipe & Build Settings Suggestions */}
          <div className="flex flex-col gap-2 border-t pt-4">
            <DeploymentRecipeSuggestions
              selectedFramework={selectedFramework}
              onSelectRecipe={handleSelectRecipe}
              config={config}
              onChangeConfig={setConfig}
              analysis={repositoryAnalysis}
              disabled={submitting}
            />
          </div>

          {/* Section 3: Target & Environment Configuration */}
          <div className="flex flex-col gap-2 border-t pt-4">
            <TargetEnvironmentConfig
              targetName={targetName}
              onChangeTargetName={setTargetName}
              environment={environment}
              onChangeEnvironment={setEnvironment}
              gitBranch={gitBranch}
              onChangeGitBranch={setGitBranch}
              disabled={submitting}
            />
          </div>

          <DialogFooter className="border-t pt-4">
            <Button
              type="button"
              variant="outline"
              disabled={submitting}
              onClick={() => onOpenChange(false)}
              data-testid="attach-dialog-cancel"
            >
              {t(($) => $.deployments.cancel)}
            </Button>
            <Button
              type="submit"
              disabled={!isFormValid() || submitting}
              data-testid="attach-dialog-submit"
            >
              {submitting ? (
                <Spinner className="size-4" />
              ) : (
                t(($) => $.deployments.link_target)
              )}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
