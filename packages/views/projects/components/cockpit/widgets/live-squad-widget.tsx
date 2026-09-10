"use client";

import { useMemo } from "react";
import { Users, ArrowUpRight, Cpu } from "lucide-react";

import type { AutonomousProjectSnapshot } from "@multica/core/types";
import { useWorkspacePaths } from "@multica/core/paths";
import { cn } from "@multica/ui/lib/utils";
import { Button } from "@multica/ui/components/ui/button";
import { Badge } from "@multica/ui/components/ui/badge";

import { ActorAvatar } from "../../../../common/actor-avatar";
import { useNavigation } from "../../../../navigation";
import { useT } from "../../../../i18n";

export interface LiveSquadWidgetProps {
  snapshot?: AutonomousProjectSnapshot | null;
  onConfigureSquad?: () => void;
  className?: string;
}

export function LiveSquadWidget({
  snapshot,
  onConfigureSquad,
  className,
}: LiveSquadWidgetProps) {
  const { t } = useT("projects");
  const router = useNavigation();
  const wsPaths = useWorkspacePaths();

  const members = useMemo(
    () => snapshot?.team?.members ?? [],
    [snapshot?.team?.members],
  );

  const activeMembersCount = useMemo(
    () => members.filter((m) => m.active).length,
    [members],
  );

  return (
    <div
      className={cn(
        "flex flex-col rounded-xl border bg-card p-4 shadow-xs transition-colors",
        className,
      )}
    >
      {/* Header */}
      <div className="flex items-center justify-between gap-2 pb-3">
        <div className="flex items-center gap-2">
          <div className="flex size-7 items-center justify-center rounded-lg bg-primary/10 text-primary">
            <Users className="size-4" />
          </div>
          <div>
            <h3 className="text-sm font-semibold leading-none text-foreground">
              {t(($) => $.cockpit.squad_title)}
            </h3>
            <p className="mt-1 text-[11px] text-muted-foreground">
              {snapshot?.team?.intent || t(($) => $.cockpit.subtitle)}
            </p>
          </div>
        </div>

        <div className="flex items-center gap-1.5">
          {members.length > 0 && (
            <Badge variant="secondary" className="gap-1 font-mono text-[10px]">
              <span className="size-1.5 rounded-full bg-emerald-500 animate-pulse" />
              {activeMembersCount}/{members.length} {t(($) => $.detail.status_active)}
            </Badge>
          )}
          {onConfigureSquad && (
            <Button
              variant="ghost"
              size="sm"
              className="text-xs h-7 px-2"
              onClick={onConfigureSquad}
            >
              {t(($) => $.cockpit.squad_configure)}
            </Button>
          )}
        </div>
      </div>

      {/* Body */}
      {members.length === 0 ? (
        <div className="flex flex-1 flex-col items-center justify-center rounded-lg border border-dashed p-6 text-center">
          {snapshot?.draft ? (
            <>
              <Badge
                variant="outline"
                className="border-amber-500/30 text-amber-600 dark:text-amber-400 font-mono text-[10px] mb-2"
              >
                {t(($) => $.cockpit.bootstrapping_roles_count, {
                  count: snapshot.draft.plan.roles?.length ?? 0,
                })}
              </Badge>
              <p className="text-xs text-muted-foreground">
                {t(($) => $.cockpit.bootstrapping_awaiting_approval)}
              </p>
            </>
          ) : (
            <>
              <p className="text-xs text-muted-foreground">
                {t(($) => $.cockpit.squad_empty)}
              </p>
              {onConfigureSquad && (
                <Button
                  variant="outline"
                  size="sm"
                  className="mt-3 text-xs"
                  onClick={onConfigureSquad}
                >
                  <Cpu className="size-3.5 mr-1" />
                  {t(($) => $.cockpit.squad_configure)}
                </Button>
              )}
            </>
          )}
        </div>
      ) : (
        <div className="grid flex-1 gap-2 sm:grid-cols-2">
          {members.map((member) => {
            const hasTask = Boolean(member.current_task_title);

            return (
              <div
                key={member.agent_id}
                role="button"
                tabIndex={0}
                onClick={() => router.push(wsPaths.agentDetail(member.agent_id))}
                onKeyDown={(e) => {
                  if (e.key === "Enter" || e.key === " ") {
                    e.preventDefault();
                    router.push(wsPaths.agentDetail(member.agent_id));
                  }
                }}
                className="group relative flex flex-col justify-between rounded-lg border bg-muted/20 p-3 text-left transition-all hover:border-primary/40 hover:bg-muted/40 cursor-pointer focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ring"
              >
                <div className="flex items-start justify-between gap-2">
                  <div className="flex items-center gap-2.5 min-w-0">
                    <ActorAvatar
                      actorType="agent"
                      actorId={member.agent_id}
                      size="sm"
                      showStatusDot={member.active}
                    />
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-1.5">
                        <span className="truncate text-xs font-medium text-foreground group-hover:text-primary transition-colors">
                          {member.agent_name}
                        </span>
                        <ArrowUpRight className="size-3 text-muted-foreground/50 opacity-0 transition-opacity group-hover:opacity-100 shrink-0" />
                      </div>
                      <span className="text-[10px] font-mono text-muted-foreground capitalize">
                        {member.role}
                      </span>
                    </div>
                  </div>

                  <span
                    className={cn(
                      "size-2 shrink-0 rounded-full",
                      member.active ? "bg-emerald-500 shadow-xs shadow-emerald-500/50" : "bg-muted-foreground/30",
                    )}
                  />
                </div>

                {/* Current Task or Role Summary */}
                <div className="mt-2.5 rounded-md bg-background/60 px-2 py-1.5 border border-border/40">
                  <div className="flex items-center justify-between text-[10px] text-muted-foreground">
                    <span className="font-mono">
                      {hasTask ? "Current Task" : "Responsibility"}
                    </span>
                    {member.current_task_status && (
                      <span className="text-emerald-500 font-medium">
                        {member.current_task_status}
                      </span>
                    )}
                  </div>
                  <p className="mt-0.5 line-clamp-1 text-[11px] font-medium text-foreground">
                    {member.current_task_title ||
                      member.responsibilities?.[0] ||
                      member.capabilities?.[0] ||
                      "Ready for autonomous assignments"}
                  </p>
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
