import type { Frame, GroupContext, GroupKind, GroupSample, Origin, Scope, Usage } from "./api";

// The pure side of the process groups view: which groups a view shows and in
// what order (mirroring groups.Frame.Sorted), and how their values read.

export const SORT_KEYS = ["name", "procs", "cpu", "rss", "diskRead", "diskWrite", "netRx", "netTx", "sockets"] as const;

export type SortKey = (typeof SORT_KEYS)[number];

export type SortDir = "asc" | "desc";

/** What the filter bar and the column headers choose. */
export interface GroupsView {
  /** Matches a group's name, target, repo or command. */
  q: string;
  kind: GroupKind | "";
  /** Only the groups of the user's or of the system's processes. */
  scope: Scope | "";
  /** Also show the groups of the processes no tool owns. */
  showOther: boolean;
  sort: SortKey;
  dir: SortDir;
}

export const DEFAULT_VIEW: GroupsView = { q: "", kind: "", scope: "", showOther: false, sort: "cpu", dir: "desc" };

// Processes no tool holds hang under a synthetic group per scope, other:<scope>,
// which holds none itself; its children are other:<scope>:<program>.
const SYNTHETIC_OTHER = /^other:(user|system)$/;
const PROGRAM_OTHER = /^other:(?:user|system):(.+)$/;

/** The synthetic parent of a scope's programs: it starts collapsed. */
export function isSyntheticOther(group: GroupSample): boolean {
  return group.kind === "other" && SYNTHETIC_OTHER.test(group.key);
}

/** Where a group's processes are listed: a program's other group by its name, any other group by its anchor pid; none for a synthetic parent, which has no anchor. */
export function groupPath(group: GroupSample): string | undefined {
  if (isSyntheticOther(group) || group.pid === 0) return undefined;
  if (group.kind === "other") {
    const program = PROGRAM_OTHER.exec(group.key)?.[1];
    return program === undefined ? undefined : `/top/${encodeURIComponent(program)}`;
  }
  return `/top/${group.pid}`;
}

/** What started a process and, for the system's, who runs it, as the CLI prints it: brew:<service>, app:<bundle>, apple, launchd:<label>, brew. */
export function originText(origin: Origin): string {
  let source: string;
  switch (origin.source) {
    case "brew-service": source = `brew:${origin.service ?? ""}`; break;
    case "app": source = `app:${origin.service ?? ""}`; break;
    case "launchd": source = `launchd:${origin.label ?? ""}`; break;
    default: source = origin.source ?? "";
  }
  return origin.scope === "system" && origin.owner ? `${source} ${origin.owner}`.trim() : source;
}

/** A group in the order it is shown, with how deep in the tree it sits. */
export interface GroupRow {
  group: GroupSample;
  depth: number;
}

function sortValue(usage: Usage, key: Exclude<SortKey, "name">): number {
  switch (key) {
    case "procs": return usage.processes;
    case "cpu": return usage.cpuPercent ?? 0;
    case "rss": return usage.rssBytes;
    case "diskRead": return usage.diskReadBps ?? 0;
    case "diskWrite": return usage.diskWriteBps ?? 0;
    case "netRx": return usage.netRxBps ?? 0;
    case "netTx": return usage.netTxBps ?? 0;
    case "sockets": return usage.sockets ?? 0;
  }
}

function matchesText(group: GroupSample, q: string): boolean {
  const needle = q.trim().toLowerCase();
  if (!needle) return true;
  return [group.name, group.target, group.context.repo, group.command].some(field => field?.toLowerCase().includes(needle));
}

/** The groups the view shows: those it matches and their ancestors, so a match keeps its place in the tree. */
function visibleGroups(groups: GroupSample[], view: GroupsView): GroupSample[] {
  const byKey = new Map(groups.map(group => [group.key, group]));
  const keep = new Set<string>();
  for (const group of groups) {
    if (group.kind === "other" && !view.showOther) continue;
    if (view.kind && group.kind !== view.kind) continue;
    if (view.scope && group.origin.scope !== view.scope) continue;
    if (!matchesText(group, view.q)) continue;
    for (let at: GroupSample | undefined = group; at && !keep.has(at.key); at = at.parentKey ? byKey.get(at.parentKey) : undefined) keep.add(at.key);
  }
  return groups.filter(group => keep.has(group.key));
}

/**
 * The groups the view shows, as a tree again: each group's children, and the top-level groups, sorted by the view's column; a group whose parent is not shown is top-level.
 * The programs under a synthetic other:<scope> group are listed only when the key is in expanded, or a text filter is set (so a match is never hidden).
 */
