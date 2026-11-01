import { keepPreviousData, useQuery } from "@tanstack/react-query";
import type { JsonSchemaObject } from "@flanksource/clicky-ui/components";
import { failure, TRACES_ENDPOINT, type StartFieldError } from "../traces/api";

// The server keeps trace definitions as YAML files in its config dirs and
// serves them under <TRACES_ENDPOINT>/definitions (pkg/definition's store).

export const DEFINITIONS_ENDPOINT = `${TRACES_ENDPOINT}/definitions`;

export const DEFINITION_API_VERSION = "telltale.flanksource.com/v1alpha1";
export const DEFINITION_KIND = "TraceDefinition";

/** The config dir a definition file lives in: the working directory's, the user's, or built in (read-only). */
export type DefinitionLayer = "cwd" | "global" | "builtin";

export interface DefinitionCounts {
  proxy: number;
  instrument: number;
  collect: number;
}

/** One definition as the list shows it. */
export interface DefinitionSummary {
  name: string;
  layer: DefinitionLayer;
  path: string;
  resourceVersion: string;
  description?: string;
  counts: DefinitionCounts;
  /** Why the file could not be read as a definition. */
  problem?: string;
}

/** An inline default connection: a commons-db connection whose credentials are references, never values. */
export interface InlineConnection {
  name?: string;
  type?: string;
  url?: string;
  username?: { valueFrom?: { onePassword?: string } };
  password?: { valueFrom?: { onePassword?: string } };
  [property: string]: unknown;
}

/** A default connection: a connection://<name> reference, or an inline connection. */
export type DefinitionConnection = string | InlineConnection;

export interface DefinitionSpec {
  description?: string;
  every?: string;
  connections?: DefinitionConnection[];
  [section: string]: unknown;
}

/** A TraceDefinition resource. */
export interface TraceDefinition {
  apiVersion: string;
  kind: string;
  metadata: { name: string; labels?: Record<string, string>; [field: string]: unknown };
  spec: DefinitionSpec;
}

/** One capture of a definition with its resolved name and the connection it takes. */
export interface DefinitionEntry {
  /** Its JSON pointer in the resource, such as /spec/collect/prometheus/scrape/0. */
  pointer: string;
  /** Its list's path under spec, such as collect.prometheus.scrape. */
  path: string;
  kind: string;
  name: string;
  defaultName: string;
  connection?: string;
}

/** One stored definition (GET and PUT answer it). */
export interface StoredDefinition {
  name: string;
  layer: DefinitionLayer;
  path: string;
  resourceVersion: string;
  definition: TraceDefinition;
  yaml: string;
  entries: DefinitionEntry[];
}

/** A typed capture list a definition can hold. */
export interface DefinitionList {
  /** Its path under spec, such as collect.prometheus.scrape. */
  path: string;
  section: string;
  /** Empty under proxy, whose lists sit directly in the section. */
  group: string;
  type: string;
  kind: string;
  title: string;
  description?: string;
}

export interface DefinitionLayerInfo {
  name: DefinitionLayer;
  writable: boolean;
  dir: string;
}

/** The CRD schema the editor renders, the lists it offers and the layers it saves to. */
export interface DefinitionSchema {
  schema: JsonSchemaObject;
  lists: DefinitionList[];
  layers: DefinitionLayerInfo[];
}

export interface DefinitionValidation {
  errors: StartFieldError[];
  entries: DefinitionEntry[];
}

/** A connection a definition can reference (telltale.ConnectionInfo). */
export interface ConnectionInfo {
  reference: string;
  name: string;
  namespace?: string;
  type: string;
  source: string;
}

/** A definition the server refused field by field (HTTP 400). */
export class DefinitionInvalid extends Error {
  constructor(readonly errors: StartFieldError[]) {
    super(errors.map(error => (error.instancePath ? `${error.instancePath}: ` : "") + error.message).join("; "));
  }
}

/** A write refused because the file changed on disk since it was read (HTTP 409). */
export class DefinitionConflict extends Error {
  constructor(message: string, readonly resourceVersion: string) {
    super(message);
  }
}

async function readJSON<T>(res: Response, what: string, check: (value: unknown) => boolean, expected: string): Promise<T> {
  const value: unknown = await res.json();
  if (!check(value)) throw new Error(`${what}: expected ${expected}, got ${JSON.stringify(value)}`);
  return value as T;
}

const isObject = (value: unknown): value is Record<string, unknown> => !!value && typeof value === "object" && !Array.isArray(value);

async function refusal(res: Response, what: string): Promise<Error> {
  if (res.status === 400) {
    const body = await readJSON<{ errors: StartFieldError[] }>(res, what, value => isObject(value) && Array.isArray(value.errors), "{errors: [...]}");
    return new DefinitionInvalid(body.errors);
  }
  if (res.status === 409) {
    const body = await readJSON<{ error: string; resourceVersion: string }>(res, what, value => isObject(value) && typeof value.error === "string", "{error, resourceVersion}");
    return new DefinitionConflict(body.error, body.resourceVersion);
  }
  return failure(res, what);
}

