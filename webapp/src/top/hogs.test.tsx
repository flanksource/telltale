import { describe, expect, it } from "vitest";
import { groupRows, DEFAULT_VIEW } from "./groups";
import { group, usage } from "./testFrames";
import { projectGroups } from "./grouping";
import { sortProcesses, sumProcesses } from "./processes";
import type { ProcessSample } from "./api";

describe("resource hog pinning", () => {
  const sampledAt = "2026-11-06T10:00:00Z";
  const hogs = { memory: "2026-11-06T10:02:00Z" };
  const groups = [
    group({ key: "busy", name: "a", sampledAt, total: usage({ cpuPercent: 100, rssBytes: 1000 }) }),
    group({ key: "hog", name: "z", sampledAt, self: usage({ compressedBytes: 100, swapBytes: 200, hogs }), total: usage({ hogs }) }),
  ];

  it("pins recent memory hogs ahead of ascending and descending sorts", () => {
    for (const sort of ["cpu", "rss", "swap", "compressed", "name"] as const) {
      for (const dir of ["asc", "desc"] as const) {
        expect(groupRows(groups, { ...DEFAULT_VIEW, sort, dir })[0]!.group.key).toBe("hog");
      }
    }
    const expired = groups.map(group => ({ ...group, sampledAt: "2026-11-06T10:02:00Z" }));
    expect(groupRows(expired, DEFAULT_VIEW)[0]!.group.key).toBe("busy");
  });

  it("preserves memory and pins when regrouping or summing processes", () => {
    const bucket = projectGroups(groups, "repo")[0]!;
    expect(bucket.total).toMatchObject({ compressedBytes: 100, swapBytes: 200, hogs });
    const processes: ProcessSample[] = groups.map((group, index) => ({
      ...group.self, key: `${index}@1`, name: group.name, command: group.name, groupKey: group.key,
      pid: index + 1, ppid: 0, origin: {},
    }));
    expect(sumProcesses(processes)).toMatchObject({ compressedBytes: 100, swapBytes: 200 });
    expect(sortProcesses(processes, { by: "cpu", sampledAt })[0]!.key).toBe("1@1");
    expect(sortProcesses(processes, { by: "cpu", sampledAt: "2026-11-06T10:02:00Z" })[0]!.key).toBe("0@1");
  });
});
