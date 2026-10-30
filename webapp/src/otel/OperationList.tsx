import type { ReactNode } from "react";
import { duration, type OperationStats } from "./api";
import { shortOperation } from "./query";

/** The root operations of the window with their counts and p50/p95; choosing one filters to it, "All operations" clears it. */
export function OperationList({ stats, selected, onSelect, hideSingle }: {
  stats: OperationStats[];
  /** The one root operation included, "" for none, undefined for any other selection. */
  selected: string | undefined;
  onSelect: (operation: string) => void;
  hideSingle: boolean;
}) {
  const total = stats.reduce((sum, stat) => sum + stat.count, 0);
  const item = (operation: string, label: string, body: ReactNode, dim = false) => (
    <li key={operation || "all"}>
      <button
        type="button"
        aria-pressed={selected === operation}
        onClick={() => onSelect(operation)}
        className={`w-full rounded-md px-density-2 py-1.5 text-left text-xs ${selected === operation ? "bg-accent" : "hover:bg-muted"} ${dim ? "opacity-50" : ""}`}
      >
        <span className="block truncate text-sm font-medium" title={operation || label}>{label}</span>
        {body}
      </button>
    </li>
  );
  return (
    <aside className="min-w-0 space-y-2 rounded-lg border border-border p-density-2">
      <h3 className="px-density-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">Root operations</h3>
      <ul className="space-y-0.5">
        {item("", "All operations", <span className="text-muted-foreground">{total.toLocaleString()} {total === 1 ? "trace" : "traces"}</span>)}
        {stats.map(stat => item(stat.operation, shortOperation(stat.operation), (
          <span className="flex items-center gap-1.5 text-muted-foreground">
            <span className="tabular-nums">{stat.count.toLocaleString()} {stat.singleSpan ? "single-span" : stat.count === 1 ? "trace" : "traces"}</span>
            <span className="ml-auto tabular-nums">p50 {duration(stat.p50Ms)} · p95 {duration(stat.p95Ms)}</span>
          </span>
        ), hideSingle && stat.singleSpan))}
      </ul>
      {hideSingle && stats.some(stat => stat.singleSpan) && (
        <p className="px-density-2 text-xs text-muted-foreground">Single-span operations are dimmed while single-span traces are hidden.</p>
      )}
    </aside>
  );
}
