import type { TraceFeedPage, TraceFeedProps, TraceFeedRecord, TraceFeedRequest } from "@flanksource/clicky-ui/data";
import { subscribeJSON, type EventSourceFactory } from "@flanksource/clicky-ui/hooks";
import { failure } from "./api";

function feedUrl(mount: string, values: Record<string, string>): string {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(values)) if (value) params.set(key, value);
  return `${mount}/records?${params}`;
}

export function traceFeedSource(mount: string, open: EventSourceFactory): Pick<TraceFeedProps, "loadPage" | "follow"> {
  const tokens = new Map<string, string>();
  return {
    loadPage: async (request: TraceFeedRequest, signal: AbortSignal): Promise<TraceFeedPage> => {
      const response = await fetch(feedUrl(mount, { ...request, excludeKind: "process", presentation: "ansi", limit: "100", tail: "1" }), { signal });
      if (!response.ok) throw await failure(response, "Read trace feed");
      const page = await response.json() as TraceFeedPage;
      if (!signal.aborted && !request.cursor && page.resume) tokens.set(request.kind, page.resume);
      return page;
    },
    follow: ({ kind, onRecord, onError, onOpen, onEnd }) => {
      return subscribeJSON<TraceFeedRecord>(open, feedUrl(mount, { kind, excludeKind: "process", presentation: "ansi", follow: "1", resume: tokens.get(kind) ?? "" }), {
        onData: (record, id) => {
          if (!Number.isSafeInteger(record.seq) || record.seq < 1 || !record.kind || !record.stream || !record.record) throw new Error("Invalid trace feed record");
          onRecord(record);
          tokens.set(kind, id);
        },
        onError,
        onOpen,
        onEnd,
      });
    },
  };
}
