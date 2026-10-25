import { describe, expect, it } from "vitest";
import type { Frame } from "./api";
import {
  appendFrame, contextText, cpuTrend, DEFAULT_VIEW, formatBytes, formatPercent, groupLabel, groupPath, groupRows, originText, topCpuSeries,
  type GroupsView,
} from "./groups";
import { group, othersFrame, sampleFrame, usage } from "./testFrames";

const view = (overrides: Partial<GroupsView> = {}): GroupsView => ({ ...DEFAULT_VIEW, ...overrides });
const names = (rows: ReturnType<typeof groupRows>) => rows.map(row => `${row.depth}:${row.group.name}`);

describe("groupRows", () => {
  it("sorts siblings by total CPU, highest first, keeping every child under its parent", () => {
    // gavel (1%) sorts after claude (12.5%); vitest (30%) sorts before chrome (2%) inside claude.
    expect(names(groupRows(sampleFrame().groups, view()))).toEqual(["0:claude", "1:vitest", "1:chrome", "0:gavel"]);
  });

  it("sorts a different column and direction without leaving a parent's subtree", () => {
    const rows = groupRows(sampleFrame().groups, view({ sort: "rss", dir: "desc" }));
    expect(names(rows)).toEqual(["0:gavel", "0:claude", "1:vitest", "1:chrome"]);
    expect(names(groupRows(sampleFrame().groups, view({ sort: "rss", dir: "asc" })))).toEqual(["0:claude", "1:chrome", "1:vitest", "0:gavel"]);
  });

  it("sorts by name ascending", () => {
    expect(names(groupRows(sampleFrame().groups, view({ sort: "name", dir: "asc" })))).toEqual(["0:claude", "1:chrome", "1:vitest", "0:gavel"]);
  });

  it("counts an unknown value as zero when sorting, as the CLI does", () => {
    const groups = [
      group({ key: "a", name: "a", total: usage({ netRxBps: 10 }) }),
      group({ key: "b", name: "b", total: usage() }),
      group({ key: "c", name: "c", total: usage({ netRxBps: 20 }) }),
    ];
    expect(names(groupRows(groups, view({ sort: "netRx" })))).toEqual(["0:c", "0:a", "0:b"]);
  });

  it("makes a group whose parent is not in the frame top-level", () => {
    const groups = [group({ key: "child", parentKey: "gone", name: "child" })];
    expect(names(groupRows(groups, view()))).toEqual(["0:child"]);
  });

  it("filters by text over name, target, repo and command, keeping the ancestors of a match", () => {
    expect(names(groupRows(sampleFrame().groups, view({ q: "vitest" })))).toEqual(["0:claude", "1:vitest"]);
    expect(names(groupRows(sampleFrame().groups, view({ q: "0123" })))).toEqual(["0:claude"]);
    expect(names(groupRows(sampleFrame().groups, view({ q: "FLANKSOURCE/GAVEL" })))).toEqual(["0:gavel"]);
    expect(names(groupRows(sampleFrame().groups, view({ q: "google" })))).toEqual(["0:claude", "1:chrome"]);
  });

  it("filters by kind", () => {
    expect(names(groupRows(sampleFrame().groups, view({ kind: "build" })))).toEqual(["0:gavel"]);
  });

  it("hides the other processes unless asked to show them", () => {
    const groups = [...sampleFrame().groups, group({ key: "zsh", name: "zsh", kind: "other" })];
    expect(names(groupRows(groups, view()))).not.toContain("0:zsh");
    expect(names(groupRows(groups, view({ showOther: true })))).toContain("0:zsh");
  });
});

describe("formatting", () => {
  it("shows unknown values as a dash and known ones with their unit", () => {
    expect(formatPercent(undefined)).toBe("–");
    expect(formatPercent(12.345)).toBe("12.3");
    expect(formatPercent(0)).toBe("0.0");
    expect(formatBytes(undefined)).toBe("–");
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(1536)).toBe("1.5 KiB");
    expect(formatBytes(600 * 1024 * 1024)).toBe("600 MiB");
  });

  it("truncates an agent's target to 8 characters but not another kind's", () => {
    expect(groupLabel(sampleFrame().groups[0]!)).toBe("claude 01234567");
    expect(groupLabel(group({ key: "k", name: "go-test", kind: "test", target: "./pkg/traces/..." }))).toBe("go-test ./pkg/traces/...");
    expect(groupLabel(group({ key: "k", name: "chrome" }))).toBe("chrome");
  });

  it("summarises the context as repo@branch, agent:session and cmux workspace", () => {
    expect(contextText(sampleFrame().groups[0]!.context)).toBe("telltale@main claude:0123456… cmux:workspa…");
    expect(contextText({ repo: "/work/gavel" })).toBe("gavel");
    expect(contextText({})).toBe("");
  });

  it("names the session and cmux workspace where they have titles, as the CLI does", () => {
    const context = { repo: "/work/acme-api", branch: "main", agent: "claude", session: "5f0c3a52-acme", sessionTitle: "Fix the flaky acme login test", workspace: "8E1C7A10-ACME", workspaceTitle: "acme-api" };
    expect(contextText(context)).toBe(`acme-api@main claude:"Fix the flaky acme login test" cmux:"acme-api"`);
  });
});

