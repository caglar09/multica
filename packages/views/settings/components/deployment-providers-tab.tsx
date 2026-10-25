"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { CheckCircle2, ExternalLink, Plus, Server, Unplug } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { Badge } from "@multica/ui/components/ui/badge";
import { Card, CardContent } from "@multica/ui/components/ui/card";
import { useWorkspaceId } from "@multica/core/hooks";
import { useCurrentMember } from "@multica/core/permissions";
import {
  deploymentConnectionsOptions,
  deploymentTargetsOptions,
} from "@multica/core/deployments/queries";
import { useDisconnectDeploymentConnection } from "@multica/core/deployments/mutations";
import type { DeploymentProviderConnection } from "@multica/core/types";
import { DisconnectProviderConfirmDialog } from "../../deployments";
import { ConnectVercelDialog } from "./connect-vercel-dialog";
import { ConfigureVercelWebhookDialog } from "./configure-vercel-webhook-dialog";
import { useT } from "../../i18n";
import { SettingsCard, SettingsSection, SettingsTab } from "./settings-layout";

interface AvailableProvider {
  id: "vercel" | "render" | "railway" | "heroku";
  name: string;
  docsUrl: string;
  available: boolean;
}

const AVAILABLE_PROVIDERS: AvailableProvider[] = [
  {
    id: "vercel",
    name: "Vercel",
    docsUrl: "https://vercel.com/docs",
    available: true,
  },
  {
    id: "render",
    name: "Render",
    docsUrl: "https://render.com/docs",
    available: false,
  },
  {
    id: "railway",
    name: "Railway",
    docsUrl: "https://docs.railway.app",
    available: false,
  },
  {
    id: "heroku",
    name: "Heroku",
    docsUrl: "https://devcenter.heroku.com",
    available: false,
  },
];

