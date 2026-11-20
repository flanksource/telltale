import { expect, it } from "vitest";
import { appendSnapshot, metricPoints, trendPanels, type Metrics } from "./metrics";

export const snapshot: Metrics = {
  capturedAt: "2026-11-13T10:00:00Z",
  runtime: { javaVersion: "21", osName: "Example OS", osVersion: "1", processors: 4, uptime: 60000, systemLoadAverage: 1.5 },
  processCpuPercent: 25,
  memory: { heap: [{ name: "Eden", used: 100, committed: 200, max: -1 }], nonHeap: [], buffer: [] },
  garbageCollectors: [{ name: "Young", collectionCount: 2, collectionTime: 12 }],
  connectors: [{ name: "http", currentThreadsBusy: 3, maxThreads: 100 }], dataSources: [],
};

it("keeps bounded chronological live samples and replaces repeated timestamps", () => {
  let history: Metrics[] = [];
  for (let second = 0; second < 245; second++) history = appendSnapshot(history, { ...snapshot, capturedAt: new Date(Date.parse(snapshot.capturedAt) + second * 1000).toISOString() });
  expect(history).toHaveLength(240);
  expect(history[0]?.capturedAt).toBe("2026-11-13T10:00:05.000Z");
  const updated = appendSnapshot(history, { ...history[0]!, processCpuPercent: 50 });
  expect(updated).toHaveLength(240);
  expect(updated[0]?.processCpuPercent).toBe(50);
  expect(history[0]?.processCpuPercent).toBe(25);
});

it("plots native units without inventing unavailable or earlier values", () => {
  const panels = trendPanels(snapshot);
  expect(panels.map(panel => panel.title)).toEqual(["Memory", "CPU / load", "Garbage collection", "Server threads"]);
  const cpu = panels[1]!.series[0]!;
  expect(cpu.unit).toBe("percent");
  const unavailable: Metrics = { capturedAt: "2026-11-13T10:00:15Z", runtime: snapshot.runtime, memory: snapshot.memory, garbageCollectors: snapshot.garbageCollectors, connectors: snapshot.connectors, dataSources: [] };
  expect(metricPoints([snapshot, unavailable], cpu)).toEqual([{ at: snapshot.capturedAt, value: 25 }]);
  expect(metricPoints([], cpu)).toEqual([]);
  const eden = panels[0]!.series[0]!;
  expect(metricPoints([snapshot], eden)).toEqual([{ at: snapshot.capturedAt, value: 100 }]);
  expect(eden.unit).toBe("bytes");
});

it("rejects an invalid capture timestamp before it can become chart history", () => {
  expect(() => appendSnapshot([], { ...snapshot, capturedAt: "invalid" })).toThrow("Invalid JVM metric timestamp");
});

it("expires old samples after a polling gap rather than presenting them as current history", () => {
  expect(appendSnapshot([snapshot], { ...snapshot, capturedAt: "2026-11-13T12:00:00Z" })).toEqual([{ ...snapshot, capturedAt: "2026-11-13T12:00:00Z" }]);
});
