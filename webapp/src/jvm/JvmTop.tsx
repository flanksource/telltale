import { useEffect, useState, useSyncExternalStore } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Button, Panel, InputField, Select } from "@flanksource/clicky-ui/components";
import { DataTable, type DataTableColumn } from "@flanksource/clicky-ui/data";
import { persistFilter } from "./api";
import { ThreadDetail } from "./JvmInspectors";
import type { Threads, ThreadRow } from "./diagnostics";
import type { Metrics } from "./metrics";
import { useSnapshotQuery } from "../monitoring/useSnapshotQuery";

interface Frame { agentId: string; sampledAt: string; metrics: Metrics; threads: Threads }

async function snapshot(agent: string, signal: AbortSignal): Promise<Frame> {
  const response = await fetch("/api/v1/jvm/top", { method: "POST", headers: { "Content-Type": "application/json", Accept: "application/json" }, body: JSON.stringify({ agent, once: true, format: "json" }), signal });
  const text = await response.text();
  if (!response.ok) throw new Error(`JVM Top (${response.status}): ${text}`);
  const result = JSON.parse(text) as { success: boolean; output?: string; error?: string };
  if (!result.success || !result.output) throw new Error(`JVM Top: ${result.error ?? "missing snapshot"}`);
  const frame = JSON.parse(result.output) as Frame;
  if (!frame.agentId || !frame.metrics?.memory || !Array.isArray(frame.threads?.threads) || !Number.isFinite(Date.parse(frame.sampledAt))) throw new Error("Invalid JVM Top snapshot");
  return frame;
}

const subscribe = (listener: () => void) => { window.addEventListener("popstate", listener); return () => window.removeEventListener("popstate", listener); };
const visibleSubscribe = (listener: () => void) => { document.addEventListener("visibilitychange", listener); return () => document.removeEventListener("visibilitychange", listener); };
const bytes = (value: number) => `${(value / 1024 ** 2).toFixed(1)} MiB`;
const percent = (value: number | null | undefined) => value == null ? "Unavailable" : `${value.toFixed(1)}%`;

