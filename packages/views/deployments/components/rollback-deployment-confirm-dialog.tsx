"use client";

import { useState } from "react";
import {
  AlertTriangle,
  GitCommit,
  Layers,
  Server,
  ShieldAlert,
} from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { Spinner } from "@multica/ui/components/ui/spinner";
import { Alert, AlertDescription } from "@multica/ui/components/ui/alert";
import type {
  DeploymentOperation,
  DeploymentTarget,
} from "@multica/core/types";
import { useT } from "../../i18n";

export interface RollbackDeploymentConfirmDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  targetOperation: DeploymentOperation;
  target?: DeploymentTarget;
  targetName?: string;
  providerName?: string;
  onConfirm: (approvalReference: string) => Promise<void> | void;
}

export function RollbackDeploymentConfirmDialog({
  open,
  onOpenChange,
  targetOperation,
  target,
  targetName: targetNameProp,
  providerName: providerNameProp,
  onConfirm,
}: RollbackDeploymentConfirmDialogProps) {
  const { t } = useT("projects");
  const targetName =
    targetNameProp ||
    target?.name ||
    t(($) => $.deployments.default_prod_target_name);
  const providerName =
    providerNameProp || t(($) => $.deployments.default_provider_name);
  const [confirmedCheckbox, setConfirmedCheckbox] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  const handleConfirm = async () => {
    if (!confirmedCheckbox || submitting) return;
    setSubmitting(true);
    try {
      const approvalRef = `rollback_${Date.now()}_${Math.random().toString(36).slice(2, 9)}`;
      await onConfirm(approvalRef);
      onOpenChange(false);
    } finally {
      setSubmitting(false);
    }
  };

  const commitSha = targetOperation.request.commit_sha || "";
  const shortSha = commitSha ? commitSha.slice(0, 7) : "unknown";
  const gitRef = targetOperation.request.git_ref || "main";
  const commitMessage = targetOperation.request.commit_message;

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (!submitting) {
          if (!v) setConfirmedCheckbox(false);
          onOpenChange(v);
        }
      }}
    >
      <DialogContent
        className="sm:max-w-lg"
        data-testid="rollback-deployment-dialog-content"
      >
        <DialogHeader>
          <div className="flex items-center gap-2 text-destructive">
            <ShieldAlert className="size-5" />
            <DialogTitle data-testid="rollback-deployment-dialog-title">
              {t(
                ($) =>
                  $.deployments.rollback_confirm_title ??
                  "Rollback Production Deployment",
              )}
            </DialogTitle>
          </div>
          <DialogDescription className="sr-only">
            {t(
              ($) =>
                $.deployments.rollback_confirm_desc ??
                "Rollback production deployment",
            )}
          </DialogDescription>
        </DialogHeader>

        {/* Prominent Destructive Alert Banner */}
        <Alert
          variant="destructive"
          className="border-red-500/30 bg-red-500/10 text-red-950 dark:text-red-200"
          data-testid="rollback-deployment-alert-banner"
        >
          <AlertTriangle className="size-4 text-red-600 dark:text-red-400" />
          <AlertDescription className="text-xs font-medium leading-relaxed">
            {t(
              ($) =>
                $.deployments.rollback_confirm_alert ??
                "Warning: You are about to roll back live production traffic to an earlier deployment. This will instantly change what end-users see.",
            )}
          </AlertDescription>
        </Alert>

        {/* Context Grid */}
        <div
          className="grid grid-cols-2 gap-3 rounded-lg border bg-muted/40 p-3.5 text-xs"
          data-testid="rollback-deployment-context-grid"
        >
          {/* Target */}
          <div className="flex flex-col gap-1">
            <span className="flex items-center gap-1.5 text-muted-foreground font-medium">
              <Layers className="size-3.5" />
              {t(($) => $.deployments.prod_confirm_target)}
            </span>
            <span className="font-semibold text-foreground">
              {targetName}{" "}
              {t(($) => $.deployments.target_env_suffix, { env: "production" })}
            </span>
          </div>

          {/* Provider */}
          <div className="flex flex-col gap-1">
            <span className="flex items-center gap-1.5 text-muted-foreground font-medium">
              <Server className="size-3.5" />
              {t(($) => $.deployments.prod_confirm_provider)}
            </span>
            <span className="font-semibold text-foreground">
              {providerName}
            </span>
          </div>

          {/* Target Rollback Revision */}
          <div className="flex flex-col gap-1">
            <span className="flex items-center gap-1.5 text-muted-foreground font-medium">
              <GitCommit className="size-3.5" />
              {t(
                ($) =>
                  $.deployments.rollback_to_revision ?? "Rollback To Revision",
              )}
            </span>
            <span className="font-mono font-semibold text-foreground">
              {gitRef} ({shortSha})
            </span>
          </div>

          {/* Rollback Operation ID */}
          <div className="flex flex-col gap-1">
            <span className="text-muted-foreground font-medium">
              {t(($) => $.deployments.operation_id ?? "Operation ID")}
            </span>
            <span className="font-mono text-[11px] text-foreground truncate">
              {targetOperation.id}
            </span>
          </div>

          {/* Commit Message */}
          {commitMessage ? (
            <div className="col-span-2 mt-1 flex flex-col gap-1 border-t pt-2">
              <span className="text-[11px] text-muted-foreground">
                {t(($) => $.deployments.commit_message)}
              </span>
              <span className="font-mono text-[11px] text-foreground line-clamp-2">
                {commitMessage}
              </span>
            </div>
          ) : null}
        </div>

        {/* Confirmation Checkbox */}
        <div className="flex items-start space-x-2.5 pt-1">
          <Checkbox
            id="rollback-deploy-checkbox"
            checked={confirmedCheckbox}
            onCheckedChange={(checked) =>
              setConfirmedCheckbox(checked === true)
            }
            disabled={submitting}
            data-testid="rollback-confirm-checkbox"
            className="mt-0.5"
          />
          <label
            htmlFor="rollback-deploy-checkbox"
            className="cursor-pointer text-xs font-medium text-foreground select-none leading-normal"
          >
            {t(
              ($) =>
                $.deployments.rollback_confirm_checkbox ??
                "I confirm rolling back live production traffic to this earlier deployment.",
            )}
          </label>
        </div>

        {/* Footer actions */}
        <DialogFooter className="gap-2 sm:gap-0">
          <Button
            type="button"
            variant="outline"
            disabled={submitting}
            onClick={() => onOpenChange(false)}
            data-testid="cancel-rollback-button"
          >
            {t(($) => $.deployments.cancel)}
          </Button>
          <Button
            type="button"
            variant="destructive"
            disabled={!confirmedCheckbox || submitting}
            onClick={handleConfirm}
            data-testid="rollback-confirm-button"
          >
            {submitting ? (
              <Spinner className="size-4" />
            ) : (
              t(($) => $.deployments.rollback ?? "Confirm & Rollback")
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
