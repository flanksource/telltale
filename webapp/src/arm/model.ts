/**
 * The arm wizard's model: which kinds can be armed, where each one runs, and
 * the StartRequest (start_request.go) a finished draft becomes.
 *
 * Kinds, their summaries, seams, defaults and the connection types they take
 * are read from the server's start form (GET /api/traces/start). The groups
 * and cost notes are presentation only.
 */
import type { StartForm, StartRequest } from "../traces/api";
import { SERVER_KUBECONFIG, backendForm, backendParams, captureBackend } from "./backends";
import { argumentErrors, connectionTypes } from "./schemaValidation";

export type Seam = "agent" | "process" | "proxy" | "host" | "connection";

export type JsonSchema = {
  type?: string;
  title?: string;
  description?: string;
  default?: unknown;
  properties?: Record<string, JsonSchema>;
  required?: string[];
  allOf?: JsonSchema[];
  if?: JsonSchema;
  then?: JsonSchema;
  else?: JsonSchema;
  oneOf?: JsonSchema[];
  [key: string]: unknown;
};

export type GroupId = "traffic" | "sql" | "sql_server" | "query" | "jvm" | "clicky" | "captain" | "other";

export const GROUPS: { id: GroupId; label: string; description: string }[] = [
  { id: "traffic", label: "Traffic", description: "What the target sends over the wire" },
  { id: "sql", label: "SQL", description: "Statements seen through a proxy or agent, and the queries a server is running" },
  { id: "sql_server", label: "SQL Server", description: "SQL Server internals read through a saved connection; no process needed" },
  { id: "query", label: "Logs/Traces", description: "Logs and spans the application emits or a backend stores" },
  { id: "jvm", label: "JVM", description: "Inside a Java process: profiles, methods, MBeans and messaging" },
  { id: "clicky", label: "Clicky", description: "Probes a connected clicky agent offers" },
  { id: "captain", label: "Captain", description: "Model provider calls" },
  { id: "other", label: "Other", description: "Kinds without a group of their own" },
];

const PRESENTATION: Record<string, { group: GroupId; cost?: string }> = {
  http: { group: "traffic", cost: "bodies to 256KiB" },
  http_proxy: { group: "traffic" },
  http_top: { group: "traffic" },
  sql: { group: "sql" },
  pg_proxy: { group: "sql" },
  sql_top: { group: "sql" },
  sql_deadlock: { group: "sql_server" },
  sql_plancache: { group: "sql_server" },
  sql_xevent: { group: "sql_server", cost: "creates an XE session" },
  logs: { group: "query" },
  spans: { group: "query" },
  query: { group: "query" },
  jvm_jfr: { group: "jvm", cost: "attaches to the JVM" },
  jvm_trace: { group: "jvm", cost: "instruments methods" },
  jvm_notifications: { group: "jvm" },
  jvm_ognl: { group: "jvm" },
  jvm_redefine: { group: "jvm", cost: "changes classes" },
  jms: { group: "jvm" },
  jms_top: { group: "jvm" },
  exec: { group: "clicky" },
  entity: { group: "clicky" },
  task: { group: "clicky" },
  llm: { group: "captain" },
};

export const SEAMS: Record<Seam, { label: string; description: string }> = {
  agent: { label: "Connected agent", description: "A process running the telltale agent" },
  connection: { label: "Saved connection", description: "A server reached through a saved connection" },
  proxy: { label: "Local proxy", description: "The run's command is pointed at a proxy telltale starts" },
  process: { label: "Local process", description: "Attach to a running process by pid" },
  host: { label: "This host", description: "Read from the host's files or backends" },
};

export interface Kind {
  name: string;
  title: string;
  summary: string;
  group: GroupId;
  cost?: string;
  seams: Seam[];
  defaultSeam: Seam;
  /** The saved-connection types the connection seam picks from. */
  connectionTypes: string[];
  /** The kind's params once enabled: the `then` of its if-enabled conditional. */
  params: JsonSchema;
  defaults: Record<string, unknown>;
}

const isSeam = (seam: string): seam is Seam => seam in SEAMS;