export function JvmTop({ agent }: { agent: string }) {
  const client = useQueryClient();
  const search = useSyncExternalStore(subscribe, () => window.location.search);
  const visible = useSyncExternalStore(visibleSubscribe, () => document.visibilityState !== "hidden");
  const params = new URLSearchParams(search);
  const filter = params.get("threadFilter") ?? "";
  const state = params.get("threadState") ?? "";
  const selected = params.get("thread");
  const [paused, setPaused] = useState(Boolean(selected));
  const hasSnapshot = client.getQueryData(["jvm-top", agent]) !== undefined;
  const key = params.get("topSort") ?? "cpuPercent";
  const dir = params.get("topDir") === "asc" ? "asc" : "desc";
  const query = useSnapshotQuery({ key: ["jvm-top", agent], read: ({ signal }) => snapshot(agent, signal), enabled: visible && (!paused || !hasSnapshot), stream: visible && !paused ? `/api/v1/jvm/top?${new URLSearchParams({ agent, follow: "1" })}` : null });
  useEffect(() => { if (!visible || (paused && hasSnapshot)) void client.cancelQueries({ queryKey: ["jvm-top", agent] }); }, [visible, paused, hasSnapshot, client, agent]);
  const frame = query.data;
  const select = (id: number) => { setPaused(true); persistFilter("thread", String(id)); };
  const columns: DataTableColumn<ThreadRow>[] = [
    { key: "id", label: "ID" }, { key: "name", label: "Thread", minWidth: 160 }, { key: "state", label: "State" },
    { key: "cpuPercent", label: "CPU % (one core)", render: (_, row) => percent(row.cpuPercent) },
    { key: "cpuTimeMs", label: "CPU ms", render: (_, row) => row.cpuTimeMs ?? "Unavailable" },
    { key: "blockedCount", label: "Blocked" }, { key: "waitedCount", label: "Waited" },
    { key: "lockOwnerName", label: "Lock owner", render: (_, row) => row.lockOwnerId == null ? "—" : <Button variant="ghost" onClick={event => { event.stopPropagation(); select(row.lockOwnerId!); }}>{row.lockOwnerName ?? String(row.lockOwnerId)}</Button> },
  ];
  const rows = (frame?.threads.threads ?? []).filter(row => (!state || row.state === state) && `${row.id} ${row.name} ${row.state}`.toLowerCase().includes(filter.toLowerCase()));
  rows.sort((a, b) => {
    if (key === "cpuPercent" && (a.cpuPercent == null || b.cpuPercent == null)) return a.cpuPercent == null ? b.cpuPercent == null ? a.id - b.id : 1 : -1;
    const av = a[key as keyof ThreadRow], bv = b[key as keyof ThreadRow];
    const order = typeof av === "number" && typeof bv === "number" ? av - bv : String(av).localeCompare(String(bv));
    return (dir === "asc" ? order : -order) || a.id - b.id;
  });
  return <div className="min-w-0 space-y-3">
    <div className="flex flex-wrap items-end gap-3">
      <label className="text-sm">Filter threads<InputField value={filter} onChange={value => persistFilter("threadFilter", value)} /></label>
      <label className="text-sm">Thread state<Select value={state} onChange={event => persistFilter("threadState", event.target.value)}><option value="">All states</option>{["NEW", "RUNNABLE", "BLOCKED", "WAITING", "TIMED_WAITING", "TERMINATED"].map(value => <option key={value}>{value}</option>)}</Select></label>
      <Button variant="outline" onClick={() => { if (paused) persistFilter("thread", ""); setPaused(!paused); }}>{paused ? "Resume live" : "Pause"}</Button>
    </div>
    {query.isPending && <p role="status">Reading JVM Top…</p>}
    {query.error && <p role="alert">{frame && "Stale snapshot · "}{query.error.message}</p>}
    {frame && <>
      <p className="text-xs text-muted-foreground">{frame.agentId} · Java {frame.metrics.runtime.javaVersion} · uptime {Math.floor(frame.metrics.runtime.uptime / 1000)}s · sampled {new Date(frame.sampledAt).toLocaleTimeString()}{paused && " · Paused"}</p>
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <Panel title="CPU"><p>{percent(frame.metrics.processCpuPercent)} of JVM capacity</p></Panel>
        {(["heap", "nonHeap", "buffer"] as const).map(area => <Panel key={area} title={area === "heap" ? "Heap" : area === "nonHeap" ? "Non-heap" : "Buffers"}>{frame.metrics.memory[area].map(pool => <div key={pool.name} className="space-y-1 py-1 text-xs"><p>{pool.name}: {bytes(pool.used)} / {bytes(pool.committed)} committed · {pool.max < 0 ? "unbounded" : `${bytes(pool.max)} max`}</p>{pool.max > 0 && <progress aria-label={`${pool.name} usage`} className="w-full" value={pool.used} max={pool.max} />}</div>)}</Panel>)}
      </div>
      <Panel title="Garbage collection"><div className="flex flex-wrap gap-4 text-sm">{frame.metrics.garbageCollectors.map(gc => <p key={gc.name}>{gc.name}: {gc.collectionCount < 0 ? "Unavailable" : gc.collectionCount} collections · {gc.collectionTime < 0 ? "Unavailable" : `${gc.collectionTime} ms`} (lifetime)</p>)}</div></Panel>
      <p className="text-sm">{frame.threads.threads.length} threads · {["RUNNABLE", "BLOCKED", "WAITING", "TIMED_WAITING"].map(value => `${value}: ${frame.threads.threads.filter(row => row.state === value).length}`).join(" · ")}{!frame.threads.cpuTimeEnabled && " · Thread CPU timing disabled"}</p>
      {frame.threads.deadlocked.length > 0 && <p role="alert" className="text-destructive">Deadlock detected: {frame.threads.deadlocked.join(", ")}</p>}
    </>}
    <div className="flex h-96 min-h-0 min-w-0 flex-col"><DataTable data={rows} columns={columns} getRowId={row => String(row.id)} loading={query.isPending && !paused} manualSort sort={{ key, dir }} onSortChange={sort => { persistFilter("topSort", sort?.key ?? "cpuPercent"); persistFilter("topDir", sort?.dir ?? "desc"); }} onRowClick={row => select(row.id)} emptyMessage="No matching threads" /></div>
    {selected && <><Button variant="outline" onClick={() => persistFilter("thread", "")}>Close stack</Button><ThreadDetail key={`${agent}:${selected}`} agent={frame?.agentId ?? agent} id={Number(selected)} /></>}
  </div>;
}
