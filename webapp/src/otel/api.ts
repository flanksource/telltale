import type { JsonSchemaObject } from "@flanksource/clicky-ui/components";
import { fetchJSON } from "@flanksource/clicky-ui/profiles";

export type Span = {
  traceId: string;
  spanId: string;
  parentId?: string;
  service: string;
  operation: string;
  kind?: string;
  startedAt: string;
  durationMs: number;
  status: string;
  attributes?: Record<string, unknown>;
  resource?: Record<string, unknown>;
  otlp?: Record<string, unknown>;
};
export type Summary = {
  traceId: string;
  service: string;
  operation: string;
  startedAt: string;
  durationMs: number;
  spanCount: number;
  errors: number;
  services: string[];
};
export interface Trace {
  summary: Summary;
  spans: Span[];
  missingParents: string[];
}
export interface SearchResult {
  traces: Summary[];
  total: number;
  services: string[];
  operations: string[];
}
export interface Source {
  id: string;
  name: string;
  type: string;
}
export type SearchValues = Record<string, unknown> & {
  source?: string;
  run?: string;
  index?: string;
  format?: string;
  service?: string;
  operation?: string;
  traceId?: string;
  lookback?: string;
  from?: string;
  until?: string;
  minDuration?: string;
  maxDuration?: string;
  status?: string;
  tags?: Record<string, string>;
  limit?: number;
};
export interface SearchForm {
  schema: JsonSchemaObject;
  defaults: SearchValues;
}
export const fetchSources = () => fetchJSON<Source[]>("/api/otel/sources");
export const fetchForm = () => fetchJSON<SearchForm>("/api/otel/form");
// fetchSpanRuns are the runs a recorded-trace search can select: command runs
// that captured spans, then standing stores such as the OTLP receiver.
export const fetchSpanRuns = () =>
  fetchJSON<{ runs: { runId: string }[] }>("/api/traces?kind=spans").then(
    ({ runs }) => runs.map((run) => run.runId),
  );

export function searchParams(value: SearchValues): URLSearchParams {
  const params = new URLSearchParams();
  for (const [key, item] of Object.entries(value))
    if (item !== undefined && item !== null && item !== "")
      params.set(
        key,
        typeof item === "object" ? JSON.stringify(item) : String(item),
      );
  return params;
}

export function readSearchValues(): SearchValues {
  const params = new URLSearchParams(window.location.search);
  const result: SearchValues = {};
  for (const [key, value] of params) {
    if (key === "span") continue;
    if (key === "tags")
      result.tags = JSON.parse(value) as Record<string, string>;
    else if (key === "limit") result.limit = Number(value);
    else result[key] = value;
  }
  return result;
}

export const fetchTraces = (value: SearchValues) =>
  fetchJSON<SearchResult>(`/api/otel?${searchParams(value)}`);
export const fetchTrace = (value: SearchValues, id: string) =>
  fetchJSON<Trace>(
    `/api/otel/trace/${encodeURIComponent(id)}?${searchParams({ source: value.source, run: value.run, index: value.index, format: value.format })}`,
  );
export const duration = (ms: number) =>
  ms >= 1000
    ? `${(ms / 1000).toFixed(2)} s`
    : `${ms.toFixed(ms < 1 ? 3 : 1)} ms`;
