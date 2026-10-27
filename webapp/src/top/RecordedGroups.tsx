import { useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Button } from "@flanksource/clicky-ui/components";
import { fetchLatestSeq, fetchTraceRecords, type CaptureResult, type RunTraces, type TraceRecord } from "../traces/api";
import { framesFromRecords, PROCESS_GROUPS_KIND, type Frame } from "./api";
import { CpuChart } from "./charts";
import { GroupsFilterBar, GroupsTable } from "./GroupsTable";
import { viewGroups, type GroupsView } from "./groups";
import { useRefreshInterval } from "../monitoring/RefreshControl";

/** The process_groups capture a host run armed, if it did. */
export function processGroupsCapture(run: RunTraces): CaptureResult | undefined {
  return run.captures?.find(capture => capture.kind === PROCESS_GROUPS_KIND);
}

interface Loaded {
  seq: number;
  rows: TraceRecord[];
  frames: Frame[];
}

/**
 * The frames a run's process_groups capture holds, read once for a finished
 * run and followed while it runs: each poll fetches only the rows past the
 * last seq it read.
 */
function useRecordedFrames(run: RunTraces, capture: CaptureResult) {
  const interval = useRefreshInterval();
  const { ref } = capture;
  const running = run.status === "running";
  const loaded = useRef<Loaded>({ seq: ref.afterSeq ?? 0, rows: [], frames: [] });
  return useQuery({
    queryKey: ["top-recorded", run.mount, ref.stream],
    queryFn: async ({ signal }) => {
      const latest = running || !ref.toSeq ? await fetchLatestSeq(run.mount, ref) : ref.toSeq;
      const upTo = ref.toSeq ? Math.min(latest, ref.toSeq) : latest;
      if (upTo > loaded.current.seq) {
        const more = await fetchTraceRecords(run.mount, { ...ref, afterSeq: loaded.current.seq, toSeq: upTo }, signal);
        const rows = [...loaded.current.rows, ...more];
        loaded.current = { seq: upTo, rows, frames: framesFromRecords(rows) };
      }
      return loaded.current.frames;
    },
    refetchInterval: running ? interval : false,
    retry: false,
  });
}

const time = (frame: Frame) => new Date(frame.sampledAt).toLocaleTimeString();

export interface RecordedGroupsProps {
  run: RunTraces;
  view: GroupsView;
  onViewChange: (next: Partial<GroupsView>) => void;
}

/** A host run's recorded process groups: the table at a chosen tick (the latest by default) and the CPU of the busiest groups across ticks. */
export function RecordedGroups({ run, view, onViewChange }: RecordedGroupsProps) {
  const capture = processGroupsCapture(run);
  if (!capture) return <p role="alert">Run {run.runId} armed no {PROCESS_GROUPS_KIND} capture.</p>;
  return <RecordedFrames run={run} capture={capture} view={view} onViewChange={onViewChange} />;
}

function RecordedFrames({ run, capture, view, onViewChange }: RecordedGroupsProps & { capture: CaptureResult }) {
  const query = useRecordedFrames(run, capture);
  const [picked, setPicked] = useState<number | null>(null);
  // While the run records, rows of the newest tick are still arriving: its frame is whole once a later tick has begun.
  const frames = useMemo(() => {
    const read = query.data ?? [];
    return run.status === "running" && read.length > 1 ? read.slice(0, -1) : read;
  }, [query.data, run.status]);
  const index = picked === null ? frames.length - 1 : Math.min(picked, frames.length - 1);
  const frame = frames[index];
  return (
    <div className="space-y-4">
      <GroupsFilterBar view={view} onChange={onViewChange} />
      {query.isPending && <p role="status">Loading recorded process groups…</p>}
      {query.isError && (
        <div className="space-y-2">
          <p role="alert">{query.error.message}</p>
          <Button onClick={() => void query.refetch()}>Retry</Button>
        </div>
      )}
      {query.isSuccess && !frame && <p className="text-sm text-muted-foreground">This recording holds no frames yet.</p>}
      {frame && (
        <>
          <div className="flex flex-wrap items-center gap-3 text-sm">
            <label className="flex items-center gap-2">
              Tick
              <input type="range" min={0} max={frames.length - 1} value={index} onChange={event => setPicked(Number(event.target.value))} />
            </label>
            <span className="text-muted-foreground">tick {frame.tick} of {frames.at(-1)!.tick} · {time(frame)}</span>
            <Button type="button" size="sm" variant="outline" disabled={picked === null} onClick={() => setPicked(null)}>Latest</Button>
            {run.status === "running" && <span role="status" className="rounded bg-blue-100 px-2 py-0.5 text-blue-800 dark:bg-blue-900 dark:text-blue-100">recording</span>}
          </div>
          <GroupsTable groups={frame.groups} view={view} onViewChange={onViewChange} />
          <CpuChart frames={frames.map(frame => ({ ...frame, groups: viewGroups(frame.groups, view) }))} />
        </>
      )}
    </div>
  );
}
