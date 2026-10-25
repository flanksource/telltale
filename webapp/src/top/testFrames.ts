import type { Frame, GroupSample, Usage } from "./api";

// Fixtures of the tests of this folder: a small tree of groups, read at one tick.

export function usage(overrides: Partial<Usage> = {}): Usage {
  return { processes: 1, rssBytes: 0, ...overrides };
}

export function group(overrides: Partial<GroupSample> & Pick<GroupSample, "key" | "name">): GroupSample {
  return {
    sampledAt: "2026-10-25T10:00:02Z",
    tick: 1,
    kind: "tool",
    pid: 100,
    command: overrides.name,
    context: {},
    origin: {},
    self: usage(),
    total: usage(),
    ...overrides,
  };
}

/**
 * claude (agent, 12.5% CPU in total) holds a vitest run (30%) and a chrome
 * (2%, no network reading); gavel (build) is a second top-level group using
 * more memory.
 */
export function sampleFrame(tick = 1): Frame {
  const sampledAt = `2026-10-25T10:00:0${tick}Z`;
  const groups: GroupSample[] = [
    group({
      key: "claude:1", name: "claude", kind: "agent", target: "0123456789abcdef", pid: 1, sampledAt, tick,
      command: "claude --resume",
      context: { repo: "/work/flanksource/telltale", branch: "main", agent: "claude", session: "0123456789abcdef", workspace: "workspace-1234567" },
      self: usage({ cpuPercent: 0.5, rssBytes: 100 * 1024 * 1024 }),
      total: usage({ processes: 5, cpuPercent: 12.5, rssBytes: 600 * 1024 * 1024, diskReadBps: 2048, diskWriteBps: 0, sockets: 7 }),
    }),
    group({
      key: "vitest:2", parentKey: "claude:1", name: "vitest", kind: "test", pid: 2, sampledAt, tick,
      command: "node vitest run",
      total: usage({ processes: 2, cpuPercent: 30, rssBytes: 300 * 1024 * 1024, netRxBps: 1536 }),
    }),
    group({
      key: "chrome:3", parentKey: "claude:1", name: "chrome", kind: "browser", pid: 3, sampledAt, tick,
      command: "Google Chrome",
      total: usage({ processes: 2, cpuPercent: 2, rssBytes: 200 * 1024 * 1024 }),
    }),
    group({
      key: "gavel:4", name: "gavel", kind: "build", pid: 4, sampledAt, tick,
      command: "gavel test",
      context: { repo: "/work/flanksource/gavel" },
      total: usage({ processes: 1, cpuPercent: 1, rssBytes: 900 * 1024 * 1024 }),
    }),
  ];
  return { sampledAt, tick, groups };
}

/**
 * A brew service (its own group), and the processes no tool holds nested as
 * other ▸ program under a synthetic parent per scope (pid 0, no members).
 */
export function othersFrame(tick = 1): Frame {
  const sampledAt = `2026-10-25T10:00:0${tick}Z`;
  const base = { sampledAt, tick };
  const groups: GroupSample[] = [
    group({
      ...base, key: "svc:900", name: "postgresql@17", kind: "service", pid: 900, command: "/opt/homebrew/opt/postgresql@17/bin/postgres",
      origin: { owner: "moshe", scope: "user", source: "brew-service", domain: "gui", label: "homebrew.mxcl.postgresql@17", service: "postgresql@17" },
      total: usage({ processes: 6, cpuPercent: 4, rssBytes: 400 * 1024 * 1024 }),
    }),
    group({
      ...base, key: "other:user", name: "other", kind: "other", pid: 0, origin: { scope: "user" },
      self: usage({ processes: 0 }), total: usage({ processes: 3, cpuPercent: 9, rssBytes: 500 * 1024 * 1024 }),
    }),
    group({
      ...base, key: "other:user:Slack", parentKey: "other:user", name: "Slack", kind: "other", pid: 700, origin: { owner: "moshe", scope: "user", source: "app", service: "com.tinyspeck.slackmacgap" },
      total: usage({ processes: 3, cpuPercent: 9, rssBytes: 500 * 1024 * 1024 }),
    }),
    group({
      ...base, key: "other:system", name: "system", kind: "other", pid: 0, origin: { scope: "system" },
      self: usage({ processes: 0 }), total: usage({ processes: 2, cpuPercent: 3, rssBytes: 80 * 1024 * 1024 }),
    }),
    group({
      ...base, key: "other:system:com.apple.WebKit.Networking", parentKey: "other:system", name: "com.apple.WebKit.Networking", kind: "other", pid: 650,
      origin: { owner: "_networkd", scope: "system", source: "launchd", label: "com.example.networking" },
      total: usage({ processes: 2, cpuPercent: 3, rssBytes: 80 * 1024 * 1024 }),
    }),
  ];
  return { sampledAt, tick, groups };
}
