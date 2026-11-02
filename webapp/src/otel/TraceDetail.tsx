import { useMemo, useState } from "react";
import { Button, Panel, Section } from "@flanksource/clicky-ui/components";
import {
  CodeBlock,
  DataTable,
  Properties,
  type DataTableColumn,
} from "@flanksource/clicky-ui/data";
import { duration, type Trace } from "./api";
import { spanRows, type SpanRow } from "./model";

export function TraceDetail({ trace }: { trace: Trace }) {
  const [collapsed, setCollapsed] = useState(new Set<string>());
  const [selected, setSelected] = useState(
    () =>
      new URLSearchParams(window.location.search).get("span") ??
      trace.spans[0]?.spanId,
  );
  const selectSpan = (id: string) => {
    setSelected(id);
    const params = new URLSearchParams(window.location.search);
    params.set("span", id);
    window.history.replaceState(
      null,
      "",
      `${window.location.pathname}?${params}`,
    );
  };
  const rows = useMemo(
    () => spanRows(trace.spans, collapsed),
    [trace.spans, collapsed],
  );
  const span = trace.spans.find((span) => span.spanId === selected);
  const start = Date.parse(trace.summary.startedAt);
  const total = trace.summary.durationMs;
  const columns: DataTableColumn<SpanRow>[] = [
    {
      key: "operation",
      label: "Service / operation",
      sortable: false,
      render: (_, row) => (
        <div
          className="flex items-center gap-1"
          style={{ paddingLeft: `${row.depth * 16}px` }}
        >
          {row.hasChildren ? (
            <Button
              variant="ghost"
              size="sm"
              aria-label={`${collapsed.has(row.spanId) ? "Expand" : "Collapse"} ${row.operation}`}
              onClick={() =>
                setCollapsed((previous) => {
                  const next = new Set(previous);
                  if (next.has(row.spanId)) next.delete(row.spanId);
                  else next.add(row.spanId);
                  return next;
                })
              }
            >
              {collapsed.has(row.spanId) ? "+" : "−"}
            </Button>
          ) : (
            <span className="inline-block w-7" />
          )}
          <button
            type="button"
            className="text-left hover:underline"
            aria-label={`Inspect ${row.operation}`}
            onClick={() => selectSpan(row.spanId)}
          >
            <span className="block text-xs text-muted-foreground">
              {row.service || "Unknown service"}
            </span>
            {row.operation || "Unnamed span"}
          </button>
        </div>
      ),
    },
    {
      key: "durationMs",
      label: "Duration",
      sortable: false,
      render: (_, row) => duration(row.durationMs),
    },
    { key: "status", label: "Status", sortable: false },
    {
      key: "timeline",
      label: `Timeline · ${duration(total)}`,
      sortable: false,
      render: (_, row) => {
        const left = total
          ? Math.max(0, ((Date.parse(row.startedAt) - start) / total) * 100)
          : 0;
        const width = total ? (row.durationMs / total) * 100 : 0;
        return (
          <button
            type="button"
            aria-label={`Inspect ${row.operation} at ${duration(Date.parse(row.startedAt) - start)}`}
            className="relative block h-7 w-full min-w-64 rounded bg-muted"
            onClick={() => selectSpan(row.spanId)}
            title={`${row.operation}: ${duration(row.durationMs)}`}
          >
            <span
              className={`absolute inset-y-1 rounded ${row.status === "error" ? "bg-destructive" : "bg-primary"}`}
              style={{
                left: `${left}%`,
                width: `${Math.min(width, 100 - left)}%`,
                minWidth: 2,
              }}
            />
          </button>
        );
      },
    },
  ];
  return (
    <div className="space-y-4 min-w-0">
      <Panel title={trace.summary.operation || "Trace"}>
        <p className="break-all font-mono text-sm">{trace.summary.traceId}</p>
        <p className="mt-2 text-sm text-muted-foreground">
          {duration(total)} · {trace.spans.length} spans ·{" "}
          {trace.summary.services.length} services · {trace.summary.errors}{" "}
          errors · {new Date(trace.summary.startedAt).toLocaleString()}
        </p>
      </Panel>
      {trace.missingParents.length > 0 && (
        <p role="status" className="text-sm text-muted-foreground">
          Partial trace: {trace.missingParents.length} parent spans are missing
          from this source.
        </p>
      )}
      <div className="flex gap-2">
        <Button
          variant="outline"
          size="sm"
          onClick={() => setCollapsed(new Set())}
        >
          Expand all
        </Button>
        <Button
          variant="outline"
          size="sm"
          onClick={() =>
            setCollapsed(new Set(trace.spans.map((span) => span.spanId)))
          }
        >
          Collapse all
        </Button>
      </div>
      <DataTable
        data={rows}
        columns={columns}
        getRowId={(row) => row.spanId}
        autoFilter={false}
        onRowClick={(row) => selectSpan(row.spanId)}
        className="min-h-48"
      />
      {span && (
        <Panel title={`Span: ${span.operation || span.spanId}`}>
          <div className="space-y-4">
            <Properties
              items={[
                { key: "Span ID", value: span.spanId },
                { key: "Parent ID", value: span.parentId || "Root span" },
                { key: "Service", value: span.service },
                { key: "Kind", value: span.kind },
                { key: "Status", value: span.status },
                { key: "Duration", value: duration(span.durationMs) },
                { key: "Started", value: span.startedAt },
              ]}
            />
            <h3 className="text-sm font-semibold">Span attributes</h3>
            <Properties
              renderLabel={(key) => key}
              items={Object.entries(span.attributes ?? {}).map(
                ([key, value]) => ({ key, value }),
              )}
            />
            <h3 className="text-sm font-semibold">Resource attributes</h3>
            <Properties
              renderLabel={(key) => key}
              items={Object.entries(span.resource ?? {}).map(
                ([key, value]) => ({ key, value }),
              )}
            />
            {span.otlp && (
              <Section title="OpenTelemetry metadata" defaultOpen={false}>
                <CodeBlock
                  language="json"
                  source={JSON.stringify(span.otlp, null, 2)}
                  jsonDefaultOpenDepth={4}
                  copyable
                />
              </Section>
            )}
          </div>
        </Panel>
      )}
    </div>
  );
}
