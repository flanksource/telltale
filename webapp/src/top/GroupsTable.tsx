import { useMemo, useState, type ReactNode } from "react";
import { Sparkline } from "@flanksource/clicky-ui/data";
import { useRouter } from "@flanksource/clicky-ui/rpc";
import { GROUP_KINDS, SCOPES, type GroupKind, type GroupSample, type Scope, type Usage } from "./api";
import { GROUPINGS, GROUPING_LABELS, type GroupBy } from "./grouping";
import { isCPUHog } from "./cpu";
import { hogLabel, isPinned } from "./hogs";
import {
  contextText, formatBytes, formatCount, formatPercent, formatRate, groupLabel, groupPath, groupRows, isSyntheticOther, originText,
  type GroupsView, type SortKey,
} from "./groups";

interface Column {
  key: SortKey;
  label: string;
  value: (usage: Usage) => string;
}

const COLUMNS: Column[] = [
  { key: "procs", label: "Procs", value: usage => String(usage.processes) },
  { key: "cpu", label: "CPU %", value: usage => formatPercent(usage.cpuPercent) },
  { key: "rss", label: "RSS", value: usage => formatBytes(usage.rssBytes) },
  { key: "compressed", label: "Compressed", value: usage => formatBytes(usage.compressedBytes) },
  { key: "swap", label: "Swap", value: usage => formatBytes(usage.swapBytes) },
  { key: "diskRead", label: "Disk read/s", value: usage => formatRate(usage.diskReadBps) },
  { key: "diskWrite", label: "Disk write/s", value: usage => formatRate(usage.diskWriteBps) },
  { key: "netRx", label: "Net in/s", value: usage => formatRate(usage.netRxBps) },
  { key: "netTx", label: "Net out/s", value: usage => formatRate(usage.netTxBps) },
  { key: "sockets", label: "Sockets", value: usage => formatCount(usage.sockets) },
];

const KIND_CLASS: Record<GroupSample["kind"], string> = {
  bucket: "font-medium",
  agent: "text-blue-600 dark:text-blue-400",
  browser: "text-purple-600 dark:text-purple-400",
  test: "text-green-700 dark:text-green-400",
  build: "",
  tool: "",
  service: "text-teal-700 dark:text-teal-400",
  app: "",
  other: "text-muted-foreground",
};

const INDENT_REM = 1.25;

const input = "rounded border border-border bg-background px-2 py-1";

/** The text, kind, scope and "other processes" filters of a view; stays shown whatever the groups, so a filter that matches nothing can be undone. */
export function GroupsFilterBar({ view, onChange, children }: { view: GroupsView; onChange: (next: Partial<GroupsView>) => void; children?: ReactNode }) {
  return (
    <div className="flex flex-wrap items-center gap-3 text-sm">
      <label className="flex items-center gap-2">
        Group by
        <select className={input} value={view.groupBy} onChange={event => onChange({ groupBy: event.target.value as GroupBy })}>
          {GROUPINGS.map(by => <option key={by} value={by}>{GROUPING_LABELS[by]}</option>)}
        </select>
      </label>
      <label className="flex items-center gap-2">
        Filter
        <input type="search" className={`${input} w-64`} placeholder="Name, target, repo or command" value={view.q} onChange={event => onChange({ q: event.target.value })} />
      </label>
      <label className="flex items-center gap-2">
        Kind
        <select className={input} value={view.kind} onChange={event => onChange({ kind: event.target.value as GroupKind | "" })}>
          <option value="">Any</option>
          {GROUP_KINDS.map(kind => <option key={kind} value={kind}>{kind}</option>)}
        </select>
      </label>
      <label className="flex items-center gap-2">
        Scope
        <select className={input} value={view.scope} onChange={event => onChange({ scope: event.target.value as Scope | "" })}>
          <option value="">Any</option>
          {SCOPES.map(scope => <option key={scope} value={scope}>{scope}</option>)}
        </select>
      </label>
      <label className="flex items-center gap-2">
        <input type="checkbox" checked={view.showOther} onChange={event => onChange({ showOther: event.target.checked })} />
        Show other processes
      </label>
      {children}
    </div>
  );
}

function sortDescription(view: GroupsView, key: SortKey): "ascending" | "descending" | "none" {
  if (view.sort !== key) return "none";
  return view.dir === "asc" ? "ascending" : "descending";
}

function ownerText(group: GroupSample): string {
  const { owner, scope } = group.origin;
  return [owner, scope && `(${scope})`].filter(Boolean).join(" ");
}

interface GroupRowProps {
  cores: number | undefined;
  group: GroupSample;
  depth: number;
  trend: number[] | undefined;
  link: boolean;
  expanded: boolean;
  onToggle: () => void;
}

