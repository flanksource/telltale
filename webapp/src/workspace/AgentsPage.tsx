import { useQuery } from "@tanstack/react-query";
import { Button, Panel } from "@flanksource/clicky-ui/components";
import { useRouter } from "@flanksource/clicky-ui/rpc";
import { failure } from "../traces/api";

interface Agent {
  agentId: string;
  service: string;
  runtime: { lang: string; version: string };
  resource: Record<string, string>;
  labels: Record<string, string>;
  probes: { name: string; title: string; mutates?: boolean; checkpoints?: boolean }[];
  warnings?: string[];
}

async function fetchAgents(): Promise<Agent[]> {
  const response = await fetch("/api/agents", { headers: { Accept: "application/json" } });
  if (!response.ok) throw await failure(response, "List agents");
  return response.json() as Promise<Agent[]>;
}

export default function AgentsPage() {
  const { navigate } = useRouter();
  const agents = useQuery({ queryKey: ["agents"], queryFn: fetchAgents, retry: false, refetchInterval: 5000 });
  return <div className="space-y-4">
    <div className="flex items-center justify-between gap-3">
      <h1 className="text-2xl font-semibold">Agents</h1>
      <Button variant="outline" onClick={() => void agents.refetch()} disabled={agents.isFetching}>Refresh</Button>
    </div>
    <p className="text-sm text-muted-foreground">Connected agents and the probes they advertise. Select a probe to configure and arm a capture.</p>
    {agents.isPending && <p role="status">Loading agents…</p>}
    {agents.error && <p role="alert">{agents.error.message}</p>}
    {agents.data?.length === 0 && <p>No agents connected. Connect an instrumented application to this Telltale server to begin.</p>}
    {agents.data?.map(agent => <Panel key={agent.agentId} title={agent.service || agent.agentId}>
      <div className="space-y-3">
        <p><code>{agent.agentId}</code> · {agent.runtime.lang} {agent.runtime.version}</p>
        <dl className="grid gap-1 text-sm sm:grid-cols-2">
          {Object.entries({ ...agent.resource, ...agent.labels }).map(([key, value]) => <div key={key} className="min-w-0 break-words"><dt className="inline text-muted-foreground">{key}: </dt><dd className="inline">{value}</dd></div>)}
        </dl>
        {agent.warnings?.map(warning => <p role="alert" key={warning}>{warning}</p>)}
        <div className="divide-y divide-border">
          {agent.probes.map(probe => <div key={probe.name} className="flex flex-wrap items-center justify-between gap-3 py-2">
            <div><span className="font-medium">{probe.title || probe.name}</span> <code className="text-xs text-muted-foreground">{probe.name}</code>{probe.mutates && <p className="text-sm text-muted-foreground">Changes runtime settings; mutation must be enabled in the capture target.</p>}</div>
            <Button size="sm" variant="outline" onClick={() => navigate(`/arm/${encodeURIComponent(agent.agentId)}/${encodeURIComponent(probe.name)}`)}>Arm capture</Button>
          </div>)}
          {agent.probes.length === 0 && <p>No probes advertised.</p>}
        </div>
      </div>
    </Panel>)}
  </div>;
}