function enabledParams(name: string, schema: JsonSchema): JsonSchema {
  const item = schema.oneOf?.find(candidate => candidate.type === "object");
  const branch = item?.allOf?.find(entry => entry.if?.properties?.enabled)?.then;
  if (!branch) throw new Error(`trace kind ${name} has no if-enabled params branch`);
  return { type: "object", ...branch };
}

/** The armable kinds of a start form. Telltale's own process (seam self) is not offered here. */
export function readKinds(form: StartForm): Kind[] {
  const traces = (form.schema as JsonSchema).properties?.traces?.properties;
  if (!traces) throw new Error("start form has no traces.properties");
  const allDefaults = (form.defaults.traces ?? {}) as Record<string, Record<string, unknown>>;
  return Object.entries(traces).flatMap(([name, schema]) => {
    const declared = schema["x-telltale-seams"] as string[] | undefined;
    if (!declared) throw new Error(`trace kind ${name} publishes no x-telltale-seams`);
    if (!schema.description) throw new Error(`trace kind ${name} publishes no summary`);
    const seams = declared.filter(isSeam);
    if (seams.length === 0) return [];
    const params = enabledParams(name, schema);
    const defaults = allDefaults[name] ?? {};
    const preferred = [(defaults.target as { seam?: string } | undefined)?.seam, schema["x-telltale-default-seam"] as string | undefined];
    const defaultSeam = preferred.find((seam): seam is Seam => !!seam && (seams as string[]).includes(seam)) ?? seams[0]!;
    const presentation = PRESENTATION[name] ?? { group: "other" as const };
    return [{
      name,
      title: schema.title ?? name,
      summary: schema.description,
      group: presentation.group,
      ...(presentation.cost ? { cost: presentation.cost } : {}),
      seams,
      defaultSeam,
      connectionTypes: seams.includes("connection") ? connectionTypes(params.properties?.connection) : [],
      params,
      defaults,
    }];
  });
}

export function kindByName(kinds: Kind[], name: string): Kind {
  const kind = kinds.find(k => k.name === name);
  if (!kind) throw new Error(`unknown trace kind ${name}`);
  return kind;
}

/** Keys the wizard's own steps own; a kind's options form never shows them. `connection` stays where it is not a saved connection (sql, logs). */
export function hiddenParamKeys(kind: Kind): string[] {
  return kind.seams.includes("connection") ? ["enabled", "target", "connection"] : ["enabled", "target"];
}

/** `agents` holds the chosen agent ids; a selector matches agents by resource attributes. */
export type AgentChoice = { mode: "id"; agents: string[] } | { mode: "selector"; selector: string; wait: boolean };

export const chosenAgents = (choice: AgentChoice): string[] => (choice.mode === "id" ? choice.agents.filter(Boolean) : []);

export const agentText = (choice: AgentChoice): string => (choice.mode === "id" ? chosenAgents(choice).join(", ") : choice.selector.trim());

export type RunTarget = { mode: "command"; command: string } | { mode: "pid"; pid: number | null } | { mode: "none" };

export type StopMode = "exit" | "duration" | "manual";

/** One armed capture: a kind with its own target and params. A kind may be armed more than once. */
export interface Capture {
  id: string;
  kind: string;
  backend?: string;
  seam: Seam;
  agent: AgentChoice;
  /** A connection:// reference for a connection seam or a host backend. */
  connection: string;
  /** Lets an agent capture change the runtime, such as instrumenting methods; the agent must allow mutations too. */
  allowMutation: boolean;
  params: Record<string, unknown>;
}

export type CapturePatch = Partial<Omit<Capture, "id" | "kind">>;

export interface ArmDraft {
  source: { kind: "captures" } | { kind: "definition"; name: string } | null;
  captures: Capture[];
  run: RunTarget;
  window: { stop: StopMode; duration: string; every: string; facets: boolean };
}

export const EMPTY_DRAFT: ArmDraft = {
  source: null,
  captures: [],
  run: { mode: "command", command: "" },
  window: { stop: "exit", duration: "30s", every: "", facets: false },
};

function nextCaptureId(draft: ArmDraft, kind: string): string {
  const taken = new Set(draft.captures.map(capture => capture.id));
  for (let n = 1; ; n++) {
    const id = n === 1 ? kind : `${kind}-${n}`;
    if (!taken.has(id)) return id;
  }
}

