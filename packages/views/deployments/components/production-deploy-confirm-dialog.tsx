"use client";

import { useState } from "react";
import {
  AlertTriangle,
  GitCommit,
  ShieldAlert,
  Layers,
  User,
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
import { useT } from "../../i18n";

export interface ProductionDeployConfirmDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  targetName: string;
  providerName?: string;
  teamSlug?: string;
  commitSha: string;
  gitRef?: string;
  commitMessage?: string;
  initiatorName?: string;
  environment?: string;
  onConfirm: (approvalReference: string) => Promise<void> | void;
}

export function ProductionDeployConfirmDialog({
  open,
  onOpenChange,
  targetName,
  providerName = "Vercel",
  teamSlug,
  commitSha,
  gitRef,
  commitMessage,
  initiatorName = "Current User",
  environment = "production",
  onConfirm,
}: ProductionDeployConfirmDialogProps) {
  const { t } = useT("projects");
  const [confirmedCheckbox, setConfirmedCheckbox] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  const handleConfirm = async () => {
    if (!confirmedCheckbox || submitting) return;
    setSubmitting(true);
    try {
      // Ephemeral approval reference generated upon explicit human confirmation
      const approvalRef = `appr_${Date.now()}_${Math.random().toString(36).slice(2, 9)}`;
      await onConfirm(approvalRef);
      onOpenChange(false);
    } finally {
      setSubmitting(false);
    }
  };

  const sourceRevision = commitSha.slice(0, 7) || gitRef;

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
        data-testid="production-deploy-dialog-content"
      >
        <DialogHeader>
          <div className="flex items-center gap-2 text-destructive">
            <ShieldAlert className="size-5" />
            <DialogTitle data-testid="production-deploy-dialog-title">
              {t(($) => $.deployments.prod_confirm_title)}
            </DialogTitle>
          </div>
          <DialogDescription className="sr-only">
            {t(($) => $.deployments.prod_dialog_desc)}
          </DialogDescription>
        </DialogHeader>

        {/* Prominent Alert Banner */}
        <Alert
          variant="destructive"
          className="border-red-500/30 bg-red-500/10 text-red-950 dark:text-red-200"
          data-testid="production-deploy-alert-banner"
        >
          <AlertTriangle className="size-4 text-red-600 dark:text-red-400" />
          <AlertDescription className="text-xs font-medium leading-relaxed">
            {t(($) => $.deployments.prod_confirm_alert, {
              sha: sourceRevision,
              target: targetName,
            })}
          </AlertDescription>
        </Alert>

        {/* Structured Context Grid */}
        <div
          className="grid grid-cols-2 gap-3 rounded-lg border bg-muted/40 p-3.5 text-xs"
          data-testid="production-deploy-context-grid"
        >
          {/* Target */}
          <div className="flex flex-col gap-1">
            <span className="flex items-center gap-1.5 text-muted-foreground font-medium">
              <Layers className="size-3.5" />
              {t(($) => $.deployments.prod_confirm_target)}
            </span>
            <span className="font-semibold text-foreground">
              {targetName} ({environment})
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
              {teamSlug ? ` (Team: ${teamSlug})` : ""}
            </span>
          </div>

          {/* Git Reference */}
          <div className="flex flex-col gap-1">
            <span className="flex items-center gap-1.5 text-muted-foreground font-medium">
              <GitCommit className="size-3.5" />
              {t(($) => $.deployments.prod_confirm_git_ref)}
            </span>
            <span className="font-mono font-semibold text-foreground">
              {gitRef}
              {commitSha ? ` (${sourceRevision})` : ""}
            </span>
          </div>

          {/* Initiator */}
          <div className="flex flex-col gap-1">
            <span className="flex items-center gap-1.5 text-muted-foreground font-medium">
              <User className="size-3.5" />
              {t(($) => $.deployments.prod_confirm_initiator)}
            </span>
            <span className="font-semibold text-foreground">
              {initiatorName}
            </span>
          </div>

          {/* Commit Message if available */}
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

        {/* Mandatory Confirmation Checkbox */}
        <div className="flex items-start space-x-2.5 pt-1">
          <Checkbox
            id="production-deploy-checkbox"
            checked={confirmedCheckbox}
            onCheckedChange={(checked) =>
              setConfirmedCheckbox(checked === true)
            }
            disabled={submitting}
            data-testid="production-deploy-checkbox"
            className="mt-0.5"
          />
          <label
            htmlFor="production-deploy-checkbox"
            className="cursor-pointer text-xs font-medium text-foreground select-none leading-normal"
          >
            {t(($) => $.deployments.prod_confirm_checkbox)}
          </label>
        </div>

        {/* Footer actions */}
        <DialogFooter className="gap-2 sm:gap-0">
          <Button
            type="button"
            variant="outline"
            disabled={submitting}
            onClick={() => onOpenChange(false)}
            data-testid="cancel-deploy-button"
          >
            {t(($) => $.deployments.cancel)}
          </Button>
          <Button
            type="button"
            variant="destructive"
            disabled={!confirmedCheckbox || submitting}
            onClick={handleConfirm}
            data-testid="confirm-deploy-button"
          >
            {submitting ? (
              <Spinner className="size-4" />
            ) : (
              t(($) => $.deployments.prod_confirm_btn)
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
