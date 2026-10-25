"use client";

import { useState } from "react";
import { Check, FolderPlus, Globe, Search, AlertTriangle } from "lucide-react";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Badge } from "@multica/ui/components/ui/badge";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import {
  Alert,
  AlertDescription,
  AlertTitle,
} from "@multica/ui/components/ui/alert";
import { Button } from "@multica/ui/components/ui/button";
import type { DiscoveredVercelProject } from "@multica/core/types";
import { useT } from "../../i18n";

export interface VercelProjectSelectProps {
  projects: DiscoveredVercelProject[];
  selectedProjectId: string | null;
  onSelectProject: (project: DiscoveredVercelProject | null) => void;
  newProjectName: string;
  onChangeNewProjectName: (name: string) => void;
  isCreatingNew: boolean;
  onToggleCreatingNew: (creating: boolean) => void;
  isConfirmedNew: boolean;
  onConfirmNew: (confirmed: boolean) => void;
  disabled?: boolean;
}

export function VercelProjectSelect({
  projects,
  selectedProjectId,
  onSelectProject,
  newProjectName,
  onChangeNewProjectName,
  isCreatingNew,
  onToggleCreatingNew,
  isConfirmedNew,
  onConfirmNew,
  disabled = false,
}: VercelProjectSelectProps) {
  const { t } = useT("projects");
  const [search, setSearch] = useState("");

  const filteredProjects = projects.filter((p) =>
    p.name.toLowerCase().includes(search.toLowerCase().trim()),
  );

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <Label className="text-sm font-medium">
          {t(($) => $.deployments.select_project)}
        </Label>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          disabled={disabled}
          onClick={() => {
            const next = !isCreatingNew;
            onToggleCreatingNew(next);
            if (next) {
              onSelectProject(null);
            }
          }}
          className="text-xs text-muted-foreground hover:text-foreground"
          data-testid="toggle-create-project-mode"
        >
          {isCreatingNew ? (
            <>
              <Globe className="mr-1.5 size-3.5" />
              {t(($) => $.deployments.select_existing_toggle)}
            </>
          ) : (
            <>
              <FolderPlus className="mr-1.5 size-3.5" />
              {t(($) => $.deployments.create_new_toggle)}
            </>
          )}
        </Button>
      </div>

      {!isCreatingNew ? (
        <div className="flex flex-col gap-2.5">
          <div className="relative">
            <Search className="absolute left-2.5 top-2.5 size-4 text-muted-foreground" />
            <Input
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder={t(($) => $.deployments.search_projects)}
              disabled={disabled}
              className="pl-8 text-sm"
              data-testid="search-vercel-projects"
            />
          </div>

          <div
            className="flex max-h-52 flex-col divide-y overflow-y-auto rounded-md border bg-card"
            role="listbox"
            aria-label={t(($) => $.deployments.existing_projects)}
          >
            {filteredProjects.length === 0 ? (
              <div className="py-6 text-center text-xs text-muted-foreground">
                {t(($) => $.deployments.no_targets)}
              </div>
            ) : (
              filteredProjects.map((project) => {
                const isSelected = selectedProjectId === project.id;
                return (
                  <button
                    key={project.id}
                    type="button"
                    role="option"
                    aria-selected={isSelected}
                    disabled={disabled}
                    onClick={() => onSelectProject(project)}
                    className={`flex items-center justify-between px-3 py-2.5 text-left text-sm transition-colors hover:bg-muted/50 ${
                      isSelected ? "bg-muted/70 font-medium" : ""
                    }`}
                    data-testid={`vercel-project-item-${project.id}`}
                  >
                    <div className="flex flex-col gap-0.5">
                      <div className="flex items-center gap-2">
                        <span className="text-foreground">{project.name}</span>
                        {project.framework ? (
                          <Badge variant="secondary" className="text-[10px]">
                            {project.framework}
                          </Badge>
                        ) : null}
                      </div>
                      {project.latest_deployment_url ? (
                        <span className="text-xs text-muted-foreground">
                          {project.latest_deployment_url}
                        </span>
                      ) : null}
                    </div>
                    {isSelected ? (
                      <Check
                        className="size-4 text-primary"
                        data-testid="selected-project-check"
                      />
                    ) : null}
                  </button>
                );
              })
            )}
          </div>
        </div>
      ) : (
        <div
          className="flex flex-col gap-3 rounded-lg border bg-card p-4"
          data-testid="new-project-creation-container"
        >
          <div className="flex flex-col gap-1.5">
            <Label
              htmlFor="new-vercel-project-input"
              className="text-xs font-medium text-foreground"
            >
              {t(($) => $.deployments.new_project_name)}
            </Label>
            <Input
              id="new-vercel-project-input"
              value={newProjectName}
              onChange={(e) => {
                onChangeNewProjectName(e.target.value);
                onConfirmNew(false);
              }}
              placeholder={t(($) => $.deployments.new_project_placeholder)}
              disabled={disabled}
              className="font-mono text-sm"
              data-testid="new-vercel-project-input"
            />
          </div>

          {newProjectName.trim() ? (
            <Alert className="border-amber-500/30 bg-amber-500/5 text-amber-950 dark:text-amber-200">
              <AlertTriangle className="size-4 text-amber-600 dark:text-amber-400" />
              <div className="flex flex-col gap-2">
                <AlertTitle className="text-xs font-semibold">
                  {t(($) => $.deployments.confirm_create_title)}
                </AlertTitle>
                <AlertDescription className="text-xs text-muted-foreground">
                  {t(($) => $.deployments.confirm_create_desc)}
                </AlertDescription>
                <div className="mt-1 flex items-center space-x-2">
                  <Checkbox
                    id="confirm-new-project"
                    checked={isConfirmedNew}
                    onCheckedChange={(checked) =>
                      onConfirmNew(checked === true)
                    }
                    disabled={disabled || !newProjectName.trim()}
                    data-testid="confirm-new-project-checkbox"
                  />
                  <label
                    htmlFor="confirm-new-project"
                    className="cursor-pointer text-xs font-medium text-foreground select-none"
                  >
                    {t(($) => $.deployments.confirm_new_checkbox)}
                  </label>
                </div>
              </div>
            </Alert>
          ) : null}
        </div>
      )}
    </div>
  );
}
