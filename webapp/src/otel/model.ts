import type { Span } from "./api";

export type SpanRow = Span & { depth: number; hasChildren: boolean };

export function spanRows(spans: Span[], collapsed: Set<string>): SpanRow[] {
  const nodes = new Map<string, Span>();
  for (const span of spans) {
    if (nodes.has(span.spanId))
      throw new Error(`duplicate span ${span.spanId}`);
    nodes.set(span.spanId, span);
  }
  const children = new Map<string, Span[]>();
  for (const span of spans) {
    const parent = nodes.has(span.parentId ?? "") ? span.parentId! : "";
    children.set(parent, [...(children.get(parent) ?? []), span]);
  }
  const order = (a: Span, b: Span) =>
    Date.parse(a.startedAt) - Date.parse(b.startedAt) ||
    a.spanId.localeCompare(b.spanId);
  for (const siblings of children.values()) siblings.sort(order);
  const pending = [...(children.get("") ?? [])]
    .reverse()
    .map((span) => ({ span, depth: 0, visible: true }));
  const rows: SpanRow[] = [];
  const seen = new Set<string>();
  while (pending.length) {
    const current = pending.pop()!;
    if (seen.has(current.span.spanId)) throw new Error("span parent cycle");
    seen.add(current.span.spanId);
    const nested = children.get(current.span.spanId) ?? [];
    if (current.visible)
      rows.push({
        ...current.span,
        depth: current.depth,
        hasChildren: nested.length > 0,
      });
    for (const span of [...nested].reverse())
      pending.push({
        span,
        depth: current.depth + 1,
        visible: current.visible && !collapsed.has(current.span.spanId),
      });
  }
  if (seen.size !== spans.length) throw new Error("span parent cycle");
  return rows;
}
