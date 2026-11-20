import type { ProcessSample, ProcessTraceNode } from "./api";

export type ProcessIdentity = Omit<ProcessTraceNode, "status" | "lastSeenAt" | "rssBytes" | "peakRssBytes"> & { firstObservedAt: string };
export interface ProcessMetric {
  entity: string;
  observedAt: string;
  tick: number;
  rssBytes: number;
  memoryPercent: number;
  cpuPercent: number | null;
  diskReadBytes?: number | null;
  diskWriteBytes?: number | null;
  openFiles?: number | null;
  receivingSockets?: number | null;
  connectionsIn?: number | null;
  connectionsOut?: number | null;
  connectionsUnknown?: number | null;
}
type Descriptor = NonNullable<ProcessSample["openFileList"]>[number];
export interface FacetChange {
  process: string;
  observedAt: string;
  tick: number;
  facet: string;
  entity: string;
  operation: "added" | "removed";
  resource: Descriptor;
}

export class ProcessFacets {
  private held = new Map<string, FacetChange>();
  apply(changes: FacetChange[]) {
    for (const event of changes) {
      const key = `${event.process}/${event.facet}/${event.entity}`;
      if (event.operation === "added") this.held.set(key, event);
      else if (event.operation === "removed") this.held.delete(key);
      else throw new Error(`Unknown facet operation ${String(event.operation)}`);
    }
  }
  list(process: string, facets: string[]): Descriptor[] {
    return [...this.held.values()].filter(event => event.process === process && facets.includes(event.facet)).map(event => event.resource);
  }
}

export function projectProcesses(identities: ProcessIdentity[], metrics: ProcessMetric[], facets: ProcessFacets): { nodes: ProcessTraceNode[]; samples: ProcessSample[] } {
  const nodes = identities.map(identity => ({ ...identity, status: identity.exitedAt ? "exited" : "running", isRoot: !identity.parentKey, lastSeenAt: identity.firstObservedAt, rssBytes: 0, peakRssBytes: 0 }));
  const index = new Map(nodes.map(node => [node.key, node]));
  const latest = new Map<string, ProcessSample>();
  const samples = metrics.map(metric => {
    const node = index.get(metric.entity);
    if (!node) throw new Error(`Metrics reference unknown process ${metric.entity}`);
    if (metric.observedAt >= node.lastSeenAt) {
      node.cpuPercent = metric.cpuPercent;
      node.rssBytes = metric.rssBytes;
      node.lastSeenAt = metric.observedAt;
    }
    node.peakRssBytes = Math.max(node.peakRssBytes, metric.rssBytes);
    if (metric.cpuPercent !== null) node.peakCpuPercent = Math.max(node.peakCpuPercent ?? 0, metric.cpuPercent);
    const connections = [metric.connectionsIn, metric.connectionsOut, metric.connectionsUnknown];
    const sample: ProcessSample = {
      key: metric.entity, pid: node.pid, sampledAt: metric.observedAt, tick: metric.tick, rssBytes: metric.rssBytes, memoryPercent: metric.memoryPercent, cpuPercent: metric.cpuPercent,
      ...(metric.diskReadBytes != null ? { diskReadBytes: metric.diskReadBytes } : {}),
      ...(metric.diskWriteBytes != null ? { diskWriteBytes: metric.diskWriteBytes } : {}),
      ...(metric.openFiles != null ? { openFiles: metric.openFiles } : {}),
      ...(metric.receivingSockets != null ? { ports: metric.receivingSockets, sockets: metric.receivingSockets } : {}),
      ...(connections.every(count => count != null) ? { connections: connections.reduce<number>((sum, count) => sum + count!, 0) } : {}),
    };
    if (!latest.has(sample.key) || latest.get(sample.key)!.tick < sample.tick) latest.set(sample.key, sample);
    return sample;
  });
  for (const [key, sample] of latest) {
    if (sample.openFiles !== undefined) sample.openFileList = facets.list(key, ["files"]);
    if (sample.ports !== undefined) sample.portList = sample.socketList = facets.list(key, ["sockets"]);
    if (sample.connections !== undefined) sample.connectionList = facets.list(key, ["connection_in", "connection_out", "connection_unknown"]);
  }
  return { nodes, samples };
}
