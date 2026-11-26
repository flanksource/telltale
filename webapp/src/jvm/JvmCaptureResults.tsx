import { useMemo, useState } from "react";
import { Button, Tabs } from "@flanksource/clicky-ui/components";
import { TraceFeed } from "@flanksource/clicky-ui/data";
import { useEventSourceFactory } from "@flanksource/clicky-ui/hooks";
import { useRouter } from "@flanksource/clicky-ui/rpc";
import { traceFeedSource } from "../traces/feed";
import type { RunTraces } from "../traces/api";
import { TraceRecords } from "../traces/TraceRecords";
import type { Recording } from "./captures";

export function JvmCaptureResults({ recording, stop, stopping, error }: { recording?: Recording; stop: (run: RunTraces) => void; stopping: boolean; error: Error | null }) {
  const { renderLink } = useRouter();
  const open = useEventSourceFactory();
  const [view, setView] = useState("live");
  const mount = recording?.run.mount;
  const kind = recording?.capture.kind;
  const source = useMemo(() => {
    if (!mount || !kind) return undefined;
    const feed = traceFeedSource(mount, open);
    return { loadPage: ((request, signal) => feed.loadPage({ ...request, kind }, signal)) as typeof feed.loadPage, follow: ((request) => feed.follow!({ ...request, kind })) as NonNullable<typeof feed.follow> };
  }, [mount, kind, open]);
  if (!recording || !source) return <div className="grid h-full min-h-48 place-content-center gap-2 p-4 text-center text-sm text-muted-foreground"><p>Select a recording or start a capture.</p><p>Events stay available after the capture stops.</p></div>;
  const { run, capture } = recording;
  const live = run.status === "running";
  return <div className="flex h-full min-h-0 min-w-0 flex-col gap-3">
    <div className="flex flex-wrap items-center gap-3 text-sm"><strong className="break-all font-mono">{run.runId}</strong><span role="status">{run.status}</span>
      {live && <Button variant="outline" disabled={stopping} onClick={() => stop(run)}>{stopping ? "Stopping…" : capture.kind === "jvm_trace" ? "Stop and restore" : "Stop recording"}</Button>}
      {renderLink({ to: `/?run=${encodeURIComponent(run.runId)}`, children: "Open full run", className: "underline" })}
    </div>
    {(error || run.error) && <p role="alert" className="text-sm">{error?.message ?? run.error}</p>}
    <Tabs tabs={[{ id: "live", label: "Event stream" }, { id: "records", label: capture.kind === "jvm_trace" ? "Records / call tree" : "Query records" }]} value={view} onChange={setView} />
    <div className="min-h-0 min-w-0 flex-1 overflow-hidden">
      {view === "live" ? <TraceFeed key={run.runId} kinds={[capture.kind]} loadPage={source.loadPage} presentation="ansi" {...(live && source.follow ? { follow: source.follow } : {})} /> : <TraceRecords key={capture.ref.stream} run={run.runId} kind={capture.kind} stream={capture.ref.stream} />}
    </div>
  </div>;
}
