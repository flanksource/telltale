import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Button, Tabs } from "@flanksource/clicky-ui/components";
import { fetchTraceRuns } from "../traces/api";
import { fetchTopFrame, PROCESS_GROUPS_KIND, type Frame } from "./api";
import { GroupsFilterBar, GroupsTable } from "./GroupsTable";
import { appendFrame, cpuTrend } from "./groups";
import { HostStrip } from "./HostStrip";
import { RecordedGroups } from "./RecordedGroups";
import { useTopState, type TopState, type TopTab } from "./useTopState";

const LIVE_POLL_MS = 1000;
const RUNS_POLL_MS = 2000;

// HISTORY_FRAMES is how many readings the live tab keeps for its CPU trends.
const HISTORY_FRAMES = 60;

const TABS: Array<{ id: TopTab; label: string }> = [
  { id: "live", label: "Live" },
  { id: "recorded", label: "Recorded" },
];

interface TabProps {
  state: TopState;
  setState: (next: Partial<TopState>) => void;
}

function useLiveFrame({ showOther, net }: TopState, enabled: boolean) {
  return useQuery({
    queryKey: ["top-live", { all: showOther, net }],
    queryFn: ({ signal }) => fetchTopFrame({ all: showOther, net }, signal),
    enabled,
    retry: false,
    // A refused reading is not polled again until Retry: the server would refuse it every second.
    refetchInterval: current => current.state.status === "error" ? false : LIVE_POLL_MS,
  });
}

function LiveGroups({ state, setState, query }: TabProps & { query: ReturnType<typeof useLiveFrame> }) {
  const [history, setHistory] = useState<Frame[]>([]);
  useEffect(() => {
    if (query.data) setHistory(previous => appendFrame(previous, query.data, HISTORY_FRAMES));
  }, [query.data]);
  const trend = useMemo(() => cpuTrend(history), [history]);
  return (
    <div className="space-y-4">
      <GroupsFilterBar view={state} onChange={setState}>
        <label className="flex items-center gap-2">
          <input type="checkbox" checked={state.net} onChange={event => setState({ net: event.target.checked })} />
          Network bytes (nettop, costly)
        </label>
      </GroupsFilterBar>
      {query.isError && (
        <div className="space-y-2">
          <p role="alert">{query.error.message}</p>
          <Button onClick={() => void query.refetch()}>Retry</Button>
        </div>
      )}
      {query.isPending && <p role="status">Reading process groups…</p>}
      {query.data && (
        <>
          <p className="text-sm text-muted-foreground">tick {query.data.tick} · {new Date(query.data.sampledAt).toLocaleTimeString()}</p>
          <GroupsTable groups={query.data.groups} view={state} onViewChange={setState} trend={trend} linkGroups />
        </>
      )}
    </div>
  );
}

function RecordedRuns({ state, setState }: TabProps) {
  const query = useQuery({
    queryKey: ["top-runs"],
    queryFn: () => fetchTraceRuns({ kind: PROCESS_GROUPS_KIND }),
    retry: false,
    refetchInterval: current => current.state.data?.runs.some(run => run.status === "running") ? RUNS_POLL_MS : false,
  });
  if (query.isPending) return <p role="status">Loading recordings…</p>;
  if (query.isError) return (
    <div className="space-y-2">
      <p role="alert">{query.error.message}</p>
      <Button onClick={() => void query.refetch()}>Retry</Button>
    </div>
  );
  const runs = query.data.runs;
  if (runs.length === 0) return (
    <p className="text-sm text-muted-foreground">
      No process groups recorded yet. Record them with <code>telltale top --record</code>, or arm the <code>{PROCESS_GROUPS_KIND}</code> trace on a host run.
    </p>
  );
  const run = runs.find(candidate => candidate.runId === state.run) ?? runs[0]!;
  return (
    <div className="space-y-4">
      <label className="flex items-center gap-2 text-sm">
        Recording
        <select className="rounded border border-border bg-background px-2 py-1" value={run.runId} onChange={event => setState({ run: event.target.value })}>
          {runs.map(candidate => <option key={candidate.runId} value={candidate.runId}>{candidate.runId} ({candidate.status})</option>)}
        </select>
      </label>
      <RecordedGroups key={run.runId} run={run} view={state} onViewChange={setState} />
    </div>
  );
}

/** The host's processes grouped by the tool that owns them: read live, or replayed from a recorded host run. */
export default function TopPage() {
  const [state, setState] = useTopState();
  const live = state.tab === "live";
  const query = useLiveFrame(state, live);
  return (
    <div className="space-y-4">
      {live && query.data?.host && <HostStrip host={query.data.host} overhead={query.data.overhead} />}
      <Tabs tabs={TABS} value={state.tab} onChange={tab => setState({ tab: tab as TopTab })} />
      {live ? <LiveGroups state={state} setState={setState} query={query} /> : <RecordedRuns state={state} setState={setState} />}
    </div>
  );
}
