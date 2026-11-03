import { useEffect, useState } from "react";
import { keepPreviousData, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRouter } from "@flanksource/clicky-ui/rpc";
import {
  fetchForm,
  fetchSources,
  fetchSpanRuns,
  fetchTrace,
  fetchTraces,
  readSearchValues,
  searchParams,
  type SearchValues,
  type Source,
} from "./api";
import { OtelTraceSearch, OtelTraceDetail as TraceDetail } from "@flanksource/clicky-ui/data";
import { otelDurationOf as durationOf } from "@flanksource/clicky-ui/data";

function TraceSearch({ sources, traceId, initial }: { sources: Source[]; traceId?: string; initial: SearchValues }) {
  const { navigate, renderLink } = useRouter();
  const client = useQueryClient();
  const [value, setValue] = useState<SearchValues>(initial);
  const [selectedSpan, setSelectedSpan] = useState(() => new URLSearchParams(window.location.search).get("span"));
  useEffect(() => {
    const restore = () => { setSelectedSpan(new URLSearchParams(window.location.search).get("span")); setValue({ ...initial, ...readSearchValues() }); };
    window.addEventListener("popstate", restore);
    return () => window.removeEventListener("popstate", restore);
  }, [initial]);
  const runs = useQuery({ queryKey: ["otel-span-runs"], queryFn: fetchSpanRuns, enabled: !traceId, retry: false });
  const search = useQuery({
    queryKey: ["otel-search", value],
    queryFn: ({ signal }) => fetchTraces(value, signal),
    enabled: !traceId,
    retry: false,
    placeholderData: keepPreviousData,
  });
  const detail = useQuery({
    queryKey: ["otel-trace", value.source, value.run, value.index, value.format, traceId],
    queryFn: () => fetchTrace(value, traceId!),
    enabled: !!traceId,
    retry: false,
  });
  const update = (next: Partial<SearchValues>) => {
    const merged = { ...value, ...next };
    setValue(merged);
    navigate(`/otel?${searchParams(merged)}`, { replace: true });
  };
  const href = (id: string) => `/otel/${encodeURIComponent(id)}?${searchParams(value)}`;
  const open = (id: string) => navigate(href(id));

  if (traceId)
    return (
      <div className="min-w-0 space-y-4">
        {renderLink({ to: `/otel?${searchParams(value)}`, children: "Back to trace search", className: "text-sm underline" })}
        {detail.isPending && <p role="status">Loading trace…</p>}
        {detail.error && <p role="alert">{detail.error.message}</p>}
        {detail.data && <TraceDetail trace={detail.data} {...(selectedSpan ? { selectedSpanId: selectedSpan } : {})} onSelectSpan={id => { setSelectedSpan(id); const params = new URLSearchParams(window.location.search); params.set("span", id); navigate(`/otel/${encodeURIComponent(traceId)}?${params}`, { replace: true }); }} />}
      </div>
    );
  return <OtelTraceSearch value={value} sources={sources} runs={runs.data ?? []} {...(search.data ? { result: search.data } : {})} loading={search.isFetching} {...(search.error ? { error: search.error.message } : {})} onChange={update} onOpen={open} href={href} onCancel={() => void client.cancelQueries({ queryKey: ["otel-search", value] })} onRetry={() => void search.refetch()} />;
}

export default function OtelPage({ traceId }: { traceId?: string }) {
  const { renderLink } = useRouter();
  const form = useQuery({ queryKey: ["otel-form"], queryFn: fetchForm, retry: false, staleTime: Infinity });
  const sources = useQuery({ queryKey: ["otel-sources"], queryFn: fetchSources, retry: false });
  if (form.error || sources.error) return <p role="alert">{form.error?.message ?? sources.error?.message}</p>;
  if (!form.data || !sources.data) return <p role="status">Loading trace search…</p>;
  let initial: SearchValues;
  try {
    initial = { ...form.data.defaults, ...readSearchValues() };
    durationOf(initial);
  } catch (error) {
    return (
      <div className="space-y-3">
        <p role="alert">Invalid trace search URL: {error instanceof Error ? error.message : String(error)}</p>
        {renderLink({ to: "/otel", children: "Clear search", className: "underline" })}
      </div>
    );
  }
  return (
    <div className="min-w-0 space-y-4">
      <h1 className="text-2xl font-semibold">OpenTelemetry traces</h1>
      <TraceSearch key={traceId ?? "search"} sources={sources.data} traceId={traceId} initial={initial} />
    </div>
  );
}
