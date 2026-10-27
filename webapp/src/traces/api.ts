import type { ProcessNodeRow, ProcessSampleRow } from "@flanksource/clicky-ui/data";

// The server lists, starts and stops runs at /api/traces (telltale.Handler),
// and serves each one's trace routes under its mount, /api/traces/<run id>
// (traceserve.Handler).

export const TRACES_ENDPOINT = "/api/traces";

/** A window of a trace stream: the records after afterSeq through toSeq (0 = unbounded) of kind's stream. */
export interface TraceRef {
  kind: string;
  stream: string;
  afterSeq?: number;
  toSeq?: number;
}

/** The streams of a run's process capture. */
export interface ProcessTraceRefs {
  tree: TraceRef;
  samples: TraceRef;
  stacks: TraceRef;
  goprof?: TraceRef;
}

/** One armed capture of a run, with its kind's summary of the stream once the run finished. */
export interface CaptureResult {
  kind: string;
  ref: TraceRef;
  summary?: Record<string, unknown>;
}

export type RunStatus = "running" | "succeeded" | "failed" | "unfinished";

export const RUN_STATUSES: RunStatus[] = ["running", "succeeded", "failed", "unfinished"];

/** One run as the browser reaches it (telltale.MountedRun). */
export interface RunTraces {
  runId: string;
  mount: string;
  dir: string;
  status: RunStatus;
  command?: string[];
  /** The process an attached run traced, in place of a command. */
  pid?: number;
  startedAt?: string;
  endedAt?: string;
  exitCode?: number;
  captures?: CaptureResult[];
  process?: ProcessTraceRefs;
  /** Set while the run is running, with the sessions a checkpoint names. */
  live?: { sessions: { tree: string; samples: string; stacks: string } };
  /** Why an unfinished run has nothing to read. */
  unavailable?: string;
  /** The checkpoints that failed, which fail the run. */
  checkpointErrors?: string[];
}

/** Which runs a listing returns; empty fields keep every run. */
export interface RunFilter {
  q?: string;
  status?: RunStatus | "";
  kind?: string;
}

/** A field of a start request the server refused, at its JSON pointer ("" for the request as a whole). */
export interface StartFieldError {
  instancePath: string;
  message: string;
}

/** A start request the server refused field by field (HTTP 400). */
export class StartRunError extends Error {
  constructor(readonly errors: StartFieldError[]) {
    super(errors.map(error => (error.instancePath ? `${error.instancePath}: ` : "") + error.message).join("; "));
  }
}

/** The start form's JSON schema and the values it starts from (telltale.StartForm). */
export interface StartForm {
  schema: Record<string, unknown>;
  defaults: Record<string, unknown>;
}

/** Where a capture is armed. */
export interface TraceTarget {
  seam?: "self" | "agent" | "process" | "proxy" | "host";
  pid?: number;
  agent?: string;
  selector?: Record<string, string>;
  wait?: boolean;
}

export interface StartRequest extends Record<string, unknown> {
  command?: string;
  facets?: boolean;
  traces?: Record<string, { enabled?: boolean; target?: TraceTarget; [param: string]: unknown }>;
}

export interface TracesStatus {
  runs: RunTraces[];
}

export type ProcessTraceNode = ProcessNodeRow;
export type ProcessSample = ProcessSampleRow;
export type TraceRecord = Record<string, unknown>;

export interface TraceExportFormat {
  name: string;
  ext: string;
  mime: string;
}

export interface TraceKindInfo {
  name: string;
  title: string;
  exportFormats: TraceExportFormat[];
}

// TraceStreamNotFound is a window whose stream has committed no record yet
// (or does not exist): the export answers 404.
export class TraceStreamNotFound extends Error {}

export async function failure(res: Response, what: string): Promise<Error> {
  const text = (await res.text()).trim();
  return new Error(`${what} failed (${res.status})${text ? `: ${text}` : ""}`);
}

/** The run stores the server serves, newest first. */
export async function fetchTraceRuns(filter: RunFilter = {}): Promise<TracesStatus> {
  const query = runFilterQuery(filter);
  const res = await fetch(query ? `${TRACES_ENDPOINT}?${query}` : TRACES_ENDPOINT, { headers: { Accept: "application/json" } });
  if (!res.ok) throw await failure(res, "list trace runs");
  return res.json() as Promise<TracesStatus>;
}

