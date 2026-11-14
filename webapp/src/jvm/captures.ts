import type { CaptureResult, RunTraces, StartRequest } from "../traces/api";

export interface MethodTarget { class: string; method: string; loader: string }
/** One predicate on a root call's rendered argument (by index) or return value, optionally inside it by a dotted path. */
export interface ValueMatch { on: "arg" | "return"; index: number; path: string; op: "equals" | "contains" | "regex"; value: string }
export interface ProbeOptions { args: boolean; return: boolean; exception: boolean; calls: boolean; platform: boolean; events: number; expand: number; chars: number; gc: boolean; alloc: boolean; minCost: string; exceptionsOnly: boolean; match: ValueMatch[] }
export const defaultProbeOptions: ProbeOptions = { args: true, return: true, exception: true, calls: false, platform: false, events: 1000, expand: 3, chars: 16384, gc: false, alloc: false, minCost: "", exceptionsOnly: false, match: [] };

const goDuration = /^(\d+(\.\d+)?(ns|us|µs|ms|s|m|h))+$/;

/** Why the root-call filters cannot be sent as they are, or undefined when they can. */
export function filterError(options: ProbeOptions): string | undefined {
  if (options.minCost.trim() && !goDuration.test(options.minCost.trim())) return `Minimum cost must be a duration such as 250ms or 1m30s, got "${options.minCost}"`;
  for (const [position, match] of options.match.entries()) {
    const row = `Match ${position + 1}`;
    if (match.on === "arg" && !options.args) return `${row} tests an argument, so record arguments`;
    if (match.on === "return" && !options.return) return `${row} tests the return value, so record return values`;
    if (match.on === "arg" && (!Number.isInteger(match.index) || match.index < 0)) return `${row} needs a non-negative argument index`;
  }
  return undefined;
}

function nativeFilters({ minCost, exceptionsOnly, match }: Pick<ProbeOptions, "minCost" | "exceptionsOnly" | "match">) {
  return {
    ...(minCost.trim() ? { minCost: minCost.trim() } : {}),
    ...(exceptionsOnly ? { exceptionsOnly } : {}),
    ...(match.length ? { match: match.map(({ path, index, ...row }) => ({ ...row, index: row.on === "arg" ? index : 0, ...(path.trim() ? { path: path.trim() } : {}) })) } : {}),
  };
}
export interface Recording { run: RunTraces; capture: CaptureResult }

export function readMethodTargets(search: string): MethodTarget[] {
  const params = new URLSearchParams(search);
  const rows = params.getAll("target").map(value => {
    const match = /^([^|]*)\|([^#]*)#(.*)$/.exec(value);
    if (!match) throw new Error(`Invalid method target: ${value}`);
    return { loader: match[1]!, class: match[2]!, method: match[3]! };
  });
  if (rows.length) return rows;
  return [{ class: params.get("class") ?? "", loader: params.get("loader") ?? "", method: params.get("method") ?? "" }];
}

export function writeMethodTargets(search: string, targets: MethodTarget[]): string {
  const params = new URLSearchParams(search);
  for (const key of ["target", "class", "method", "loader"]) params.delete(key);
  for (const row of targets) params.append("target", `${row.loader}|${row.class}#${row.method}`);
  return params.toString();
}

export function methodCaptureRequest({ agent, targets, options, duration, allowed }: { agent: string; targets: MethodTarget[]; options: ProbeOptions; duration: string; allowed: boolean }): StartRequest {
  if (!agent || !allowed) throw new Error("Allow instrumentation before starting a capture");
  if (!duration.trim()) throw new Error("A capture duration is required");
  if (!targets.length || targets.length > 8 || targets.some(row => !row.class.trim() || !row.method.trim() || row.loader.startsWith("~"))) throw new Error("Choose one to eight complete methods with concrete class loaders");
  const invalid = filterError(options);
  if (invalid) throw new Error(invalid);
  const { chars, minCost, exceptionsOnly, match, ...native } = options;
  return { duration, traces: { jvm_trace: { enabled: true, target: { seam: "agent", agent, allowMutation: allowed }, targets: targets.map(row => ({ class: row.class.trim(), method: row.method.trim(), ...(row.loader ? { loader: row.loader } : {}) })), ...native, "arg-chars": chars, ...nativeFilters({ minCost, exceptionsOnly, match }) } } };
}

export function agentRecordings(runs: RunTraces[], agent: string, kind: string): Recording[] {
  return runs.flatMap(run => (run.captures ?? []).filter(capture => capture.kind === kind && (capture.target?.agent === agent || (capture.target?.seam === "agent" && capture.target.pid !== undefined && String(capture.target.pid) === agent))).map(capture => ({ run, capture })));
}

export function surfaceHref({ agent, from, to, search }: { agent: string; from: string; to: string; search: string }): string {
  const params = new URLSearchParams(search);
  if (from !== to && !(["instrument", "traces"].includes(from) && ["instrument", "traces"].includes(to))) {
    for (const key of ["recording", "recordingFilter", "recordingState", "feedKind", "feedSort", "feedView", "q", "afterSeq", "toSeq", "key"]) params.delete(key);
  }
  return `/jvm/${encodeURIComponent(agent)}/${to}${params.size ? `?${params}` : ""}`;
}
