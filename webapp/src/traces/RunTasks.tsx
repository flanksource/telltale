import { useEffect, useMemo, useState } from "react";
import { TaskProgress } from "@flanksource/clicky-ui/data";
import { fetchTraceRecords, followTraceRecords, TraceStreamNotFound, type CaptureResult, type TraceRecord } from "./api";
import { taskSnapshots } from "./taskSnapshots";

/** A clicky agent's task capture in clicky-ui's task viewer, followed while the run records. */
export function RunTasks({ mount, capture, live }: { mount: string; capture: CaptureResult; live: boolean }) {
  const [records, setRecords] = useState<TraceRecord[]>([]);
  const [error, setError] = useState<string>();
  const [loading, setLoading] = useState(true);
  useEffect(() => {
    const controller = new AbortController();
    let stop: (() => void) | undefined;
    setRecords([]);
    setError(undefined);
    setLoading(true);
    fetchTraceRecords(mount, capture.ref, controller.signal)
      .catch((reason: unknown) => {
        if (reason instanceof TraceStreamNotFound) return [] as TraceRecord[];
        throw reason;
      })
      .then(initial => {
        if (controller.signal.aborted) return;
        setRecords(initial);
        setLoading(false);
        if (!live) return;
        const afterSeq = initial.reduce((max, row) => Math.max(max, Number(row.seq) || 0), capture.ref.afterSeq ?? 0);
        stop = followTraceRecords({
          mount, ref: { ...capture.ref, afterSeq },
          onRow: row => setRecords(previous => [...previous, row]),
          onError: reason => setError(reason.message),
          onOpen: () => setError(undefined),
          onEnd: () => {},
        });
      })
      .catch((reason: unknown) => {
        if (controller.signal.aborted) return;
        setLoading(false);
        setError(reason instanceof Error ? reason.message : String(reason));
      });
    return () => { controller.abort(); stop?.(); };
  }, [mount, capture.ref, live]);
  const projected = useMemo(() => {
    try { return { snapshots: taskSnapshots(records) }; }
    catch (reason) { return { error: reason instanceof Error ? reason.message : String(reason) }; }
  }, [records]);
  if (loading) return <p role="status" className="p-3 text-sm text-muted-foreground">Loading tasks…</p>;
  const message = error ?? projected.error;
  return <div className="flex flex-col gap-2 p-1">
    {message && <p role="alert" className="text-sm text-destructive">{message}</p>}
    {projected.snapshots && <TaskProgress snapshots={projected.snapshots} compact initialShowAllTasks {...(live ? { connectionStatus: error ? "disconnected" : "connected" } : {})} />}
  </div>;
}
