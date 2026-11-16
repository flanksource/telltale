import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Button, Workspace } from "@flanksource/clicky-ui/components";
import { DataTable, type DataTableColumn } from "@flanksource/clicky-ui/data";
import { fieldClass, inspect } from "./diagnostics";
import { persistFilter } from "./api";
import { useRecordings } from "./useRecordings";
import { JvmRecordings } from "./JvmRecordings";
import { JvmCaptureResults } from "./JvmCaptureResults";

interface Subscriber extends Record<string, unknown> { id: string; mbean: string; queued: number; dropped: number; active: boolean }

export function JvmNotifications({ agent }: { agent: string }) {
  const params = new URLSearchParams(window.location.search);
  const [mbean, setMBean] = useState(params.get("objectName") ?? params.get("mbean") ?? "java.lang:type=GarbageCollector,*");
  const [types, setTypes] = useState(params.get("notificationTypes") ?? "");
  const [duration, setDuration] = useState("5m");
  const [capacity, setCapacity] = useState(500);
  const [selected, setSelected] = useState(params.get("recording") ?? "");
  const { query, recordings, start, stop } = useRecordings(agent, "jvm_notifications");
  const form = <form className="space-y-3 rounded border p-3" onSubmit={event => { event.preventDefault(); start.mutate({ duration, traces: { jvm_notifications: { enabled: true, target: { seam: "agent", agent }, mbean: mbean.trim(), types: types.split(",").map(value => value.trim()).filter(Boolean), capacity } } }, { onSuccess: run => setSelected(run.runId) }); }}>
    <p className="text-sm text-muted-foreground">Record JMX notifications and decoded garbage collections into a durable capture.</p>
    <label className="block text-sm">MBean object name or pattern<input required className={fieldClass} value={mbean} onChange={event => { setMBean(event.target.value); persistFilter("mbean", event.target.value); persistFilter("objectName", ""); }} /></label>
    <label className="block text-sm">Notification type prefixes<input className={fieldClass} value={types} onChange={event => { setTypes(event.target.value); persistFilter("notificationTypes", event.target.value); }} placeholder="Comma-separated; blank captures all types" /></label>
    <div className="grid gap-3 sm:grid-cols-2"><label className="block text-sm">Capture duration<input required className={fieldClass} value={duration} onChange={event => setDuration(event.target.value)} /></label><label className="block text-sm">Buffer capacity<input required type="number" min={1} max={50000} step={1} className={fieldClass} value={capacity} onChange={event => setCapacity(Number(event.target.value))} /></label></div>
    {start.error && <p role="alert" className="text-sm">{start.error.message}</p>}
    <Button type="submit" disabled={start.isPending || !mbean.trim() || !duration.trim() || !Number.isInteger(capacity) || capacity < 1 || capacity > 50000}>{start.isPending ? "Starting…" : "Arm recording"}</Button>
  </form>;
  return <Workspace className="h-[1000px] rounded border" storageKey={`telltale.jvm.${agent}.notifications`} panes={[
    { id: "recordings", label: "Notification recordings", location: "center", collapsible: false, contentClassName: "flex min-h-0 flex-col gap-4 overflow-auto p-3", content: <>{form}<JvmRecordings recordings={recordings} loading={query.isPending} error={query.error} selected={selected} onSelect={row => { setSelected(row.run.runId); persistFilter("recording", row.run.runId); }} onRefresh={() => void query.refetch()} /></> },
    { id: "events", label: "Notification stream", location: "bottom", height: 400, contentClassName: "flex min-h-0 flex-col overflow-hidden p-3", content: <JvmCaptureResults recording={recordings.find(row => row.run.runId === selected)} stop={run => stop.mutate(run)} stopping={stop.isPending} error={stop.error} /> },
    { id: "subscribers", label: "Active subscribers", location: "right", width: 350, minWidth: 280, contentClassName: "flex min-h-0 flex-col overflow-auto p-3", content: <Subscribers agent={agent} /> },
  ]} />;
}

function Subscribers({ agent }: { agent: string }) {
  const subscribers = useQuery({ queryKey: ["jvm-notification-subscribers", agent], queryFn: () => inspect<Subscriber[]>(agent, "jvm.notifications.list"), refetchInterval: 2000, retry: false });
  const columns: DataTableColumn<Subscriber>[] = [{ key: "mbean", label: "MBean", minWidth: 180 }, { key: "queued", label: "Queued" }, { key: "dropped", label: "Dropped" }, { key: "active", label: "Active" }];
  return <div className="flex h-full min-h-64 min-w-0 flex-col gap-3"><p className="text-xs text-muted-foreground">Subscribers held by this JVM. Queued and dropped counts expose buffer pressure.</p>{subscribers.error && <p role="alert" className="text-sm">{subscribers.error.message}</p>}<DataTable columns={columns} data={subscribers.data ?? []} getRowId={row => row.id} loading={subscribers.isPending} emptyMessage="No active notification subscribers" /></div>;
}
