import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Button, Panel } from "@flanksource/clicky-ui/components";
import { DataTable, type DataTableColumn } from "@flanksource/clicky-ui/data";
import { useRouter } from "@flanksource/clicky-ui/rpc";
import {
  duration,
  fetchForm,
  fetchSources,
  fetchSpanRuns,
  fetchTrace,
  fetchTraces,
  readSearchValues,
  searchParams,
  type SearchValues,
  type Summary,
  type SearchForm as Form,
  type Source,
} from "./api";
import { SearchForm } from "./SearchForm";
import { TraceDetail } from "./TraceDetail";

function TraceSearch({
  form,
  sources,
  traceId,
  initial,
}: {
  form: Form;
  sources: Source[];
  traceId?: string;
  initial: SearchValues;
}) {
  const { navigate, renderLink } = useRouter();
  const [value, setValue] = useState<SearchValues>(initial);
  const [request, setRequest] = useState<SearchValues | null>(() =>
    window.location.search ? value : null,
  );
  const runs = useQuery({
    queryKey: ["otel-span-runs"],
    queryFn: fetchSpanRuns,
    enabled: !traceId,
    retry: false,
  });
  const search = useQuery({
    queryKey: ["otel-search", request],
    queryFn: () => fetchTraces(request!),
    enabled: request !== null && !traceId,
    retry: false,
  });
  const detail = useQuery({
    queryKey: [
      "otel-trace",
      value.source,
      value.run,
      value.index,
      value.format,
      traceId,
    ],
    queryFn: () => fetchTrace(value, traceId!),
    enabled: !!traceId,
    retry: false,
  });
  const open = (id: string) =>
    `/otel/${encodeURIComponent(id)}?${searchParams(request ?? value)}`;
  const columns: DataTableColumn<Summary>[] = [
    {
      key: "operation",
      label: "Trace",
      render: (_, row) => row.operation || row.traceId,
    },
    { key: "service", label: "Root service" },
    { key: "startedAt", label: "Started", kind: "timestamp" },
    {
      key: "durationMs",
      label: "Duration",
      render: (_, row) => duration(row.durationMs),
    },
    { key: "spanCount", label: "Spans" },
    { key: "errors", label: "Errors" },
    {
      key: "services",
      label: "Services",
      render: (_, row) => row.services.join(", "),
    },
  ];
  if (traceId)
    return (
      <div className="space-y-4 min-w-0">
        {renderLink({
          to: `/otel?${searchParams(value)}`,
          children: "Back to trace search",
          className: "text-sm underline",
        })}
        {detail.isPending && <p role="status">Loading trace…</p>}
        {detail.error && <p role="alert">{detail.error.message}</p>}
        {detail.data && <TraceDetail trace={detail.data} />}
      </div>
    );
  return (
    <div className="grid min-w-0 items-start gap-6 xl:grid-cols-[20rem_minmax(0,1fr)]">
      <Panel title="Search traces">
        <SearchForm
          form={form}
          sources={sources}
          runs={runs.data ?? []}
          value={value}
          onChange={setValue}
          pending={search.isFetching}
          services={search.data?.services ?? []}
          operations={search.data?.operations ?? []}
          onSearch={() => {
            setRequest({ ...value });
            navigate(`/otel?${searchParams(value)}`, { replace: true });
          }}
        />
        {runs.error && (
          <p role="alert">Could not list runs: {runs.error.message}</p>
        )}
      </Panel>
      <div className="space-y-4 min-w-0">
        {search.isFetching && <p role="status">Searching traces…</p>}
        {search.error && (
          <div className="space-y-2">
            <p role="alert">{search.error.message}</p>
            <Button variant="outline" onClick={() => void search.refetch()}>
              Retry
            </Button>
          </div>
        )}
        {!request && (
          <p className="text-sm text-muted-foreground">
            Choose a source and find traces. Recorded traces include SDK spans,
            OpenSearch imports and, as the receiver run, spans telltale serve
            --otlp received.
          </p>
        )}
        {search.data && (
          <>
            <p className="text-sm text-muted-foreground">
              Showing {search.data.traces.length} of {search.data.total} traces
            </p>
            <DataTable
              data={search.data.traces}
              columns={columns}
              getRowId={(row) => row.traceId}
              autoFilter={false}
              getRowHref={(row) => open(row.traceId)}
              onRowClick={(row) => navigate(open(row.traceId))}
            />
            {search.data.traces.length === 0 && (
              <p>
                No traces match these filters. Change the source, time window,
                or span filters and search again.
              </p>
            )}
          </>
        )}
      </div>
    </div>
  );
}

export default function OtelPage({ traceId }: { traceId?: string }) {
  const { renderLink } = useRouter();
  const form = useQuery({
    queryKey: ["otel-form"],
    queryFn: fetchForm,
    retry: false,
    staleTime: Infinity,
  });
  const sources = useQuery({
    queryKey: ["otel-sources"],
    queryFn: fetchSources,
    retry: false,
  });
  if (form.error || sources.error)
    return <p role="alert">{form.error?.message ?? sources.error?.message}</p>;
  if (!form.data || !sources.data)
    return <p role="status">Loading trace search…</p>;
  let initial: SearchValues;
  try {
    initial = { ...form.data.defaults, ...readSearchValues() };
  } catch (error) {
    return (
      <div className="space-y-3">
        <p role="alert">
          Invalid trace search URL:{" "}
          {error instanceof Error ? error.message : String(error)}
        </p>
        {renderLink({
          to: "/otel",
          children: "Clear search",
          className: "underline",
        })}
      </div>
    );
  }
  return (
    <div className="space-y-4 min-w-0">
      <h1 className="text-2xl font-semibold">OpenTelemetry traces</h1>
      <TraceSearch
        key={`${traceId ?? "search"}:${window.location.search}`}
        form={form.data}
        sources={sources.data}
        traceId={traceId}
        initial={initial}
      />
    </div>
  );
}
