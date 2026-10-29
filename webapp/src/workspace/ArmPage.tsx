import { Panel } from "@flanksource/clicky-ui/components";
import { useRouter } from "@flanksource/clicky-ui/rpc";
import { StartRunForm } from "../traces/StartRunForm";

export default function ArmPage({ agent, probe }: { agent?: string; probe?: string }) {
  const { navigate, renderLink } = useRouter();
  return <div className="space-y-4">
    <h1 className="text-2xl font-semibold">Arm captures</h1>
    <p className="text-sm text-muted-foreground">Configure captures against an agent, connection, host, or command. Set a duration to stop automatically, or stop the run from the overview.</p>
    {agent && <p>Agent <code>{agent}</code> · Probe <code>{probe}</code></p>}
    {renderLink({ to: "/agents", children: "Choose an agent", className: "text-sm underline underline-offset-4" })}
    <Panel title="Capture configuration">
      <StartRunForm key={`${agent}:${probe}`} agent={agent} probe={probe} onStarted={run => navigate(`/?run=${encodeURIComponent(run.runId)}`)} />
    </Panel>
  </div>;
}
