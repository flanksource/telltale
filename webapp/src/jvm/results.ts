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
}

export function executionNodes(events: MethodEvent[]): ExecutionNode[] {
  const frames = new Map<string, ExecutionNode>();
  const roots: ExecutionNode[] = [];
  for (const event of events) {
    const key = `${event.key ?? ""}:${event.threadId}:${event.frame}`;
    if (event.kind === "enter" || event.kind === "call") {
      const node: ExecutionNode = { id: key, label: event.method ?? event.kind, className: event.class, lineNumber: event.line, status: "running", children: [] };
      frames.set(key, node);
      const parent = event.parent ? frames.get(`${event.key ?? ""}:${event.threadId}:${event.parent}`) : undefined;
      if (parent) parent.children!.push(node); else roots.push(node);
    } else {
      const node = frames.get(key);
      if (node) {
        if (event.error || event.kind === "throw") node.status = "error"; else if (node.status !== "error") node.status = "ok";
        if (event.durationNs !== undefined) node.cost = event.durationNs / 1e6;
      }
    }
  }
  return roots;
}
