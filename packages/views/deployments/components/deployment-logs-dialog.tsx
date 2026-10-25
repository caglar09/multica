"use client";

import { useQuery } from "@tanstack/react-query";
import { FileText, RefreshCw } from "lucide-react";
import type { DeploymentOperation } from "@multica/core/types";
import { deploymentLogsOptions } from "@multica/core/deployments/queries";
import { useWorkspaceId } from "@multica/core/hooks";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Spinner } from "@multica/ui/components/ui/spinner";
import { useT } from "../../i18n";

export function DeploymentLogsDialog({
  operation,
  onClose,
}: {
  operation: DeploymentOperation;
  onClose: () => void;
}) {
  const { t } = useT("projects");
  const wsId = useWorkspaceId();
  const query = useQuery({
    ...deploymentLogsOptions(wsId, operation.id),
    refetchInterval: [
      "queued",
      "dispatching",
      "building",
      "deploying",
      "running",
      "retry_wait",
    ].includes(operation.status)
      ? 3_000
      : false,
  });

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="flex max-h-[85vh] flex-col gap-0 p-0 sm:max-w-3xl">
        <DialogHeader className="border-b px-6 py-5">
          <div className="flex items-start justify-between gap-4 pr-8">
            <div>
              <DialogTitle className="flex items-center gap-2 text-title-sm">
                <FileText className="size-4 text-muted-foreground" />
                {t(($) => $.deployments.logs_title)}
              </DialogTitle>
              <DialogDescription className="mt-1 text-caption">
                {t(($) => $.deployments.logs_description)}
              </DialogDescription>
            </div>
            <Button
              variant="outline"
              size="sm"
              onClick={() => query.refetch()}
              disabled={query.isFetching}
              className="h-8 gap-1.5 text-caption"
            >
              <RefreshCw className="size-3" />
              {t(($) => $.deployments.logs_refresh)}
            </Button>
          </div>
        </DialogHeader>

        <div className="min-h-64 overflow-auto bg-muted/30 p-4">
          {query.isPending ? (
            <div className="flex min-h-56 items-center justify-center gap-2 text-caption text-muted-foreground">
              <Spinner className="size-4" />
              {t(($) => $.deployments.logs_loading)}
            </div>
          ) : query.isError ? (
            <div className="min-h-56 rounded-md border border-destructive/30 bg-destructive/10 p-3 text-caption text-destructive">
              {t(($) => $.deployments.logs_error)}
            </div>
          ) : query.data.logs.length === 0 ? (
            <div className="flex min-h-56 items-center justify-center text-caption text-muted-foreground">
              {t(($) => $.deployments.logs_empty)}
            </div>
          ) : (
            <pre className="whitespace-pre-wrap break-words font-mono text-caption leading-5 text-foreground">
              {query.data.logs.join("\n")}
            </pre>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