describe("history", () => {
  it("appends each new frame once and keeps the latest ones", () => {
    const one = sampleFrame(1);
    const two = sampleFrame(2);
    expect(appendFrame([one], one, 60)).toEqual([one]);
    expect(appendFrame([one], two, 60)).toEqual([one, two]);
    expect(appendFrame([one, two], sampleFrame(3), 2).map(frame => frame.tick)).toEqual([2, 3]);
  });

  it("reads each group's CPU trend, skipping the ticks it was unknown or absent", () => {
    const frames: Frame[] = [sampleFrame(1), sampleFrame(2), sampleFrame(3)];
    frames[1]!.groups = frames[1]!.groups.filter(g => g.key !== "chrome:3");
    frames[2]!.groups[2]!.total = usage();
    expect(cpuTrend(frames)["chrome:3"]).toEqual([2]);
    expect(cpuTrend(frames)["claude:1"]).toEqual([12.5, 12.5, 12.5]);
  });

  it("picks the groups with the highest peak CPU for the time series", () => {
    const frames = [sampleFrame(1), sampleFrame(2)];
    const series = topCpuSeries(frames, 2);
    expect(series.map(line => line.name)).toEqual(["vitest", "claude 01234567"]);
    expect(series[0]!.values).toEqual([30, 30]);
  });
});

describe("originText", () => {
  it.each([
    ["a brew service", { source: "brew-service", service: "postgresql@17", label: "homebrew.mxcl.postgresql@17", owner: "moshe", scope: "user" }, "brew:postgresql@17"],
    ["an app", { source: "app", service: "com.example.editor", owner: "moshe", scope: "user" }, "app:com.example.editor"],
    ["an Apple job", { source: "apple", owner: "moshe", scope: "user" }, "apple"],
    ["another launchd job", { source: "launchd", label: "com.example.agent", owner: "moshe", scope: "user" }, "launchd:com.example.agent"],
    ["a program brew installed", { source: "brew", owner: "moshe", scope: "user" }, "brew"],
    ["a system daemon, with its owner", { source: "launchd", label: "com.example.daemon", owner: "_example", scope: "system" }, "launchd:com.example.daemon _example"],
    ["a system process of no known source", { owner: "root", scope: "system" }, "root"],
    ["a user process of no known source", { owner: "moshe", scope: "user" }, ""],
    ["no origin at all", {}, ""],
  ] as const)("reads %s as the CLI does", (_name, origin, expected) => {
    expect(originText(origin)).toBe(expected);
  });
});

describe("groupPath", () => {
  it("links a program's other group to its encoded name, any other group to its pid, a synthetic parent to nothing", () => {
    const [service, user, slack, system, webkit] = othersFrame().groups;
    expect([service, user, slack, system, webkit].map(g => groupPath(g!))).toEqual(["/top/900", undefined, "/top/Slack", undefined, "/top/com.apple.WebKit.Networking"]);
    expect(groupPath(group({ key: "other:user:Google Chrome Helper (GPU)", name: "Google Chrome Helper (GPU)", kind: "other" }))).toBe("/top/Google%20Chrome%20Helper%20(GPU)");
    expect(groupPath(group({ key: "other:user:a:b", name: "a:b", kind: "other" }))).toBe("/top/a%3Ab");
  });

  it("links an app's group to its anchor pid, and the applications parent, which has no anchor, to nothing", () => {
    const anchorPid = 724;
    expect(groupPath(group({ key: "apps", name: "applications", kind: "app", pid: 0 }))).toBeUndefined();
    expect(groupPath(group({ key: "app:/Applications/Acme Chat.app", parentKey: "apps", name: "Acme Chat", kind: "app", pid: anchorPid }))).toBe(`/top/${anchorPid}`);
  });
});

describe("groupRows with the other processes tree", () => {
  const all = view({ showOther: true });
  const labels = (rows: ReturnType<typeof groupRows>) => rows.map(row => `${row.depth}:${row.group.key}`);

  it("nests a program under its scope's synthetic parent, collapsed until expanded", () => {
    const groups = othersFrame().groups;
    // Siblings by CPU: other (9%), the service (4%), system (3%).
    expect(labels(groupRows(groups, all))).toEqual(["0:other:user", "0:svc:900", "0:other:system"]);
    expect(labels(groupRows(groups, all, new Set(["other:user", "other:system"])))).toEqual([
      "0:other:user", "1:other:user:Slack", "0:svc:900", "0:other:system", "1:other:system:com.apple.WebKit.Networking",
    ]);
  });

  it("keeps a matching program visible whatever the collapse, under its ancestors", () => {
    expect(labels(groupRows(othersFrame().groups, view({ showOther: true, q: "webkit" })))).toEqual(["0:other:system", "1:other:system:com.apple.WebKit.Networking"]);
  });

  it("hides the whole tree unless other processes are shown", () => {
    expect(labels(groupRows(othersFrame().groups, view()))).toEqual(["0:svc:900"]);
  });

  it("filters by scope, keeping the ancestors of a match and dropping the other scope's groups", () => {
    expect(labels(groupRows(othersFrame().groups, view({ showOther: true, scope: "system" })))).toEqual(["0:other:system"]);
    expect(labels(groupRows(othersFrame().groups, view({ showOther: true, scope: "user" })))).toEqual(["0:other:user", "0:svc:900"]);
  });

  it("filters the kind service", () => {
    expect(labels(groupRows(othersFrame().groups, view({ kind: "service" })))).toEqual(["0:svc:900"]);
  });
});
