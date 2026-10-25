"use client";

import { useEffect, useState, type FormEvent } from "react";
import { toast } from "sonner";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Alert, AlertDescription } from "@multica/ui/components/ui/alert";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Spinner } from "@multica/ui/components/ui/spinner";
import { useSetDeploymentWebhookSecret } from "@multica/core/deployments/mutations";
import type { DeploymentProviderConnection } from "@multica/core/types";
import { useT } from "../../i18n";

export interface ConfigureVercelWebhookDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  connection: DeploymentProviderConnection;
}

export function ConfigureVercelWebhookDialog({
  open,
  onOpenChange,
  connection,
}: ConfigureVercelWebhookDialogProps) {
  const { t } = useT("settings");
  const mutation = useSetDeploymentWebhookSecret();
  const [secret, setSecret] = useState("");
  const [invalid, setInvalid] = useState(false);
  const [error, setError] = useState(false);

  useEffect(() => {
    if (!open) {
      setSecret("");
      setInvalid(false);
      setError(false);
    }
  }, [open]);

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const value = secret.trim();
    setInvalid(!value);
    if (!value || mutation.isPending) return;
    setError(false);
    try {
      await mutation.mutateAsync({ connectionId: connection.id, webhookSecret: value });
      toast.success(t(($) => $.deployment_providers.webhook_saved));
      onOpenChange(false);
    } catch {
      setError(true);
    }
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (!mutation.isPending) onOpenChange(nextOpen);
      }}
    >
      <DialogContent className="sm:max-w-lg" data-testid="configure-vercel-webhook-dialog-content">
        <DialogHeader>
          <DialogTitle>
            {t(($) => $.deployment_providers.webhook_setup_title)}
          </DialogTitle>
          <DialogDescription>
            {t(($) => $.deployment_providers.webhook_setup_description)}
          </DialogDescription>
        </DialogHeader>
        <form id="configure-vercel-webhook-form" className="flex flex-col gap-4" onSubmit={handleSubmit} noValidate>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="vercel-webhook-secret">
              {t(($) => $.deployment_providers.vercel_webhook_secret_label)}
            </Label>
            <Input
              id="vercel-webhook-secret"
              data-testid="vercel-webhook-secret"
              type="password"
              value={secret}
              onChange={(event) => {
                setSecret(event.target.value);
                setInvalid(false);
              }}
              aria-invalid={invalid}
              disabled={mutation.isPending}
              autoComplete="new-password"
              spellCheck={false}
            />
            {invalid ? (
              <p className="text-xs text-destructive" data-testid="vercel-webhook-secret-error">
                {t(($) => $.deployment_providers.vercel_required_field)}
              </p>
            ) : null}
          </div>
          <div className="flex flex-col gap-1.5 text-xs text-muted-foreground">
            <span>{t(($) => $.deployment_providers.webhook_url_label)}</span>
            <code className="break-all">{connection.webhook_url}</code>
          </div>
          {error ? (
            <Alert variant="destructive" data-testid="configure-vercel-webhook-error">
              <AlertDescription>
                {t(($) => $.deployment_providers.webhook_configure_error)}
              </AlertDescription>
            </Alert>
          ) : null}
        </form>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={mutation.isPending}>
            {t(($) => $.deployment_providers.cancel)}
          </Button>
          <Button type="submit" form="configure-vercel-webhook-form" disabled={mutation.isPending} data-testid="vercel-webhook-save">
            {mutation.isPending ? <Spinner className="size-4" /> : t(($) => $.deployment_providers.webhook_save)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
