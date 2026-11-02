import { useEffect, useState, type FormEvent } from "react";
import { keepPreviousData, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button, SearchInput } from "@flanksource/clicky-ui/components";
import { useRouter } from "@flanksource/clicky-ui/rpc";
import {
  fetchForm,
  fetchSources,
  fetchSpanRuns,
  fetchTrace,
  fetchTraces,
  readSearchValues,
  searchParams,
  type Modes,
  type SearchValues,
  type Source,
} from "./api";
import { OperationList } from "./OperationList";
import { QueryBar } from "./QueryBar";
import { durationOf, isTraceId, MULTI_SPAN } from "./query";
import { TraceDetail } from "./TraceDetail";
import { ResultSummary, ResultTable, Scatter } from "./TraceResults";

/** The one root operation included, "" for none, undefined for any other selection. */
function singleOperation(operations: Modes = {}): string | undefined {
  const keys = Object.keys(operations);
  if (keys.length === 0) return "";
  return keys.length === 1 && operations[keys[0]!] === "include" ? keys[0] : undefined;
}

function PendingBanner({ since, onCancel }: { since: number; onCancel: () => void }) {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 100);
    return () => clearInterval(timer);
  }, []);
  return (
    <div role="status" className="absolute inset-x-0 top-0 z-10 flex justify-center">
      <span className="flex items-center gap-3 rounded-full border border-border bg-background px-density-3 py-1 text-xs shadow-sm">
        Searching… <span className="tabular-nums text-muted-foreground">{((now - since) / 1000).toFixed(1)} s</span>
        <Button size="sm" variant="ghost" onClick={onCancel}>Cancel</Button>
      </span>
    </div>
  );
}

/** When the search in flight started, while one is. */
function useFetchingSince(fetching: boolean): number | undefined {
  const [since, setSince] = useState<number>();
  useEffect(() => setSince(fetching ? Date.now() : undefined), [fetching]);
  return fetching ? since ?? Date.now() : undefined;
}

function TraceSearch({ sources, traceId, initial }: { sources: Source[]; traceId?: string; initial: SearchValues }) {
  const { navigate, renderLink } = useRouter();
  const client = useQueryClient();
  const [value, setValue] = useState<SearchValues>(initial);
  const [notice, setNotice] = useState<string>();
  const [jumpId, setJumpId] = useState("");
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
  const since = useFetchingSince(search.isFetching);
  const update = (next: Partial<SearchValues>) => {
    const merged = { ...value, ...next };
    setValue(merged);
    setNotice(undefined);
    navigate(`/otel?${searchParams(merged)}`, { replace: true });
  };
  const href = (id: string) => `/otel/${encodeURIComponent(id)}?${searchParams(value)}`;
  const open = (id: string) => navigate(href(id));
  const jump = (event: FormEvent) => {
    event.preventDefault();
    if (!isTraceId(jumpId)) return setNotice("A trace ID is 16 or 32 hex characters.");
    open(jumpId.trim().toLowerCase());
  };

  if (traceId)
    return (
      <div className="min-w-0 space-y-4">
        {renderLink({ to: `/otel?${searchParams(value)}`, children: "Back to trace search", className: "text-sm underline" })}
        {detail.isPending && <p role="status">Loading trace…</p>}
        {detail.error && <p role="alert">{detail.error.message}</p>}
        {detail.data && <TraceDetail trace={detail.data} />}
      </div>
    );
  const result = search.data;
  const cancelled = search.isPlaceholderData && !search.isFetching;
  return (
    <section className="min-w-0 space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-xl font-semibold tracking-tight">Traces</h2>
        <form onSubmit={jump} className="flex items-center gap-2">
          <SearchInput className="w-80" shortcut={null} placeholder="Jump to trace ID" aria-label="Trace ID" value={jumpId} onChange={setJumpId} />
          <Button type="submit" size="sm" variant="outline">Open</Button>
        </form>
      </div>
      <QueryBar value={value} onChange={update} onError={setNotice} sources={sources} runs={runs.data ?? []} {...(result ? { result } : {})} />
      {runs.error && <p role="alert">Could not list runs: {runs.error.message}</p>}
      {notice && <p role="alert" className="rounded-md border border-border bg-muted p-density-2 text-xs">{notice}</p>}
      {search.error && (
        <div className="space-y-2">
          <p role="alert">{search.error.message}</p>
          <Button variant="outline" onClick={() => void search.refetch()}>Retry</Button>
        </div>
      )}
      {cancelled && (
        <p role="status" className="flex items-center gap-2 text-sm text-muted-foreground">
          Search cancelled. These results are from the previous filters.
          <Button size="sm" variant="outline" onClick={() => void search.refetch()}>Search again</Button>
        </p>
      )}
      <div className="flex min-w-0 flex-wrap items-start gap-4">
        {result && (
          <div className="min-w-0 max-w-[26rem] flex-[1_1_20rem]">
            <OperationList
              stats={result.rootOperations}
              selected={singleOperation(value.operations)}
              onSelect={operation => update({
                operations: operation ? { [operation]: "include" } : {},
                // A single-span operation has nothing to show while single-span traces are hidden.
                ...(result.rootOperations.find(stat => stat.operation === operation)?.singleSpan ? { minSpans: undefined } : {}),
              })}
              hideSingle={(value.minSpans ?? 0) >= MULTI_SPAN}
            />
          </div>
        )}
        <div className="relative min-w-0 flex-[999_1_40rem] space-y-3">
          {since !== undefined && <PendingBanner since={since} onCancel={() => void client.cancelQueries({ queryKey: ["otel-search", value] })} />}
          {!result && search.isPending && <p role="status">Searching traces…</p>}
          {result && (
            <div className={`space-y-3 ${search.isFetching || cancelled ? "opacity-50" : ""}`}>
              <ResultSummary result={result} />
              {result.traces.length > 0 && <Scatter traces={result.traces} onOpen={open} />}
              <ResultTable traces={result.traces} href={href} onOpen={open} now={Date.now()} />
            </div>
          )}
        </div>
      </div>
    </section>
  );
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
