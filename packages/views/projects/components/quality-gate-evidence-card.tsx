import { ShieldCheck, ShieldAlert } from "lucide-react";

import type { AutonomousQualityGate } from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import { Card } from "@multica/ui/components/ui/card";
import { useT } from "../../i18n";

function asRecord(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

function stringValue(value: unknown): string | null {
  return typeof value === "string" && value.trim() !== "" ? value.trim() : null;
}

function numberValue(value: unknown): number | null {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function safeCommitURL(value: unknown): string | null {
  const candidate = stringValue(value);
  if (!candidate) return null;
  try {
    const parsed = new URL(candidate);
    return parsed.protocol === "https:" || parsed.protocol === "http:"
      ? parsed.toString()
      : null;
  } catch {
    return null;
  }
}

function runnerPresentation(runnerType: string | null): "runner_daemon" | "runner_webhook" | "runner_advisory" {
  switch (runnerType) {
    case "daemon_oci":
      return "runner_daemon";
    case "webhook_oci":
      return "runner_webhook";
    default:
      return "runner_advisory";
  }
}

export function QualityGateEvidenceCard({
  gate,
}: {
  gate: AutonomousQualityGate;
}) {
  const { t } = useT("projects");
  const evidence = asRecord(gate.evidence);
  const runnerEvidence = asRecord(evidence.runner_evidence);
  const runnerType = stringValue(runnerEvidence.runner_type ?? evidence.runner_type);
  const runnerLabel = runnerPresentation(runnerType);
  const presentation = runnerLabel === "runner_daemon"
    ? { className: "border-emerald-600/30 bg-emerald-600/10 text-emerald-700 dark:text-emerald-400", advisory: false }
    : runnerLabel === "runner_webhook"
      ? { className: "border-primary/30 bg-primary/10 text-primary", advisory: false }
      : { className: "border-amber-600/30 bg-amber-600/10 text-amber-700 dark:text-amber-400", advisory: true };
  const commitSHA = stringValue(runnerEvidence.commit_sha ?? evidence.commit_sha);
  const commitURL = safeCommitURL(runnerEvidence.commit_url ?? evidence.commit_url);
  const command = stringValue(runnerEvidence.executed_command ?? evidence.executed_command);
  const stdout = stringValue(runnerEvidence.stdout);
  const stderr = stringValue(runnerEvidence.stderr);
  const exitCode = numberValue(runnerEvidence.exit_code);
  const durationMs = numberValue(runnerEvidence.duration_ms);
  const browserQA = asRecord(runnerEvidence.browser_qa);
  const screenshots = Array.isArray(browserQA.screenshots)
    ? browserQA.screenshots.flatMap((value) => {
      const screenshot = asRecord(value);
      const contentType = stringValue(screenshot.content_type);
      const data = stringValue(screenshot.data_base64);
      if (
        (contentType !== "image/jpeg" && contentType !== "image/png") ||
        !data || data.length > 4_194_304 || !/^[A-Za-z0-9+/]*={0,2}$/.test(data)
      ) return [];
      return [{ contentType, data }];
    })
    : [];
  const waiver = asRecord(evidence.waiver ?? runnerEvidence.waiver);
  const waiverReason = stringValue(waiver.reason);
  const waivedBy = stringValue(waiver.waived_by ?? waiver.actor);
  const waivedAt = stringValue(waiver.waived_at ?? waiver.created_at);
  const hasDetails = Boolean(
    commitSHA || command || stdout || stderr || exitCode !== null ||
      durationMs !== null || waiverReason || waivedBy || waivedAt || gate.last_error || screenshots.length > 0,
  );

  return (
    <Card className="min-w-0 p-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="min-w-0">
          <div className="truncate font-medium">{gate.gate_type}</div>
          {gate.updated_at ? (
            <time className="text-caption text-muted-foreground" dateTime={gate.updated_at}>
              {gate.updated_at}
            </time>
          ) : null}
        </div>
        <Badge variant={gate.status === "failed" ? "destructive" : gate.status === "passed" ? "default" : "outline"}>
          {gate.status}
        </Badge>
      </div>

      <div className="mt-3 flex flex-wrap items-center gap-2">
        <Badge variant="outline" className={presentation.className}>
          {presentation.advisory ? <ShieldAlert aria-hidden="true" /> : <ShieldCheck aria-hidden="true" />}
          {t(($) => $.autonomous_evidence[runnerLabel])}
        </Badge>
        {commitSHA ? (
          commitURL ? (
            <a className="max-w-full truncate text-caption underline underline-offset-2" href={commitURL} target="_blank" rel="noreferrer">
              <code>{commitSHA}</code>
            </a>
          ) : <code className="max-w-full truncate text-caption">{commitSHA}</code>
        ) : null}
      </div>

      {hasDetails ? (
        <details className="mt-3 rounded-md border px-3 py-2">
          <summary className="cursor-pointer text-caption font-medium">{t(($) => $.autonomous_evidence.evidence_details)}</summary>
          <dl className="mt-3 grid gap-2 text-caption sm:grid-cols-[auto_1fr]">
            {command ? <><dt className="text-muted-foreground">{t(($) => $.autonomous_evidence.command)}</dt><dd className="break-all"><code>{command}</code></dd></> : null}
            {exitCode !== null ? <><dt className="text-muted-foreground">{t(($) => $.autonomous_evidence.exit_code)}</dt><dd>{exitCode}</dd></> : null}
            {durationMs !== null ? <><dt className="text-muted-foreground">{t(($) => $.autonomous_evidence.duration)}</dt><dd>{durationMs} {t(($) => $.autonomous_evidence.milliseconds)}</dd></> : null}
            {gate.last_error ? <><dt className="text-muted-foreground">{t(($) => $.autonomous_evidence.error)}</dt><dd className="break-words">{gate.last_error}</dd></> : null}
            {waiverReason || waivedBy || waivedAt ? (
              <>
                <dt className="text-muted-foreground">{t(($) => $.autonomous_evidence.waiver)}</dt>
                <dd className="break-words">
                  {[waiverReason, waivedBy, waivedAt].filter(Boolean).join(" · ")}
                </dd>
              </>
            ) : null}
            {stdout ? <><dt className="text-muted-foreground">{t(($) => $.autonomous_evidence.stdout)}</dt><dd className="max-h-48 overflow-auto whitespace-pre-wrap break-words rounded bg-muted p-2">{stdout}</dd></> : null}
            {stderr ? <><dt className="text-muted-foreground">{t(($) => $.autonomous_evidence.stderr)}</dt><dd className="max-h-48 overflow-auto whitespace-pre-wrap break-words rounded bg-muted p-2">{stderr}</dd></> : null}
          </dl>
          {screenshots.length > 0 ? (
            <div className="mt-3 space-y-2">
              <div className="text-caption font-medium">{t(($) => $.autonomous_evidence.screenshots)}</div>
              <div className="grid gap-2 sm:grid-cols-2">
                {screenshots.map((screenshot, index) => (
                  <img
                    key={`${screenshot.contentType}:${index}`}
                    alt={`${t(($) => $.autonomous_evidence.screenshot)} ${index + 1}`}
                    className="w-full rounded-md border"
                    loading="lazy"
                    src={`data:${screenshot.contentType};base64,${screenshot.data}`}
                  />
                ))}
              </div>
            </div>
          ) : null}
        </details>
      ) : (
        <p className="mt-2 text-caption text-muted-foreground">{t(($) => $.autonomous_evidence.no_additional_evidence)}</p>
      )}
    </Card>
  );
}
