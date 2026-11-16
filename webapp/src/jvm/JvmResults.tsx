import { useState } from "react";
import { CodeBlock, DataTable, ObjectGraph, type DataTableColumn } from "@flanksource/clicky-ui/data";
import { graphNode, objectNode, type MethodEvent } from "./results";
import { MethodTree } from "./MethodTree";
import { persistFilter } from "./api";
import { InputField } from "@flanksource/clicky-ui/components";

function record(value: unknown): value is Record<string, unknown> { return value !== null && typeof value === "object" && !Array.isArray(value); }

export function JvmResults({ value, onClass }: { value: unknown; onClass?: (name: string, loader: string) => void }) {
  const [filter, setFilter] = useState(() => new URLSearchParams(window.location.search).get("resultFilter") ?? "");
  if (value === undefined) return null;
  const rows = Array.isArray(value) ? value : record(value) && typeof value.total === "number" && Array.isArray(value.classes) ? value.classes : undefined;
  if (rows) {
    const objects: Record<string, unknown>[] = rows.map(entry => record(entry) ? entry : { value: entry });
    const columns: DataTableColumn<Record<string, unknown>>[] = [...new Set(objects.flatMap(entry => Object.keys(entry)))].map(key => ({
      key, label: key, minWidth: 120, render: (_, row) => typeof row[key] === "object" ? <details><summary>Inspect</summary><ObjectGraph roots={[objectNode(row[key], key)]} /></details> : <span className="break-words">{String(row[key] ?? "")}</span>,
    }));
    const filtered = objects.filter(row => JSON.stringify(row).toLowerCase().includes(filter.toLowerCase()));
    const methods = objects.filter((row): row is Record<string, unknown> & MethodEvent => typeof row.frame === "number" && typeof row.kind === "string");
    return <div className="space-y-3 min-w-0">
      <label className="block text-sm">Filter results<InputField value={filter} onChange={value => { setFilter(value); persistFilter("resultFilter", value); }} /></label>
      {record(value) && typeof value.total === "number" && <p className="text-sm">{value.total} classes · offset {String(value.offset)} · limit {String(value.limit)}</p>}
      {methods.length > 0 && <MethodTree events={methods} />}
      <div className="flex h-96 min-h-0 min-w-0 flex-col"><DataTable data={filtered} columns={columns} getRowId={(_, index) => String(index)} emptyMessage="No matching results" showFullscreenControl={false}
        {...(onClass ? { onRowClick: (row: Record<string, unknown>) => { if (typeof row.name === "string" && typeof row.loader === "string") onClass(row.name, row.loader); } } : {})} /></div>
    </div>;
  }
  if (record(value)) return <div className="space-y-3 min-w-0">
    {typeof value.stdout === "string" && value.stdout && <div><h3 className="text-sm font-medium">Standard output</h3><CodeBlock source={value.stdout} /></div>}
    {typeof value.stderr === "string" && value.stderr && <div><h3 className="text-sm font-medium">Standard error</h3><CodeBlock source={value.stderr} /></div>}
    {typeof value.source === "string" && <CodeBlock source={value.source} language="java" />}
    {typeof value.success === "boolean" && <p role="status" className="text-sm font-medium">{value.success ? value.applied === true ? "Patch applied" : "Compiled successfully; not applied" : "Compilation failed"}</p>}
    {Array.isArray(value.diagnostics) && value.diagnostics.map((entry: unknown, index: number) => record(entry) ? <p key={index} role={entry.kind === "ERROR" ? "alert" : "status"} className="whitespace-pre-wrap break-words font-mono text-sm">{String(entry.kind)} · {String(entry.line)}:{String(entry.column)} · {String(entry.message)}</p> : null)}
    {record(value.graph) ? <ObjectGraph roots={[graphNode(value.graph)]} showControls showOptionsMenu showFullscreenControl /> : <ObjectGraph roots={[objectNode(Object.fromEntries(Object.entries(value).filter(([key]) => !["stdout", "stderr", "source"].includes(key))))]} showControls showOptionsMenu showFullscreenControl />}
    <details><summary className="cursor-pointer text-xs">Raw result / copy</summary><CodeBlock source={JSON.stringify(value, null, 2)} language="json" /></details>
  </div>;
  return <ObjectGraph roots={[objectNode(value)]} showControls />;
}
