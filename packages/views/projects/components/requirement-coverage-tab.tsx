import { Badge } from "@multica/ui/components/ui/badge";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@multica/ui/components/ui/card";
import type {
  AutonomousProjectPlanNode,
  AutonomousProjectSnapshot,
} from "@multica/core/types";
import { useT } from "../../i18n";

function nodeStatus(
  nodes: AutonomousProjectPlanNode[],
  gateStatuses: string[],
): string {
  if (nodes.length === 0) return "unmapped";
  if (gateStatuses.includes("failed")) return "verification_failed";
  if (gateStatuses.length > 0 && gateStatuses.every((status) => status === "passed")) {
    return "verified";
  }
  if (nodes.every((node) => node.status === "completed")) return "awaiting_verification";
  return "in_progress";
}

type RequirementCoverageSnapshot = Pick<
  AutonomousProjectSnapshot,
  "plan" | "quality_gates" | "deployments"
>;

function latestDeployments(snapshot: RequirementCoverageSnapshot) {
  const latest = new Map<string, AutonomousProjectSnapshot["deployments"][number]>();
  for (const deployment of snapshot.deployments) {
    const current = latest.get(deployment.environment);
    if (!current || deployment.updated_at > current.updated_at) {
      latest.set(deployment.environment, deployment);
    }
  }
  return Array.from(latest.values()).sort((left, right) =>
    left.environment.localeCompare(right.environment),
  );
}

export function RequirementCoverageTab({
  snapshot,
}: {
  snapshot: RequirementCoverageSnapshot;
}) {
  const { t } = useT("projects");
  const specification = snapshot.plan?.specification;
  const requirements = specification?.requirement_details ?? [];
  const planNodes = snapshot.plan?.nodes ?? [];
  const deployments = latestDeployments(snapshot);
  const nonGoals = specification?.non_goals ?? [];

  return (
    <div className="space-y-4" aria-label={t(($) => $.autonomous_evidence.requirement_coverage)}>
      <Card>
        <CardHeader>
          <CardTitle>{t(($) => $.autonomous_evidence.requirement_coverage)}</CardTitle>
          <CardDescription>
            {t(($) => $.autonomous_evidence.coverage_description)}
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          {requirements.length > 0 ? requirements.map((requirement) => {
            const nodes = planNodes.filter((node) => node.requirement_ids.includes(requirement.id));
            const nodeIDs = new Set(nodes.map((node) => node.id));
            const gates = snapshot.quality_gates.filter((gate) => nodeIDs.has(gate.node_id));
            const status = nodeStatus(nodes, gates.map((gate) => gate.status));

            return (
              <article key={requirement.id} className="min-w-0 rounded-lg border p-3">
                <div className="flex flex-wrap items-center gap-2">
                  <Badge variant="outline">{requirement.id}</Badge>
                  <Badge variant={status === "verified" ? "default" : status === "verification_failed" ? "destructive" : "secondary"}>
                    {t(($) => $.autonomous_evidence.coverage_status[status as keyof typeof $.autonomous_evidence.coverage_status])}
                  </Badge>
                  <span className="font-medium">{requirement.text}</span>
                </div>
                {requirement.rationale ? (
                  <p className="mt-2 text-caption text-muted-foreground">{requirement.rationale}</p>
                ) : null}
                <div className="mt-3 grid gap-3 text-caption md:grid-cols-3">
                  <div className="min-w-0">
                    <div className="font-medium">{t(($) => $.autonomous_evidence.plan_nodes)}</div>
                    {nodes.length > 0 ? (
                      <ul className="mt-1 space-y-1">
                        {nodes.map((node) => (
                          <li key={node.id} className="break-words">
                            {node.is_epic ? `${t(($) => $.autonomous_evidence.epic)} · ` : `${t(($) => $.autonomous_evidence.task)} · `}{node.title} <span className="text-muted-foreground">({node.status})</span>
                          </li>
                        ))}
                      </ul>
                    ) : <p className="mt-1 text-muted-foreground">{t(($) => $.autonomous_evidence.no_linked_plan_node)}</p>}
                  </div>
                  <div className="min-w-0">
                    <div className="font-medium">{t(($) => $.autonomous_evidence.verification_gates)}</div>
                    {gates.length > 0 ? (
                      <ul className="mt-1 space-y-1">
                        {gates.map((gate) => (
                          <li key={gate.id} className="break-words">
                            {gate.gate_type} · {gate.status}
                          </li>
                        ))}
                      </ul>
                    ) : <p className="mt-1 text-muted-foreground">{t(($) => $.autonomous_evidence.no_verification_evidence)}</p>}
                  </div>
                  <div className="min-w-0">
                    <div className="font-medium">{t(($) => $.autonomous_evidence.latest_deployments)}</div>
                    {deployments.length > 0 ? (
                      <ul className="mt-1 space-y-1">
                        {deployments.map((deployment) => (
                          <li key={deployment.id} className="break-words">
                            {deployment.environment} · {deployment.status}
                            {deployment.commit_sha ? <code className="ml-1">{deployment.commit_sha}</code> : null}
                          </li>
                        ))}
                      </ul>
                    ) : <p className="mt-1 text-muted-foreground">{t(($) => $.autonomous_evidence.no_deployment_recorded)}</p>}
                  </div>
                </div>
              </article>
            );
          }) : (
            <p className="py-4 text-center text-muted-foreground">
              {t(($) => $.autonomous_evidence.stable_requirements_missing)}
            </p>
          )}
        </CardContent>
      </Card>

      {nonGoals.length > 0 ? (
        <Card>
          <CardHeader>
            <CardTitle>{t(($) => $.autonomous_evidence.non_goals)}</CardTitle>
            <CardDescription>{t(($) => $.autonomous_evidence.non_goals_description)}</CardDescription>
          </CardHeader>
          <CardContent className="space-y-2">
            {nonGoals.map((item, index) => (
              <div key={`${index}:${item}`} className="flex flex-wrap items-start gap-2 rounded-md border p-3">
                <Badge variant="outline">{t(($) => $.autonomous_evidence.non_goal)}</Badge>
                <span className="text-sm">{item}</span>
              </div>
            ))}
          </CardContent>
        </Card>
      ) : null}
    </div>
  );
}
