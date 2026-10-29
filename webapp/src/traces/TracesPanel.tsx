import { useCallback, useEffect, useMemo, useState } from "react";
import { TraceWindowList, type TraceWindow, type TraceWindowSource } from "@flanksource/clicky-ui/data";
import { fetchTraceKinds, fetchTraceRecords, traceExportUrl, type RunTraces, type TraceExportFormat } from "./api";

function useExportFormats(mount: string) {
  const [byKind, setByKind] = useState<Record<string, TraceExportFormat[]>>({});
  const [error, setError] = useState<string | undefined>(undefined);
  useEffect(() => {
    fetchTraceKinds(mount)
      .then(kinds => setByKind(Object.fromEntries(kinds.map(kind => [kind.name, kind.exportFormats]))))
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)));
  }, [mount]);
  return { byKind, error };
}

/** The windows of the captures a run armed, as TraceWindowList reads them. */
export function captureWindows(run: RunTraces): TraceWindow[] {
  return (run.captures ?? []).map(capture => ({
    ...capture.ref,
    run_id: run.runId,
    count: Number(capture.summary?.count ?? 0),
    summary: capture.summary ?? {},
  }));
}

/** The captures one run armed, read from its store. */
export function TracesPanel({ run }: { run: RunTraces }) {
  const formats = useExportFormats(run.mount);
  const windows = useMemo(() => captureWindows(run), [run]);
  const resolve = useCallback((): TraceWindowSource => ({ mount: run.mount }), [run.mount]);
  const props = useMemo(() => ({
    formats: (kind: string) => formats.byKind[kind] ?? [],
    fetchRecords: (window: TraceWindow, signal: AbortSignal) => fetchTraceRecords(run.mount, window, signal),
    exportUrl: (window: TraceWindow, format: string) => traceExportUrl(run.mount, window, format),
    openUrl: (window: TraceWindow) => {
      const params = new URLSearchParams();
      if (window.afterSeq) params.set("afterSeq", String(window.afterSeq));
      if (window.toSeq) params.set("toSeq", String(window.toSeq));
      return `/records/${encodeURIComponent(run.runId)}/${encodeURIComponent(window.kind)}/${encodeURIComponent(window.stream)}${params.size ? `?${params}` : ""}`;
    },
  }), [formats.byKind, run.mount]);

  if (windows.length === 0) return <p className="text-sm text-muted-foreground">This run armed no captures.</p>;
  return (
    <TraceWindowList
      windows={windows}
      resolve={resolve}
      {...props}
      {...(formats.error ? { error: formats.error } : {})}
    />
  );
}
