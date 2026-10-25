"use client";

import { ScrollText } from "lucide-react";
import { Label } from "@multica/ui/components/ui/label";
import { Switch } from "@multica/ui/components/ui/switch";
import {
  setDiagnosticsLogsEnabled,
  useDiagnosticsLogsEnabled,
} from "@multica/core/diagnostics";
import { useT } from "../../i18n";
import { SettingsCard, SettingsTab } from "./settings-layout";

export function LabsTab() {
  const { t } = useT("settings");
  const logsEnabled = useDiagnosticsLogsEnabled();

  return (
    <SettingsTab title={t(($) => $.page.tabs.labs)}>
      <SettingsCard>
        <div className="flex items-center justify-between gap-6 p-4">
          <div className="flex min-w-0 items-start gap-3">
            <div className="rounded-md border bg-muted/50 p-2 text-muted-foreground">
              <ScrollText className="h-4 w-4" />
            </div>
            <div className="space-y-1">
              <Label
                htmlFor="labs-diagnostics-logs"
                className="text-body font-medium"
              >
                {t(($) => $.labs.diagnostics_logs_title)}
              </Label>
              <p className="max-w-2xl text-body text-muted-foreground">
                {t(($) => $.labs.diagnostics_logs_description)}
              </p>
            </div>
          </div>
          <Switch
            id="labs-diagnostics-logs"
            checked={logsEnabled}
            onCheckedChange={setDiagnosticsLogsEnabled}
          />
        </div>
      </SettingsCard>
    </SettingsTab>
  );
}