/** A new capture of kind on its default seam with the server's defaults, appended to the draft. */
export function addCapture(draft: ArmDraft, kind: Kind, patch: CapturePatch = {}): ArmDraft {
  const capture: Capture = {
    id: nextCaptureId(draft, kind.name),
    kind: kind.name,
    seam: kind.defaultSeam,
    agent: { mode: "id", agents: [] },
    connection: "",
    allowMutation: false,
    ...patch,
    params: {},
  };
  const backend = captureBackend(capture);
  const fields = backend && backendForm(kind, backend)?.properties;
  const hidden = hiddenParamKeys(kind);
  const defaults = backend?.kind === "spans" ? (kind.defaults.opensearch as Record<string, unknown> | undefined) ?? {} : kind.defaults;
  capture.params = {
    ...Object.fromEntries(Object.entries(defaults).filter(([key]) => backend ? Object.hasOwn(fields ?? {}, key) : !hidden.includes(key))),
    ...patch.params,
  };
  return { ...draft, captures: [...draft.captures, capture] };
}

/**
 * The capture patch with its agent chosen when it runs on an agent, names
 * none, and exactly one agent can run it: the one connected agent offering
 * the probe, else, with none connected, the one deployed target.
 */
export function withOnlyAgent(kind: Kind, patch: CapturePatch, offering: string[], deployed: string[]): CapturePatch {
  const seam = patch.seam ?? kind.defaultSeam;
  if (seam !== "agent" || (patch.agent && chosenAgents(patch.agent).length > 0) || patch.agent?.mode === "selector") return patch;
  const only = offering.length === 1 ? offering[0] : offering.length === 0 && deployed.length === 1 ? deployed[0] : undefined;
  return only ? { ...patch, seam, agent: { mode: "id", agents: [only] } } : patch;
}

/** A copy of a capture, target and params included, under the next free id of its kind. */
export function cloneCapture(draft: ArmDraft, capture: Capture): Capture {
  return { ...structuredClone(capture), id: nextCaptureId(draft, capture.kind) };
}

/** A draft armed for one agent probe, as /arm/<agent>/<probe> opens it. */
export function agentProbeDraft(kinds: Kind[], agent: string, probe: string, params: Record<string, unknown> = {}): ArmDraft {
  const kind = kindByName(kinds, probe);
  if (!kind.seams.includes("agent")) throw new Error(`trace kind ${probe} cannot be armed on an agent (seams: ${kind.seams.join(", ")})`);
  const base: ArmDraft = { ...EMPTY_DRAFT, source: { kind: "captures" }, run: { mode: "none" }, window: { ...EMPTY_DRAFT.window, stop: "manual" } };
  return addCapture(base, kind, { seam: "agent", agent: { mode: "id", agents: [agent] }, params });
}

/** Where a capture runs, in a few words: the connection, the agents, or the seam. */
export function captureTargetText(capture: Capture): string {
  const backend = captureBackend(capture);
  if (capture.seam === "connection" || backend) return capture.connection.replace("connection://", "") || (backend?.provider === "k8s" ? SERVER_KUBECONFIG : "");
  if (capture.seam === "agent") return agentText(capture.agent);
  return SEAMS[capture.seam].label.toLowerCase();
}

/** Whether the run needs a command or pid of its own: a proxy routes a command, a process seam attaches to a pid. */
export function runTargetNeed(draft: ArmDraft): "command" | "pid" | "optional" {
  if (draft.source?.kind === "definition") return "optional";
  const seams = new Set(draft.captures.map(capture => capture.seam));
  if (seams.has("process")) return "pid";
  if (seams.has("proxy")) return "command";
  return "optional";
}

function selectorMap(selector: string): Record<string, string> {
  return Object.fromEntries(selector.split(",").map(pair => pair.split("=").map(part => part.trim())).filter((pair): pair is [string, string] => pair.length === 2 && !!pair[0] && !!pair[1]));
}

/** A target names one agent, so a capture aimed at several agents is one entry per agent. */
function agentTargets(choice: AgentChoice, allowMutation: boolean): Record<string, unknown>[] {
  const mutation = allowMutation ? { allowMutation: true } : {};
  if (choice.mode === "selector") return [{ seam: "agent", selector: selectorMap(choice.selector), ...(choice.wait ? { wait: true } : {}), ...mutation }];
  const agents = chosenAgents(choice);
  return (agents.length ? agents : [""]).map(agent => ({ seam: "agent", agent, ...mutation }));
}

