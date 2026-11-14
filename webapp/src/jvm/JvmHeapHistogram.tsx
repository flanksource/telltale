import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { Button, Panel } from "@flanksource/clicky-ui/components";
import { DataTable, type DataTableColumn } from "@flanksource/clicky-ui/data";
import { persistFilter } from "./api";
import { fieldClass, inspect, type HeapClass, type HeapHistogram } from "./diagnostics";
import { formatBytes } from "../top/groups";

const columns: DataTableColumn<HeapClass>[] = [
  { key: "rank", label: "#", align: "right" },
  { key: "className", label: "Class", minWidth: 240, render: (_, row) => <span className="break-all font-mono">{row.className}</span> },
  { key: "instances", label: "Instances", align: "right", render: (_, row) => row.instances.toLocaleString() },
  { key: "bytes", label: "Bytes", align: "right", render: (_, row) => formatBytes(row.bytes) },
  { key: "loader", label: "Module or loader", render: (_, row) => row.loader ?? "—" },
];

/** jvm.heap.histogram: the heap's classes by retained bytes, read through GC.class_histogram. */
export function JvmHeapHistogram({ agent }: { agent: string }) {
  const [top, setTop] = useState(100);
  const [classPattern, setClassPattern] = useState(() => new URLSearchParams(window.location.search).get("classPattern") ?? "");
  const [live, setLive] = useState(false);
  const read = useMutation({ mutationFn: () => inspect<HeapHistogram>(agent, "jvm.heap.histogram", { top, ...(classPattern.trim() ? { classPattern: classPattern.trim() } : {}), live }) });
  const valid = Number.isInteger(top) && top > 0;
  const histogram = read.data;
  return <Panel title="Heap histogram"><div className="space-y-3 min-w-0">
    <p className="text-sm text-muted-foreground">Classes by retained bytes, largest first. Totals cover the whole heap.</p>
    <form className="flex flex-wrap items-end gap-3" onSubmit={event => { event.preventDefault(); read.mutate(); }}>
      <label className="block w-28 text-sm">Rows<input type="number" min={1} className={fieldClass} value={top} onChange={event => setTop(Number(event.target.value))} /></label>
      <label className="block text-sm">Class pattern<input className={fieldClass} placeholder="com.example.*" value={classPattern} onChange={event => { setClassPattern(event.target.value); persistFilter("classPattern", event.target.value); }} /></label>
      <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={live} onChange={event => setLive(event.target.checked)} />Count only live objects (forces a full GC first)</label>
      <Button type="submit" disabled={!valid || read.isPending}>{read.isPending ? "Reading…" : "Read histogram"}</Button>
    </form>
    {!valid && <p role="alert" className="text-sm">Rows must be a positive whole number</p>}
    {read.error && <p role="alert" className="text-sm">{read.error.message}</p>}
    {histogram && <p role="status" className="text-sm">{histogram.live ? "Live objects" : "All objects"} · {formatBytes(histogram.totalBytes)} in {histogram.totalInstances.toLocaleString()} instances · {histogram.matched} classes matched · {new Date(histogram.collectedAt).toLocaleString()}</p>}
    {histogram && <div className="flex h-96 min-h-0 min-w-0 flex-col"><DataTable data={histogram.rows} columns={columns} getRowId={row => `${row.rank}:${row.className}`} emptyMessage="No classes match this pattern" /></div>}
  </div></Panel>;
}
