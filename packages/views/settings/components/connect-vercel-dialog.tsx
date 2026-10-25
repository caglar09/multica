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
import { useCreateDeploymentConnection } from "@multica/core/deployments/mutations";
import { useT } from "../../i18n";

export interface ConnectVercelDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function ConnectVercelDialog({
  open,
  onOpenChange,
}: ConnectVercelDialogProps) {
  const { t } = useT("settings");
  const mutation = useCreateDeploymentConnection();
  const [name, setName] = useState("");
  const [token, setToken] = useState("");
  const [teamId, setTeamId] = useState("");
  const [teamSlug, setTeamSlug] = useState("");
  const [validation, setValidation] = useState({ name: false, token: false });
  const [error, setError] = useState(false);

  useEffect(() => {
    if (!open) {
      setName("");
      setToken("");
      setTeamId("");
      setTeamSlug("");
      setValidation({ name: false, token: false });
      setError(false);
    }
  }, [open]);

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (mutation.isPending) return;

    const nextValidation = {
      name: !name.trim(),
      token: !token.trim(),
    };
    setValidation(nextValidation);
    if (nextValidation.name || nextValidation.token) return;

    setError(false);
    const config = {
      ...(teamId.trim() ? { team_id: teamId.trim() } : {}),
      ...(teamSlug.trim() ? { team_slug: teamSlug.trim() } : {}),
    };

    try {
      await mutation.mutateAsync({
        provider: "vercel",
        name: name.trim(),
        token: token.trim(),
        ...(Object.keys(config).length > 0 ? { config } : {}),
      });
      toast.success(t(($) => $.deployment_providers.vercel_connect_success));
      onOpenChange(false);
    } catch {
      // Keep provider errors out of the UI: they may contain sensitive payloads.
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
      <DialogContent
        className="sm:max-w-lg"
        data-testid="connect-vercel-dialog-content"
      >
        <DialogHeader>
          <DialogTitle>
            {t(($) => $.deployment_providers.vercel_connect_title)}
          </DialogTitle>
          <DialogDescription>
            {t(($) => $.deployment_providers.vercel_connect_description)}
          </DialogDescription>
        </DialogHeader>

        <form
          id="connect-vercel-form"
          className="flex flex-col gap-4"
          onSubmit={handleSubmit}
          noValidate
        >
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="vercel-connection-name">
              {t(($) => $.deployment_providers.vercel_connection_name)}
            </Label>
            <Input
              id="vercel-connection-name"
              data-testid="vercel-connection-name"
              value={name}
              onChange={(event) => {
                setName(event.target.value);
                setValidation((current) => ({ ...current, name: false }));
              }}
              aria-invalid={validation.name}
              disabled={mutation.isPending}
              autoComplete="off"
            />
            {validation.name ? (
              <p className="text-xs text-destructive" data-testid="vercel-name-error">
                {t(($) => $.deployment_providers.vercel_required_field)}
              </p>
            ) : null}
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="vercel-pat">
              {t(($) => $.deployment_providers.vercel_pat_label)}
            </Label>
            <Input
              id="vercel-pat"
              data-testid="vercel-pat"
              type="password"
              value={token}
              onChange={(event) => {
                setToken(event.target.value);
                setValidation((current) => ({ ...current, token: false }));
              }}
              aria-invalid={validation.token}
              disabled={mutation.isPending}
              autoComplete="new-password"
              spellCheck={false}
            />
            {validation.token ? (
              <p className="text-xs text-destructive" data-testid="vercel-token-error">
                {t(($) => $.deployment_providers.vercel_required_field)}
              </p>
            ) : null}
          </div>

          <div className="grid gap-4 sm:grid-cols-2">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="vercel-team-id">
                {t(($) => $.deployment_providers.vercel_team_id_label)}
              </Label>
              <Input
                id="vercel-team-id"
                data-testid="vercel-team-id"
                value={teamId}
                onChange={(event) => setTeamId(event.target.value)}
                disabled={mutation.isPending}
                autoComplete="off"
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="vercel-team-slug">
                {t(($) => $.deployment_providers.vercel_team_slug_label)}
              </Label>
              <Input
                id="vercel-team-slug"
                data-testid="vercel-team-slug"
                value={teamSlug}
                onChange={(event) => setTeamSlug(event.target.value)}
                disabled={mutation.isPending}
                autoComplete="off"
              />
            </div>
          </div>

          <p className="text-xs text-muted-foreground">
            {t(($) => $.deployment_providers.webhook_setup_description)}
          </p>

          {error ? (
            <Alert variant="destructive" data-testid="connect-vercel-error">
              <AlertDescription>
                {t(($) => $.deployment_providers.vercel_connect_error)}
              </AlertDescription>
            </Alert>
          ) : null}
        </form>

        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={mutation.isPending}
          >
            {t(($) => $.deployment_providers.cancel)}
          </Button>
          <Button
            type="submit"
            form="connect-vercel-form"
            disabled={mutation.isPending}
            data-testid="vercel-connect-submit"
          >
            {mutation.isPending ? (
              <Spinner className="size-4" />
            ) : (
              t(($) => $.deployment_providers.vercel_connect_submit)
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