const jsonHeaders = { "Content-Type": "application/json", Accept: "application/json" };

export async function fetchDefinitions(): Promise<DefinitionSummary[]> {
  const res = await fetch(DEFINITIONS_ENDPOINT, { headers: { Accept: "application/json" } });
  if (!res.ok) throw await failure(res, "list trace definitions");
  return readJSON(res, "list trace definitions", Array.isArray, "an array");
}

export async function fetchDefinitionSchema(): Promise<DefinitionSchema> {
  const res = await fetch(`${DEFINITIONS_ENDPOINT}/schema`, { headers: { Accept: "application/json" } });
  if (!res.ok) throw await failure(res, "load the trace definition schema");
  return readJSON(res, "load the trace definition schema", value => isObject(value) && isObject(value.schema) && Array.isArray(value.lists) && Array.isArray(value.layers), "{schema, lists, layers}");
}

export async function fetchDefinition(name: string): Promise<StoredDefinition> {
  const res = await fetch(`${DEFINITIONS_ENDPOINT}/${encodeURIComponent(name)}`, { headers: { Accept: "application/json" } });
  if (!res.ok) throw await failure(res, `load trace definition ${name}`);
  return readJSON(res, `load trace definition ${name}`, value => isObject(value) && isObject(value.definition), "a stored definition");
}

/** The static errors and resolved entries of a definition being edited. */
export async function validateDefinition(definition: TraceDefinition, signal?: AbortSignal): Promise<DefinitionValidation> {
  const res = await fetch(`${DEFINITIONS_ENDPOINT}/validate`, {
    method: "POST", headers: jsonHeaders, body: JSON.stringify({ definition }), ...(signal ? { signal } : {}),
  });
  if (!res.ok) throw await failure(res, "validate the trace definition");
  return readJSON(res, "validate the trace definition", value => isObject(value) && Array.isArray(value.errors) && Array.isArray(value.entries), "{errors, entries}");
}

export interface SaveDefinitionRequest {
  name: string;
  layer: DefinitionLayer;
  /** The version the edit started from; empty creates a file the layer does not hold yet. */
  resourceVersion: string;
  definition: TraceDefinition;
}

/** Writes a definition to its layer; throws DefinitionInvalid (400) or DefinitionConflict (409). */
export async function saveDefinition({ name, ...body }: SaveDefinitionRequest): Promise<StoredDefinition> {
  const res = await fetch(`${DEFINITIONS_ENDPOINT}/${encodeURIComponent(name)}`, { method: "PUT", headers: jsonHeaders, body: JSON.stringify(body) });
  if (!res.ok) throw await refusal(res, `save trace definition ${name}`);
  return readJSON(res, `save trace definition ${name}`, value => isObject(value) && isObject(value.definition), "a stored definition");
}

export async function deleteDefinition({ name, layer, resourceVersion }: { name: string; layer: DefinitionLayer; resourceVersion: string }): Promise<void> {
  const query = new URLSearchParams({ layer, resourceVersion });
  const res = await fetch(`${DEFINITIONS_ENDPOINT}/${encodeURIComponent(name)}?${query}`, { method: "DELETE" });
  if (!res.ok) throw await refusal(res, `delete trace definition ${name}`);
}

/** The connections a definition can reference. */
export async function fetchConnections(): Promise<ConnectionInfo[]> {
  const res = await fetch(`${TRACES_ENDPOINT}/connections`, { headers: { Accept: "application/json" } });
  if (!res.ok) throw await failure(res, "list connections");
  const body = await readJSON<{ connections: ConnectionInfo[] }>(res, "list connections", value => isObject(value) && Array.isArray(value.connections), "{connections: [...]}");
  return body.connections;
}

export const definitionKeys = {
  list: ["trace-definitions"] as const,
  schema: ["trace-definition-schema"] as const,
  one: (name: string) => ["trace-definition", name] as const,
  validate: (json: string) => ["trace-definition-validate", json] as const,
  connections: ["trace-connections"] as const,
};

export function useDefinitions() {
  return useQuery({ queryKey: definitionKeys.list, queryFn: fetchDefinitions, retry: false });
}

export function useDefinitionSchema() {
  return useQuery({ queryKey: definitionKeys.schema, queryFn: fetchDefinitionSchema, retry: false, staleTime: Infinity });
}

export function useDefinition(name: string) {
  return useQuery({ queryKey: definitionKeys.one(name), queryFn: () => fetchDefinition(name), retry: false });
}

export function useConnections() {
  return useQuery({ queryKey: definitionKeys.connections, queryFn: fetchConnections, retry: false });
}

/** Validates json (a serialized definition) when it is set; keeps the last answer while the next is read. */
export function useDefinitionValidation(json: string | null) {
  return useQuery({
    queryKey: definitionKeys.validate(json ?? ""),
    queryFn: ({ signal }) => validateDefinition(JSON.parse(json!) as TraceDefinition, signal),
    enabled: json !== null,
    retry: false,
    placeholderData: keepPreviousData,
  });
}
