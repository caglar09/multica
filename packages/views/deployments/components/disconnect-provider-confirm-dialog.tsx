"use client";

import { useState } from "react";
import { AlertTriangle, Server, Unplug } from "lucide-react";
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
import type { DeploymentProviderConnection } from "@multica/core/types";
import { useT } from "../../i18n";

export interface DisconnectProviderConfirmDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  connection: DeploymentProviderConnection;
  targetCount?: number;
  onConfirm: () => Promise<void> | void;
}

export function DisconnectProviderConfirmDialog({
  open,
  onOpenChange,
  connection,
  targetCount,
  onConfirm,
}: DisconnectProviderConfirmDialogProps) {
  const { t } = useT("settings");
  const [confirmedCheckbox, setConfirmedCheckbox] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  const handleConfirm = async () => {
    if (!confirmedCheckbox || submitting) return;
    setSubmitting(true);
    try {
      await onConfirm();
      onOpenChange(false);
    } finally {
      setSubmitting(false);
    }
  };

  const providerName =
    connection.name ||
    (typeof connection.provider === "string"
      ? connection.provider.toUpperCase()
      : t(($) => $.deployment_providers.default_provider_name));

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
        data-testid="disconnect-provider-dialog-content"
      >
        <DialogHeader>
          <div className="flex items-center gap-2 text-destructive">
            <Unplug className="size-5" />
            <DialogTitle data-testid="disconnect-provider-dialog-title">
              {t(($) => $.deployment_providers?.disconnect_confirm_title, {
                provider: providerName,
              }) ?? `Disconnect ${providerName}?`}
            </DialogTitle>
          </div>
          <DialogDescription className="sr-only">
            {t(($) => $.deployment_providers?.disconnect_confirm_desc, {
              provider: providerName,
            }) ?? "Disconnecting will disable active deployment targets."}
          </DialogDescription>
        </DialogHeader>

        {/* Warning Alert Banner */}
        <Alert
          variant="destructive"
          className="border-red-500/30 bg-red-500/10 text-red-950 dark:text-red-200"
          data-testid="disconnect-provider-alert-banner"
        >
          <AlertTriangle className="size-4 text-red-600 dark:text-red-400" />
          <AlertDescription className="text-xs font-medium leading-relaxed">
            {t(($) => $.deployment_providers?.disconnect_confirm_desc, {
              provider: providerName,
            }) ??
              `Disconnecting this provider will disable all linked deployment targets in this workspace. Active continuous deployment pipelines and manual triggers will fail until the provider is reconnected.`}
          </AlertDescription>
        </Alert>

        {/* Connection Details */}
        <div
          className="flex flex-col gap-2 rounded-lg border bg-muted/40 p-3.5 text-xs"
          data-testid="disconnect-provider-context"
        >
          <div className="flex items-center justify-between">
            <span className="flex items-center gap-1.5 text-muted-foreground font-medium">
              <Server className="size-3.5" />
              {t(
                ($) =>
                  $.deployment_providers?.provider_connection ??
                  "Provider Connection",
              )}
            </span>
            <span className="font-semibold text-foreground">
              {connection.name}
            </span>
          </div>
          {connection.config?.team_slug ? (
            <div className="flex items-center justify-between border-t pt-2">
              <span className="text-muted-foreground">
                {t(
                  ($) =>
                    $.deployment_providers?.team_or_scope ?? "Team / Scope",
                )}
              </span>
              <span className="font-mono text-foreground">
                {connection.config.team_slug}
              </span>
            </div>
          ) : null}
          {typeof targetCount === "number" ? (
            <div className="flex items-center justify-between border-t pt-2">
              <span className="text-muted-foreground">
                {t(
                  ($) =>
                    $.deployment_providers?.linked_targets ?? "Linked Targets",
                )}
              </span>
              <span className="font-semibold text-destructive">
                {t(($) => $.deployment_providers?.targets_disabled_warning, {
                  count: targetCount,
                }) ?? `${targetCount} targets will be disabled`}
              </span>
            </div>
          ) : null}
        </div>

        {/* Confirmation Checkbox */}
        <div className="flex items-start space-x-2.5 pt-1">
          <Checkbox
            id="disconnect-provider-checkbox"
            checked={confirmedCheckbox}
            onCheckedChange={(checked) =>
              setConfirmedCheckbox(checked === true)
            }
            disabled={submitting}
            data-testid="disconnect-confirm-checkbox"
            className="mt-0.5"
          />
          <label
            htmlFor="disconnect-provider-checkbox"
            className="cursor-pointer text-xs font-medium text-foreground select-none leading-normal"
          >
            {t(
              ($) =>
                $.deployment_providers?.disconnect_checkbox_confirm ??
                "I confirm disconnecting this provider and understand the consequences for active targets.",
            )}
          </label>
        </div>

        {/* Footer Actions */}
        <DialogFooter className="gap-2 sm:gap-0">
          <Button
            type="button"
            variant="outline"
            disabled={submitting}
            onClick={() => onOpenChange(false)}
            data-testid="cancel-disconnect-button"
          >
            {t(($) => $.deployment_providers?.cancel ?? "Cancel")}
          </Button>
          <Button
            type="button"
            variant="destructive"
            disabled={!confirmedCheckbox || submitting}
            onClick={handleConfirm}
            data-testid="disconnect-confirm-button"
          >
            {submitting ? (
              <Spinner className="size-4" />
            ) : (
              t(($) => $.deployment_providers?.disconnect ?? "Disconnect")
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
