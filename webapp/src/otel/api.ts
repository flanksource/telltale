import { fetchJSON } from "@flanksource/clicky-ui/profiles";
import { otelSearchParams as searchParams } from "@flanksource/clicky-ui/data";
import type { OtelSearchValues as SearchValues, OtelSearchResult as SearchResult, OtelTrace as Trace, OtelSource as Source, OtelSearchForm as SearchForm } from "@flanksource/clicky-ui/data";
export { otelSearchParams as searchParams, readOtelSearchValues as readSearchValues } from "@flanksource/clicky-ui/data";
export type { OtelSearchValues as SearchValues, OtelSearchResult as SearchResult, OtelTrace as Trace, OtelSource as Source, OtelSearchForm as SearchForm } from "@flanksource/clicky-ui/data";

export const fetchSources = () => fetchJSON<Source[]>("/api/otel/sources");
export const fetchForm = () => fetchJSON<SearchForm>("/api/otel/form");
export const fetchSpanRuns = () => fetchJSON<{ runs: { runId: string }[] }>("/api/traces?kind=spans").then(({ runs }) => runs.map(run => run.runId));
export const fetchTraces = (value: SearchValues, signal?: AbortSignal) => fetchJSON<SearchResult>(`/api/otel?${searchParams(value)}`, signal ? { signal } : undefined);
export const fetchTrace = (value: SearchValues, id: string) => fetchJSON<Trace>(`/api/otel/trace/${encodeURIComponent(id)}?${searchParams({source:value.source,run:value.run,index:value.index,format:value.format})}`);