function captureEntries(capture: Capture, kind: Kind): Record<string, unknown>[] {
  const backend = captureBackend(capture);
  const base: Record<string, unknown> = { enabled: true, ...(backend ? backendParams(capture, backend) : capture.params) };
  if (capture.seam === "agent") return agentTargets(capture.agent, capture.allowMutation).map(target => ({ ...base, target }));
  if (capture.seam === "connection") return [{ ...base, connection: capture.connection }];
  return [kind.params.properties?.target ? { ...base, target: { seam: capture.seam } } : base];
}

type Entry = { captureId: string; entry: Record<string, unknown> };

function entriesByKind(draft: ArmDraft, kinds: Kind[]): Map<string, Entry[]> {
  const byKind = new Map<string, Entry[]>();
  for (const capture of draft.captures) {
    const list = byKind.get(capture.kind) ?? [];
    list.push(...captureEntries(capture, kindByName(kinds, capture.kind)).map(entry => ({ captureId: capture.id, entry })));
    byKind.set(capture.kind, list);
  }
  return byKind;
}

/** The request pointer of each capture's first entry: /traces/<kind>, or /traces/<kind>/<i> when the kind is sent as a list. */
export function capturePointers(draft: ArmDraft, kinds: Kind[]): Map<string, string> {
  const pointers = new Map<string, string>();
  for (const [kind, entries] of entriesByKind(draft, kinds)) {
    entries.forEach(({ captureId }, index) => {
      if (!pointers.has(captureId)) pointers.set(captureId, entries.length > 1 ? `/traces/${kind}/${index}` : `/traces/${kind}`);
    });
  }
  return pointers;
}

/** The capture a server refusal at pointer belongs to, for its row to show it. */
export function captureAt(draft: ArmDraft, kinds: Kind[], pointer: string): string | undefined {
  for (const [kind, entries] of entriesByKind(draft, kinds)) {
    if (entries.length === 1 && (pointer === `/traces/${kind}` || pointer.startsWith(`/traces/${kind}/`))) return entries[0]!.captureId;
    const index = new RegExp(`^/traces/${kind}/(\\d+)(/|$)`).exec(pointer)?.[1];
    if (index !== undefined) return entries[Number(index)]?.captureId;
  }
  return undefined;
}

export function buildStartRequest(draft: ArmDraft, kinds: Kind[]): StartRequest {
  const request: StartRequest = {};
  if (draft.source?.kind === "definition") request.definition = draft.source.name;
  if (draft.run.mode === "command" && draft.run.command.trim()) request.command = draft.run.command.trim();
  if (draft.run.mode === "pid" && draft.run.pid !== null) request.pid = draft.run.pid;
  if (draft.window.stop === "duration") request.duration = draft.window.duration.trim();
  if (draft.window.every.trim()) request.every = draft.window.every.trim();
  if (draft.source?.kind === "captures") {
    if (draft.window.facets) request.tree = { facets: true };
    request.traces = Object.fromEntries([...entriesByKind(draft, kinds)].map(([kind, entries]) => [kind, entries.length === 1 ? entries[0]!.entry : entries.map(e => e.entry)]));
  }
  return request;
}

export interface DraftError {
  /** JSON pointer into the StartRequest, as the server's ValidationError reports it. */
  instancePath: string;
  message: string;
  /** The capture the error belongs to, so its row can show it. */
  captureId?: string;
}

const DURATION = /^\d+(ms|s|m|h)$/;

function captureErrors(capture: Capture, kind: Kind, pointer: string): DraftError[] {
  const at = (path: string, message: string): DraftError => ({ instancePath: pointer + path, message, captureId: capture.id });
  if (!kind.seams.includes(capture.seam)) return [at("/target/seam", `${kind.title} cannot run on ${SEAMS[capture.seam].label}.`)];
  if (capture.seam === "connection" && !capture.connection) return [at("/connection", `${kind.title} needs a ${kind.connectionTypes.join(" or ") || "saved"} connection.`)];
  if (capture.seam === "agent" && capture.agent.mode === "id" && chosenAgents(capture.agent).length === 0) return [at("/target/agent", `${kind.title} needs an agent.`)];
  if (capture.seam === "agent" && capture.agent.mode === "selector" && Object.keys(selectorMap(capture.agent.selector)).length === 0) return [at("/target/selector", "An agent selector needs at least one key=value.")];
  const backend = captureBackend(capture);
  return argumentErrors(kind.params, captureEntries(capture, kind)[0]!).map(({ instancePath, message }) =>
    at(instancePath, backend && !capture.connection && instancePath.endsWith("/connection") ? `${backend.title} needs a saved connection.` : message),
  );
}

