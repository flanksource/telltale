import { useState } from "react";
import { CodeBlock, DataTable, ExecutionTree, ObjectGraph, type DataTableColumn, type ObjectGraphNode } from "@flanksource/clicky-ui/data";
import { executionNodes, objectNode, type MethodEvent } from "./results";
import { persistFilter } from "./api";

function record(value: unknown): value is Record<string, unknown> { return value !== null && typeof value === "object" && !Array.isArray(value); }

export function JvmResults({ value, onClass }: { value: unknown; onClass?: (name: string, loader: string) => void }) {
  const [filter, setFilter] = useState(() => new URLSearchParams(window.location.search).get("resultFilter") ?? "");
  if (value === undefined) return null;
  const rows = Array.isArray(value) ? value : record(value) && Array.isArray(value.classes) ? value.classes : undefined;
  if (rows) {
    const objects: Record<string, unknown>[] = rows.map(entry => record(entry) ? entry : { value: entry });
    const columns: DataTableColumn<Record<string, unknown>>[] = [...new Set(objects.flatMap(entry => Object.keys(entry)))].map(key => ({
      key, label: key, minWidth: 120, render: (_, row) => typeof row[key] === "object" ? <details><summary>Inspect</summary><ObjectGraph roots={[objectNode(row[key], key)]} /></details> : <span className="break-words">{String(row[key] ?? "")}</span>,
    }));
    const filtered = objects.filter(row => JSON.stringify(row).toLowerCase().includes(filter.toLowerCase()));
    const methods = objects.filter((row): row is Record<string, unknown> & MethodEvent => typeof row.frame === "number" && typeof row.kind === "string");
    return <div className="space-y-3 min-w-0">
      <label className="block text-sm">Filter results<input className="ml-2 rounded border border-border bg-background px-2 py-1" value={filter} onChange={event => { setFilter(event.target.value); persistFilter("resultFilter", event.target.value); }} /></label>
      {record(value) && typeof value.total === "number" && <p className="text-sm">{value.total} classes · offset {String(value.offset)} · limit {String(value.limit)}</p>}
      {methods.length > 0 && <ExecutionTree roots={executionNodes(methods)} showControls />}
      <div className="flex h-96 min-h-0 min-w-0 flex-col"><DataTable data={filtered} columns={columns} getRowId={(_, index) => String(index)} emptyMessage="No matching results" showFullscreenControl={false}
        {...(onClass ? { onRowClick: (row: Record<string, unknown>) => { if (typeof row.name === "string" && typeof row.loader === "string") onClass(row.name, row.loader); } } : {})} /></div>
    </div>;
  }
  if (record(value)) return <div className="space-y-3 min-w-0">
    {typeof value.stdout === "string" && value.stdout && <div><h3 className="text-sm font-medium">Standard output</h3><CodeBlock source={value.stdout} /></div>}
    {typeof value.stderr === "string" && value.stderr && <div><h3 className="text-sm font-medium">Standard error</h3><CodeBlock source={value.stderr} /></div>}
    {typeof value.source === "string" && <CodeBlock source={value.source} language="java" />}
    {record(value.graph) ? <ObjectGraph roots={[graphNode(value.graph)]} showControls /> : <ObjectGraph roots={[objectNode(Object.fromEntries(Object.entries(value).filter(([key]) => !["stdout", "stderr", "source"].includes(key))))]} showControls />}
  </div>;
  return <ObjectGraph roots={[objectNode(value)]} showControls />;
}

function graphNode(value: Record<string, unknown>, id = "$"): ObjectGraphNode {
  if (typeof value.label !== "string") throw new Error("Object graph node is missing its label");
  return { id, label: value.label, ...(typeof value.type === "string" ? { type: value.type } : {}),
    ...(value.value === null || ["string", "number", "boolean"].includes(typeof value.value) ? { value: value.value as string | number | boolean | null } : {}),
    ...(Array.isArray(value.children) ? { children: value.children.map((child: unknown, index: number) => { if (!record(child)) throw new Error("Object graph child must be an object"); return graphNode(child, `${id}/${index}`); }) } : {}),
  };
}
