import type { ClickyDocument } from "@flanksource/clicky-ui/clicky";
import { CodeBlock } from "@flanksource/clicky-ui/data";
import { formatDuration } from "@flanksource/clicky-ui/utils";
import type { RecordProfileColumn } from "./recordQuery";

const columns: Record<string, string> = {
  sql: "SQL statement",
  database: "Database",
  operationId: "Request",
  command: "Command",
  elapsedMs: "Elapsed",
  percentComplete: "Progress",
  estimatedRemainingMs: "Est. remaining",
  transactionState: "Transaction state",
};

export function sqlServerTable(table: ClickyDocument): ClickyDocument {
  const byName = new Map(table.node.columns?.map(column => [column.name, column]));
  const visible = Object.entries(columns).flatMap(([name, label]) => {
    const column = byName.get(name);
    if (!column) return [];
    byName.delete(name);
    return [{ ...column, label, defaultHidden: false,
      ...(name !== "sql" && name in sqlServerCellRenderers ? { align: "right" as const } : {}),
      ...(name === "percentComplete" ? { minWidth: 114 } : {}),
      ...(name === "sql" ? { grow: true, minWidth: 340 } : {}),
    }];
  });
  return { ...table, node: { ...table.node, columns: [...visible, ...[...byName.values()].map(column => ({ ...column, defaultHidden: true }))] } };
}

export function sqlServerFilters(profile: RecordProfileColumn[], values: Record<string, string>): RecordProfileColumn[] {
  return profile.flatMap(column => {
    const label = columns[column.name];
    const filter = column.filter;
    if (!label && !(filter && values[filter.key])) return [];
    return [{ ...column, ...(label ? { label } : {}), ...(filter?.lookup ? { filter: { ...filter, lookup: false } } : {}) }];
  });
}

function durationCell(value: unknown) {
  return typeof value === "number" ? <span className="whitespace-nowrap tabular-nums">{formatDuration(value)}</span> : "—";
}

export const sqlServerCellRenderers = {
  sql: (value: unknown) => typeof value === "string" && value ? <CodeBlock inline language="sql" source={value} /> : null,
  elapsedMs: durationCell,
  estimatedRemainingMs: durationCell,
  percentComplete: (value: unknown, row: Record<string, unknown>) => {
    if (typeof value !== "number" || !Number.isFinite(value) || value < 0 || value > 100) return "—";
    const rollback = row.command === "KILLED/ROLLBACK" || row.command === "ROLLBACK" || row.transactionState === "rolling back";
    const percent = value.toLocaleString(undefined, { maximumFractionDigits: 1 });
    return <div className="flex items-center justify-end gap-2">
      <progress aria-label={rollback ? "Rollback progress" : "Request progress"} className="h-1.5 w-14 shrink-0 accent-primary" max={100} value={value} />
      <span className="min-w-10 text-right tabular-nums">{percent}%</span>
    </div>;
  },
};
