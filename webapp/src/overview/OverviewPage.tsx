import { useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Button, Panel, Tabs } from "@flanksource/clicky-ui/components";
import { Badge, DataTable, type DataTableColumn } from "@flanksource/clicky-ui/data";
import { formatDuration } from "@flanksource/clicky-ui/utils";
import { useRouter } from "@flanksource/clicky-ui/rpc";
import { fetchForm, fetchSources, fetchTraces, searchParams } from "../otel/api";
import { fetchTraceRuns } from "../traces/api";
import { RunDetail } from "../traces/TracesPage";
import { StartRunForm } from "../traces/StartRunForm";
import { activityRows, type ActivityRow } from "./activity";
import { useOverviewState } from "./state";
import { RunShellSlot } from "../layout/RunShellSlot";

const inputClass = "min-w-0 rounded border border-border bg-background px-2 py-1 text-sm";

function ActivityTable({ rows, loading, filtered }: { rows: ActivityRow[]; loading: boolean; filtered: boolean }) {
  const { navigate } = useRouter();
  const columns: DataTableColumn<ActivityRow>[] = [
    { key: "kind", label: "Type", render: (_, row) => <Badge size="sm" clickToCopy={false} tone={row.kind === "otel" ? "info" : "neutral"}>{row.kind === "otel" ? "OpenTelemetry" : "Recorded run"}</Badge> },
    { key: "title", label: "Trace / run", grow: true, minWidth: 240, render: (_, row) => <div className="flex min-w-0 items-center gap-2 whitespace-nowrap" title={`${row.title} · ${row.detail}`}>
      <span className="truncate font-medium">{row.title}</span><span className="max-w-[45%] shrink-0 truncate font-mono text-xs text-muted-foreground">{row.detail}</span>
    </div> },
    { key: "status", label: "Status", render: (_, row) => <Badge size="sm" clickToCopy={false} tone={row.status.includes("error") || row.status === "failed" ? "danger" : row.status === "succeeded" || row.status === "ok" ? "success" : row.status === "running" ? "info" : "warning"}>{row.status}</Badge> },
    { key: "startedAt", label: "Started", render: (_, row) => row.startedAt ? <time className="whitespace-nowrap text-xs tabular-nums" dateTime={row.startedAt} title={row.startedAt}>{new Date(row.startedAt).toLocaleString()}</time> : <span className="whitespace-nowrap text-muted-foreground">Not recorded</span> },
    { key: "durationMs", label: "Duration", align: "right", render: (_, row) => <span className="whitespace-nowrap tabular-nums">{row.durationMs === null ? "—" : formatDuration(row.durationMs)}</span> },
    { key: "contents", label: "Contents", minWidth: 180, render: (_, row) => <div className="flex min-w-0 items-center gap-1 overflow-hidden whitespace-nowrap" title={row.contents.join(" · ")}>{row.contents.map(content => <Badge key={content} size="sm" className="shrink-0" clickToCopy={false} tone="neutral">{content}</Badge>)}</div> },
  ];
  return <div className="flex h-[min(36rem,65dvh)] min-h-64 min-w-0 flex-col overflow-hidden">
    <DataTable className="min-h-0 flex-1" data={rows} columns={columns} getRowId={row => row.id}
      getRowHref={row => row.href} onRowClick={row => navigate(row.href)} loading={loading} showFullscreenControl={false}
      emptyMessage={filtered ? "No activity matches these filters." : "No traces or recorded runs yet."} />
  </div>;
}

