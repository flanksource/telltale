import { useState } from "react";
import { Button } from "@flanksource/clicky-ui/components";
import { DataTable, type DataTableColumn } from "@flanksource/clicky-ui/data";
import { useRouter } from "@flanksource/clicky-ui/rpc";
import { persistFilter } from "./api";
import { fieldClass } from "./diagnostics";
import type { Recording } from "./captures";

type RecordingRow = Record<string, unknown> & { id: string; started: string; state: string; count: number | undefined; recording: Recording };

export function JvmRecordings({ recordings, loading, error, selected, onSelect, onRefresh }: { recordings: Recording[]; loading: boolean; error: Error | null; selected: string; onSelect: (recording: Recording) => void; onRefresh: () => void }) {
  const { renderLink } = useRouter();
  const [filter, setFilter] = useState(() => new URLSearchParams(window.location.search).get("recordingFilter") ?? "");
  const [state, setState] = useState(() => new URLSearchParams(window.location.search).get("recordingState") ?? "");
  const rows: RecordingRow[] = recordings.map(recording => ({ id: recording.run.runId, started: recording.run.startedAt ?? "", state: recording.run.status, count: typeof recording.capture.summary?.count === "number" ? recording.capture.summary.count : undefined, recording }));
  const columns: DataTableColumn<RecordingRow>[] = [
    { key: "started", label: "Started", render: (_, row) => row.started ? new Date(row.started).toLocaleString() : "Unknown" },
    { key: "id", label: "Recording", render: (_, row) => <Button variant="ghost" className="max-w-full font-mono text-xs" aria-label={`Open recording ${row.id}`} onClick={() => onSelect(row.recording)}>{row.id === selected ? "● " : ""}{row.id}</Button> },
    { key: "state", label: "State" }, { key: "count", label: "Events", render: (_, row) => row.count ?? (row.state === "running" ? "Recording…" : "Unavailable") },
  ];
  return <div className="flex h-full min-h-0 min-w-0 flex-col gap-3">
    <div className="flex flex-wrap items-end gap-3">
      <label className="min-w-0 flex-1 text-sm">Filter recordings<input className={fieldClass} value={filter} onChange={event => { setFilter(event.target.value); persistFilter("recordingFilter", event.target.value); }} /></label>
      <label className="text-sm">Recording state<select className={fieldClass} value={state} onChange={event => { setState(event.target.value); persistFilter("recordingState", event.target.value); }}><option value="">All states</option>{["running", "succeeded", "failed", "unfinished"].map(value => <option key={value}>{value}</option>)}</select></label>
      <Button variant="outline" onClick={onRefresh}>Refresh recordings</Button>
    </div>
    {error && <p role="alert" className="text-sm">{error.message}</p>}
    <p className="text-xs text-muted-foreground">Recordings identify this JVM by agent ID or PID. {renderLink({ to: "/", children: "All runs", className: "underline" })} also includes selector-based captures and older recordings without target metadata.</p>
    <div className="flex min-h-64 min-w-0 flex-1 flex-col"><DataTable columns={columns} data={rows.filter(row => (!state || row.state === state) && row.id.toLowerCase().includes(filter.toLowerCase()))} getRowId={row => row.id} loading={loading} onRowClick={row => onSelect(row.recording)} emptyMessage="No recordings match this JVM and these filters" /></div>
  </div>;
}
