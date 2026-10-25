import type { ProcessDescriptor, ProcessEnv, ProcessSampleFacet } from "@flanksource/clicky-ui/data";
import { failure, type TraceRecord } from "../traces/api";

// The server reads the host's process groups live at /api/top
// (internal/server.TopHandler), and records them, when a host run arms the
// process_groups trace kind, as GroupSample rows (pkg/tracing/groups).

export const TOP_ENDPOINT = "/api/top";

/** The trace kind a host run records its process groups as. */
export const PROCESS_GROUPS_KIND = "process_groups";

export const GROUP_KINDS = ["agent", "browser", "test", "build", "tool", "service", "app", "other"] as const;

export type GroupKind = (typeof GROUP_KINDS)[number];

/** What a set of processes use at one tick; a missing field is unknown, not zero. */
export interface Usage {
  processes: number;
  cpuPercent?: number;
  rssBytes: number;
  diskReadBps?: number;
  diskWriteBps?: number;
  netRxBps?: number;
  netTxBps?: number;
  sockets?: number;
  connections?: number;
}

/** Where a group runs and on whose behalf. */
export interface GroupContext {
  cwd?: string;
  repo?: string;
  branch?: string;
  agent?: string;
  session?: string;
  /** The session's title: the one its agent named it, or its opening prompt's. */
  sessionTitle?: string;
  workspace?: string;
  /** What cmux calls the workspace and the surface. */
  workspaceTitle?: string;
  surface?: string;
  surfaceTitle?: string;
  /** Why some of the context could not be read. */
  error?: string;
}

export const SCOPES = ["user", "system"] as const;

export type Scope = (typeof SCOPES)[number];

export type OriginSource = "brew-service" | "brew" | "app" | "apple" | "launchd";

/** Who runs a process and what started it (groups.Origin). */
export interface Origin {
  owner?: string;
  scope?: Scope;
  source?: OriginSource;
  /** The launchd domain of the job that started it. */
  domain?: "system" | "gui";
  label?: string;
  service?: string;
}

/** One group at one tick (groups.GroupSample). */
export interface GroupSample {
  sampledAt: string;
  tick: number;
  key: string;
  parentKey?: string;
  name: string;
  kind: GroupKind;
  target?: string;
  pid: number;
  command: string;
  context: GroupContext;
  origin: Origin;
  self: Usage;
  total: Usage;
}

/** What the whole host uses at one live tick (groups.HostUsage); the CPU shares are missing on the first tick. */
export interface HostUsage {
  cores: number;
  cpuUserPercent?: number;
  /** Kernel time. */
  cpuSystemPercent?: number;
  cpuIdlePercent?: number;
  memoryUsedBytes: number;
  memoryTotalBytes: number;
  swapUsedBytes: number;
  swapTotalBytes: number;
  /** Host-wide disk operations and bytes per second; missing on the first tick. */
  diskReadOps?: number;
  diskWriteOps?: number;
  diskReadBps?: number;
  diskWriteBps?: number;
  load1: number;
  load5: number;
  load15: number;
}

/** What reading a live tick cost telltale itself (groups.Overhead); the CPU is missing on the first tick. */
export interface Overhead {
  /** telltale's CPU and that of the commands it ran, of one core, since the tick before. */
  cpuPercent?: number;
  tickMillis: number;
}

/** Every group shown at one tick, parents before their children (groups.Frame); recorded frames hold no host or overhead. */
export interface Frame {
  sampledAt: string;
  tick: number;
  host?: HostUsage;
  overhead?: Overhead;
  groups: GroupSample[];
}

/** One process at one tick: who runs it, the group it is in and what it uses (groups.ProcessSample). */
export interface ProcessSample extends Usage {
  key: string;
  pid: number;
  ppid: number;
  name: string;
  command: string;
  groupKey: string;
  origin: Origin;
}

/** The processes a name or pid picks at one tick, with the host's use (server.TopProcesses). */
export interface TopProcesses {
  sampledAt: string;
  tick: number;
  host?: HostUsage;
  processes: ProcessSample[];
}

/** The processes running a program, or a process and those under it. */
export type ProcessTarget = { name: string } | { pid: number };

export interface TopQuery {
  /** Also read the processes no tool's group holds. */
  all: boolean;
  /** Read each process's network bytes with nettop. */
  net: boolean;
}

/** The latest frame of the host's process groups. */
export async function fetchTopFrame({ all, net }: TopQuery, signal?: AbortSignal): Promise<Frame> {
  const query = new URLSearchParams({ all: String(all), net: String(net) });
  const res = await fetch(`${TOP_ENDPOINT}?${query}`, { headers: { Accept: "application/json" }, ...(signal ? { signal } : {}) });
  if (!res.ok) throw await failure(res, "read process groups");
  return res.json() as Promise<Frame>;
}

export const TOP_PROCESSES_ENDPOINT = `${TOP_ENDPOINT}/processes`;

/** The latest reading of the processes running a program (name) or of a process and those under it (pid), including those no group holds. */
export async function fetchTopProcesses(target: ProcessTarget, signal?: AbortSignal): Promise<TopProcesses> {
  const query = new URLSearchParams({ ...("name" in target ? { name: target.name } : { pid: String(target.pid) }), all: "true" });
  const res = await fetch(`${TOP_PROCESSES_ENDPOINT}?${query}`, { headers: { Accept: "application/json" }, ...(signal ? { signal } : {}) });
  if (!res.ok) throw await failure(res, "read processes");
  return res.json() as Promise<TopProcesses>;
}

export const TOP_FACETS_ENDPOINT = `${TOP_ENDPOINT}/facets`;

/** What a process holds and runs with at one reading (groups.Facets): its context, its environment with secrets masked, and its open descriptors by facet. */
export interface ProcessFacets {
  pid: number;
  key: string;
  name: string;
  command: string;
  readAt: string;
  context: GroupContext;
  environment?: ProcessEnv;
  environmentError?: string;
  descriptors: Record<ProcessSampleFacet, ProcessDescriptor[]>;
}

/** Reads the facets of a process the latest tick listed; a 404 means it did not list it. */
export async function fetchTopFacets(pid: number, signal?: AbortSignal): Promise<ProcessFacets> {
  const query = new URLSearchParams({ pid: String(pid), all: "true" });
  const res = await fetch(`${TOP_FACETS_ENDPOINT}?${query}`, { headers: { Accept: "application/json" }, ...(signal ? { signal } : {}) });
  if (!res.ok) throw await failure(res, `read the facets of pid ${pid}`);
  return res.json() as Promise<ProcessFacets>;
}

/** Rebuilds frames from a process_groups capture's rows: those of one tick make one frame, oldest first, rows in the order the capture holds them. */
export function framesFromRecords(records: TraceRecord[]): Frame[] {
  const byTick = new Map<number, GroupSample[]>();
  for (const record of records) {
    if (typeof record.tick !== "number") throw new Error(`process_groups row has no numeric tick: ${JSON.stringify(record)}`);
    if (typeof record.origin !== "object" || record.origin === null) throw new Error(`process_groups row has no origin (recorded before groups carried one?): ${JSON.stringify(record)}`);
    const row = record as unknown as GroupSample;
    const rows = byTick.get(row.tick);
    if (rows) rows.push(row);
    else byTick.set(row.tick, [row]);
  }
  return [...byTick.entries()]
    .sort(([a], [b]) => a - b)
    .map(([tick, groups]) => ({ sampledAt: groups[0]!.sampledAt, tick, groups }));
}
