"use client";

import { useQuery } from "@tanstack/react-query";
import { FolderKanban, Plus } from "lucide-react";
import { projectListOptions } from "@multica/core/projects";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { useModalStore } from "@multica/core/modals";
import { Button } from "@multica/ui/components/ui/button";
import { AppLink } from "../../navigation";
import {
  CollectionPageHeader,
  CollectionPageHeaderAction,
  CollectionPageState,
} from "../../layout/collection-page";
import { useT } from "../../i18n";
import { ProjectIcon } from "./project-icon";

export function ProjectDashboardPage() {
  const { t } = useT("projects");
  const wsId = useWorkspaceId();
  const wsPaths = useWorkspacePaths();
  const { data: projects = [], isLoading } = useQuery(projectListOptions(wsId));
  const openCreateProject = () => useModalStore.getState().open("create-project");

  const issueCount = projects.reduce(
    (total, project) => total + project.issue_count,
    0,
  );
  const doneCount = projects.reduce(
    (total, project) => total + project.done_count,
    0,
  );
  const stats = [
    { label: t(($) => $.page.title), value: projects.length },
    { label: t(($) => $.table.issues), value: issueCount },
    { label: t(($) => $.status.completed), value: doneCount },
  ];

  return (
    <div className="flex h-full min-h-0 flex-col">
      <CollectionPageHeader
        icon={FolderKanban}
        title={t(($) => $.page.title)}
        count={projects.length}
        actions={
          <CollectionPageHeaderAction
            icon={Plus}
            label={t(($) => $.page.new_project)}
            onClick={openCreateProject}
          />
        }
      />

      <div className="flex-1 overflow-y-auto">
        <div className="mx-auto max-w-6xl space-y-6 p-6">
          {isLoading ? (
            <div className="grid grid-cols-1 divide-y rounded-lg border bg-card sm:grid-cols-3 sm:divide-x sm:divide-y-0">
              {[0, 1, 2].map((item) => (
                <div key={item} className="h-20 animate-pulse bg-muted/40" />
              ))}
            </div>
          ) : projects.length === 0 ? (
            <CollectionPageState
              icon={FolderKanban}
              title={t(($) => $.page.empty)}
              actions={
                <Button size="sm" variant="outline" onClick={openCreateProject}>
                  {t(($) => $.page.create_first)}
                </Button>
              }
            />
          ) : (
            <>
              <dl className="grid grid-cols-1 divide-y rounded-lg border bg-card sm:grid-cols-3 sm:divide-x sm:divide-y-0">
                {stats.map((stat) => (
                  <div key={stat.label} className="px-4 py-3">
                    <dt className="text-caption text-muted-foreground">{stat.label}</dt>
                    <dd className="mt-1 text-title font-semibold tabular-nums">{stat.value}</dd>
                  </div>
                ))}
              </dl>

              <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
                {projects.map((project) => (
                  <AppLink
                    key={project.id}
                    href={wsPaths.projectDetail(project.id)}
                    className="group rounded-lg border bg-card p-4 transition-colors hover:border-primary/50"
                  >
                    <div className="flex items-center gap-2">
                      <ProjectIcon project={project} />
                      <span className="min-w-0 flex-1 truncate text-body font-medium">
                        {project.title}
                      </span>
                      <span className="shrink-0 text-caption text-muted-foreground">
                        {t(($) => $.status[project.status])}
                      </span>
                    </div>
                    <div className="mt-4 flex items-baseline justify-between gap-2">
                      <span className="text-caption text-muted-foreground">
                        {t(($) => $.table.issues)}
                      </span>
                      <span className="text-body tabular-nums">
                        {project.done_count}/{project.issue_count}
                      </span>
                    </div>
                  </AppLink>
                ))}
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  );
}