function GroupRowView({ group, depth, trend, link, expanded, onToggle, cores }: GroupRowProps) {
  const { renderLink } = useRouter();
  const label = groupLabel(group);
  const path = link ? groupPath(group) : undefined;
  const origin = originText(group.origin);
  const hog = hogLabel(group.total, group.sampledAt) || (isCPUHog(group.total.cpuPercent, cores) ? "CPU hog" : "");
  return (
    <tr data-depth={depth} className={`border-t border-border align-top hover:bg-muted/40 ${hog ? "bg-destructive/10" : ""}`} title={group.command}>
      <td className={`py-1 pr-3 ${KIND_CLASS[group.kind]}`}>
        <span data-indent className="flex max-w-[22rem] items-center gap-1" style={{ paddingLeft: `${depth * INDENT_REM}rem` }}>
          {(isSyntheticOther(group) || group.kind === "bucket") && (
            <button type="button" aria-expanded={expanded} aria-label={`${expanded ? "Collapse" : "Expand"} ${label}`} className="w-4 text-left" onClick={onToggle}>
              <span aria-hidden>{expanded ? "▾" : "▸"}</span>
            </button>
          )}
          <span className="truncate" title={label}>
            {path ? renderLink({ to: path, className: "hover:underline", children: label, title: `Processes of ${label}` }) : label}
          </span>
          {hog && <span className="font-semibold text-destructive">{hog}</span>}
        </span>
      </td>
      {trend !== undefined && (
        <td className="px-2 py-1">
          {trend.length < 2
            ? <span className="text-muted-foreground">–</span>
            : <span role="img" aria-label={`CPU trend, latest ${trend.at(-1)!.toFixed(1)}%, peak ${Math.max(...trend).toFixed(1)}%`}>
                <span aria-hidden="true">
                  <Sparkline values={trend} width={64} height={16} color="currentColor" className="text-blue-600 dark:text-blue-400" />
                </span>
              </span>}
        </td>
      )}
      {COLUMNS.map(column => (
        <td key={column.key} className="whitespace-nowrap px-2 py-1 text-right tabular-nums" title={group.total.memoryError && (column.key === "swap" || column.key === "compressed") ? group.total.memoryError : `self ${column.value(group.self)}`}>
          {column.value(group.total)}
        </td>
      ))}
      <td className="whitespace-nowrap px-2 py-1 text-muted-foreground">{origin}</td>
      <td className="whitespace-nowrap px-2 py-1 text-muted-foreground">{ownerText(group)}</td>
      <td className="py-1 pl-3 text-muted-foreground" title={group.context.error}>
        <span className="block max-w-[16rem] truncate" title={contextText(group.context)}>{contextText(group.context)}</span>
      </td>
    </tr>
  );
}

export interface GroupsTableProps {
  cores?: number;
  groups: GroupSample[];
  view: GroupsView;
  onViewChange: (next: Partial<GroupsView>) => void;
  /** Each group's CPU % over the readings kept, by group key; adds a trend column. */
  trend?: Record<string, number[]>;
  /** Link each group to the page of its processes: only for live groups, whose processes still run. */
  linkGroups?: boolean;
}

/** One frame's groups as a tree table: each group's total (its own use as the title of a cell), sorted within each parent by the view's column. The processes no tool holds hang under a collapsed other:<scope> group. */
export function GroupsTable({ groups, view, onViewChange, trend, linkGroups = false, cores }: GroupsTableProps) {
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set());
  const [collapsed, setCollapsed] = useState<ReadonlySet<string>>(new Set());
  const rows = useMemo(() => groupRows(groups, view, expanded, collapsed), [groups, view, expanded, collapsed]);
  if (groups.length === 0) return <p className="text-sm text-muted-foreground">No process groups yet.</p>;
  if (rows.length === 0) return <p className="text-sm text-muted-foreground">No group matches these filters.</p>;
  const toggle = (key: string, open: boolean) => {
    setExpanded(previous => {
      const next = new Set(previous);
      if (open) next.delete(key); else next.add(key);
      return next;
    });
    setCollapsed(previous => {
      const next = new Set(previous);
      if (open) next.add(key); else next.delete(key);
      return next;
    });
  };
  const sortBy = (key: SortKey) => onViewChange(view.sort === key
    ? { dir: view.dir === "desc" ? "asc" : "desc" }
    : { sort: key, dir: key === "name" ? "asc" : "desc" });
  const header = (key: SortKey, label: string, align: string) => (
    <th key={key} scope="col" aria-sort={sortDescription(view, key)} className={`whitespace-nowrap px-2 py-1 font-medium ${align}`}>
      <button type="button" className="font-medium hover:underline" onClick={() => sortBy(key)}>
        {label}
        <span aria-hidden>{view.sort === key ? (view.dir === "desc" ? " ▼" : " ▲") : ""}</span>
      </button>
    </th>
  );
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm">
        <thead>
          <tr className="text-muted-foreground">
            {header("name", "Group", "text-left")}
            {trend !== undefined && <th scope="col" className="px-2 py-1 text-left font-medium">Trend</th>}
            {COLUMNS.map(column => header(column.key, column.label, "text-right"))}
            <th scope="col" className="px-2 py-1 text-left font-medium">Origin</th>
            <th scope="col" className="px-2 py-1 text-left font-medium">Owner</th>
            <th scope="col" className="py-1 pl-3 text-left font-medium">Context</th>
          </tr>
        </thead>
        <tbody>
          {rows.map(({ group, depth }) => {
            const open = (!collapsed.has(group.key) && (group.kind === "bucket" || expanded.has(group.key) || isPinned(group.total, group.sampledAt))) || !!view.q.trim();
            return <GroupRowView
              key={group.key}
              group={group}
              cores={cores}
              depth={depth}
              trend={trend ? trend[group.key] ?? [] : undefined}
              link={linkGroups}
              expanded={open}
              onToggle={() => toggle(group.key, open)}
            />;
          })}
        </tbody>
      </table>
    </div>
  );
}