export function validate(draft: ArmDraft, kinds: Kind[]): DraftError[] {
  if (!draft.source) return [{ instancePath: "", message: "Choose a saved definition or build the captures." }];
  const errors: DraftError[] = [];
  if (draft.source.kind === "captures") {
    if (draft.captures.length === 0) errors.push({ instancePath: "/traces", message: "Add at least one capture." });
    const pointers = capturePointers(draft, kinds);
    for (const capture of draft.captures) errors.push(...captureErrors(capture, kindByName(kinds, capture.kind), pointers.get(capture.id)!));
  }
  const need = runTargetNeed(draft);
  const command = draft.run.mode === "command" && draft.run.command.trim();
  if (need === "command" && !command) errors.push({ instancePath: "/command", message: "A proxy capture needs the command whose traffic it routes." });
  if (need === "pid" && !(draft.run.mode === "pid" && draft.run.pid)) errors.push({ instancePath: "/pid", message: "A process capture needs the pid to attach to." });
  if (draft.run.mode === "pid" && need !== "pid" && !draft.run.pid) errors.push({ instancePath: "/pid", message: "Choose the process to attach to." });
  if (draft.run.mode === "command" && need === "optional" && !command && draft.source.kind === "definition") errors.push({ instancePath: "/command", message: "Enter the command, or choose No process." });
  if (draft.window.stop === "duration" && !DURATION.test(draft.window.duration.trim())) errors.push({ instancePath: "/duration", message: "Duration must look like 30s, 5m or 1h." });
  if (draft.window.every.trim() && !DURATION.test(draft.window.every.trim())) errors.push({ instancePath: "/every", message: "Checkpoint interval must look like 10s or 1m." });
  return errors;
}

export type StepId = "source" | "captures" | "process" | "window" | "review";

/** The step whose input an error path belongs to; none for a path no step edits. */
export function stepForError(error: DraftError): StepId | undefined {
  const path = error.instancePath;
  if (path === "" || path === "/definition") return "source";
  if (path === "/command" || path === "/pid") return "process";
  if (path === "/duration" || path === "/every") return "window";
  if (path === "/tree" || path.startsWith("/tree/")) return "window";
  if (path === "/traces" || path.startsWith("/traces/")) return "captures";
  return undefined;
}

const shellQuote = (text: string) => `'${text.replaceAll("'", `'\\''`)}'`;

/** The CLI line that starts the same run, or why there is none. */
export function cliEquivalent(draft: ArmDraft, kinds: Kind[]): { command: string } | { gap: string } {
  const request = buildStartRequest(draft, kinds);
  const window = [request.duration && `--duration ${request.duration}`, request.every && `--every ${request.every}`].filter(Boolean);
  if (request.definition) {
    return { command: ["telltale run", `--definition ${request.definition}`, request.pid && `--pid ${request.pid}`, ...window, request.command && `-- ${request.command}`].filter(Boolean).join(" ") };
  }
  if (request.pid !== undefined) return { gap: "telltale trace run has no --pid; attaching captures to a running process is server-only today." };
  // --trace is repeatable (internal/cli/trace.go), so a kind armed twice is two flags.
  const traces = Object.entries(request.traces ?? {}).flatMap(([name, entries]) => (Array.isArray(entries) ? entries : [entries]).map(entry => {
    const { enabled: _enabled, ...params } = entry as Record<string, unknown>;
    return Object.keys(params).length ? `--trace ${shellQuote(`${name}:${JSON.stringify(params)}`)}` : `--trace ${name}`;
  }));
  return { command: ["telltale trace run", ...traces, ...window, request.tree?.facets && "--facets", request.command && `-- ${request.command}`].filter(Boolean).join(" ") };
}
