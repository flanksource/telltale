import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button, Panel } from "@flanksource/clicky-ui/components";
import { fetchTraceRuns, RUN_STATUSES, stopRun, type RunFilter, type RunStatus, type RunTraces } from "./api";
import { DEFAULT_VIEW, type GroupsView } from "../top/groups";
import { processGroupsCapture, RecordedGroups } from "../top/RecordedGroups";
import { RunProcesses } from "./RunProcesses";
import { StartRunForm } from "./StartRunForm";
import { TracesPanel } from "./TracesPanel";
import { useProcessTrace } from "./useProcessTrace";

const LIVE_POLL_MS = 1000;

// The page's state lives in the URL: the filter (q, status, kind) and the
// selected run.
interface PageState extends RunFilter {
  run?: string;
}

const PAGE_PARAMS = ["q", "status", "kind", "run"];

function readPageState(): PageState {
  const params = new URLSearchParams(window.location.search);
  const status = params.get("status") ?? "";
  return {
    q: params.get("q") ?? "",
    status: (RUN_STATUSES as string[]).includes(status) ? status as RunStatus : "",
    kind: params.get("kind") ?? "",
    ...(params.get("run") ? { run: params.get("run")! } : {}),
  };
}

function usePageState(): [PageState, (next: Partial<PageState>) => void] {
  const [state, setState] = useState(readPageState);
  useEffect(() => {
    // The page owns only its own parameters: others, such as the process groups panel's, stay.
    const params = new URLSearchParams(window.location.search);
    for (const key of PAGE_PARAMS) params.delete(key);
    for (const [key, value] of Object.entries(state)) if (value) params.set(key, String(value));
    const search = params.toString();
    window.history.replaceState(null, "", `${window.location.pathname}${search ? `?${search}` : ""}${window.location.hash}`);
  }, [state]);
  return [state, next => setState(previous => ({ ...previous, ...next }))];
}

function RunFilters({ filter, onChange }: { filter: RunFilter; onChange: (next: RunFilter) => void }) {
  const input = "rounded border border-border bg-background px-2 py-1";
  return (
    <div className="flex flex-wrap items-center gap-3 text-sm">
      <label className="flex items-center gap-2">
        Search
        <input type="search" className={input} placeholder="Command or run id" value={filter.q ?? ""} onChange={event => onChange({ q: event.target.value })} />
      </label>
      <label className="flex items-center gap-2">
        Status
        <select className={input} value={filter.status ?? ""} onChange={event => onChange({ status: event.target.value as RunStatus | "" })}>
          <option value="">Any</option>
          {RUN_STATUSES.map(status => <option key={status} value={status}>{status}</option>)}
        </select>
      </label>
      <label className="flex items-center gap-2">
        Capture
        <input type="search" className={input} placeholder="Trace kind, e.g. http_proxy" value={filter.kind ?? ""} onChange={event => onChange({ kind: event.target.value })} />
      </label>
    </div>
  );
}

function RunSummary({ run }: { run: RunTraces }) {
  const queryClient = useQueryClient();
  const stop = useMutation({
    mutationFn: () => stopRun(run),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["trace-runs"] }),
  });
  // A host run traces the host, not a command or a pid, and is live while it is running (it has no live sessions).
  const hostRun = !run.pid && !run.command?.length;
  const running = !!run.live || (hostRun && run.status === "running");
  const command = <code>{run.pid ? `pid ${run.pid}` : hostRun ? "host run" : run.command?.join(" ")}</code>;
  const failed = run.checkpointErrors?.length ? (
    <ul role="alert" className="text-sm text-red-700 dark:text-red-400">
      {run.checkpointErrors.map(error => <li key={error}>{error}</li>)}
    </ul>
  ) : null;
  if (!running) {
    return (
      <div>
        <p className="text-sm text-muted-foreground">{command} {run.pid ? "attached run ended" : hostRun ? "ended" : `exited with code ${run.exitCode}`}</p>
        {failed}
      </div>
    );
  }
  return (
    <div className="flex flex-wrap items-center gap-3 text-sm">
      <span role="status" className="rounded bg-blue-100 px-2 py-0.5 text-blue-800 dark:bg-blue-900 dark:text-blue-100">running</span>
      {command}
      <Button type="button" size="sm" variant="outline" disabled={stop.isPending} onClick={() => stop.mutate()}>Stop</Button>
      {stop.error && <span role="alert" className="text-red-700 dark:text-red-400">{stop.error.message}</span>}
    </div>
  );
}

