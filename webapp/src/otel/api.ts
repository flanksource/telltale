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
/** The traces of one root operation in the window (tracequery.OperationStats). */
export interface OperationStats {
  operation: string;
  count: number;
  singleSpan?: boolean;
  p50Ms: number;
  p95Ms: number;
}
export interface SearchResult {
  traces: Summary[];
  total: number;
  services: string[];
  /** Every filter but the operation and span-count ones applied, by root operation. */
  rootOperations: OperationStats[];
  /** key=value suggestions from those traces' root spans, most frequent first. */
  attributes: { value: string; count: number }[];
}
export interface Source {
  id: string;
  name: string;
  type: string;
}
/** A filter's values, each included or excluded (tracequery.Modes). */
export type Modes = Record<string, "include" | "exclude">;
export const MODE_FILTERS = ["services", "operations", "statuses", "attributes"] as const;
export type ModeFilter = (typeof MODE_FILTERS)[number];
export type SearchValues = Record<string, unknown> & {
  source?: string;
  run?: string;
  index?: string;
  format?: string;
  services?: Modes;
  /** Root operations. */
  operations?: Modes;
  statuses?: Modes;
  /** key, key=value or key=prefix* expressions. */
  attributes?: Modes;
  minSpans?: number;
  traceId?: string;
  lookback?: string;
  from?: string;
  until?: string;
  minDuration?: string;
  maxDuration?: string;
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
    if (
      item !== undefined && item !== null && item !== "" &&
      !(typeof item === "object" && Object.keys(item).length === 0)
    )
      params.set(
        key,
        typeof item === "object" ? JSON.stringify(item) : String(item),
      );
  return params;
}

function parseModes(key: string, text: string): Modes {
  const value: unknown = JSON.parse(text);
  if (!value || typeof value !== "object" || Array.isArray(value) || Object.values(value).some(mode => mode !== "include" && mode !== "exclude"))
    throw new Error(`${key} must be a JSON object of values to include or exclude`);
  return value as Modes;
}

export function readSearchValues(): SearchValues {
  const params = new URLSearchParams(window.location.search);
  const result: SearchValues = {};
  for (const [key, value] of params) {
    if (key === "span") continue;
    if ((MODE_FILTERS as readonly string[]).includes(key)) result[key] = parseModes(key, value);
    else if (key === "limit" || key === "minSpans") {
      if (!/^\d+$/.test(value)) throw new Error(`${key} must be a whole number, not ${JSON.stringify(value)}`);
      result[key] = Number(value);
    } else result[key] = value;
  }
  return result;
}

export const fetchTraces = (value: SearchValues, signal?: AbortSignal) =>
  fetchJSON<SearchResult>(`/api/otel?${searchParams(value)}`, signal ? { signal } : undefined);
export const fetchTrace = (value: SearchValues, id: string) =>
  fetchJSON<Trace>(
    `/api/otel/trace/${encodeURIComponent(id)}?${searchParams({ source: value.source, run: value.run, index: value.index, format: value.format })}`,
  );
export const duration = (ms: number) =>
  ms >= 1000
    ? `${(ms / 1000).toFixed(2)} s`
    : ms >= 10
      ? `${ms.toFixed(0)} ms`
      : ms >= 1
        ? `${ms.toFixed(1)} ms`
        : `${(ms * 1000).toFixed(0)} µs`;