export default function OverviewPage() {
  const { renderLink } = useRouter();
  const client = useQueryClient();
  const [state, setState, activitySearch] = useOverviewState();
  const [recording, setRecording] = useState(false);
  const wantsTraces = !state.run && state.activity !== "run";
  const runs = useQuery({ queryKey: ["trace-runs", {}], queryFn: () => fetchTraceRuns(), retry: false,
    refetchInterval: query => query.state.status === "error" ? false : query.state.data?.runs.some(run => run.status === "running" && !run.standing) ? 1000 : 5000 });
  const form = useQuery({ queryKey: ["otel-form"], queryFn: fetchForm, retry: false, staleTime: Infinity, enabled: wantsTraces });
  const sources = useQuery({ queryKey: ["otel-sources"], queryFn: fetchSources, retry: false, enabled: wantsTraces });
  const values = useMemo(() => ({ ...form.data?.defaults, ...(state.otelSource ? { source: state.otelSource } : {}), ...(state.otelWindow ? { lookback: state.otelWindow } : {}) }), [form.data, state.otelSource, state.otelWindow]);
  const traces = useQuery({ queryKey: ["overview-traces", values], queryFn: ({ signal }) => fetchTraces(values, signal), retry: false,
    enabled: wantsTraces && !!form.data && !!sources.data, refetchInterval: query => wantsTraces && query.state.status !== "error" ? 5000 : false });
  const rows = useMemo(() => activityRows({ runs: runs.data?.runs ?? [], traces: wantsTraces ? traces.data?.traces ?? [] : [], values, search: activitySearch }), [runs.data, traces.data, wantsTraces, values, activitySearch]);
  const visible = rows.filter(row => (state.activity === "all" || state.activity === row.kind) && `${row.title} ${row.detail} ${row.status} ${row.contents.join(" ")}`.toLowerCase().includes(state.activityQ.trim().toLowerCase()));
  const telemetryError = form.error ?? sources.error ?? traces.error;
  const params = new URLSearchParams(activitySearch);
  for (const key of ["run", "feedKind", "feedSort", "feedView", "showExited", "runView"]) params.delete(key);
  const back = `/${params.size ? `?${params}` : ""}${window.location.hash}`;
  if (state.run) {
    const run = runs.data?.runs.find(candidate => candidate.runId === state.run);
    return <div className="flex h-full min-h-0 min-w-0 flex-col overflow-hidden">
      <RunShellSlot name="nav"><div className="flex min-w-0 items-center gap-3">
        {renderLink({ to: back, className: "shrink-0 text-sm text-primary underline", children: "Back to overview" })}
        <h1 className="truncate text-sm font-semibold" title={state.run}>Recorded trace run</h1>
      </div></RunShellSlot>
      {runs.isPending && <p role="status">Loading trace run…</p>}
      {runs.error && <p role="alert">{runs.error.message}</p>}
      {runs.data && !run && <p role="alert">Run {state.run} is not available.</p>}
      {run && <RunDetail key={run.runId} run={run} />}
    </div>;
  }
  return <div className="min-w-0 space-y-4">
    <div className="flex flex-wrap items-center justify-between gap-3">
      <div><h1 className="text-2xl font-semibold">Overview</h1><p className="mt-1 text-sm text-muted-foreground">OpenTelemetry traces and recorded trace runs.</p></div>
      <div className="flex items-center gap-3">
        {renderLink({ to: `/otel?${searchParams(values)}`, className: "text-sm text-primary underline", children: "Search traces" })}
        <Button type="button" onClick={() => setRecording(previous => !previous)}>{recording ? "Close recording form" : "Record a run"}</Button>
      </div>
    </div>
    {recording && <Panel title="New run"><StartRunForm onStarted={run => { void client.invalidateQueries({ queryKey: ["trace-runs"] }); setRecording(false); setState({ run: run.runId }); }} /></Panel>}
    <Tabs className="max-w-full overflow-x-auto" tabs={[{ id: "all", label: "All activity" }, { id: "otel", label: "OpenTelemetry traces" }, { id: "run", label: "Recorded runs" }]} value={state.activity} onChange={activity => setState({ activity })} />
    <div className="flex flex-wrap items-end gap-3">
      <label className="flex min-w-0 flex-col gap-1 text-xs">Filter activity<input type="search" className={inputClass} value={state.activityQ} placeholder="Service, command, ID or capture kind" onChange={event => setState({ activityQ: event.target.value })} /></label>
      {wantsTraces && <>
        <label className="flex min-w-0 flex-col gap-1 text-xs">Trace source<select className={inputClass} value={values.source ?? ""} disabled={!sources.data} onChange={event => setState({ otelSource: event.target.value })}>
          {sources.data?.map(source => <option key={source.id} value={source.id}>{source.name}</option>)}
        </select></label>
        <label className="flex min-w-0 flex-col gap-1 text-xs">Trace window<select className={inputClass} value={values.lookback ?? ""} disabled={!form.data} onChange={event => setState({ otelWindow: event.target.value })}>
          {[...new Set([...(values.lookback ? [values.lookback] : []), "1h", "6h", "24h", "168h"])].map(window => <option key={window} value={window}>{window === "168h" ? "7 days" : window}</option>)}
        </select></label>
      </>}
      <Button type="button" variant="outline" size="sm" onClick={() => { void runs.refetch(); if (wantsTraces) { void form.refetch(); void sources.refetch(); if (form.data && sources.data) void traces.refetch(); } }}>Refresh activity</Button>
    </div>
    {wantsTraces && telemetryError && <div className="flex flex-wrap items-center gap-3 text-sm">
      <p role="alert" className="text-destructive">OpenTelemetry: {telemetryError.message}</p>
      <Button type="button" variant="outline" size="sm" onClick={() => { void form.refetch(); void sources.refetch(); if (form.data && sources.data) void traces.refetch(); }}>Retry OpenTelemetry</Button>
    </div>}
    {runs.error && <p role="alert" className="text-sm text-destructive">Recorded runs: {runs.error.message}</p>}
    {!["all", "otel", "run"].includes(state.activity) && <p role="alert">Unknown activity type: {state.activity}. Select a tab to reset it.</p>}
    <p className="text-xs text-muted-foreground">{wantsTraces && traces.data ? `${traces.data.traces.length} of ${traces.data.total} OpenTelemetry traces in the selected window${values.minSpans && values.minSpans > 1 ? ` with at least ${values.minSpans} spans` : ""} · ` : ""}{runs.data ? `${runs.data.runs.filter(run => !run.standing).length} recorded ${runs.data.runs.filter(run => !run.standing).length === 1 ? "run" : "runs"}` : "Loading recorded runs…"} · Newest first</p>
    <ActivityTable rows={visible} loading={runs.isPending || (wantsTraces && !telemetryError && (form.isPending || sources.isPending || traces.isPending))} filtered={state.activity !== "all" || !!state.activityQ.trim()} />
  </div>;
}
