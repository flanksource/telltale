import { useState } from "react";
import { Button, Panel } from "@flanksource/clicky-ui/components";
import { useRouter } from "@flanksource/clicky-ui/rpc";
import { useAgents, type AgentInfo } from "../agents/api";
import { DeployAgentDialog } from "../agents/DeployAgentDialog";

export default function AgentsPage() {
  const { navigate } = useRouter();
  const agents = useAgents();
  const [deploying, setDeploying] = useState(false);
  const groups = new Map<string, AgentInfo[]>();
  for (const agent of agents.data ?? []) {
    const entries = groups.get(agent.group);
    if (entries) entries.push(agent);
    else groups.set(agent.group, [agent]);
  }
  // One new agent opens its JVM diagnostics; several deployed targets stay listed here.
  const deployed = (ids: string[]) => {
    if (ids.length === 1) navigate(`/jvm/${encodeURIComponent(ids[0]!)}`);
  };
  return <div className="space-y-4">
    <div className="flex items-center justify-between gap-3">
      <h1 className="text-2xl font-semibold">Agents</h1>
      <div className="flex gap-2">
        <Button onClick={() => setDeploying(true)}>Deploy an agent</Button>
        <Button variant="outline" onClick={() => void agents.refetch()} disabled={agents.isFetching}>Refresh</Button>
      </div>
    </div>
    <p className="text-sm text-muted-foreground">Connected agents and saved deployed targets, grouped by namespace, cloud, or host. Open diagnostics to connect to a saved JVM target.</p>
    {deploying && <DeployAgentDialog open onClose={() => setDeploying(false)} onDeployed={deployed} />}
    {agents.isPending && <p role="status">Loading agents…</p>}
    {agents.error && <p role="alert">{agents.error.message}</p>}
    {agents.data?.length === 0 && <p>No agents found. <button type="button" className="text-primary underline underline-offset-4" onClick={() => setDeploying(true)}>Deploy the Java agent</button> into a JVM on this host or into Kubernetes pods, or connect an instrumented application to this Telltale server.</p>}
    {[...groups].map(([group, entries]) => <section key={group} className="space-y-3" aria-label={group}>
      <h2 className="text-lg font-semibold break-words">{group}</h2>
      {entries.map(agent => {
        const runtime = agent.connected ? agent.runtime.lang : agent.known.runtime;
        const name = agent.connected ? agent.service : agent.known.names?.[0];
        return <Panel key={agent.agentId} title={name || agent.agentId}>
      <div className="space-y-3">
        <p className="break-words"><code>{agent.agentId}</code> · {runtime}{agent.connected && ` ${agent.runtime.version}`}</p>
        <p className="text-sm"><span>{agent.connected ? "Connected" : "Not connected"}</span>{!agent.connected && agent.known.state && <> · <span>{agent.known.state}</span></>}</p>
        {!agent.connected && agent.known.endpoint && <p className="break-all text-sm"><code>{agent.known.endpoint}</code></p>}
        {runtime === "java" && <Button variant="outline" onClick={() => navigate(`/jvm/${encodeURIComponent(agent.agentId)}`)}>JVM diagnostics</Button>}
        <dl className="grid gap-1 text-sm sm:grid-cols-2">
          {Object.entries({ ...agent.resource, ...(agent.connected ? agent.labels : {}) }).map(([key, value]) => <div key={key} className="min-w-0 break-words"><dt className="inline text-muted-foreground">{key}: </dt><dd className="inline">{value}</dd></div>)}
        </dl>
        {agent.connected && agent.warnings?.map(warning => <p role="alert" key={warning}>{warning}</p>)}
        {agent.connected ? <div className="divide-y divide-border">
          {agent.probes.map(probe => <div key={probe.name} className="flex flex-wrap items-center justify-between gap-3 py-2">
            <div><span className="font-medium">{probe.title || probe.name}</span> <code className="text-xs text-muted-foreground">{probe.name}</code>{probe.mutates && <p className="text-sm text-muted-foreground">Changes runtime settings; mutation must be enabled in the capture target.</p>}</div>
            <Button size="sm" variant="outline" onClick={() => navigate(`/arm/${encodeURIComponent(agent.agentId)}/${encodeURIComponent(probe.name)}`)}>Arm capture</Button>
          </div>)}
          {agent.probes.length === 0 && <p>No probes advertised.</p>}
        </div> : <p className="text-sm text-muted-foreground">Live probes and capabilities load when this target connects.</p>}
      </div>
    </Panel>;
      })}
    </section>)}
  </div>;
}
