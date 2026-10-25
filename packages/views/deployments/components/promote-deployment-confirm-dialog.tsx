"use client";

import { useState } from "react";
import {
  AlertTriangle,
  ArrowUpRight,
  GitCommit,
  Layers,
  Server,
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

export interface PromoteDeploymentConfirmDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  candidateOperation: DeploymentOperation;
  target?: DeploymentTarget;
  targetName?: string;
  providerName?: string;
  onConfirm: (approvalReference: string) => Promise<void> | void;
}

export function PromoteDeploymentConfirmDialog({
  open,
  onOpenChange,
  candidateOperation,
  target,
  targetName: targetNameProp,
  providerName: providerNameProp,
  onConfirm,
}: PromoteDeploymentConfirmDialogProps) {
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
      const approvalRef = `promote_${Date.now()}_${Math.random().toString(36).slice(2, 9)}`;
      await onConfirm(approvalRef);
      onOpenChange(false);
    } finally {
      setSubmitting(false);
    }
  };

  const commitSha = candidateOperation.request.commit_sha || "";
  const shortSha = commitSha ? commitSha.slice(0, 7) : "unknown";
  const gitRef = candidateOperation.request.git_ref || "main";
  const commitMessage = candidateOperation.request.commit_message;

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
        data-testid="promote-deployment-dialog-content"
      >
        <DialogHeader>
          <div className="flex items-center gap-2 text-primary">
            <ArrowUpRight className="size-5" />
            <DialogTitle data-testid="promote-deployment-dialog-title">
              {t(
                ($) =>
                  $.deployments.promote_confirm_title ??
                  "Promote to Production",
              )}
            </DialogTitle>
          </div>
          <DialogDescription className="sr-only">
            {t(
              ($) =>
                $.deployments.promote_confirm_desc ??
                "Promote preview deployment to production",
            )}
          </DialogDescription>
        </DialogHeader>

        {/* Warning Alert */}
        <Alert
          variant="destructive"
          className="border-amber-500/30 bg-amber-500/10 text-amber-950 dark:text-amber-200"
          data-testid="promote-deployment-alert-banner"
        >
          <AlertTriangle className="size-4 text-amber-600 dark:text-amber-400" />
          <AlertDescription className="text-xs font-medium leading-relaxed">
            {t(
              ($) =>
                $.deployments.promote_confirm_alert ??
                "You are about to promote candidate deployment to live production traffic. This will immediately switch incoming user traffic.",
            )}
          </AlertDescription>
        </Alert>

        {/* Context Grid */}
        <div
          className="grid grid-cols-2 gap-3 rounded-lg border bg-muted/40 p-3.5 text-xs"
          data-testid="promote-deployment-context-grid"
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

          {/* Source Revision */}
          <div className="flex flex-col gap-1">
            <span className="flex items-center gap-1.5 text-muted-foreground font-medium">
              <GitCommit className="size-3.5" />
              {t(($) => $.deployments.prod_confirm_git_ref)}
            </span>
            <span className="font-mono font-semibold text-foreground">
              {gitRef} ({shortSha})
            </span>
          </div>

          {/* Candidate Operation ID */}
          <div className="flex flex-col gap-1">
            <span className="text-muted-foreground font-medium">
              {t(($) => $.deployments.operation_id ?? "Operation ID")}
            </span>
            <span className="font-mono text-[11px] text-foreground truncate">
              {candidateOperation.id}
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
            id="promote-deploy-checkbox"
            checked={confirmedCheckbox}
            onCheckedChange={(checked) =>
              setConfirmedCheckbox(checked === true)
            }
            disabled={submitting}
            data-testid="promote-confirm-checkbox"
            className="mt-0.5"
          />
          <label
            htmlFor="promote-deploy-checkbox"
            className="cursor-pointer text-xs font-medium text-foreground select-none leading-normal"
          >
            {t(
              ($) =>
                $.deployments.prod_confirm_checkbox ??
                "I understand this will immediately affect live production traffic.",
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
            data-testid="cancel-promote-button"
          >
            {t(($) => $.deployments.cancel)}
          </Button>
          <Button
            type="button"
            variant="default"
            disabled={!confirmedCheckbox || submitting}
            onClick={handleConfirm}
            data-testid="promote-confirm-button"
          >
            {submitting ? (
              <Spinner className="size-4" />
            ) : (
              t(($) => $.deployments.promote ?? "Confirm & Promote")
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
