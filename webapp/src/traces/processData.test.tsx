import { describe, expect, it } from "vitest";
import { ProcessFacets, projectProcesses, type ProcessIdentity, type ProcessMetric } from "./processData";

describe("process table projection", () => {
  it("joins metrics to startup identity and applies file removals independently of sockets", () => {
    const identity: ProcessIdentity = { key: "worker", pid: 123, ppid: 1, name: "worker", command: "worker", startedAt: "2026-11-08T10:00:00Z", firstObservedAt: "2026-11-08T10:00:00Z" };
    const metric: ProcessMetric = { entity: identity.key, observedAt: "2026-11-08T10:00:02Z", tick: 2, cpuPercent: null, rssBytes: 1024, memoryPercent: 0, openFiles: 0, receivingSockets: 1, connectionsIn: 0, connectionsOut: 0, connectionsUnknown: 0 };
    const facets = new ProcessFacets();
    const file = { process: identity.key, observedAt: identity.firstObservedAt, tick: 1, facet: "files", entity: "config", operation: "added", resource: { fd: "3", type: "REG", name: "config.yaml" } } as const;
    facets.apply([file, { ...file, facet: "sockets", entity: "listener", resource: { fd: "4", type: "IPv4", name: "localhost:8080" } }]);
    facets.apply([{ ...file, operation: "removed", tick: 2 }]);
    const result = projectProcesses([identity], [metric], facets);
    expect(result.nodes[0]).toMatchObject({ key: identity.key, status: "running", rssBytes: 1024, cpuPercent: null });
    expect(result.samples[0]).toMatchObject({ key: identity.key, pid: identity.pid, openFiles: 0, openFileList: [], ports: 1, portList: [{ fd: "4" }], connections: 0 });
  });

  it("rejects metrics without a recorded identity", () => {
    expect(() => projectProcesses([], [{ entity: "unknown", observedAt: "2026-11-08T10:00:00Z", tick: 1, rssBytes: 0, memoryPercent: 0, cpuPercent: null }], new ProcessFacets())).toThrow(/unknown process/);
  });
});
