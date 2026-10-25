"use client";

import { Sparkles, SlidersHorizontal } from "lucide-react";
import { FRAMEWORK_RECIPES } from "@multica/core/deployments/recipes";
import type { RepositoryAnalysis } from "@multica/core/deployments/recipes";
import type { DeploymentRecipe } from "@multica/core/types";
import type { VercelTargetConfig } from "@multica/core/types";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n";

export interface DeploymentRecipeSuggestionsProps {
  selectedFramework: string;
  onSelectRecipe: (recipe: DeploymentRecipe) => void;
  config: VercelTargetConfig;
  onChangeConfig: (newConfig: VercelTargetConfig) => void;
  analysis?: RepositoryAnalysis;
  disabled?: boolean;
}

export function DeploymentRecipeSuggestions({
  selectedFramework,
  onSelectRecipe,
  config,
  onChangeConfig,
  analysis,
  disabled = false,
}: DeploymentRecipeSuggestionsProps) {
  const { t } = useT("projects");

  const handleRecipeClick = (recipe: DeploymentRecipe) => {
    onSelectRecipe(recipe);
    onChangeConfig({
      ...config,
      framework: recipe.framework,
      root_directory: config.root_directory || recipe.defaultRootDir,
      build_command: recipe.defaultBuildCommand,
      output_directory: recipe.defaultOutputDir,
      install_command: recipe.defaultInstallCommand,
      dev_command: recipe.defaultDevCommand,
    });
  };

  const handleFieldChange = (
    field: keyof VercelTargetConfig,
    value: string,
  ) => {
    onChangeConfig({
      ...config,
      [field]: value,
    });
  };

  return (
    <div className="flex flex-col gap-4">
      {/* Recipe presets suggestion header & pills */}
      <div className="flex flex-col gap-2">
        <div className="flex items-center gap-1.5">
          <Sparkles className="size-4 text-primary" />
          <Label className="text-sm font-medium">
            {t(($) => $.deployments.recipe_title)}
          </Label>
        </div>
        <p className="text-xs text-muted-foreground">
          {t(($) => $.deployments.recipe_subtitle)}
        </p>
        {analysis ? (
          <div className="flex flex-col gap-2" data-testid="repository-analysis">
            <p className="text-xs text-muted-foreground">
              Detected <strong>{analysis.recipe.name}</strong> in
              <code className="ml-1">{analysis.rootDirectory}</code>.
            </p>
            {analysis.environmentVariableNames.length > 0 ? (
              <p
                className="text-xs text-muted-foreground"
                data-testid="environment-variable-names"
              >
                Required environment variable names: {analysis.environmentVariableNames.join(", ")}
              </p>
            ) : null}
            {analysis.warnings.length > 0 ? (
              <div
                role="alert"
                className="flex flex-col gap-1 rounded-md border border-amber-500/30 bg-amber-500/10 p-2 text-xs text-amber-900 dark:text-amber-200"
                data-testid="recipe-suitability-warnings"
              >
                <span className="font-medium">
                  Review Vercel suitability before saving.
                </span>
                {analysis.warnings.map((warning) => (
                  <span key={warning.code}>• {warning.message}</span>
                ))}
              </div>
            ) : null}
          </div>
        ) : null}

        {/* Clickable recipe suggestions */}
        <div
          className="grid grid-cols-2 gap-2 sm:grid-cols-4"
          data-testid="recipe-suggestions-grid"
        >
          {FRAMEWORK_RECIPES.map((recipe) => {
            const isSelected = selectedFramework === recipe.framework;
            return (
              <Button
                key={recipe.id}
                type="button"
                variant={isSelected ? "default" : "outline"}
                size="sm"
                disabled={disabled}
                onClick={() => handleRecipeClick(recipe)}
                className={`flex h-auto flex-col items-start gap-1 p-2.5 text-left transition-all ${
                  isSelected ? "border-primary" : "hover:border-border/80"
                }`}
                data-testid={`recipe-card-${recipe.id}`}
              >
                <div className="flex w-full items-center justify-between">
                  <span className="text-xs font-semibold">{recipe.name}</span>
                  {isSelected ? (
                    <Badge variant="secondary" className="px-1 text-[9px]">
                      {t(($) => $.deployments.recipe_selected)}
                    </Badge>
                  ) : null}
                </div>
                <span className="line-clamp-1 text-[10px] text-muted-foreground font-normal">
                  {recipe.defaultOutputDir || "No output dir"}
                </span>
              </Button>
            );
          })}
        </div>
      </div>

      {/* Editable Overrides Section */}
      <div className="rounded-lg border bg-card/60 p-4">
        <div className="mb-3 flex items-center gap-1.5 text-xs font-medium text-foreground">
          <SlidersHorizontal className="size-3.5 text-muted-foreground" />
          <span>{t(($) => $.deployments.build_settings)}</span>
        </div>

        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          {/* Framework preset */}
          <div className="flex flex-col gap-1.5">
            <Label
              htmlFor="target-framework-input"
              className="text-xs font-medium text-muted-foreground"
            >
              {t(($) => $.deployments.framework)}
            </Label>
            <Input
              id="target-framework-input"
              value={config.framework ?? ""}
              onChange={(e) => handleFieldChange("framework", e.target.value)}
              placeholder={t(($) => $.deployments.framework_placeholder)}
              disabled={disabled}
              className="font-mono text-xs"
              data-testid="framework-input"
            />
          </div>

          {/* Root Directory */}
          <div className="flex flex-col gap-1.5">
            <Label
              htmlFor="target-root-dir-input"
              className="text-xs font-medium text-muted-foreground"
            >
              {t(($) => $.deployments.root_directory)}
            </Label>
            <Input
              id="target-root-dir-input"
              value={config.root_directory ?? ""}
              onChange={(e) =>
                handleFieldChange("root_directory", e.target.value)
              }
              placeholder={t(($) => $.deployments.root_directory_placeholder)}
              disabled={disabled}
              className="font-mono text-xs"
              data-testid="root-dir-input"
            />
          </div>

          {/* Build Command */}
          <div className="flex flex-col gap-1.5">
            <Label
              htmlFor="target-build-command-input"
              className="text-xs font-medium text-muted-foreground"
            >
              {t(($) => $.deployments.build_command)}
            </Label>
            <Input
              id="target-build-command-input"
              value={config.build_command ?? ""}
              onChange={(e) =>
                handleFieldChange("build_command", e.target.value)
              }
              placeholder={t(($) => $.deployments.build_command_placeholder)}
              disabled={disabled}
              className="font-mono text-xs"
              data-testid="build-command-input"
            />
          </div>

          {/* Output Directory */}
          <div className="flex flex-col gap-1.5">
            <Label
              htmlFor="target-output-dir-input"
              className="text-xs font-medium text-muted-foreground"
            >
              {t(($) => $.deployments.output_directory)}
            </Label>
            <Input
              id="target-output-dir-input"
              value={config.output_directory ?? ""}
              onChange={(e) =>
                handleFieldChange("output_directory", e.target.value)
              }
              placeholder={t(($) => $.deployments.output_directory_placeholder)}
              disabled={disabled}
              className="font-mono text-xs"
              data-testid="output-dir-input"
            />
          </div>

          {/* Install Command */}
          <div className="flex flex-col gap-1.5">
            <Label
              htmlFor="target-install-command-input"
              className="text-xs font-medium text-muted-foreground"
            >
              {t(($) => $.deployments.install_command)}
            </Label>
            <Input
              id="target-install-command-input"
              value={config.install_command ?? ""}
              onChange={(e) =>
                handleFieldChange("install_command", e.target.value)
              }
              placeholder={t(($) => $.deployments.install_command_placeholder)}
              disabled={disabled}
              className="font-mono text-xs"
              data-testid="install-command-input"
            />
          </div>

          {/* Development Command */}
          <div className="flex flex-col gap-1.5">
            <Label
              htmlFor="target-dev-command-input"
              className="text-xs font-medium text-muted-foreground"
            >
              {t(($) => $.deployments.dev_command)}
            </Label>
            <Input
              id="target-dev-command-input"
              value={config.dev_command ?? ""}
              onChange={(e) => handleFieldChange("dev_command", e.target.value)}
              placeholder={t(($) => $.deployments.dev_command_placeholder)}
              disabled={disabled}
              className="font-mono text-xs"
              data-testid="dev-command-input"
            />
          </div>
        </div>
      </div>
    </div>
  );
}
