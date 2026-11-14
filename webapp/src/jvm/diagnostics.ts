import { call } from "./api";

export interface MBeanAttribute { name: string; type: string; description?: string; readable: boolean; writable: boolean }
export interface MBeanOperation { name: string; returnType: string; description?: string; signature: Array<{ name: string; type: string; description?: string }> }
export interface MBeanInfo { objectName: string; className: string; description?: string; attributes: MBeanAttribute[]; operations: MBeanOperation[]; notifications: Array<{ name: string; types: string[] }> }
export interface ClassLoader { hash: string; label: string; className: string; classCount: number; virtual?: boolean; kind: string }
export interface ClassPage { total: number; offset: number; limit: number; classes: Array<{ name: string; loader: string }> }
export interface ClassSource { class: string; loader: string; source: string; methods: Array<{ name: string; descriptor: string; line: number; endLine: number }>; sourceLineNumbers: number[]; decompiler: string; digest: string }
export interface Compilation { success: boolean; diagnostics: Array<{ kind: string; line: number; column: number; message: string }>; applied?: boolean }
export interface LoggerRow extends Record<string, unknown> { name: string; level: string | null; parent: string | null }
export interface PatchRow extends Record<string, unknown> { class: string; loader: string; patched: boolean; leaseExpiresAt: number; error?: string }
export interface ProbeOwner { key: string; owners: string[] }
export interface ThreadRow extends Record<string, unknown> { id: number; name: string; state: string; cpuPercent: number | null; cpuTimeMs: number | null; lockName: string | null; lockOwnerId: number | null; lockOwnerName: string | null; daemon: boolean }
export interface Threads { sampledMs: number; cpuTimeEnabled: boolean; deadlocked: number[]; threads: ThreadRow[] }
export interface ThreadStack { thread: ThreadRow; text: string; frames: Array<{ className: string; methodName: string; lineNumber: number }> }

export async function inspect<T>(agent: string, operation: string, params: Record<string, unknown> = {}): Promise<T> {
  return await call(agent, operation, params) as T;
}

export const fieldClass = "w-full rounded border border-border bg-background px-2 py-1 text-sm";

export function jvmHref(agent: string, operation: string, selection: Record<string, string>) {
  const params = new URLSearchParams(selection);
  return `/jvm/${encodeURIComponent(agent)}/${operation}${params.size ? `?${params}` : ""}`;
}
