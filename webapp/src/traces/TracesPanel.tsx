import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { ProcessTraceHeader, ProcessTraceTree, TraceFeed, type ProcessHeaderSnapshot } from "@flanksource/clicky-ui/data";
import { Button, Tabs } from "@flanksource/clicky-ui/components";
import { useEventSourceFactory } from "@flanksource/clicky-ui/hooks";
import { DEFAULT_VIEW, type GroupsView } from "../top/groups";
import { processGroupsCapture, RecordedGroups } from "../top/RecordedGroups";
import { traceFeedSource } from "./feed";
import { failure, fetchTraceKinds, type RunTraces } from "./api";
import { RunProcessDetail } from "./RunProcessDetail";
import { RunTasks } from "./RunTasks";
import { useProcessTrace } from "./useProcessTrace";
import { RunShellSlot } from "../layout/RunShellSlot";
import { RunTop } from "./RunTop";
import { useSnapshotQuery } from "../monitoring/useSnapshotQuery";

type RunView = "top" | "events" | "process" | "tasks";

function readView(): RunView {
  const view = new URLSearchParams(window.location.search).get("runView");
  return view === "process" || view === "tasks" || view === "events" ? view : "top";
}

/** The captures one run armed, read from its store. */
export function TracesPanel({ run, summary }: { run: RunTraces; summary?: ReactNode }) {
  const open = useEventSourceFactory();
  const source = useMemo(() => traceFeedSource(run.mount, open), [run.mount, open]);
  const live = run.status === "running" || !!run.standing;
  const [showExited, setShowExited] = useState(() => new URLSearchParams(window.location.search).get("showExited") === "1");
  const [view, setView] = useState(readView);
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const [groupsView, setGroupsView] = useState<GroupsView>(DEFAULT_VIEW);
  const trace = useProcessTrace({
    mount: run.mount,
    ...(run.process ? { refs: run.process } : {}),
    ...(run.live ? { sessions: run.live.sessions } : {}),
    ...(selectedKey ? { selectedKey } : {}),
    done: !live,
  });
  const hasDetails = !!run.process || !!processGroupsCapture(run);
  const hasProcessCapture = run.captures?.some(capture => capture.kind === "process") === true;
  const taskCapture = run.captures?.find(capture => capture.kind === "task");
  const catalog = useQuery({ queryKey: ["sample-kinds", run.mount], queryFn: () => fetchTraceKinds(run.mount), retry: false });
  const kinds = [...new Set([...(run.captures ?? []).map(capture => capture.kind), ...(run.process ? ["stacks"] : [])])].filter(kind => kind !== "process" && !catalog.data?.some(info => info.name === kind && info.top));
  const hasTop = [...(run.captures ?? []), ...(run.process ? [{ kind: run.process.tree.kind }, { kind: run.process.stacks.kind }] : [])].some(capture => catalog.data?.some(info => info.name === capture.kind && info.top));
  const tabs = [...(hasTop ? [{ id: "top", label: "Top" }] : []), { id: "events", label: "History" }, ...(taskCapture ? [{ id: "tasks", label: "Tasks" }] : []), ...(hasDetails ? [{ id: "process", label: "Process details" }] : [])];
  const activeView: RunView = tabs.some(tab => tab.id === view) ? view : "events";
  const processes = useSnapshotQuery({
    key: ["trace-process-header", run.runId, run.mount, live],
    read: async ({ signal }) => {
      const response = await fetch(`${run.mount}/processes`, { signal });
      if (!response.ok) throw await failure(response, "Read process measurements");
      const snapshot = await response.json() as ProcessHeaderSnapshot;
      if (!snapshot || typeof snapshot.armed !== "boolean" || !Array.isArray(snapshot.rows)) throw new Error("Expected a process snapshot with armed and rows fields");
      return snapshot;
    },
    stream: live ? `${run.mount}/processes?follow=1` : null,
    enabled: !run.process && hasProcessCapture,
  });
  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    if (showExited) params.set("showExited", "1"); else params.delete("showExited");
    if (activeView === "top" || (!hasTop && activeView === "events")) params.delete("runView"); else params.set("runView", activeView);
    window.history.replaceState(null, "", `${window.location.pathname}?${params}${window.location.hash}`);
  }, [showExited, activeView, hasTop]);
  useEffect(() => {
    const restore = () => {
      setShowExited(new URLSearchParams(window.location.search).get("showExited") === "1");
      setView(readView());
    };
    window.addEventListener("popstate", restore);
    return () => window.removeEventListener("popstate", restore);
  }, []);
  return <div className="flex h-full min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-hidden">
    <RunShellSlot name="header">
    {run.process && <section aria-label="Process usage" className="flex min-h-0 max-h-[33dvh] shrink-0 flex-col overflow-hidden rounded-md border border-border bg-background">
      <ProcessTraceTree nodes={trace.nodes} samples={trace.samples} selectedKey={selectedKey}
        className="min-h-0" showUsageBars expandAll hideExited={!showExited} onHideExitedChange={hide => setShowExited(!hide)}
        loading={trace.loading} {...(trace.error ? { error: trace.error } : {})}
        onSelect={key => { setSelectedKey(key); setView("process"); }} />
    </section>}
    {!run.process && hasProcessCapture && (processes.data?.armed || processes.error || processes.isPending) && <ProcessTraceHeader
      rows={processes.data?.rows ?? []} showExited={showExited} onShowExitedChange={setShowExited} loading={processes.isPending}
      {...(processes.error ? { error: processes.error.message } : {})}
    />}
    </RunShellSlot>
    <RunShellSlot name="actions">
    {summary && <div className="min-w-0 max-w-full sm:max-w-lg">{summary}</div>}
    {tabs.length > 1 && <Tabs className="shrink-0" tabs={tabs}
      value={activeView} onChange={id => setView(id === "process" || id === "tasks" || id === "top" ? id : "events")} />}
    </RunShellSlot>
    {catalog.isPending && activeView === "events" && <p role="status" className="text-sm text-muted-foreground">Loading capture catalog…</p>}
    {catalog.error && <div className="flex shrink-0 flex-wrap items-center gap-2">
      <p role="alert" className="text-sm text-destructive">{catalog.error.message}</p>
      <Button variant="outline" size="sm" loading={catalog.isFetching} onClick={() => void catalog.refetch()}>Retry capture catalog</Button>
    </div>}
    {activeView === "top" && <RunTop run={run} />}
    {!catalog.isPending && <div hidden={activeView !== "events"} className="min-h-0 flex-1 overflow-hidden">
      <div role="tabpanel" aria-label="Events" className="flex h-full min-h-0 flex-col">
        <TraceFeed kinds={kinds} loadPage={source.loadPage} presentation="ansi" {...(live && source.follow ? { follow: source.follow } : {})} />
      </div>
    </div>}
    {activeView === "tasks" && taskCapture && <div role="tabpanel" aria-label="Tasks" className="min-h-0 flex-1 overflow-auto">
      <RunTasks mount={run.mount} capture={taskCapture} live={live} />
    </div>}
    {activeView === "process" && <div role="tabpanel" aria-label="Process details" className="min-h-0 flex-1 overflow-auto">
      {run.process
        ? <RunProcessDetail trace={trace} selectedKey={selectedKey} />
        : <RecordedGroups run={run} view={groupsView} onViewChange={next => setGroupsView(previous => ({ ...previous, ...next }))} />}
    </div>}
  </div>;
}
