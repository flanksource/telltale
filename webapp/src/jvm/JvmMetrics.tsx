import { useCallback, useId, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Button, Workspace, type WorkspacePaneSpec } from "@flanksource/clicky-ui/components";
import { TimeseriesPanel, type TimeseriesSeries } from "@flanksource/clicky-ui/data";
import { inspect } from "./diagnostics";
import { JvmResults } from "./JvmResults";
import { appendSnapshot, metricPoints, trendPanels, type MemoryPool, type Metrics } from "./metrics";

export function JvmMetrics({ agent }: { agent: string }) {
  const [paused, setPaused] = useState(false);
  const history = useRef<Metrics[]>([]);
  const client = useQueryClient();
  const identity = useId();
  const sample = useCallback(async () => {
    const snapshot = await client.fetchQuery({ queryKey: ["jvm-metrics", agent], queryFn: () => inspect<Metrics>(agent, "jvm.metrics"), staleTime: 1000 });
    history.current = appendSnapshot(history.current, snapshot);
    return snapshot;
  }, [agent, client]);
  const metrics = useQuery({ queryKey: ["jvm-dashboard", agent], queryFn: sample, refetchInterval: paused ? false : 15000 });
  const snapshot = metrics.data;
  const panes = useMemo<WorkspacePaneSpec[]>(() => {
    if (!snapshot) return [];
    return [
      { id: "trends", label: "Trends", location: "center", collapsible: false, contentClassName: "overflow-auto p-3", content: <div className="grid gap-3 xl:grid-cols-2">{trendPanels(snapshot).map(panel => {
        const series: TimeseriesSeries[] = panel.series.map(metric => ({ id: `${agent}/${identity}/${metric.id}`, label: metric.label, unit: metric.unit, load: async () => { await sample(); return { id: metric.id, points: metricPoints(history.current, metric) }; } }));
        return <TimeseriesPanel key={panel.title} title={panel.title} series={series} range="1h" refreshMs={paused ? 0 : 15000} />;
      })}</div> },
      { id: "runtime", label: "Runtime", location: "right", width: 340, minWidth: 260, contentClassName: "overflow-auto p-3", content: <RuntimePanel snapshot={snapshot} /> },
      { id: "memory-pools", label: "Memory pools", location: "bottom", height: 260, contentClassName: "overflow-auto p-3", content: <MemoryPools snapshot={snapshot} /> },
    ];
  }, [snapshot, agent, identity, sample, paused]);
  return <div className="min-w-0 space-y-3">
    <div className="flex flex-wrap items-center gap-2"><Button variant="outline" onClick={() => setPaused(!paused)}>{paused ? "Resume polling" : "Pause polling"}</Button><Button variant="outline" disabled={metrics.isFetching} onClick={() => void metrics.refetch()}>Refresh metrics</Button><p className="text-xs text-muted-foreground">Live samples collected while this page is open, up to one hour.</p></div>
    {metrics.isPending && <p role="status">Reading JVM metrics…</p>}{metrics.error && <p role="alert">JVM metrics unavailable: {metrics.error.message}</p>}
    {snapshot && <Workspace panes={panes} storageKey={`telltale.jvm.${agent}.dashboard`} className="h-[760px] rounded border" />}
  </div>;
}

function RuntimePanel({ snapshot }: { snapshot: Metrics }) {
  const runtime = snapshot.runtime;
  return <div className="space-y-4 text-sm"><dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2">
    <dt className="text-muted-foreground">Java</dt><dd className="break-all font-mono">{runtime.javaVersion}</dd>
    <dt className="text-muted-foreground">OS</dt><dd>{runtime.osName} {runtime.osVersion}</dd>
    <dt className="text-muted-foreground">Processors</dt><dd>{runtime.processors}</dd>
    <dt className="text-muted-foreground">Uptime</dt><dd>{Math.floor(runtime.uptime / 3600000)}h {Math.floor(runtime.uptime % 3600000 / 60000)}m</dd>
    <dt className="text-muted-foreground">CPU</dt><dd>{snapshot.processCpuPercent === undefined ? "Unavailable" : `${snapshot.processCpuPercent.toFixed(1)}%`}</dd>
    <dt className="text-muted-foreground">Load average</dt><dd>{runtime.systemLoadAverage < 0 ? "Unavailable" : runtime.systemLoadAverage}</dd>
    <dt className="text-muted-foreground">Captured</dt><dd>{new Date(snapshot.capturedAt).toLocaleString()}</dd>
  </dl><section><h3 className="mb-2 font-medium">Garbage collectors</h3>{snapshot.garbageCollectors.map(gc => <div key={gc.name} className="mb-2 rounded border p-2"><div className="font-medium">{gc.name}</div><div className="text-xs text-muted-foreground">{gc.collectionCount} collections · {gc.collectionTime} ms</div></div>)}</section>
    {snapshot.connectors.length > 0 && <section><h3 className="mb-2 font-medium">Server connectors</h3><JvmResults value={snapshot.connectors} /></section>}
    {snapshot.dataSources.length > 0 && <section><h3 className="mb-2 font-medium">Data sources</h3><JvmResults value={snapshot.dataSources} /></section>}
  </div>;
}

function MemoryPools({ snapshot }: { snapshot: Metrics }) {
  return <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">{([["Heap", snapshot.memory.heap], ["Non-heap", snapshot.memory.nonHeap], ["Buffers", snapshot.memory.buffer]] as const).map(([label, pools]) => <section key={label} className="rounded border p-3"><h3 className="mb-2 text-sm font-medium">{label}</h3><div className="space-y-2">{pools.map(pool => <PoolBar key={pool.name} pool={pool} />)}</div></section>)}</div>;
}

function PoolBar({ pool }: { pool: MemoryPool }) {
  const limit = pool.max > 0 ? pool.max : pool.committed;
  return <div className="space-y-1"><div className="flex flex-wrap justify-between gap-2 text-xs"><span>{pool.name}</span><span>{bytes(pool.used)} / {limit > 0 ? bytes(limit) : "unbounded"}</span></div>{limit > 0 && <progress className="h-2 w-full accent-primary" aria-label={`${pool.name} memory used`} value={Math.min(pool.used, limit)} max={limit} />}</div>;
}

function bytes(value: number) { return value >= 1024 ** 3 ? `${(value / 1024 ** 3).toFixed(1)} GiB` : `${(value / 1024 ** 2).toFixed(1)} MiB`; }
