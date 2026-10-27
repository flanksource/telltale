import { describe, expect, it } from "vitest";
import fixture from "../../../testdata/top-grouping.json";
import type { Frame } from "./api";
import { projectGroups, type GroupBy } from "./grouping";

describe("grouping projection", () => {
  it.each(["repo", "worktree", "claude-session"] as const)("projects the shared %s fixture without counting parent totals twice", by => {
    const frame = structuredClone(fixture.frame) as Frame;
    const projected = projectGroups(frame.groups, by);
    const actual = projected.filter(group => group.kind === "bucket").map(bucket => {
      const included = new Set([bucket.key]);
      const members: string[] = [];
      for (const group of projected) {
        if (group.kind !== "bucket" && group.parentKey && included.has(group.parentKey)) {
          included.add(group.key);
          members.push(group.key);
        }
      }
      return { target: bucket.target ?? "", members, processes: bucket.total.processes, cpuPercent: bucket.total.cpuPercent, rssBytes: bucket.total.rssBytes };
    });
    expect(actual).toEqual(fixture.expected[by]);
    expect(frame.groups[0]!.total.cpuPercent).toBe(999);
  });

  it("leaves the tree unchanged and rejects unknown grouping modes", () => {
    const groups = fixture.frame.groups as Frame["groups"];
    expect(projectGroups(groups, "tree")).toBe(groups);
    expect(() => projectGroups(groups, "invalid" as GroupBy)).toThrow("group-by");
  });

  it("keeps missing measurements unknown and historical repository identity unattributed", () => {
    const group = { ...fixture.frame.groups[0]!, context: { repo: "/work/old" }, self: { processes: 1, rssBytes: 50 } } as Frame["groups"][number];
    const projected = projectGroups([group], "repo");
    expect(projected[0]).toMatchObject({ name: "Unattributed", total: { processes: 1, rssBytes: 50 } });
    expect(projected[0]!.total.cpuPercent).toBeUndefined();
  });

  it("keeps identities stable when branches and session titles change", () => {
    for (const by of ["repo", "worktree", "claude-session"] as const) {
      const groups = structuredClone(fixture.frame.groups) as Frame["groups"];
      const before = projectGroups(groups, by).map(group => group.key);
      groups[0]!.context.branch = "renamed";
      groups[0]!.context.sessionTitle = "Renamed session";
      expect(projectGroups(groups, by).map(group => group.key)).toEqual(before);
    }
  });

  it("keeps relationships only within a bucket and recomputes parent totals", () => {
    const groups = new Map(projectGroups(fixture.frame.groups as Frame["groups"], "repo").map(group => [group.key, group]));
    expect(groups.get("tests")!.parentKey).toBe("agent-a");
    expect(groups.get("codex")!.parentKey).toBe("bucket:repo:/work/other/.git");
    expect(groups.get("agent-a")!.total.cpuPercent).toBe(30);
  });

  it("rejects cyclic and duplicate group identities", () => {
    const groups = structuredClone(fixture.frame.groups) as Frame["groups"];
    groups[0]!.parentKey = "tests";
    expect(() => projectGroups(groups, "repo")).toThrow("cycle");
    delete groups[0]!.parentKey;
    groups.push(groups[0]!);
    expect(() => projectGroups(groups, "repo")).toThrow("duplicate");
  });

  it("distinguishes repositories with the same name and sums known optional readings", () => {
    const groups = structuredClone([fixture.frame.groups[0]!, fixture.frame.groups[3]!]) as Frame["groups"];
    groups[0]!.self.netRxBps = 1024;
    groups[1]!.context.gitCommonDir = "/other/acme/.git";
    const projected = projectGroups(groups, "repo");
    expect(projected[0]!.name).toBe(projected[2]!.name);
    expect(projected[0]!.key).not.toBe(projected[2]!.key);
    expect(projected[0]!.total.netRxBps).toBe(1024);
    expect(projected[2]!.total.netRxBps).toBeUndefined();
  });
});
