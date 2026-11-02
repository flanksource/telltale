import type { DataTableColumnFilter, DataTableServerColumn, QueryBrowserRequest, QueryBrowserResult } from "@flanksource/clicky-ui/data";
import { failure, traceRecordsUrl, type TraceRef } from "./api";

export type RecordProfileColumn = Omit<DataTableServerColumn, "filter"> & {
  filter?: Omit<DataTableColumnFilter, "kind"> & { key: string; kind: DataTableColumnFilter["kind"] | "match" };
};

export function recordQueryColumns(columns: RecordProfileColumn[]): DataTableServerColumn[] {
  return columns.map(({ filter, ...column }) => {
    if (!filter) return column;
    if (!filter.key) throw new Error(`Profile column ${column.name} is missing its filter key`);
    return { ...column, filterKey: filter.key, filter: { ...filter, kind: filter.kind === "match" ? "terms" : filter.kind } };
  });
}

export async function executeRecordQuery({ mount, ref, columns, request }: {
  mount: string; ref: TraceRef; columns: DataTableServerColumn[]; request: QueryBrowserRequest;
}): Promise<QueryBrowserResult> {
  const params: Record<string, string> = { q: request.query, limit: String(request.pagination?.limit ?? 100) };
  if (request.pagination?.cursor) params.cursor = request.pagination.cursor;
  for (const [key, value] of Object.entries(request.options)) {
    if (typeof value !== "string") throw new Error(`Record option ${key} must be a string`);
    if (value) params[key] = value;
  }
  const url = new URL(traceRecordsUrl(mount, ref, params), window.location.origin);
  for (const [key, value] of Object.entries(request.filters ?? {})) {
    if (!key.startsWith("filter.")) throw new Error(`Unknown record filter ${key}`);
    if (value) url.searchParams.append("filter", `${key.slice(7)}=${value}`);
  }
  const response = await fetch(url.pathname + url.search, { headers: { Accept: "application/json" } });
  if (!response.ok) throw await failure(response, "Query records");
  const page = await response.json() as { rows: Record<string, unknown>[]; nextCursor: string };
  return {
    rows: page.rows, columns,
    pagination: { mode: "cursor", limit: Number(params.limit), hasMore: !!page.nextCursor, nextCursor: page.nextCursor, totalRelation: "unknown", consistency: "live" },
  };
}
