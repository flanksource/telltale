import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { GoProfileRecord, StackRecord } from "@flanksource/clicky-ui/data";
import {
  checkpointSession,
  fetchLatestSeq,
  fetchTraceRecords,
  TraceStreamNotFound,
  traceExportUrl,
  type ProcessSample,
  type ProcessTraceNode,
  type ProcessTraceRefs,
  type TraceRef,
} from "./api";

const POLL_INTERVAL_MS = 1000;

// SAMPLE_WINDOW bounds how many of the latest samples (of every process) a
// read of the samples stream fetches, so a long run's history is not
// re-downloaded on every poll.
export const SAMPLE_WINDOW = 2000;

export const PROCESS_TRACE_ENDED = "The run has ended, so its process trace can no longer be checkpointed";

/** The running capture's session ids, which a checkpoint names. */
export interface ProcessTraceSessions {
  tree: string;
  samples: string;
  stacks: string;
}

export interface UseProcessTraceOptions {
  // mount is where the run's store is served; nothing is fetched without it.
  mount?: string;
  refs?: ProcessTraceRefs;
  // sessions are the live capture's sessions; absent for a stored run, which
  // can be read but not checkpointed.
  sessions?: ProcessTraceSessions;
  // done stops polling: a finished run is fetched once.
  done: boolean;
  // selectedKey is the process whose stack dumps and Go profiles are read.
  selectedKey?: string;
}

export interface ProcessTraceState {
  nodes: ProcessTraceNode[];
  samples: ProcessSample[];
  stacks: StackRecord[];
  goprof: GoProfileRecord[];
  // goprofExportUrl downloads the goprof window as a pprof profile; absent when the run has no goprof.
  goprofExportUrl?: string;
  // loading is true until the first read of the tree has answered.
  loading: boolean;
  error?: string;
  // canCollect is true while the run's capture is live: only then can a stack
  // be collected or a facet reading forced.
  canCollect: boolean;
  collecting: boolean;
  collectError?: string;
  collectStack: (key: string, transport: string) => Promise<void>;
  refreshing: boolean;
  refreshError?: string;
  refreshFacets: (key: string) => Promise<void>;
}

// readStream is ref's records, none while its stream has committed none yet.
async function readStream<T>(mount: string, ref: TraceRef): Promise<T[]> {
  try {
    return await fetchTraceRecords(mount, ref) as unknown as T[];
  } catch (e) {
    if (e instanceof TraceStreamNotFound) return [];
    throw e;
  }
}

// readRecentSamples is the last SAMPLE_WINDOW samples of the stream.
async function readRecentSamples(mount: string, ref: TraceRef): Promise<ProcessSample[]> {
  const latest = await fetchLatestSeq(mount, ref);
  if (latest === 0) return [];
  return readStream<ProcessSample>(mount, { ...ref, afterSeq: Math.max(0, latest - SAMPLE_WINDOW) });
}

function message(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

function usePolled<T>(enabled: boolean, poll: boolean, load: () => Promise<T>, key: string) {
  const [value, setValue] = useState<T | null>(null);
  const [error, setError] = useState<string | undefined>(undefined);
  const loadRef = useRef(load);
  loadRef.current = load;

  const reload = useCallback(async () => {
    try {
      setValue(await loadRef.current());
      setError(undefined);
    } catch (e: unknown) {
      setError(message(e));
    }
  }, []);

  useEffect(() => {
    if (!enabled) return;
    let cancelled = false;
    const run = () => {
      loadRef.current().then(next => {
        if (cancelled) return;
        setValue(next);
        setError(undefined);
      }).catch((e: unknown) => {
        if (!cancelled) setError(message(e));
      });
    };
    run();
    const timer = poll ? window.setInterval(run, POLL_INTERVAL_MS) : undefined;
    return () => {
      cancelled = true;
      if (timer !== undefined) window.clearInterval(timer);
    };
  }, [enabled, poll, key]);

  return { value, error, reload };
}

// useCheckpoint posts checkpoint params to a running session, then re-reads the
// window the checkpoint wrote to. Without a session (the run has ended) it
// fails loudly instead of posting.
function useCheckpoint(mount: string | undefined, session: string | undefined, reload: () => Promise<void>) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | undefined>(undefined);
  const checkpoint = useCallback(async (params: Record<string, string>) => {
    setBusy(true);
    setError(undefined);
    try {
      if (!mount || !session) {
        setError(PROCESS_TRACE_ENDED);
        return;
      }
      await checkpointSession({ mount, session, params });
      await reload();
    } catch (e: unknown) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  }, [mount, session, reload]);
  return { busy, error, checkpoint };
}

export function useProcessTrace({ mount, refs, sessions: reported, done, selectedKey }: UseProcessTraceOptions): ProcessTraceState {
  const live = !done;
  const enabled = !!mount && !!refs;
  const sessions = live ? reported : undefined;

  const tree = usePolled<ProcessTraceNode[]>(
    enabled, live, () => readStream<ProcessTraceNode>(mount!, refs!.tree), `${mount}|${refs?.tree.stream}`,
  );
  const sampled = usePolled<ProcessSample[]>(
    enabled, live, () => readRecentSamples(mount!, refs!.samples), `${mount}|${refs?.samples.stream}`,
  );
  const stacked = usePolled<StackRecord[]>(
    enabled && !!selectedKey, live, () => readStream<StackRecord>(mount!, refs!.stacks), `${mount}|${refs?.stacks.stream}`,
  );
  const profiled = usePolled<GoProfileRecord[]>(
    enabled && !!selectedKey && !!refs?.goprof, live, () => readStream<GoProfileRecord>(mount!, refs!.goprof!), `${mount}|${refs?.goprof?.stream}`,
  );

  const stackCheckpoint = useCheckpoint(mount, sessions?.stacks, stacked.reload);
  const sampleCheckpoint = useCheckpoint(mount, sessions?.samples, sampled.reload);
  const collectStack = useCallback(
    (key: string, transport: string) => stackCheckpoint.checkpoint({ key, transport, reason: "manual" }),
    [stackCheckpoint.checkpoint],
  );
  const refreshFacets = useCallback((key: string) => sampleCheckpoint.checkpoint({ key }), [sampleCheckpoint.checkpoint]);

  const nodes = useMemo(() => tree.value ?? [], [tree.value]);
  const samples = useMemo(() => sampled.value ?? [], [sampled.value]);
  const stacks = useMemo(() => stacked.value ?? [], [stacked.value]);
  const goprof = useMemo(() => profiled.value ?? [], [profiled.value]);
  const error = tree.error ?? sampled.error ?? stacked.error ?? profiled.error;

  return {
    nodes,
    samples,
    stacks,
    goprof,
    ...(mount && refs?.goprof ? { goprofExportUrl: traceExportUrl(mount, refs.goprof, "pprof") } : {}),
    loading: enabled && tree.value === null && !tree.error,
    ...(error ? { error } : {}),
    canCollect: enabled && !!sessions,
    collecting: stackCheckpoint.busy,
    ...(stackCheckpoint.error ? { collectError: stackCheckpoint.error } : {}),
    collectStack,
    refreshing: sampleCheckpoint.busy,
    ...(sampleCheckpoint.error ? { refreshError: sampleCheckpoint.error } : {}),
    refreshFacets,
  };
}