export function groupRows(groups: GroupSample[], view: GroupsView, expanded: ReadonlySet<string> = new Set()): GroupRow[] {
  const shown = visibleGroups(groups, view);
  const present = new Set(shown.map(group => group.key));
  const children = new Map<string, GroupSample[]>();
  for (const group of shown) {
    const parent = group.parentKey && present.has(group.parentKey) ? group.parentKey : "";
    children.set(parent, [...(children.get(parent) ?? []), group]);
  }
  const sign = view.dir === "desc" ? -1 : 1;
  const compare = (a: GroupSample, b: GroupSample): number => sign * (view.sort === "name"
    ? a.name.localeCompare(b.name)
    : sortValue(a.total, view.sort) - sortValue(b.total, view.sort));
  const rows: GroupRow[] = [];
  const walk = (parent: string, depth: number) => {
    for (const group of [...(children.get(parent) ?? [])].sort(compare)) {
      rows.push({ group, depth });
      if (isSyntheticOther(group) && !expanded.has(group.key) && !view.q.trim()) continue;
      walk(group.key, depth + 1);
    }
  };
  walk("", 0);
  return rows;
}

export function formatPercent(value: number | undefined): string {
  return value === undefined ? "–" : value.toFixed(1);
}

export function formatCount(value: number | undefined): string {
  return value === undefined ? "–" : String(value);
}

const BYTE_UNITS = ["B", "KiB", "MiB", "GiB", "TiB"];

export function formatBytes(bytes: number | undefined): string {
  if (bytes === undefined) return "–";
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < BYTE_UNITS.length - 1) {
    value /= 1024;
    unit++;
  }
  return `${unit === 0 ? value : value.toFixed(1).replace(/\.0$/, "")} ${BYTE_UNITS[unit]}`;
}

export function formatRate(bytesPerSecond: number | undefined): string {
  return bytesPerSecond === undefined ? "–" : `${formatBytes(bytesPerSecond)}/s`;
}

function truncate(text: string, width: number): string {
  return text.length <= width ? text : `${text.slice(0, width - 1)}…`;
}

/** A group's name and target; an agent's target is its session id, cut to 8 characters. */
export function groupLabel(group: GroupSample): string {
  const target = group.kind === "agent" && group.target ? group.target.slice(0, 8) : group.target;
  return [group.name, target].filter(Boolean).join(" ");
}

/** A title, quoted and cut to width, or the short id when there is none. */
function named(title: string | undefined, id: string, width: number): string {
  return title ? JSON.stringify(truncate(title, width)) : truncate(id, 8);
}

/** Where a group runs, as the CLI prints it: repo@branch, agent:session and cmux workspace, each by name where it has one. */
export function contextText(context: GroupContext): string {
  const parts: string[] = [];
  if (context.repo) parts.push(`${context.repo.split("/").filter(Boolean).pop()}${context.branch ? `@${context.branch}` : ""}`);
  if (context.session) parts.push(`${context.agent ?? ""}:${named(context.sessionTitle, context.session, 40)}`);
  if (context.workspace) parts.push(`cmux:${named(context.workspaceTitle, context.workspace, 24)}`);
  return parts.join(" ");
}

/** readings with reading appended, unless it is the latest already (the same reading polled twice), keeping the latest limit. */
export function appendFrame<T extends { sampledAt: string }>(frames: T[], frame: T, limit: number): T[] {
  if (frames.at(-1)?.sampledAt === frame.sampledAt) return frames;
  return [...frames, frame].slice(-limit);
}

/** Each group's total CPU % over the frames, for the ticks it was present and rated. */
export function cpuTrend(frames: Frame[]): Record<string, number[]> {
  const trend: Record<string, number[]> = {};
  for (const frame of frames) {
    for (const group of frame.groups) {
      if (group.total.cpuPercent !== undefined) (trend[group.key] ??= []).push(group.total.cpuPercent);
    }
  }
  return trend;
}

/** One group's total CPU % at each of the frames, null where it was absent or unrated. */
export interface CpuSeries {
  key: string;
  name: string;
  values: Array<number | null>;
}

/** The count groups with the highest peak CPU over the frames. */
export function topCpuSeries(frames: Frame[], count: number): CpuSeries[] {
  const series = new Map<string, CpuSeries>();
  frames.forEach((frame, index) => {
    for (const group of frame.groups) {
      const line = series.get(group.key) ?? { key: group.key, name: groupLabel(group), values: frames.map(() => null) };
      line.values[index] = group.total.cpuPercent ?? null;
      series.set(group.key, line);
    }
  });
  const peak = (line: CpuSeries) => Math.max(0, ...line.values.map(value => value ?? 0));
  return [...series.values()].sort((a, b) => peak(b) - peak(a)).slice(0, count);
}
