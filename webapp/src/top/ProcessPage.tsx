import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Button } from "@flanksource/clicky-ui/components";
import { LabeledSparkline, RadialGauge } from "@flanksource/clicky-ui/data";
import { useRouter } from "@flanksource/clicky-ui/rpc";
import { fetchTopProcesses, type HostUsage, type ProcessSample, type TopProcesses, type Usage } from "./api";
import { appendFrame, formatBytes, formatCount, formatPercent, formatRate, originText } from "./groups";
import { FacetsPanel } from "./FacetsPanel";
import { toneOf } from "./HostStrip";
import { isCPUHog } from "./cpu";
import { ProcessActions } from "./ProcessActions";
import { RefreshControl, useRefreshInterval } from "../monitoring/RefreshControl";
import {
  filterProcesses, metricSeries, metricValue, parseTarget, sortProcesses, sumProcesses, type Metric, type ProcessSortKey,
} from "./processes";

/** How many readings the page keeps for its trends. */
export const HISTORY_READINGS = 120;

interface MetricView {
  metric: Metric;
  label: string;
  format: (value: number | undefined) => string;
  /** The value that fills the metric's gauge, from the host where it has a scale, else the peak of the readings kept. */
  max: (host: HostUsage | undefined, peak: number) => number;
}

const PEAK = (_host: HostUsage | undefined, peak: number) => Math.max(peak, 1);

const METRIC_VIEWS: MetricView[] = [
  { metric: "cpu", label: "CPU %", format: formatPercent, max: (host, peak) => (host ? host.cores * 100 : Math.max(peak, 100)) },
  { metric: "rss", label: "RSS", format: formatBytes, max: (host, peak) => (host ? host.memoryTotalBytes : Math.max(peak, 1)) },
  { metric: "diskRead", label: "Disk read/s", format: formatRate, max: PEAK },
  { metric: "diskWrite", label: "Disk write/s", format: formatRate, max: PEAK },
  { metric: "netRx", label: "Net in/s", format: formatRate, max: PEAK },
  { metric: "netTx", label: "Net out/s", format: formatRate, max: PEAK },
  { metric: "sockets", label: "Sockets", format: formatCount, max: PEAK },
];

function MetricTile({ view, usage, host, series }: { view: MetricView; usage: Usage; host: HostUsage | undefined; series: number[] }) {
  const value = metricValue(usage, view.metric);
  const max = view.max(host, Math.max(0, ...series));
  const text = view.format(value);
  return (
    <div className="w-[15.5rem] max-w-full space-y-2 rounded-lg border border-border bg-card p-3" data-metric={view.metric}>
      <RadialGauge
        value={value ?? 0}
        max={max}
        size={44}
        thickness={5}
        tone={value === undefined ? "neutral" : view.metric === "cpu" || view.metric === "rss" ? toneOf((100 * value) / max) : "info"}
        title={`${view.label}: ${text}`}
        label={<span className="flex flex-col"><span className="text-xs text-muted-foreground">{view.label}</span><span className="text-lg font-semibold tabular-nums">{text}</span></span>}
      />
      <LabeledSparkline label={`${view.label} trend`} values={series} format={number => view.format(number)} />
    </div>
  );
}

function SortHeader({ label, active, onClick }: { label: string; active: boolean; onClick: () => void }) {
  return (
    <th scope="col" aria-sort={active ? "descending" : "none"} className="whitespace-nowrap px-2 py-1 text-right font-medium">
      <button type="button" className="font-medium hover:underline" onClick={onClick}>
        {label}
        <span aria-hidden>{active ? " ▼" : ""}</span>
      </button>
    </th>
  );
}

interface ProcessTableProps {
  cores: number | undefined;
  processes: ProcessSample[];
  sort: ProcessSortKey;
  onSort: (key: ProcessSortKey) => void;
  /** The process whose facets show, and choosing another. */
  selected: number | undefined;
  onSelect: (pid: number) => void;
}

