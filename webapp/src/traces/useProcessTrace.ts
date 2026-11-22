import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { GoProfileRecord, StackRecord } from "@flanksource/clicky-ui/data";
import { useEventSourceFactory, useLiveSnapshot } from "@flanksource/clicky-ui/hooks";
import {
  checkpointSession,
  fetchLatestSeq,
  fetchTraceRecords,
  followTraceRecords,
  TraceStreamNotFound,
  traceExportUrl,
  type ProcessSample,
  type ProcessTraceNode,
  type ProcessTraceRefs,
  type TraceRef,
} from "./api";
import { ProcessFacets, projectProcesses, type FacetChange, type ProcessIdentity, type ProcessMetric } from "./processData";

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
  // A finished run is fetched once instead of being followed.
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
async function readRecentSamples(mount: string, ref: TraceRef): Promise<ProcessMetric[]> {
  const latest = await fetchLatestSeq(mount, ref);
  if (latest === 0) return [];
  return readStream<ProcessMetric>(mount, { ...ref, afterSeq: Math.max(0, latest - SAMPLE_WINDOW), toSeq: latest });
}

function processReader(mount: string, refs: ProcessTraceRefs) {
  const facets = new ProcessFacets();
  let after = 0;
  let pending: FacetChange[] = [];
  let loading: Promise<ReturnType<typeof projectProcesses>> | undefined;
  return () => {
    if (loading) return loading;
    loading = (async () => {
      const metrics = await readRecentSamples(mount, refs.samples);
      const [identities, high] = await Promise.all([
        readStream<ProcessIdentity>(mount, refs.tree), fetchLatestSeq(mount, refs.facets),
      ]);
      if (high > after) {
        const changes = await readStream<FacetChange>(mount, { ...refs.facets, afterSeq: after, toSeq: high });
        pending.push(...changes);
        after = high;
      }
      const tick = Math.max(0, ...metrics.map(metric => metric.tick));
      facets.apply(pending.filter(event => event.tick <= tick));
      pending = pending.filter(event => event.tick > tick);
      return projectProcesses(identities, metrics, facets);
    })().finally(() => { loading = undefined; });
    return loading;
  };
}

function message(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

function useRead<T>({ enabled, key, load, follow }: {
  enabled: boolean;
  key: string;
  load: () => Promise<T>;
  follow?: (initial: T, update: (change: (previous: T) => T) => void, report: (error: string | undefined) => void) => () => void;
}) {
  const [value, setValue] = useState<T | null>(null);
  const [error, setError] = useState<string | undefined>(undefined);
  const loadRef = useRef(load);
  loadRef.current = load;
  const followRef = useRef(follow);
  followRef.current = follow;

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
    let close: (() => void) | undefined;
    loadRef.current().then(next => {
      if (cancelled) return;
      setValue(next);
      setError(undefined);
      close = followRef.current?.(next, change => setValue(previous => previous === null ? previous : change(previous)), setError);
    }).catch((e: unknown) => {
      if (!cancelled) setError(message(e));
    });
    return () => {
      cancelled = true;
      close?.();
    };
  }, [enabled, key]);

  return { value, error, reload };
}

function useProcessRows<T>({ mount, ref, enabled, live }: { mount: string; ref: TraceRef | undefined; enabled: boolean; live: boolean }) {
  const open = useEventSourceFactory();
  return useRead<T[]>({ enabled: enabled && !!ref, key: `${mount}|${ref?.stream}|${live}`, load: () => readStream<T>(mount, ref!),
    ...(live && ref ? { follow: (initial: T[], update: (change: (previous: T[]) => T[]) => void, report: (error: string | undefined) => void) => {
      const afterSeq = initial.reduce((high, row) => Math.max(high, Number((row as { seq?: number }).seq) || 0), ref.afterSeq ?? 0);
      return followTraceRecords({ mount, open, ref: { ...ref, afterSeq }, onRow: row => update(previous => [...previous, row as unknown as T]),
        onError: error => report(error.message), onOpen: () => report(undefined), onEnd: () => {} });
    } } : {}),
  });
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

  const reader = useMemo(() => mount && refs ? processReader(mount, refs) : undefined, [mount, refs?.tree.stream, refs?.samples.stream, refs?.facets.stream]);
  const process = useRead({ enabled: enabled && !live, load: () => reader!(), key: `${mount}|${refs?.tree.stream}` });
  const details = useLiveSnapshot<{ identities: ProcessIdentity[]; metrics: ProcessMetric[]; facets: FacetChange[] }>(
    mount && refs && live ? `${mount}/processes?${new URLSearchParams({ follow: "1", view: "detail", stream: refs.tree.stream })}` : null,
  );
  const liveProcess = useMemo(() => {
    if (!details.data) return undefined;
    const facets = new ProcessFacets();
    facets.apply(details.data.facets);
    return projectProcesses(details.data.identities, details.data.metrics, facets);
  }, [details.data]);
  const projected = live ? liveProcess : process.value ?? liveProcess;
  const stacked = useProcessRows<StackRecord>({ mount: mount ?? "", ref: refs?.stacks, enabled: enabled && !!selectedKey, live });
  const profiled = useProcessRows<GoProfileRecord>({ mount: mount ?? "", ref: refs?.goprof, enabled: enabled && !!selectedKey, live });

  const stackCheckpoint = useCheckpoint(mount, sessions?.stacks, stacked.reload);
  const sampleCheckpoint = useCheckpoint(mount, sessions?.samples, async () => { if (!live) await process.reload(); });
  const collectStack = useCallback(
    (key: string, transport: string) => stackCheckpoint.checkpoint({ key, transport, reason: "manual" }),
    [stackCheckpoint.checkpoint],
  );
  const refreshFacets = useCallback((key: string) => sampleCheckpoint.checkpoint({ key }), [sampleCheckpoint.checkpoint]);

  const nodes = useMemo(() => projected?.nodes ?? [], [projected]);
  const samples = useMemo(() => projected?.samples ?? [], [projected]);
  const stacks = useMemo(() => stacked.value ?? [], [stacked.value]);
  const goprof = useMemo(() => profiled.value ?? [], [profiled.value]);
  const error = (live ? details.error?.message : process.error) ?? stacked.error ?? profiled.error;

  return {
    nodes,
    samples,
    stacks,
    goprof,
    ...(mount && refs?.goprof ? { goprofExportUrl: traceExportUrl(mount, refs.goprof, "pprof") } : {}),
    loading: enabled && !projected && !error,
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
