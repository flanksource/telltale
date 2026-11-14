import type { JsonSchemaObject } from "@flanksource/clicky-ui/components";
import { failure } from "../traces/api";

export interface Operation {
  name: string;
  title: string;
  description: string;
  paramsSchema: JsonSchemaObject;
  resultSchema: JsonSchemaObject;
  mutates: boolean;
  extension?: string;
}

export async function catalog(agent: string): Promise<Operation[]> {
  const response = await fetch(`/api/agents/${encodeURIComponent(agent)}/catalog`);
  if (!response.ok) throw await failure(response, "Load JVM operations");
  return response.json() as Promise<Operation[]>;
}

export async function call(agent: string, operation: string, params: Record<string, unknown>, allowMutation = false): Promise<unknown> {
  const response = await fetch(`/api/agents/${encodeURIComponent(agent)}/call`, {
    method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ operation, params, allowMutation }),
  });
  if (!response.ok) throw await failure(response, operation);
  return response.json() as Promise<unknown>;
}

export function selectedParameters(search: string): Record<string, string> {
  const params = new URLSearchParams(search);
  return Object.fromEntries(["class", "method", "loader", "objectName", "query", "pattern", "key"].flatMap(key => params.has(key) ? [[key, params.get(key)!]] : []));
}

export function persistParameters(value: Record<string, unknown>) {
  const params = new URLSearchParams(window.location.search);
  for (const key of ["class", "method", "loader", "objectName", "query", "pattern", "key"]) {
    if (typeof value[key] === "string" && value[key]) params.set(key, value[key]); else params.delete(key);
  }
  window.history.replaceState(null, "", `${window.location.pathname}${params.size ? `?${params}` : ""}`);
}

export function persistFilter(key: string, value: string) {
  const params = new URLSearchParams(window.location.search);
  if (value) params.set(key, value); else params.delete(key);
  window.history.replaceState(null, "", `${window.location.pathname}${params.size ? `?${params}` : ""}`);
}

export function captureParameters(kind: string | undefined, selection: Record<string, string>): Record<string, unknown> {
  if (kind === "jvm_trace" && selection.class) return { targets: [{ class: selection.class, method: selection.method ?? "*", ...(selection.loader ? { loader: selection.loader } : {}) }] };
  if (kind === "jvm_redefine") return Object.fromEntries(Object.entries(selection).filter(([key]) => ["class", "method", "loader"].includes(key)));
  if (kind === "jvm_ognl" && selection.loader) return { loader: selection.loader };
  if (kind === "jvm_notifications" && selection.objectName) return { mbean: selection.objectName };
  return {};
}

export async function extensionFile(file: File): Promise<string> {
  if (file.size > 16 * 1024 * 1024) throw new Error("Extension exceeds 16 MiB");
  const bytes = new Uint8Array(await file.arrayBuffer());
  let text = "";
  for (let start = 0; start < bytes.length; start += 8192) text += String.fromCharCode(...bytes.subarray(start, start + 8192));
  return btoa(text);
}
