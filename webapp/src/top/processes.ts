import type { ProcessSample, ProcessTarget, TopProcesses, Usage } from "./api";
import { originText } from "./groups";

// The pure side of the process page: which target a path names, what the
// processes use in total, and which of them a view shows.

/** A numeric target is a process id (the process and its subtree); anything else is a program's name. */
export function parseTarget(target: string): ProcessTarget {
  return /^\d+$/.test(target) ? { pid: Number(target) } : { name: target };
}

/** The sum of the values that are known; unknown when none is, so a missing reading stays a dash and is not counted as zero. */
function sumKnown(values: Array<number | undefined>): number | undefined {
  const known = values.filter((value): value is number => value !== undefined);
  return known.length === 0 ? undefined : known.reduce((total, value) => total + value, 0);
}

/** What the processes use together. */
export function sumProcesses(processes: ProcessSample[]): Usage {
  const usage: Usage = { processes: processes.length, rssBytes: processes.reduce((total, process) => total + process.rssBytes, 0) };
  const optional = {
    cpuPercent: sumKnown(processes.map(process => process.cpuPercent)),
    diskReadBps: sumKnown(processes.map(process => process.diskReadBps)),
    diskWriteBps: sumKnown(processes.map(process => process.diskWriteBps)),
    netRxBps: sumKnown(processes.map(process => process.netRxBps)),
    netTxBps: sumKnown(processes.map(process => process.netTxBps)),
    sockets: sumKnown(processes.map(process => process.sockets)),
    connections: sumKnown(processes.map(process => process.connections)),
  };
  for (const [field, value] of Object.entries(optional)) if (value !== undefined) Object.assign(usage, { [field]: value });
  return usage;
}

export const METRICS = ["cpu", "rss", "diskRead", "diskWrite", "netRx", "netTx", "sockets"] as const;

export type Metric = (typeof METRICS)[number];

export function metricValue(usage: Usage, metric: Metric): number | undefined {
  switch (metric) {
    case "cpu": return usage.cpuPercent;
    case "rss": return usage.rssBytes;
    case "diskRead": return usage.diskReadBps;
    case "diskWrite": return usage.diskWriteBps;
    case "netRx": return usage.netRxBps;
    case "netTx": return usage.netTxBps;
    case "sockets": return usage.sockets;
  }
}

/** One metric's total at each reading, skipping the readings where it was unknown. */
export function metricSeries(readings: TopProcesses[], metric: Metric): number[] {
  return readings.flatMap(reading => {
    const value = metricValue(sumProcesses(reading.processes), metric);
    return value === undefined ? [] : [value];
  });
}

export type ProcessSortKey = "cpu" | "rss";

export function sortProcesses(processes: ProcessSample[], key: ProcessSortKey): ProcessSample[] {
  const value = (process: ProcessSample) => (key === "cpu" ? process.cpuPercent ?? 0 : process.rssBytes);
  return [...processes].sort((a, b) => value(b) - value(a) || a.pid - b.pid);
}

/** The processes whose name, command, owner or origin hold the text, case-insensitively. */
export function filterProcesses(processes: ProcessSample[], q: string): ProcessSample[] {
  const needle = q.trim().toLowerCase();
  if (!needle) return processes;
  return processes.filter(process => [process.name, process.command, process.origin.owner, originText(process.origin)].some(field => field?.toLowerCase().includes(needle)));
}
