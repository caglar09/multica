import type { AutonomousProjectPlanSnapshot } from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@multica/ui/components/ui/card";

export interface AutonomousPlanApprovalLabels {
  title: string;
  pendingDescription: string;
  approvedDescription: string;
  digest: string;
  digestMissing: string;
  adminOnly: string;
  approve: string;
}

export function AutonomousPlanApprovalCard({
  plan,
  canControl,
  isPending,
  labels,
  onApprove,
}: {
  plan: AutonomousProjectPlanSnapshot;
  canControl: boolean;
  isPending: boolean;
  labels: AutonomousPlanApprovalLabels;
  onApprove: () => void;
}) {
  const awaitingApproval = plan.status === "awaiting_approval";

  return (
    <Card>
      <CardHeader>
        <CardTitle>{labels.title}</CardTitle>
        <CardDescription>
          {awaitingApproval
            ? labels.pendingDescription
            : labels.approvedDescription}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <div>
          <div className="text-caption font-medium text-muted-foreground">
            {labels.digest}
          </div>
          {plan.plan_digest ? (
            <code className="mt-1 block break-all rounded bg-muted/40 p-2 font-mono text-[11px] select-all">
              {plan.plan_digest}
            </code>
          ) : (
            <p className="mt-1 text-caption text-destructive">
              {labels.digestMissing}
            </p>
          )}
        </div>
        {awaitingApproval ? (
          canControl ? (
            <Button
              disabled={!plan.plan_digest || isPending}
              onClick={onApprove}
            >
              {labels.approve}
            </Button>
          ) : (
            <p className="text-caption text-muted-foreground">
              {labels.adminOnly}
            </p>
          )
        ) : (
          <Badge variant="outline">{plan.status}</Badge>
        )}
      </CardContent>
    </Card>
  );
}
