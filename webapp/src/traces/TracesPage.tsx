import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Button, Panel } from "@flanksource/clicky-ui/components";
import { fetchTraceRuns, type RunTraces } from "./api";
import { RunProcesses } from "./RunProcesses";
import { TracesPanel } from "./TracesPanel";
import { useProcessTrace } from "./useProcessTrace";

function RunDetail({ run }: { run: RunTraces }) {
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const [hideExited, setHideExited] = useState(false);
  const [expandAll, setExpandAll] = useState<boolean | null>(true);
  const trace = useProcessTrace({
    mount: run.mount,
    ...(run.run ? { refs: run.run.process } : {}),
    ...(selectedKey ? { selectedKey } : {}),
    done: true,
  });
  if (!run.run) return <p role="alert">{run.unavailable}</p>;
  return (
    <div className="space-y-6">
      <p className="text-sm text-muted-foreground">
        <code>{run.run.command.join(" ")}</code> exited with code {run.run.exitCode}
      </p>
      <Panel title="Captures"><TracesPanel run={run} /></Panel>
      <Panel title="Processes">
        <RunProcesses
          className="h-[32rem] min-h-0"
          trace={trace}
          selectedKey={selectedKey}
          onSelect={setSelectedKey}
          hideExited={hideExited}
          onHideExitedChange={setHideExited}
          expandAll={expandAll}
          onExpandAllChange={setExpandAll}
        />
      </Panel>
    </div>
  );
}

/** The runs `telltale trace run` recorded, newest first, and the selected one's captures and processes. */
export default function TracesPage() {
  const query = useQuery({ queryKey: ["trace-runs"], queryFn: fetchTraceRuns, retry: false });
  const [selected, setSelected] = useState<string | null>(null);
  if (query.isPending) return <p role="status">Loading trace runs…</p>;
  if (query.isError) return (
    <div className="space-y-4">
      <p role="alert">{query.error.message}</p>
      <Button onClick={() => void query.refetch()}>Retry</Button>
    </div>
  );
  const runs = query.data.runs;
  if (runs.length === 0) return (
    <p className="text-sm text-muted-foreground">No runs recorded yet. Record one with <code>telltale trace run -- &lt;command&gt;</code>.</p>
  );
  const run = runs.find(candidate => candidate.runId === selected) ?? runs[0]!;
  return (
    <div className="space-y-6">
      <label className="flex items-center gap-2 text-sm">
        Run
        <select className="rounded border border-border bg-background px-2 py-1" value={run.runId} onChange={event => setSelected(event.target.value)}>
          {runs.map(candidate => <option key={candidate.runId} value={candidate.runId}>{candidate.runId}</option>)}
        </select>
      </label>
      <RunDetail key={run.runId} run={run} />
    </div>
  );
}
