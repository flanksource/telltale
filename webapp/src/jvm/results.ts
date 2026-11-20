import type { ObjectGraphNode, ExecutionNode } from "@flanksource/clicky-ui/data";

export function objectNode(value: unknown, label = "result", id = "$", depth = 0): ObjectGraphNode {
  if (value === null || typeof value !== "object") return { id, label, value: String(value) };
  if (depth > 15) return { id, label, value: "Display depth limit reached" };
  const fields = Object.entries(value);
  return { id, label, children: fields.map(([key, entry]) => objectNode(entry, key, `${id}/${key}`, depth + 1)) };
}

export interface MethodEvent {
  kind: string; frame: number; parent?: number; threadId: number; probeSeq: number;
  key?: string; class?: string; method?: string; durationNs?: number; line?: number; error?: string;
  args?: unknown[]; value?: unknown; truncated?: boolean; thread?: string; descriptor?: string;
  allocBytes?: number; gcCount?: number; gcPauseMs?: number; dropped?: number;
}

export interface MethodNode extends ExecutionNode { args?: unknown[]; value?: unknown; error?: string }

export function graphNode(value: Record<string, unknown>, id = "$"): ObjectGraphNode {
  if (typeof value.label !== "string") throw new Error("Object graph node is missing its label");
  return { id: typeof value.id === "string" ? value.id : id, label: value.label,
    ...Object.fromEntries(["type", "kind", "path", "raw"].flatMap(key => typeof value[key] === "string" ? [[key, value[key]]] : [])),
    ...(value.value === null || ["string", "number", "boolean"].includes(typeof value.value) ? { value: value.value as string | number | boolean | null } : {}),
    ...(Array.isArray(value.children) ? { children: value.children.map((child: unknown, index: number) => { if (!child || typeof child !== "object" || Array.isArray(child)) throw new Error("Object graph child must be an object"); return graphNode(child as Record<string, unknown>, `${id}/${index}`); }) } : {}),
  };
}

export function executionNodes(events: MethodEvent[]): MethodNode[] {
  const frames = new Map<string, MethodNode>();
  const roots: MethodNode[] = [];
  for (const event of [...events].sort((a, b) => a.probeSeq - b.probeSeq)) {
    const key = `${event.key ?? ""}:${event.threadId}:${event.frame}`;
    if (event.kind === "enter" || event.kind === "call") {
      const node: MethodNode = { id: key, label: event.method ?? event.kind, ...(event.class ? { className: event.class } : {}), ...(event.line !== undefined ? { lineNumber: event.line } : {}), status: "warning", detail: { thread: event.thread ? `${event.thread} (${event.threadId})` : String(event.threadId), completeness: "Closing event missing from this snapshot", ...(event.descriptor ? { descriptor: event.descriptor } : {}), ...(event.truncated ? { truncation: "Captured values were truncated" } : {}) }, args: event.args, children: [] };
      frames.set(key, node);
      const parent = event.parent ? frames.get(`${event.key ?? ""}:${event.threadId}:${event.parent}`) : undefined;
      if (parent) parent.children!.push(node); else { if (event.parent) node.detail!.parent = "Parent entry missing from this snapshot"; roots.push(node); }
    } else if (event.kind === "return" || event.kind === "throw" || event.kind === "exit") {
      let node = frames.get(key);
      if (node) delete node.detail!.completeness;
      else {
        node = { id: key, label: event.method ?? event.kind, ...(event.class ? { className: event.class } : {}), detail: { thread: String(event.threadId), completeness: "Entry event missing from this snapshot" } };
        frames.set(key, node);
        roots.push(node);
      }
      if (event.error) node.error = event.error;
      if (event.value !== undefined) node.value = event.value;
      if (event.truncated) node.detail!.truncation = "Captured values were truncated";
      for (const [field, unit] of [["allocBytes", "bytes allocated"], ["gcCount", "GC collections"], ["gcPauseMs", "ms GC pause"], ["dropped", "events dropped"]] as const) if (event[field] !== undefined) node.detail![unit] = String(event[field]);
      if (event.error || event.kind === "throw") node.status = "error"; else if (node.status !== "error") node.status = "ok";
      if (event.durationNs !== undefined) node.cost = event.durationNs / 1e6;
    }
  }
  return roots;
}
