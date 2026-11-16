import { failure, fetchLatestSeq, traceRecordsUrl } from "../traces/api";
import type { MethodEvent } from "./results";

export async function loadInvocation({ mount, stream, seed, signal }: { mount: string; stream: string; seed: MethodEvent; signal: AbortSignal }): Promise<MethodEvent[]> {
  if (!Number.isSafeInteger(seed.threadId) || !Number.isSafeInteger(seed.frame)) throw new Error("Invalid invocation identity");
  const ref = { kind: "jvm_trace", stream, toSeq: await fetchLatestSeq(mount, { kind: "jvm_trace", stream }, signal) };
  if (!Number.isSafeInteger(ref.toSeq) || ref.toSeq <= 0) throw new Error("This capture has no valid committed sequence yet");
  const events: MethodEvent[] = [];
  const cursors = new Set<string>();
  let cursor = "";
  do {
    const where = `row.threadId == ${seed.threadId} && row.key == ${JSON.stringify(seed.key ?? "")}`;
    const response = await fetch(traceRecordsUrl(mount, ref, { where, limit: "500", ...(cursor ? { cursor } : {}) }), { signal, headers: { Accept: "application/json" } });
    if (!response.ok) throw await failure(response, "Load method invocation");
    const page = await response.json() as { rows: MethodEvent[]; nextCursor: string };
    if (!Array.isArray(page.rows) || typeof page.nextCursor !== "string" || page.rows.some(event => !Number.isSafeInteger(event.frame) || !Number.isSafeInteger(event.probeSeq) || event.threadId !== seed.threadId || (event.key ?? "") !== (seed.key ?? ""))) throw new Error("Invocation response contains invalid method identities or pagination");
    events.push(...page.rows);
    cursor = page.nextCursor;
    if (cursor && (events.length >= 10000 || cursors.has(cursor))) throw new Error("Thread snapshot exceeds the 10,000-event viewer limit or has invalid pagination. Use a shorter capture or export the raw records.");
    cursors.add(cursor);
  } while (cursor);
  events.sort((a, b) => a.probeSeq - b.probeSeq);
  const entries = new Map(events.filter(event => event.kind === "enter" || event.kind === "call").map(event => [event.frame, event]));
  let root = entries.get(seed.frame);
  if (!root) throw new Error("Entry event is missing for this invocation; the capture may have dropped events");
  const ancestors = new Set<number>();
  while (root.parent && entries.has(root.parent)) {
    if (ancestors.has(root.frame)) throw new Error("Invocation contains a parent cycle");
    ancestors.add(root.frame);
    root = entries.get(root.parent)!;
  }
  const frames = new Set([root.frame]);
  for (const event of events) if (event.parent && frames.has(event.parent)) frames.add(event.frame);
  return events.filter(event => frames.has(event.frame));
}
