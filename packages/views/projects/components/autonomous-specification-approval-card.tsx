import type { AutonomousProjectSpecificationApproval } from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@multica/ui/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@multica/ui/components/ui/dialog";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { LoaderCircle } from "lucide-react";
import { useState } from "react";

export interface AutonomousSpecificationApprovalLabels {
  title: string;
  summary: string;
  requirements: string;
  requirementsEmpty: string;
  digest: string;
  approve: string;
  reject: string;
  adminOnly: string;
  localAdoptionRequired: string;
  awaitingApproval: string;
  approved: string;
  rejected: string;
  approvalHistory: string;
  historyEmpty: string;
  eventApproved: string;
  eventRejected: string;
  eventUnknown: string;
  originLocal: string;
  originImported: string;
  originUnknown: string;
  rejectionTitle: string;
  rejectionDescription: string;
  rejectionReason: string;
  rejectionReasonPlaceholder: string;
  rejectionReasonRequired: string;
  rejectionFailed: string;
  cancel: string;
  submitRejection: string;
}

export function AutonomousSpecificationApprovalCard({
  specification,
  canControl,
  isPending,
  labels,
  onApprove,
  onReject,
}: {
  specification: AutonomousProjectSpecificationApproval;
  canControl: boolean;
  isPending: boolean;
  labels: AutonomousSpecificationApprovalLabels;
  onApprove: () => void;
  onReject: (reason: string) => Promise<boolean>;
}) {
  const [rejectOpen, setRejectOpen] = useState(false);
  const [reason, setReason] = useState("");
  const [rejectError, setRejectError] = useState("");
  const awaitingApproval = specification.status === "awaiting_approval";
  const canDecide = canControl && specification.is_proposal &&
    Boolean(specification.change_request_id && specification.digest) && awaitingApproval;
  const statusLabel = specification.status === "approved"
    ? labels.approved
    : specification.status === "rejected"
      ? labels.rejected
      : labels.awaitingApproval;

  const submitRejection = async () => {
    const trimmedReason = reason.trim();
    if (!trimmedReason) {
      setRejectError(labels.rejectionReasonRequired);
      return;
    }
    setRejectError("");
    if (await onReject(trimmedReason)) {
      setReason("");
      setRejectOpen(false);
    } else {
      setRejectError(labels.rejectionFailed);
    }
  };

  return (
    <Card>
      <CardHeader>
        <div className="flex flex-wrap items-center justify-between gap-2">
          <CardTitle>{labels.title}</CardTitle>
          <Badge variant={specification.status === "approved" ? "default" : "outline"}>
            {statusLabel}
          </Badge>
        </div>
        <CardDescription>{specification.specification.summary ?? labels.summary}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <section aria-label={labels.requirements}>
          <h3 className="text-caption font-medium text-muted-foreground">{labels.requirements}</h3>
          {specification.specification.requirements.length ? (
            <ul className="mt-2 space-y-2">
              {specification.specification.requirements.map((requirement) => (
                <li key={requirement.id} className="rounded-md border p-2 text-caption">
                  <div className="font-medium">{requirement.id}</div>
                  <div>{requirement.text}</div>
                  {requirement.rationale ? (
                    <div className="mt-1 text-muted-foreground">{requirement.rationale}</div>
                  ) : null}
                </li>
              ))}
            </ul>
          ) : (
            <p className="mt-2 text-caption text-muted-foreground">{labels.requirementsEmpty}</p>
          )}
        </section>

        <section>
          <h3 className="text-caption font-medium text-muted-foreground">{labels.digest}</h3>
          <code className="mt-1 block break-all rounded bg-muted/40 p-2 font-mono text-[11px] select-all">
            {specification.digest}
          </code>
        </section>

        {canDecide ? (
          <div className="flex flex-wrap gap-2">
            <Button disabled={isPending} aria-busy={isPending} onClick={onApprove}>
              {isPending ? <LoaderCircle className="animate-spin" /> : null}
              {labels.approve}
            </Button>
            <Dialog open={rejectOpen} onOpenChange={setRejectOpen}>
              <DialogTrigger render={<Button type="button" variant="outline" disabled={isPending} aria-busy={isPending} />}>
                {labels.reject}
              </DialogTrigger>
              <DialogContent>
                <DialogHeader>
                  <DialogTitle>{labels.rejectionTitle}</DialogTitle>
                  <DialogDescription>{labels.rejectionDescription}</DialogDescription>
                </DialogHeader>
                <label className="grid gap-2 text-caption font-medium">
                  {labels.rejectionReason}
                  <Textarea
                    value={reason}
                    onChange={(event) => setReason(event.target.value)}
                    placeholder={labels.rejectionReasonPlaceholder}
                    aria-invalid={Boolean(rejectError)}
                    aria-describedby={rejectError ? "specification-rejection-error" : undefined}
                  />
                </label>
                {rejectError ? (
                  <p id="specification-rejection-error" role="alert" className="text-caption text-destructive">
                    {rejectError}
                  </p>
                ) : null}
                <DialogFooter>
                  <Button type="button" variant="outline" onClick={() => setRejectOpen(false)}>
                    {labels.cancel}
                  </Button>
                  <Button type="button" variant="destructive" disabled={isPending} aria-busy={isPending} onClick={() => void submitRejection()}>
                    {isPending ? <LoaderCircle className="animate-spin" /> : null}
                    {labels.submitRejection}
                  </Button>
                </DialogFooter>
              </DialogContent>
            </Dialog>
          </div>
        ) : awaitingApproval && !specification.is_proposal ? (
          <p className="text-caption text-muted-foreground">{labels.localAdoptionRequired}</p>
        ) : awaitingApproval ? (
          <p className="text-caption text-muted-foreground">{labels.adminOnly}</p>
        ) : null}

        <section>
          <h3 className="text-caption font-medium text-muted-foreground">{labels.approvalHistory}</h3>
          {specification.approval_history.length ? (
            <ol className="mt-2 space-y-2 border-l pl-4">
              {specification.approval_history.map((event) => (
                <li key={event.id} className="space-y-1 text-caption">
                  <div className="font-medium">
                    {event.decision === "approved"
                      ? labels.eventApproved
                      : event.decision === "rejected"
                        ? labels.eventRejected
                        : labels.eventUnknown} · {event.actor_display}
                  </div>
                  <div className="text-muted-foreground">
                    {event.acted_at} · {event.origin === "local"
                      ? labels.originLocal
                      : event.origin === "imported"
                        ? labels.originImported
                        : labels.originUnknown}
                  </div>
                  {event.rationale ? <p>{event.rationale}</p> : null}
                </li>
              ))}
            </ol>
          ) : (
            <p className="mt-2 text-caption text-muted-foreground">{labels.historyEmpty}</p>
          )}
        </section>
      </CardContent>
    </Card>
  );
}
