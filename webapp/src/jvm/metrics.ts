export interface MemoryPool { name: string; used: number; committed: number; max: number }
export interface Metrics {
  capturedAt: string;
  runtime: { javaVersion: string; osName: string; osVersion: string; processors: number; uptime: number; systemLoadAverage: number };
  processCpuPercent?: number;
  memory: { heap: MemoryPool[]; nonHeap: MemoryPool[]; buffer: MemoryPool[] };
  garbageCollectors: Array<{ name: string; collectionCount: number; collectionTime: number }>;
  connectors: Array<{ name: string; currentThreadsBusy?: number; maxThreads?: number }>;
  dataSources: Record<string, unknown>[];
}

export interface Metric { id: string; label: string; unit: string; read: (snapshot: Metrics) => number | undefined }
export interface MetricPanel { title: string; series: Metric[] }

export function appendSnapshot(history: Metrics[], snapshot: Metrics): Metrics[] {
  if (!Number.isFinite(Date.parse(snapshot.capturedAt))) throw new Error(`Invalid JVM metric timestamp: ${snapshot.capturedAt}`);
  const observations = [...history.filter(item => item.capturedAt !== snapshot.capturedAt), snapshot]
    .sort((a, b) => Date.parse(a.capturedAt) - Date.parse(b.capturedAt));
  const cutoff = Date.parse(observations[observations.length - 1]!.capturedAt) - 3600000;
  return observations.filter(item => Date.parse(item.capturedAt) >= cutoff).slice(-240);
}

export function metricPoints(history: Metrics[], metric: Metric): Array<{ at: string; value: number }> {
  return history.flatMap(snapshot => {
    const value = metric.read(snapshot);
    return value === undefined || value < 0 ? [] : [{ at: snapshot.capturedAt, value }];
  });
}

export function trendPanels(snapshot: Metrics): MetricPanel[] {
  return [
    { title: "Memory", series: (["heap", "nonHeap", "buffer"] as const).flatMap(group => snapshot.memory[group].map(pool => ({
      id: `memory/${group}/${pool.name}`, label: pool.name, unit: "bytes",
      read: (value: Metrics) => value.memory[group].find(item => item.name === pool.name)?.used,
    }))) },
    { title: "CPU / load", series: [
      { id: "cpu", label: "process", unit: "percent", read: (value: Metrics) => value.processCpuPercent },
      { id: "load", label: "load", unit: "short", read: (value: Metrics) => value.runtime.systemLoadAverage },
    ] },
    { title: "Garbage collection", series: snapshot.garbageCollectors.map(gc => ({
      id: `gc/${gc.name}`, label: gc.name, unit: "ms",
      read: (value: Metrics) => value.garbageCollectors.find(item => item.name === gc.name)?.collectionTime,
    })) },
    { title: "Server threads", series: snapshot.connectors.flatMap(connector => (["currentThreadsBusy", "maxThreads"] as const).map(field => ({
      id: `connector/${connector.name}/${field}`, label: `${connector.name} ${field === "currentThreadsBusy" ? "busy" : "max"}`, unit: "short",
      read: (value: Metrics) => value.connectors.find(item => item.name === connector.name)?.[field],
    }))) },
  ].filter(panel => panel.series.length > 0);
}