export function DeploymentProvidersTab() {
  const { t } = useT("settings");
  const wsId = useWorkspaceId();
  const currentMember = useCurrentMember(wsId);
  const canManage =
    currentMember.role === "owner" || currentMember.role === "admin";

  const connectionsQuery = useQuery(deploymentConnectionsOptions(wsId));
  const connections = connectionsQuery.data ?? [];

  const targetsQuery = useQuery(deploymentTargetsOptions(wsId));
  const targets = targetsQuery.data ?? [];

  const disconnectMutation = useDisconnectDeploymentConnection();
  const [disconnectTarget, setDisconnectTarget] =
    useState<DeploymentProviderConnection | null>(null);
  const [connectVercelOpen, setConnectVercelOpen] = useState(false);
  const [webhookConnection, setWebhookConnection] =
    useState<DeploymentProviderConnection | null>(null);

  const handleConfirmDisconnect = async () => {
    if (!disconnectTarget) return;
    try {
      await disconnectMutation.mutateAsync(disconnectTarget.id);
      toast.success(t(($) => $.deployment_providers.toast_disconnected));
      setDisconnectTarget(null);
    } catch {
      toast.error(t(($) => $.deployment_providers.toast_disconnect_failed));
    }
  };

  const getTargetCountForConnection = (connId: string) => {
    return targets.filter((tgt) => tgt.connection_id === connId).length;
  };

  const getPlatformDescription = (id: AvailableProvider["id"]) => {
    switch (id) {
      case "vercel":
        return t(($) => $.deployment_providers.platform_vercel_desc);
      case "render":
        return t(($) => $.deployment_providers.platform_render_desc);
      case "railway":
        return t(($) => $.deployment_providers.platform_railway_desc);
      case "heroku":
        return t(($) => $.deployment_providers.platform_heroku_desc);
    }
  };

  return (
    <SettingsTab
      title={t(($) => $.deployment_providers.title)}
      description={t(($) => $.deployment_providers.description)}
    >
      {/* Active Connections Section */}
      <SettingsSection
        title={t(($) => $.deployment_providers.section_connected)}
        description={t(($) => $.deployment_providers.section_connected_desc)}
      >
        {connections.length === 0 ? (
          <SettingsCard>
            <div
              className="flex flex-col items-center justify-center p-8 text-center"
              data-testid="empty-connections-placeholder"
            >
              <Server className="mb-2 size-8 text-muted-foreground" />
              <p className="text-sm font-medium text-foreground">
                {t(($) => $.deployment_providers.no_connections)}
              </p>
              <p className="mt-1 text-xs text-muted-foreground max-w-sm">
                {t(($) => $.deployment_providers.empty_hint)}
              </p>
            </div>
          </SettingsCard>
        ) : (
          <div
            className="flex flex-col gap-3"
            data-testid="active-connections-list"
          >
            {connections.map((conn) => {
              const targetCount = getTargetCountForConnection(conn.id);
              const isEnabled = conn.enabled !== false;

              return (
                <Card
                  key={conn.id}
                  className="bg-card border transition-all"
                  data-testid={`connection-card-${conn.id}`}
                >
                  <CardContent className="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-4 p-4">
                    <div className="flex items-center gap-3">
                      <div className="flex size-10 items-center justify-center rounded-lg bg-primary/10 text-primary">
                        <Server className="size-5" />
                      </div>
                      <div className="flex flex-col gap-1">
                        <div className="flex items-center gap-2">
                          <span className="font-semibold text-foreground text-sm">
                            {conn.name}
                          </span>
                          <Badge
                            variant="secondary"
                            className={
                              isEnabled
                                ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300 font-semibold text-[10px]"
                                : "text-[10px] text-muted-foreground"
                            }
                            data-testid={`connection-status-badge-${conn.id}`}
                          >
                            <CheckCircle2 className="mr-1 size-3 text-emerald-600 dark:text-emerald-400" />
                            <span>
                              {isEnabled
                                ? t(
                                    ($) =>
                                      $.deployment_providers.status_connected,
                                  )
                                : t(
                                    ($) =>
                                      $.deployment_providers.status_inactive,
                                  )}
                            </span>
                          </Badge>
                        </div>
                        <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground font-mono">
                          <span className="capitalize">{conn.provider}</span>
                          {conn.config?.team_slug ? (
                            <span>
                              •{" "}
                              {t(($) => $.deployment_providers.team_prefix, {
                                team: conn.config?.team_slug,
                              })}
                            </span>
                          ) : null}
                          {conn.config?.account_name ? (
                            <span>
                              •{" "}
                              {t(($) => $.deployment_providers.account_prefix, {
                                account: conn.config?.account_name,
                              })}
                            </span>
                          ) : null}
                          <span>
                            •{" "}
                            {t(($) => $.deployment_providers.targets_count, {
                              count: targetCount,
                            })}
                          </span>
                        </div>
                        {conn.provider.toLowerCase() === "vercel" && conn.webhook_url ? (
                          <div className="mt-1 flex flex-col gap-0.5 text-xs">
                            <div className="flex items-center gap-2">
                              <span className="text-muted-foreground">
                                {t(($) => $.deployment_providers.webhook_url_label)}
                              </span>
                              <Badge variant="outline" className="text-[10px]">
                                {conn.webhook_status === "configured"
                                  ? t(($) => $.deployment_providers.webhook_status_configured)
                                  : t(($) => $.deployment_providers.webhook_status_polling_only)}
                              </Badge>
                            </div>
                            <code className="break-all text-muted-foreground" data-testid={`connection-webhook-url-${conn.id}`}>
                              {conn.webhook_url}
                            </code>
                          </div>
                        ) : null}
                      </div>
                    </div>

                    {/* Actions */}
                    {canManage ? (
                      <div className="flex flex-wrap gap-2">
                        {conn.provider.toLowerCase() === "vercel" ? (
                          <Button
                            variant="outline"
                            size="sm"
                            onClick={() => setWebhookConnection(conn)}
                            className="text-xs gap-1.5 h-8"
                            data-testid={`configure-webhook-button-${conn.id}`}
                          >
                            {conn.webhook_configured
                              ? t(($) => $.deployment_providers.rotate_webhook)
                              : t(($) => $.deployment_providers.configure_webhook)}
                          </Button>
                        ) : null}
                        <Button
                          variant="outline"
                          size="sm"
                          onClick={() => setDisconnectTarget(conn)}
                          className="text-xs text-destructive hover:bg-destructive/10 hover:text-destructive gap-1.5 h-8"
                          data-testid={`disconnect-connection-button-${conn.id}`}
                        >
                          <Unplug className="size-3.5" />
                          <span>
                            {t(($) => $.deployment_providers.disconnect)}
                          </span>
                        </Button>
                      </div>
                    ) : null}
                  </CardContent>
                </Card>
              );
            })}
          </div>
        )}
      </SettingsSection>

      {/* Available Providers Section */}
      <SettingsSection
        title={t(($) => $.deployment_providers.section_platforms)}
        description={t(($) => $.deployment_providers.section_platforms_desc)}
      >
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          {AVAILABLE_PROVIDERS.map((provider) => {
            const isConnected = connections.some(
              (c) => c.provider.toLowerCase() === provider.id.toLowerCase(),
            );

            return (
              <Card
                key={provider.id}
                className="bg-card border p-4 flex flex-col justify-between gap-3"
                data-testid={`available-provider-card-${provider.id}`}
              >
                <div className="flex flex-col gap-1.5">
                  <div className="flex items-center justify-between">
                    <span className="font-semibold text-foreground text-sm">
                      {provider.name}
                    </span>
                    {isConnected ? (
                      <Badge
                        variant="outline"
                        className="text-[10px] text-emerald-700 dark:text-emerald-300 border-emerald-500/30"
                      >
                        {t(($) => $.deployment_providers.badge_active)}
                      </Badge>
                    ) : provider.available ? (
                      <Badge variant="outline" className="text-[10px]">
                        {t(($) => $.deployment_providers.badge_ready)}
                      </Badge>
                    ) : (
                      <Badge
                        variant="outline"
                        className="text-[10px] text-muted-foreground"
                      >
                        {t(($) => $.deployment_providers.badge_coming_soon)}
                      </Badge>
                    )}
                  </div>
                  <p className="text-xs text-muted-foreground leading-relaxed">
                    {getPlatformDescription(provider.id)}
                  </p>
                </div>

                <div className="flex items-center justify-between border-t pt-2.5">
                  <a
                    href={provider.docsUrl}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="flex items-center gap-1 text-[11px] text-muted-foreground hover:text-foreground"
                  >
                    <span>
                      {t(($) => $.deployment_providers.link_documentation)}
                    </span>
                    <ExternalLink className="size-2.5" />
                  </a>

                  {canManage && !isConnected && provider.available ? (
                    <Button
                      variant="outline"
                      size="sm"
                      className="h-7 text-xs gap-1"
                      onClick={() => {
                        if (provider.id === "vercel") {
                          setConnectVercelOpen(true);
                        } else {
                          toast.info(
                            t(($) => $.deployment_providers.toast_connecting, {
                              provider: provider.name,
                            }),
                          );
                        }
                      }}
                      data-testid={`connect-provider-${provider.id}`}
                    >
                      <Plus className="size-3" />
                      <span>
                        {t(($) => $.deployment_providers.action_connect)}
                      </span>
                    </Button>
                  ) : null}
                </div>
              </Card>
            );
          })}
        </div>
      </SettingsSection>

      {/* Disconnect Confirmation Dialog */}
      {disconnectTarget ? (
        <DisconnectProviderConfirmDialog
          open={!!disconnectTarget}
          onOpenChange={(open) => {
            if (!open) setDisconnectTarget(null);
          }}
          connection={disconnectTarget}
          targetCount={getTargetCountForConnection(disconnectTarget.id)}
          onConfirm={handleConfirmDisconnect}
        />
      ) : null}

      <ConnectVercelDialog
        open={connectVercelOpen}
        onOpenChange={setConnectVercelOpen}
      />

      {webhookConnection ? (
        <ConfigureVercelWebhookDialog
          open={!!webhookConnection}
          onOpenChange={(open) => {
            if (!open) setWebhookConnection(null);
          }}
          connection={webhookConnection}
        />
      ) : null}
    </SettingsTab>
  );
}