function ProcessTable({ processes, sort, onSort, selected, onSelect, cores }: ProcessTableProps) {
  const { renderLink } = useRouter();
  const muted = "whitespace-nowrap px-2 py-1 text-muted-foreground";
  const number = "whitespace-nowrap px-2 py-1 text-right tabular-nums";
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm">
        <thead>
          <tr className="text-muted-foreground">
            <th scope="col" className="px-2 py-1 text-right font-medium">PID</th>
            <th scope="col" className="px-2 py-1 text-right font-medium">PPID</th>
            <th scope="col" className="px-2 py-1 text-left font-medium">Name</th>
            <th scope="col" className="px-2 py-1 text-left font-medium">Owner</th>
            <th scope="col" className="px-2 py-1 text-left font-medium">Scope</th>
            <th scope="col" className="px-2 py-1 text-left font-medium">Origin</th>
            <SortHeader label="CPU %" active={sort === "cpu"} onClick={() => onSort("cpu")} />
            <SortHeader label="RSS" active={sort === "rss"} onClick={() => onSort("rss")} />
            <th scope="col" className="px-2 py-1 text-right font-medium">Disk read/s</th>
            <th scope="col" className="px-2 py-1 text-right font-medium">Disk write/s</th>
            <th scope="col" className="px-2 py-1 text-right font-medium">Net in/s</th>
            <th scope="col" className="px-2 py-1 text-right font-medium">Net out/s</th>
            <th scope="col" className="px-2 py-1 text-right font-medium">Sockets</th>
            <th scope="col" className="px-2 py-1 text-left font-medium">Command</th>
            <th scope="col" className="px-2 py-1 text-left font-medium">Actions</th>
          </tr>
        </thead>
        <tbody>
          {processes.map(process => (
            <tr
              key={process.key}
              aria-selected={process.pid === selected}
              title="Show this process's environment, files and connections"
              className={`cursor-pointer border-t border-border align-top hover:bg-muted/40 ${isCPUHog(process.cpuPercent, cores) ? "bg-destructive/10" : process.pid === selected ? "bg-muted/60" : ""}`}
              onClick={() => onSelect(process.pid)}
            >
              <td className={number}>{renderLink({ to: `/top/${process.pid}`, className: "hover:underline", children: process.pid, title: `Process ${process.pid} and those under it` })}</td>
              <td className={number}>{process.ppid}</td>
              <td className="whitespace-nowrap px-2 py-1">{process.name}</td>
              <td className={muted}>{process.origin.owner ?? ""}</td>
              <td className={muted}>{process.origin.scope ?? ""}</td>
              <td className={muted}>{originText(process.origin)}</td>
              <td className={number}>{formatPercent(process.cpuPercent)}{isCPUHog(process.cpuPercent, cores) && <span className="ml-2 font-semibold text-destructive">CPU hog</span>}</td>
              <td className={number}>{formatBytes(process.rssBytes)}</td>
              <td className={number}>{formatRate(process.diskReadBps)}</td>
              <td className={number}>{formatRate(process.diskWriteBps)}</td>
              <td className={number}>{formatRate(process.netRxBps)}</td>
              <td className={number}>{formatRate(process.netTxBps)}</td>
              <td className={number}>{formatCount(process.sockets)}</td>
              <td className="px-2 py-1 text-muted-foreground"><span className="block max-w-[28rem] truncate" title={process.command}>{process.command}</span></td>
              <td className="px-2 py-1"><ProcessActions process={process} /></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

const input = "rounded border border-border bg-background px-2 py-1";

function ProcessView({ target }: { target: string }) {
  const interval = useRefreshInterval();
  const { renderLink } = useRouter();
  const parsed = useMemo(() => parseTarget(target), [target]);
  const query = useQuery({
    queryKey: ["top-processes", parsed],
    queryFn: ({ signal }) => fetchTopProcesses(parsed, signal),
    retry: false,
    // A refused reading is not polled again until Retry: the server would refuse it every second.
    refetchInterval: current => current.state.status === "error" ? false : interval,
  });
  const [history, setHistory] = useState<TopProcesses[]>([]);
  useEffect(() => {
    if (query.data) setHistory(previous => appendFrame(previous, query.data, HISTORY_READINGS));
  }, [query.data]);
  const [sort, setSort] = useState<ProcessSortKey>("cpu");
  const [q, setQ] = useState("");
  const [selected, setSelected] = useState<number | undefined>("pid" in parsed ? parsed.pid : undefined);
  const latest = query.data;
  const total = useMemo(() => latest && sumProcesses(latest.processes), [latest]);
  const series = useMemo(() => Object.fromEntries(METRIC_VIEWS.map(view => [view.metric, metricSeries(history, view.metric)])) as Record<Metric, number[]>, [history]);
  const shown = useMemo(() => latest ? sortProcesses(filterProcesses(latest.processes, q), sort) : [], [latest, q, sort]);
  const heading = "pid" in parsed ? `Process ${parsed.pid} and those under it` : parsed.name;
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-baseline gap-3">
        {renderLink({ to: "/top", className: "text-sm underline underline-offset-4", children: "← Process groups" })}
        <h2 className="text-xl font-semibold tracking-tight">{heading}</h2>
        {latest && <span className="text-sm text-muted-foreground">tick {latest.tick} · {new Date(latest.sampledAt).toLocaleTimeString()} · {latest.processes.length} {latest.processes.length === 1 ? "process" : "processes"}</span>}
        <RefreshControl />
      </div>
      <label className="flex items-center gap-2 text-sm">
        Filter
        <input type="search" className={`${input} w-64`} placeholder="Name, command, owner or origin" value={q} onChange={event => setQ(event.target.value)} />
      </label>
      {query.isError && (
        <div className="space-y-2">
          <p role="alert">{query.error.message}</p>
          <Button onClick={() => void query.refetch()}>Retry</Button>
        </div>
      )}
      {query.isPending && <p role="status">Reading processes…</p>}
      {latest && total && latest.processes.length === 0 && (
        <p className="text-sm text-muted-foreground">No process {"pid" in parsed ? `${parsed.pid} is running` : `runs ${parsed.name}`} right now. This page keeps polling.</p>
      )}
      {latest && total && latest.processes.length > 0 && (
        <>
          <div className="flex flex-wrap gap-3">
            {METRIC_VIEWS.map(view => <MetricTile key={view.metric} view={view} usage={total} host={latest.host} series={series[view.metric]} />)}
          </div>
          <p className="text-sm text-muted-foreground">CPU hogs use more than 25% of all cores. Terminate sends SIGTERM to one process; renice changes its scheduling priority.</p>
          {selected !== undefined && <FacetsPanel key={selected} pid={selected} />}
          {shown.length === 0
            ? <p className="text-sm text-muted-foreground">No process matches these filters.</p>
            : <ProcessTable processes={shown} sort={sort} onSort={setSort} selected={selected} onSelect={setSelected} cores={latest.host?.cores} />}
        </>
      )}
    </div>
  );
}

/** The processes of a program (a name) or of a process and those under it (a numeric pid), polled every second, with their total use and its trend. */
export default function ProcessPage({ target }: { target: string }) {
  // A new target starts a new history.
  return <ProcessView key={target} target={target} />;
}
