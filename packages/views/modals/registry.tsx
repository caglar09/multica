"use client";

import type { ReactNode } from "react";
import { useModalStore } from "@multica/core/modals";
import { CreateIssueDialog } from "./create-issue-dialog";
import { CreateProjectModal } from "./create-project";
import { CreateSquadModal } from "./create-squad";
import { FeedbackModal } from "./feedback";
import { SetParentIssueModal } from "./set-parent-issue";
import { AddChildIssueModal } from "./add-child-issue";
import { DeleteIssueConfirmModal } from "./delete-issue-confirm";
import { RunConfirmModal } from "./run-confirm";
import { IssueLimitUpgradeDialog } from "./issue-limit-upgrade-dialog";
import {
  ProductionDeployConfirmDialog,
  VercelTargetAttachDialog,
  PromoteDeploymentConfirmDialog,
  RollbackDeploymentConfirmDialog,
  DisconnectProviderConfirmDialog,
} from "../deployments";

export function ModalRegistry() {
  const modal = useModalStore((s) => s.modal);
  const data = useModalStore((s) => s.data);
  const close = useModalStore((s) => s.close);

  let activeModal: ReactNode = null;
  switch (modal) {
    // Both modal types open the same shell so the in-modal mode switch is
    // instant — only the inner panel swaps, the Dialog Root stays mounted.
    case "create-issue":
      activeModal = (
        <CreateIssueDialog
          onClose={close}
          initialMode="manual"
          data={data}
        />
      );
      break;
    case "quick-create-issue":
      activeModal = (
        <CreateIssueDialog
          onClose={close}
          initialMode="agent"
          data={data}
        />
      );
      break;
    case "create-project":
      activeModal = <CreateProjectModal onClose={close} />;
      break;
    case "create-squad":
      activeModal = <CreateSquadModal onClose={close} />;
      break;
    case "feedback":
      activeModal = <FeedbackModal onClose={close} data={data} />;
      break;
    case "issue-set-parent":
      activeModal = <SetParentIssueModal onClose={close} data={data} />;
      break;
    case "issue-add-child":
      activeModal = <AddChildIssueModal onClose={close} data={data} />;
      break;
    case "issue-delete-confirm":
      activeModal = <DeleteIssueConfirmModal onClose={close} data={data} />;
      break;
    case "issue-run-confirm":
      activeModal = <RunConfirmModal onClose={close} data={data} />;
      break;
    case "production-deploy-confirm": {
      const d = (data ?? {}) as Record<string, unknown>;
      activeModal = (
        <ProductionDeployConfirmDialog
          open
          onOpenChange={(open) => {
            if (!open) close();
          }}
          targetName={(d.targetName as string) ?? "Production Target"}
          providerName={(d.providerName as string) ?? "Vercel"}
          teamSlug={d.teamSlug as string | undefined}
          commitSha={(d.commitSha as string) ?? ""}
          gitRef={d.gitRef as string | undefined}
          commitMessage={d.commitMessage as string | undefined}
          initiatorName={d.initiatorName as string | undefined}
          environment={(d.environment as string) ?? "production"}
          onConfirm={async (approvalRef) => {
            if (typeof d.onConfirm === "function") {
              await (d.onConfirm as (ref: string) => Promise<void> | void)(approvalRef);
            }
            close();
          }}
        />
      );
      break;
    }
    case "vercel-target-attach": {
      const d = (data ?? {}) as Record<string, unknown>;
      activeModal = (
        <VercelTargetAttachDialog
          open
          onOpenChange={(open) => {
            if (!open) close();
          }}
          connectionId={(d.connectionId as string) ?? ""}
          projectId={d.projectId as string | undefined}
          discoveredProjects={
            (d.discoveredProjects as any[]) ?? []
          }
          defaultEnvironment={(d.defaultEnvironment as any) ?? "production"}
          defaultBranch={(d.defaultBranch as string) ?? "main"}
          onSubmitTarget={async (input) => {
            if (typeof d.onSubmitTarget === "function") {
              await (d.onSubmitTarget as (inp: any) => Promise<void> | void)(input);
            }
            close();
          }}
        />
      );
      break;
    }
    case "promote-deployment-confirm": {
      const d = (data ?? {}) as Record<string, unknown>;
      activeModal = (
        <PromoteDeploymentConfirmDialog
          open
          onOpenChange={(open) => {
            if (!open) close();
          }}
          candidateOperation={d.candidateOperation as any}
          target={d.target as any}
          targetName={d.targetName as string | undefined}
          providerName={d.providerName as string | undefined}
          onConfirm={async (approvalRef) => {
            if (typeof d.onConfirm === "function") {
              await (d.onConfirm as (ref: string) => Promise<void> | void)(approvalRef);
            }
            close();
          }}
        />
      );
      break;
    }
    case "rollback-deployment-confirm": {
      const d = (data ?? {}) as Record<string, unknown>;
      activeModal = (
        <RollbackDeploymentConfirmDialog
          open
          onOpenChange={(open) => {
            if (!open) close();
          }}
          targetOperation={d.targetOperation as any}
          target={d.target as any}
          targetName={d.targetName as string | undefined}
          providerName={d.providerName as string | undefined}
          onConfirm={async (approvalRef) => {
            if (typeof d.onConfirm === "function") {
              await (d.onConfirm as (ref: string) => Promise<void> | void)(approvalRef);
            }
            close();
          }}
        />
      );
      break;
    }
    case "disconnect-provider-confirm": {
      const d = (data ?? {}) as Record<string, unknown>;
      activeModal = (
        <DisconnectProviderConfirmDialog
          open
          onOpenChange={(open) => {
            if (!open) close();
          }}
          connection={d.connection as any}
          targetCount={d.targetCount as number | undefined}
          onConfirm={async () => {
            if (typeof d.onConfirm === "function") {
              await (d.onConfirm as () => Promise<void> | void)();
            }
            close();
          }}
        />
      );
      break;
    }
  }

  return (
    <>
      {activeModal}
      <IssueLimitUpgradeDialog />
    </>
  );
}
