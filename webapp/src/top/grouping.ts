import type { GroupContext, GroupSample, Usage } from "./api";

export const GROUPINGS = ["tree", "repo", "worktree", "claude-session"] as const;
export type GroupBy = (typeof GROUPINGS)[number];
export const GROUPING_LABELS: Record<GroupBy, string> = {
  tree: "Process tree", repo: "Git repository", worktree: "Worktree", "claude-session": "Claude session",
};

const OPTIONAL_USAGE = ["cpuPercent", "diskReadBps", "diskWriteBps", "netRxBps", "netTxBps", "sockets", "connections"] as const;

function mergeUsage(a: Usage, b: Usage): Usage {
  const sum: Usage = { processes: a.processes + b.processes, rssBytes: a.rssBytes + b.rssBytes };
  for (const key of OPTIONAL_USAGE) {
    if (a[key] !== undefined || b[key] !== undefined) sum[key] = (a[key] ?? 0) + (b[key] ?? 0);
  }
  return sum;
}

function groupingContexts(groups: GroupSample[]): Map<string, GroupContext> {
  const byKey = new Map<string, GroupSample>();
  for (const group of groups) {
    if (!group.key || byKey.has(group.key)) throw new Error(`grouping: empty or duplicate group key "${group.key}"`);
    if (group.kind === "bucket") throw new Error(`grouping: expected raw groups, got bucket "${group.key}"`);
    byKey.set(group.key, group);
  }
  const resolved = new Map<string, GroupContext>();
  const visiting = new Set<string>();
  const resolve = (key: string): GroupContext => {
    const previous = resolved.get(key);
    if (previous) return previous;
    const group = byKey.get(key);
    if (!group) return {};
    if (visiting.has(key)) throw new Error(`grouping: cycle at "${key}"`);
    visiting.add(key);
    const parent = resolve(group.parentKey ?? "");
    const result = { ...group.context };
    if (!result.agent && !result.session) {
      result.agent = parent.agent;
      result.session = parent.session;
      result.sessionTitle = parent.sessionTitle;
    }
    resolved.set(key, result);
    visiting.delete(key);
    return result;
  };
  for (const group of groups) resolve(group.key);
  return resolved;
}

function bucketIdentity(by: GroupBy, context: GroupContext): [string, string] {
  const basename = (path: string) => path.split("/").filter(Boolean).at(-1) ?? "";
  if (by === "repo" && context.gitCommonDir) {
    const base = basename(context.gitCommonDir);
    const name = base === ".git" ? basename(context.gitCommonDir.slice(0, -5)) : base.replace(/\.git$/, "");
    return [context.gitCommonDir, name];
  }
  if (by === "worktree" && context.repo) return [context.repo, `${basename(context.repo)}${context.branch ? `@${context.branch}` : ""}`];
  if (by === "claude-session" && context.agent === "claude" && context.session) {
    const short = context.session.slice(0, 8);
    return [context.session, context.sessionTitle ? `${context.sessionTitle} [${short}]` : short];
  }
  return ["", "Unattributed"];
}

export function rollupGroups(groups: GroupSample[]): GroupSample[] {
  const children = new Map<string, GroupSample[]>();
  const present = new Set<string>();
  for (const group of groups) {
    if (!group.key || present.has(group.key)) throw new Error(`rollup: empty or duplicate key "${group.key}"`);
    present.add(group.key);
    const parent = group.parentKey ?? "";
    const siblings = children.get(parent) ?? [];
    siblings.push({ ...group, total: { ...group.self } });
    children.set(parent, siblings);
  }
  const ordered: GroupSample[] = [];
  const walk = (parent: string) => {
    for (const group of children.get(parent) ?? []) {
      ordered.push(group);
      walk(group.key);
    }
  };
  walk("");
  if (ordered.length !== groups.length) throw new Error("rollup: expected a complete, acyclic group forest");
  const byKey = new Map(ordered.map(group => [group.key, group]));
  for (let index = ordered.length - 1; index >= 0; index--) {
    const group = ordered[index]!;
    const parent = group.parentKey ? byKey.get(group.parentKey) : undefined;
    if (parent) parent.total = mergeUsage(parent.total, group.total);
  }
  return ordered;
}

export function projectGroups(groups: GroupSample[], by: GroupBy): GroupSample[] {
  if (!GROUPINGS.includes(by)) throw new Error(`group-by "${by}": expected one of ${GROUPINGS.join(", ")}`);
  if (by === "tree") return groups;
  const contexts = groupingContexts(groups);
  const buckets = new Map(groups.map(group => [group.key, `bucket:${by}:${bucketIdentity(by, contexts.get(group.key)!)[0]}`]));
  const skipped = new Set(groups.filter(group => group.pid === 0 && (group.kind === "other" || group.kind === "app")).map(group => group.key));
  const created = new Set<string>();
  const projected: GroupSample[] = [];
  for (const group of groups) {
    if (skipped.has(group.key)) continue;
    const key = buckets.get(group.key)!;
    const context = contexts.get(group.key)!;
    if (!created.has(key)) {
      const [target, name] = bucketIdentity(by, context);
      projected.push({ sampledAt: group.sampledAt, tick: group.tick, key, name, target, command: target, kind: "bucket", pid: 0, context: {}, origin: {}, self: { processes: 0, rssBytes: 0 }, total: { processes: 0, rssBytes: 0 } });
      created.add(key);
    }
    const parentKey = group.parentKey && buckets.get(group.parentKey) === key && !skipped.has(group.parentKey) ? group.parentKey : key;
    projected.push({ ...group, parentKey, context });
  }
  return rollupGroups(projected);
}
