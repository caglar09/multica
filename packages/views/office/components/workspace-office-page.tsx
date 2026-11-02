"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Building2, Layers, Check } from "lucide-react";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  projectListOptions,
  autonomousProjectOptions,
  projectLeaderChatOptions,
  projectLeaderChangesOptions,
  useApproveAutonomousProjectPlan,
  useApproveProjectLeaderChange,
  useRejectProjectLeaderChange,
  useResolveAutonomousEscalation,
} from "@multica/core/projects";
import { memberListOptions } from "@multica/core/workspace/queries";
import { useAuthStore } from "@multica/core/auth";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import { Button } from "@multica/ui/components/ui/button";
import { BreadcrumbHeader } from "../../layout/breadcrumb-header";
import { useT } from "../../i18n";
import { ProjectVirtualOfficeView } from "./project-virtual-office-view";

export function WorkspaceOfficePage() {
  const { t } = useT("projects");
  const wsId = useWorkspaceId();
  const userId = useAuthStore((s) => s.user?.id);

  // Queries
  const { data: projects = [] } = useQuery(projectListOptions(wsId));
  const { data: members = [] } = useQuery(memberListOptions(wsId));

  // Floor Selection: "all" or specific projectId
  const [selectedProjectId, setSelectedProjectId] = useState<string | null>(null);

  // Autonomous Project details if floor is a specific project
  const { data: snapshot } = useQuery({
    ...autonomousProjectOptions(wsId, selectedProjectId ?? ""),
    enabled: Boolean(selectedProjectId),
  });

  const { data: leaderChanges } = useQuery({
    ...projectLeaderChangesOptions(wsId, selectedProjectId ?? ""),
    enabled: Boolean(selectedProjectId),
  });
  const { data: leaderChat } = useQuery({
    ...projectLeaderChatOptions(wsId, selectedProjectId ?? ""),
    enabled: Boolean(selectedProjectId && snapshot?.enabled),
  });

  // Mutations
  const approvePlan = useApproveAutonomousProjectPlan();
  const approveChange = useApproveProjectLeaderChange();
  const rejectChange = useRejectProjectLeaderChange();
  const resolveEscalation = useResolveAutonomousEscalation();

  const selectedProject = projects.find((p) => p.id === selectedProjectId);
  const isWorkspaceAdmin = members.some(
    (member) => member.user_id === userId && (member.role === "owner" || member.role === "admin"),
  );

  return (
    <div className="flex h-screen w-full flex-col overflow-hidden bg-background">
      {/* Breadcrumb Header with Integrated Floor Switcher */}
      <BreadcrumbHeader
        segments={[]}
        leaf={
          <div className="flex items-center gap-1.5 font-medium text-foreground">
            <Building2 className="size-4 text-primary" />
            <span>{t(($) => $.office.title)}</span>
          </div>
        }
        actions={
          <div className="flex items-center gap-2">
            <DropdownMenu>
              <DropdownMenuTrigger
                render={
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    className="h-7 gap-1.5 border-border/80 bg-background/80 text-xs font-medium shadow-xs"
                  >
                    <Layers className="size-3.5 text-muted-foreground" />
                    <span>
                      {selectedProject
                        ? selectedProject.title
                        : t(($) => $.office.floor_all)}
                    </span>
                  </Button>
                }
              />
              <DropdownMenuContent align="end" className="w-56">
                <DropdownMenuLabel className="text-caption text-muted-foreground">
                  {t(($) => $.office.select_floor)}
                </DropdownMenuLabel>
                <DropdownMenuItem
                  onClick={() => setSelectedProjectId(null)}
                  className="flex items-center justify-between text-xs"
                >
                  <span className="truncate">{t(($) => $.office.floor_all)}</span>
                  {selectedProjectId === null && (
                    <Check className="size-3.5 text-primary" />
                  )}
                </DropdownMenuItem>

                {projects.length > 0 && <DropdownMenuSeparator />}

                {projects.map((proj, idx) => (
                  <DropdownMenuItem
                    key={proj.id}
                    onClick={() => setSelectedProjectId(proj.id)}
                    className="flex items-center justify-between text-xs"
                  >
                    <span className="truncate">
                      {t(($) => $.office.floor_project, {
                        index: idx + 1,
                        name: proj.title,
                      })}
                    </span>
                    {selectedProjectId === proj.id && (
                      <Check className="size-3.5 text-primary" />
                    )}
                  </DropdownMenuItem>
                ))}
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        }
      />

      {/* Main Office View Area */}
      <div className="flex flex-1 min-h-0 flex-col overflow-hidden">
        <ProjectVirtualOfficeView
          projectId={selectedProjectId}
          snapshot={snapshot}
          changeRequests={leaderChanges?.items}
          leaderSessionId={leaderChat?.session?.id}
          leaderAgentId={leaderChat?.leader?.id}
          canControl={isWorkspaceAdmin && !snapshot?.control.cancelled_at}
          onApprovePlan={
            selectedProjectId && snapshot?.plan?.plan_digest
              ? () =>
                  approvePlan.mutate({
                    projectId: selectedProjectId,
                    planDigest: snapshot.plan!.plan_digest,
                  })
              : undefined
          }
          onApproveChange={
            selectedProjectId
              ? (id) =>
                  approveChange.mutate({
                    projectId: selectedProjectId,
                    changeRequestId: id,
                  })
              : undefined
          }
          onRejectChange={
            selectedProjectId
              ? (id) =>
                  rejectChange.mutate({
                    projectId: selectedProjectId,
                    changeRequestId: id,
                  })
              : undefined
          }
          onResolveEscalation={
            selectedProjectId
              ? (id) =>
                  resolveEscalation.mutate({
                    projectId: selectedProjectId,
                    escalationId: id,
                    decision: "approved",
                  })
              : undefined
          }
          className="flex-1"
        />
      </div>
    </div>
  );
}
