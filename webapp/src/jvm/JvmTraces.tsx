import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Workspace } from "@flanksource/clicky-ui/components";
import { useRouter } from "@flanksource/clicky-ui/rpc";
import { DataTable, type DataTableColumn } from "@flanksource/clicky-ui/data";
import { useRecordings } from "./useRecordings";
import { JvmRecordings } from "./JvmRecordings";
import { JvmCaptureResults } from "./JvmCaptureResults";
import { fieldClass, inspect } from "./diagnostics";
import { persistFilter } from "./api";
import { JvmResults } from "./JvmResults";

interface Probe extends Record<string, unknown> { key: string; owners: string[]; frozen: boolean; dropped: number }

export function JvmTraces({ agent }: { agent: string }) {
  const { renderLink } = useRouter();
  const [selected, setSelected] = useState(() => new URLSearchParams(window.location.search).get("recording") ?? "");
  const { query, recordings, stop } = useRecordings(agent, "jvm_trace");
  return <div className="min-w-0 space-y-3">
    <div className="flex flex-wrap justify-between gap-3"><p className="text-sm text-muted-foreground">Running and ended method captures retain their events after probes are restored.</p>{renderLink({ to: `/jvm/${encodeURIComponent(agent)}/instrument`, children: "Start trace", className: "rounded border px-3 py-1 text-sm underline" })}</div>
    <Workspace className="h-[950px] rounded border" storageKey={`telltale.jvm.${agent}.traces`} panes={[
      { id: "recordings", label: "JVM traces", location: "center", collapsible: false, contentClassName: "flex min-h-0 flex-col overflow-auto p-3", content: <JvmRecordings recordings={recordings} loading={query.isPending} error={query.error} selected={selected} onSelect={row => { setSelected(row.run.runId); persistFilter("recording", row.run.runId); }} onRefresh={() => void query.refetch()} /> },
      { id: "events", label: "Selected trace", location: "bottom", height: 400, contentClassName: "flex min-h-0 flex-col overflow-hidden p-3", content: <JvmCaptureResults recording={recordings.find(row => row.run.runId === selected)} stop={run => stop.mutate(run)} stopping={stop.isPending} error={stop.error} /> },
      { id: "probes", label: "Runtime probes", location: "right", width: 350, minWidth: 280, contentClassName: "overflow-auto p-3", content: <RuntimeProbes agent={agent} /> },
    ]} />
  </div>;
}

function RuntimeProbes({ agent }: { agent: string }) {
  const [key, setKey] = useState(() => new URLSearchParams(window.location.search).get("key") ?? "");
  const probes = useQuery({ queryKey: ["jvm-probe-owners", agent], queryFn: () => inspect<{ keys: Probe[] }>(agent, "jvm.probes.list"), refetchInterval: 5000, retry: false });
  const events = useQuery({ queryKey: ["jvm-probe-preview", agent, key], queryFn: () => inspect(agent, "jvm.probes.read", { key, since: 0, limit: 100 }), enabled: !!key, refetchInterval: 5000, retry: false });
  const columns: DataTableColumn<Probe>[] = [{ key: "key", label: "Key" }, { key: "frozen", label: "Frozen" }, { key: "dropped", label: "Dropped" }];
  return <div className="space-y-3 min-w-0"><p className="text-xs text-muted-foreground">All probes currently held by the JVM, including captures outside this Telltale server. Reading their buffers does not drain them.</p>
    {probes.error && <p role="alert" className="text-sm">{probes.error.message}</p>}
    <div className="flex h-64 min-h-0 min-w-0 flex-col"><DataTable columns={columns} data={probes.data?.keys ?? []} loading={probes.isPending} getRowId={row => row.key} onRowClick={row => { setKey(row.key); persistFilter("key", row.key); }} emptyMessage="No active probes" /></div>
    <label className="block text-sm">Probe key<input className={`${fieldClass} font-mono`} value={key} onChange={event => { setKey(event.target.value); persistFilter("key", event.target.value); }} /></label>
    {events.error && <p role="alert" className="text-sm">{events.error.message}</p>}{events.data !== undefined && <JvmResults value={events.data} />}
    {key && <p className="text-xs text-muted-foreground">Preview of the first 100 buffered events. Recorded captures keep their durable results in the catalog.</p>}
  </div>;
}
