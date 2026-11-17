import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { useRouter } from "@flanksource/clicky-ui/rpc";
import { isConnectedAgent, useAgents, useDeployedTargets } from "../agents/api";
import { ArmWizard } from "../arm/ArmWizard";
import type { ArmCatalog } from "../arm/catalog";
import { EMPTY_DRAFT, agentProbeDraft, readKinds } from "../arm/model";
import { useConnections, useDefinitions } from "../definitions/api";
import { captureParameters, selectedParameters } from "../jvm/api";
import { fetchStartForm } from "../traces/api";

/** /arm, and /arm/<agent>/<probe> opened from an agent's probe with that capture armed. */
export default function ArmPage({ agent, probe }: { agent?: string; probe?: string }) {
  const { navigate } = useRouter();
  const form = useQuery({ queryKey: ["trace-start-form"], queryFn: fetchStartForm, retry: false, staleTime: Infinity });
  const connections = useConnections();
  const definitions = useDefinitions();
  const agents = useAgents();
  const deployed = useDeployedTargets();
  const kinds = useMemo(() => (form.data ? readKinds(form.data) : undefined), [form.data]);
  const loading = [form, connections, definitions, agents, deployed].find(query => query.isPending);
  const failed = [form, connections, definitions, agents, deployed].find(query => query.isError);
  const header = (
    <>
      <h1 className="text-2xl font-semibold">Arm captures</h1>
      <p className="text-sm text-muted-foreground">Choose what to record and where each capture runs: a connected agent, a saved connection, a local proxy or process. Deploy an agent from any agent picker when none is connected.</p>
    </>
  );
  if (failed?.error) return <div className="space-y-4">{header}<p role="alert">{failed.error.message}</p></div>;
  if (loading || !kinds) return <div className="space-y-4">{header}<p role="status">Loading the arming form…</p></div>;
  const catalog: ArmCatalog = { kinds, connections: connections.data ?? [], definitions: definitions.data ?? [], agents: (agents.data ?? []).filter(isConnectedAgent), deployed: deployed.data ?? [] };
  if (probe && !kinds.some(kind => kind.name === probe && kind.seams.includes("agent"))) {
    return <div className="space-y-4">{header}<p role="alert">Probe {probe} has no trace kind this server arms on an agent.</p></div>;
  }
  const initial = agent && probe ? agentProbeDraft(kinds, agent, probe, captureParameters(probe, selectedParameters(window.location.search))) : EMPTY_DRAFT;
  return (
    <div className="space-y-4">
      {header}
      <ArmWizard key={`${agent}:${probe}`} catalog={catalog} initial={initial} {...(agent && probe ? { initialStep: "captures" as const } : {})} onStarted={run => navigate(`/?run=${encodeURIComponent(run.runId)}`)} />
    </div>
  );
}
