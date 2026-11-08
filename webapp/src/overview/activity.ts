import type { OtelSummary, OtelSearchValues } from "@flanksource/clicky-ui/data";
import { searchParams } from "../otel/api";
import type { RunTraces } from "../traces/api";

export type ActivityRow = {
  id: string;
  kind: "otel" | "run";
  title: string;
  detail: string;
  status: string;
  startedAt?: string;
  durationMs: number | null;
  contents: string[];
  href: string;
};

function started(row: ActivityRow) {
  if (!row.startedAt) return -Infinity;
  const time = Date.parse(row.startedAt);
  if (!Number.isFinite(time)) throw new Error(`Invalid activity start time for ${row.id}: ${row.startedAt}`);
  return time;
}

export function activityRows({ runs, traces, values, search }: { runs: RunTraces[]; traces: OtelSummary[]; values: OtelSearchValues; search: string }): ActivityRow[] {
  const rows: ActivityRow[] = runs.filter(run => !run.standing).map(run => {
    const params = new URLSearchParams(search);
    params.set("run", run.runId);
    return {
      id: `run:${run.runId}`, kind: "run", title: run.command?.length ? run.command.join(" ") : run.pid ? `pid ${run.pid}` : "Host run",
      detail: run.runId, status: run.status,
      ...(run.startedAt ? { startedAt: run.startedAt } : {}),
      durationMs: run.startedAt && run.endedAt ? Date.parse(run.endedAt) - Date.parse(run.startedAt) : null,
      contents: [...new Set([...(run.captures ?? []).map(capture => capture.kind), ...(run.process ? ["process"] : [])])],
      href: `/?${params}`,
    };
  });
  for (const trace of traces) rows.push({
    id: `otel:${values.source}/${values.run ?? ""}/${trace.traceId}`, kind: "otel", title: trace.operation,
    detail: `${trace.service} · ${trace.traceId}`, status: trace.errors > 0 ? `${trace.errors} ${trace.errors === 1 ? "error" : "errors"}` : "ok",
    startedAt: trace.startedAt, durationMs: trace.durationMs,
    contents: [`${trace.spanCount} spans`, ...trace.services],
    href: `/otel/${encodeURIComponent(trace.traceId)}?${searchParams(values)}`,
  });
  return rows.sort((a, b) => started(b) - started(a) || a.id.localeCompare(b.id));
}