/** The query string of filter's non-empty fields. */
export function runFilterQuery(filter: RunFilter): string {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(filter)) if (value) params.set(key, String(value));
  return params.toString();
}

/** The start form: its schema and the values it starts from. */
export async function fetchStartForm(): Promise<StartForm> {
  const res = await fetch(`${TRACES_ENDPOINT}/start`, { headers: { Accept: "application/json" } });
  if (!res.ok) throw await failure(res, "load the start form");
  return res.json() as Promise<StartForm>;
}

/** Starts the run value (a StartRequest) describes; a refused request throws StartRunError. */
export async function startRun(value: StartRequest): Promise<RunTraces> {
  const res = await fetch(TRACES_ENDPOINT, {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify(value),
  });
  if (res.status === 400) throw new StartRunError((await res.json() as { errors: StartFieldError[] }).errors);
  if (!res.ok) throw await failure(res, "start run");
  return res.json() as Promise<RunTraces>;
}

/** Stops a running run; it still records how it ended. */
export async function stopRun(run: RunTraces): Promise<void> {
  const res = await fetch(run.mount, { method: "DELETE" });
  if (!res.ok) throw await failure(res, "stop run");
}

function windowQuery(ref: TraceRef, extra: Record<string, string> = {}): string {
  const params = new URLSearchParams({ stream: ref.stream });
  if (ref.afterSeq) params.set("afterSeq", String(ref.afterSeq));
  if (ref.toSeq) params.set("toSeq", String(ref.toSeq));
  for (const [key, value] of Object.entries(extra)) params.set(key, value);
  return params.toString();
}

/** The download of ref's window in one of its kind's export formats. */
export function traceExportUrl(mount: string, ref: TraceRef, format: string): string {
  return `${mount}/api/v1/traces/${encodeURIComponent(ref.kind)}/export?${windowQuery(ref, { format })}`;
}

/** Parses an NDJSON body: one record per non-empty line. */
export function parseNdjson(body: string): TraceRecord[] {
  return body.split("\n").filter(line => line.trim() !== "").map(line => JSON.parse(line) as TraceRecord);
}

/** The records of ref's window as its stream holds them: the NDJSON export. */
export async function fetchTraceRecords(mount: string, ref: TraceRef, signal?: AbortSignal): Promise<TraceRecord[]> {
  const res = await fetch(traceExportUrl(mount, ref, "ndjson"), signal ? { signal } : undefined);
  if (res.status === 404) throw new TraceStreamNotFound(`${ref.kind} stream ${ref.stream} has no records`);
  if (!res.ok) throw await failure(res, `export ${ref.kind} window`);
  return parseNdjson(await res.text());
}

/** The highest seq ref's stream has committed; 0 when it has none. */
export async function fetchLatestSeq(mount: string, ref: TraceRef): Promise<number> {
  const query = new URLSearchParams({ stream: ref.stream });
  const res = await fetch(`${mount}/api/v1/traces/${encodeURIComponent(ref.kind)}/seq?${query}`, { headers: { Accept: "application/json" } });
  if (!res.ok) throw await failure(res, `read ${ref.kind} stream`);
  const body = await res.json() as { seq: number };
  return body.seq;
}

/** The trace kinds the store serves, with the formats each exports in. */
export async function fetchTraceKinds(mount: string): Promise<TraceKindInfo[]> {
  const res = await fetch(`${mount}/api/v1/traces/kinds`, { headers: { Accept: "application/json" } });
  if (!res.ok) throw await failure(res, "list trace kinds");
  return res.json() as Promise<TraceKindInfo[]>;
}

export interface CheckpointRequest {
  mount: string;
  session: string;
  /**
   * The kind's checkpoint params: `{key, transport, reason}` takes a stack dump
   * of one process on a `stacks` session, `{key}` forces a full facet reading
   * of one process on a `process_samples` session.
   */
  params: Record<string, string>;
}

/** Has the running session take a reading now, as its kind's checkpoint params describe it. */
export async function checkpointSession({ mount, session, params }: CheckpointRequest): Promise<void> {
  const res = await fetch(`${mount}/api/v1/sessions/${encodeURIComponent(session)}/checkpoint`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(params),
  });
  if (!res.ok) throw await failure(res, "checkpoint");
}
