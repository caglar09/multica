// @vitest-environment jsdom
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { AutonomousQualityGate } from "@multica/core/types";
import { QualityGateEvidenceCard } from "./quality-gate-evidence-card";

vi.mock("../../i18n", () => {
  const resources = {
    autonomous_evidence: {
      runner_daemon: "Daemon Local Runner",
      runner_webhook: "Webhook Runner",
      runner_advisory: "Advisory Agent Claim",
      evidence_details: "Evidence details",
      command: "Command",
      exit_code: "Exit code",
      duration: "Duration",
      milliseconds: "ms",
      error: "Error",
      waiver: "Waiver",
      stdout: "stdout",
      stderr: "stderr",
      no_additional_evidence: "No additional runner evidence was recorded.",
      screenshots: "Screenshots",
      screenshot: "Screenshot",
    },
  };
  return {
    useT: () => ({
      t: (selector: (value: typeof resources) => string) => selector(resources),
    }),
  };
});

function gate(id: string, evidence: Record<string, unknown>): AutonomousQualityGate {
  return {
    id,
    node_id: "node-id",
    gate_type: "unit_test",
    status: "passed",
    required: true,
    evidence,
    last_error: null,
    updated_at: "2026-10-28T00:00:00Z",
  };
}

describe("autonomous control center quality evidence", () => {
  it("distinguishes deterministic runner evidence from an advisory claim", () => {
    render(
      <>
        <QualityGateEvidenceCard
          gate={gate("runner", {
            mode: "deterministic_runner",
            runner_evidence: {
              runner_type: "daemon_oci",
              commit_sha: "verified-sha",
              executed_command: "go test ./...",
              exit_code: 0,
              duration_ms: 1200,
              stdout: "ok",
              browser_qa: {
                screenshots: [{ content_type: "image/jpeg", data_base64: "aGVsbG8=" }],
              },
            },
          })}
        />
        <QualityGateEvidenceCard gate={gate("advisory", {})} />
      </>,
    );

    expect(screen.getByText("Daemon Local Runner")).toBeTruthy();
    expect(screen.getByText("Advisory Agent Claim")).toBeTruthy();
    expect(screen.getByText("verified-sha")).toBeTruthy();
    fireEvent.click(screen.getByText("Evidence details"));
    expect(screen.getByText("go test ./...")).toBeTruthy();
    expect(screen.getByText("1200 ms")).toBeTruthy();
    expect(screen.getByText("ok")).toBeTruthy();
    expect(screen.getByRole("img", { name: "Screenshot 1" })).toBeTruthy();
  });
});
