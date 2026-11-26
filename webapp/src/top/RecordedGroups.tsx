import { useEffect, useRef, useState } from "react";
import { Button } from "@flanksource/clicky-ui/components";
import { subscribeJSON, useEventSourceFactory } from "@flanksource/clicky-ui/hooks";
import { failure, type CaptureResult, type RunTraces } from "../traces/api";
import { PROCESS_GROUPS_KIND, type Frame } from "./api";
import { CpuChart } from "./charts";
import { GroupsFilterBar, GroupsTable } from "./GroupsTable";
import { viewGroups, type GroupsView } from "./groups";

/** The process_groups capture a host run armed, if it did. */
export function processGroupsCapture(run: RunTraces): CaptureResult | undefined {
  return run.captures?.find(capture => capture.kind === PROCESS_GROUPS_KIND);
}

interface Loaded {
  seq: number;
  frames: Frame[];
}

interface ProcessFrames { highSeq: number; frames: Frame[] }

/** Replay and follow the capture, retaining received rows across retries. */
function useRecordedFrames(run: RunTraces, capture: CaptureResult) {
  const { ref } = capture;
  const loaded = useRef<Loaded>({ seq: 0, frames: [] });
  const [state, setState] = useState<{ data: Frame[]; pending: boolean; ended: boolean; error?: Error }>({ data: [], pending: true, ended: false });
  const [attempt, setAttempt] = useState(0);
  const open = useEventSourceFactory();
  const { stream } = ref;
  useEffect(() => {
    setState(previous => ({ ...previous, pending: true, error: undefined }));
    const controller = new AbortController();
    let close: (() => void) | undefined;
    const append = (page: ProcessFrames) => {
      if (!Number.isSafeInteger(page.highSeq) || !Array.isArray(page.frames) || page.frames.some(frame => !Number.isSafeInteger(frame.tick) || frame.tick <= 0 || !Array.isArray(frame.groups))) throw new Error("Invalid process group frames or sequence");
      if (controller.signal.aborted || page.highSeq < loaded.current.seq) return;
      const frames = [...loaded.current.frames, ...page.frames.filter(frame => frame.tick > (loaded.current.frames.at(-1)?.tick ?? 0))];
      loaded.current = { seq: page.highSeq, frames };
      setState({ data: frames, pending: false, ended: run.status !== "running" });
    };
    const load = async () => {
      try {
        const params = new URLSearchParams({ stream, afterSeq: String(loaded.current.seq) });
        const response = await fetch(`${run.mount}/process/groups?${params}`, { signal: controller.signal });
        if (!response.ok) throw await failure(response, "Read recorded process groups");
        append(await response.json() as ProcessFrames);
        if (!controller.signal.aborted && run.status === "running") {
          params.set("afterSeq", String(loaded.current.seq));
          params.set("follow", "1");
          close = subscribeJSON<ProcessFrames>(open, `${run.mount}/process/groups?${params}`, {
            onData: append,
            onError: error => setState(previous => ({ ...previous, pending: false, error })),
            onOpen: () => setState(previous => ({ data: previous.data, pending: false, ended: false })),
            onEnd: () => setState(previous => ({ ...previous, ended: true })),
          });
        }
      } catch (error) {
        if (!controller.signal.aborted) setState(previous => ({ ...previous, pending: false, error: error instanceof Error ? error : new Error(String(error)) }));
      }
    };
    void load();
    return () => { controller.abort(); close?.(); };
  }, [open, run.mount, run.status, stream, attempt]);
  return { ...state, refetch: () => {
    if (state.error?.message.includes("cursor expired")) loaded.current = { seq: 0, frames: [] };
    setAttempt(previous => previous + 1);
  } };
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
  return <RecordedFrames key={`${run.mount}/${capture.ref.kind}/${capture.ref.stream}/${capture.ref.afterSeq}/${capture.ref.toSeq}`} run={run} capture={capture} view={view} onViewChange={onViewChange} />;
}

function RecordedFrames({ run, capture, view, onViewChange }: RecordedGroupsProps & { capture: CaptureResult }) {
  const query = useRecordedFrames(run, capture);
  const [picked, setPicked] = useState<number | null>(null);
  const frames = query.data;
  const index = picked === null ? frames.length - 1 : Math.min(picked, frames.length - 1);
  const frame = frames[index];
  return (
    <div className="space-y-4">
      <GroupsFilterBar view={view} onChange={onViewChange} />
      {query.pending && <p role="status">Loading recorded process groups…</p>}
      {query.error && (
        <div className="space-y-2">
          <p role="alert">{query.error.message}</p>
          <Button onClick={() => void query.refetch()}>Retry</Button>
        </div>
      )}
      {!query.pending && !query.error && !frame && <p className="text-sm text-muted-foreground">This recording holds no frames yet.</p>}
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