/** A host run's process groups, which it records in place of a process trace. */
function HostRunDetail({ run }: { run: RunTraces }) {
  const [view, setView] = useState<GroupsView>(DEFAULT_VIEW);
  return (
    <div className="space-y-6">
      <RunSummary run={run} />
      <Panel title="Process groups"><RecordedGroups run={run} view={view} onViewChange={next => setView(previous => ({ ...previous, ...next }))} /></Panel>
    </div>
  );
}

function RunDetail({ run }: { run: RunTraces }) {
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const [hideExited, setHideExited] = useState(false);
  const [expandAll, setExpandAll] = useState<boolean | null>(true);
  const trace = useProcessTrace({
    mount: run.mount,
    ...(run.process ? { refs: run.process } : {}),
    ...(run.live ? { sessions: run.live.sessions } : {}),
    ...(selectedKey ? { selectedKey } : {}),
    done: !run.live,
  });
  if (!run.process) return processGroupsCapture(run) ? <HostRunDetail run={run} /> : <p role="alert">{run.unavailable}</p>;
  return (
    <div className="space-y-6">
      <RunSummary run={run} />
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

function RunList({ state, setState }: { state: PageState; setState: (next: Partial<PageState>) => void }) {
  const filter: RunFilter = { q: state.q ?? "", status: state.status ?? "", kind: state.kind ?? "" };
  const query = useQuery({
    queryKey: ["trace-runs", filter],
    queryFn: () => fetchTraceRuns(filter),
    retry: false,
    refetchInterval: current => current.state.data?.runs.some(run => run.live || run.status === "running") ? LIVE_POLL_MS : false,
  });
  const filters = <RunFilters filter={filter} onChange={setState} />;
  if (query.isPending) return <div className="space-y-4">{filters}<p role="status">Loading trace runs…</p></div>;
  if (query.isError) return (
    <div className="space-y-4">
      {filters}
      <p role="alert">{query.error.message}</p>
      <Button onClick={() => void query.refetch()}>Retry</Button>
    </div>
  );
  const runs = query.data.runs;
  const filtered = !!(filter.q || filter.status || filter.kind);
  if (runs.length === 0) return (
    <div className="space-y-4">
      {filters}
      <p className="text-sm text-muted-foreground">
        {filtered ? "No run matches these filters." : <>No runs recorded yet. Start one above, or record one with <code>telltale trace run -- &lt;command&gt;</code>.</>}
      </p>
    </div>
  );
  const run = runs.find(candidate => candidate.runId === state.run) ?? runs[0]!;
  return (
    <div className="space-y-6">
      {filters}
      <label className="flex items-center gap-2 text-sm">
        Run
        <select className="rounded border border-border bg-background px-2 py-1" value={run.runId} onChange={event => setState({ run: event.target.value })}>
          {runs.map(candidate => <option key={candidate.runId} value={candidate.runId}>{candidate.runId} ({candidate.status})</option>)}
        </select>
      </label>
      <RunDetail key={`${run.runId}:${run.live || run.status === "running" ? "live" : "stored"}`} run={run} />
    </div>
  );
}

/** Starts runs, and shows the runs the server holds, newest first: the selected one's captures and processes, live while it runs. */
export default function TracesPage() {
  const [state, setState] = usePageState();
  return (
    <div className="space-y-6">
      <Panel title="New run">
        <StartRunForm onStarted={run => setState({ run: run.runId, q: "", status: "", kind: "" })} />
      </Panel>
      <RunList state={state} setState={setState} />
    </div>
  );
}
