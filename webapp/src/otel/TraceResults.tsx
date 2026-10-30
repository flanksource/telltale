import { Badge, DataTable, type DataTableColumn } from "@flanksource/clicky-ui/data";
import { duration, type SearchResult, type Summary } from "./api";
import { shortOperation } from "./query";

const startMs = (trace: Summary) => Date.parse(trace.startedAt);
const clock = (ms: number) => new Date(ms).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });

/** How long ago, in the largest whole unit up to days. */
export function ago(ms: number): string {
  const units: [number, string][] = [[86_400_000, "d"], [3_600_000, "h"], [60_000, "m"]];
  const [size, unit] = units.find(([size]) => ms >= size) ?? [1000, "s"];
  return `${Math.max(0, Math.round(ms / size))}${unit} ago`;
}

/** A round axis step that splits total into about ticks steps. */
export function niceStep(total: number, ticks: number): number {
  const raw = total / ticks;
  const power = 10 ** Math.floor(Math.log10(raw));
  return [1, 2, 5, 10].map(multiple => multiple * power).find(step => step >= raw)!;
}

export function ResultSummary({ result }: { result: SearchResult }) {
  const shown = result.traces.length;
  return (
    <p className="text-sm text-muted-foreground">
      <span className="font-medium text-foreground">{shown}</span> {shown === 1 ? "trace" : "traces"}
      {shown < result.total
        ? <> · the {shown} newest of {result.total.toLocaleString()}. Narrow the window or operation to see older ones</>
        : <> · all in the window</>}
    </p>
  );
}

/** Each trace by when it started and how long it took, dot size by span count. */
export function Scatter({ traces, onOpen }: { traces: Summary[]; onOpen: (id: string) => void }) {
  const starts = traces.map(startMs);
  const t0 = Math.min(...starts);
  const span = Math.max(...starts) - t0;
  const slowest = Math.max(...traces.map(trace => trace.durationMs)) || 1;
  const step = niceStep(slowest, 4);
  const top = Math.ceil((slowest * 1.05) / step) * step;
  const yTicks = Array.from({ length: Math.round(top / step) + 1 }, (_, i) => i * step);
  // Traces that all started together sit mid-axis under one tick.
  const xTicks = span === 0 ? [0.5] : [0, 0.25, 0.5, 0.75, 1];
  const x = (ms: number) => (span === 0 ? 0.5 : (ms - t0) / span);
  return (
    <figure className="rounded-lg border border-border p-density-2" aria-label="Duration over time">
      <figcaption className="mb-1 flex items-center gap-3 text-xs text-muted-foreground">
        <span className="font-medium text-foreground">Duration over time</span>
        <span>dot size = span count · click to open</span>
      </figcaption>
      <div className="relative ml-[4.5rem] mr-2 mt-3.5 h-[180px]">
        {yTicks.map(y => (
          <div key={y} className="absolute inset-x-0 border-t border-border/60" style={{ bottom: `${(y / top) * 100}%` }}>
            <span className="absolute right-full -translate-y-1/2 whitespace-nowrap pr-1.5 text-xs tabular-nums text-muted-foreground">{y === 0 ? "0" : duration(y)}</span>
          </div>
        ))}
        {traces.map(trace => {
          const size = 6 + Math.log2(trace.spanCount) * 1.6;
          const label = `${shortOperation(trace.operation)}, ${duration(trace.durationMs)}, ${trace.spanCount} spans at ${clock(startMs(trace))}`;
          return (
            <button
              key={trace.traceId}
              type="button"
              aria-label={label}
              title={label}
              onClick={() => onOpen(trace.traceId)}
              className={`absolute -translate-x-1/2 translate-y-1/2 rounded-full opacity-75 hover:ring-2 hover:ring-ring ${trace.errors > 0 ? "bg-destructive" : trace.spanCount > 1 ? "bg-primary" : "bg-muted-foreground"}`}
              style={{ left: `${x(startMs(trace)) * 100}%`, bottom: `${(trace.durationMs / top) * 100}%`, width: size, height: size }}
            />
          );
        })}
      </div>
      <div className="relative ml-[4.5rem] mr-2 mt-1 h-4 text-xs tabular-nums text-muted-foreground">
        {xTicks.map((fraction, i) => (
          <span key={fraction} className={`absolute whitespace-nowrap ${xTicks.length > 1 && i === 0 ? "" : xTicks.length > 1 && i === xTicks.length - 1 ? "-translate-x-full" : "-translate-x-1/2"}`} style={{ left: `${fraction * 100}%` }}>
            {clock(t0 + fraction * span)}
          </span>
        ))}
      </div>
    </figure>
  );
}

/** The traces, newest first: what ran, when, how long against the slowest, and its span count. */
export function ResultTable({ traces, href, onOpen, now }: { traces: Summary[]; href: (id: string) => string; onOpen: (id: string) => void; now: number }) {
  const slowest = Math.max(1, ...traces.map(trace => trace.durationMs));
  const columns: DataTableColumn<Summary>[] = [
    {
      key: "operation",
      label: "Trace",
      grow: true,
      minWidth: 320,
      render: (_, trace) => (
        <span className="flex min-w-0 items-center gap-2" title={trace.operation}>
          <span className="shrink-0 font-medium">{shortOperation(trace.operation) || trace.traceId}</span>
          <code className="text-xs text-muted-foreground">{trace.traceId.slice(0, 8)}</code>
          {trace.services.length > 1 && <span className="truncate text-xs text-muted-foreground">{trace.services.join(" → ")}</span>}
          {trace.errors > 0 && <Badge size="sm" tone="danger" variant="soft">{trace.errors} {trace.errors === 1 ? "error" : "errors"}</Badge>}
        </span>
      ),
    },
    {
      key: "startedAt",
      label: "Started",
      render: (_, trace) => <span className="whitespace-nowrap tabular-nums">{clock(startMs(trace))} <span className="text-muted-foreground">{ago(now - startMs(trace))}</span></span>,
    },
    {
      key: "durationMs",
      label: "Duration",
      render: (_, trace) => (
        <span className="flex items-center gap-2">
          <span className="relative h-1.5 w-24 rounded-full bg-muted">
            <span className="absolute inset-y-0 left-0 rounded-full bg-primary" style={{ width: `${(trace.durationMs / slowest) * 100}%` }} />
          </span>
          <span className="tabular-nums">{duration(trace.durationMs)}</span>
        </span>
      ),
    },
    { key: "spanCount", label: "Spans", align: "right" },
  ];
  return (
    <div className="flex h-[28rem] min-h-0 flex-col">
      <DataTable
        data={traces}
        columns={columns}
        getRowId={trace => trace.traceId}
        autoFilter={false}
        getRowHref={trace => href(trace.traceId)}
        onRowClick={trace => onOpen(trace.traceId)}
        emptyMessage="No traces match. Widen the time range, clear the duration or attribute filters, or show single-span traces."
      />
    </div>
  );
}
