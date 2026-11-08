import type { TraceFeedPage, TraceFeedProps, TraceFeedRecord, TraceFeedRequest } from "@flanksource/clicky-ui/data";
import { failure } from "./api";

function feedUrl(mount: string, values: Record<string, string>): string {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(values)) if (value) params.set(key, value);
  return `${mount}/records?${params}`;
}

export function traceFeedSource(mount: string): Pick<TraceFeedProps, "loadPage" | "follow"> {
  const tokens = new Map<string, string>();
  return {
    loadPage: async (request: TraceFeedRequest, signal: AbortSignal): Promise<TraceFeedPage> => {
      const response = await fetch(feedUrl(mount, { ...request, limit: "100" }), { signal });
      if (!response.ok) throw await failure(response, "Read trace feed");
      return response.json() as Promise<TraceFeedPage>;
    },
    follow: ({ kind, onRecord, onError, onEnd }) => {
      const source = new EventSource(feedUrl(mount, { kind, follow: "1", resume: tokens.get(kind) ?? "" }));
      source.onmessage = event => {
        try {
          const record = JSON.parse(event.data) as TraceFeedRecord;
          if (!Number.isSafeInteger(record.seq) || record.seq < 1 || !record.kind || !record.stream || !record.record) throw new Error("Invalid trace feed record");
          onRecord(record);
          tokens.set(kind, event.lastEventId);
        } catch (error) {
          source.close();
          onError(error instanceof Error ? error : new Error(String(error)));
        }
      };
      source.addEventListener("end", () => { source.close(); onEnd(); });
      source.addEventListener("error", event => {
        if (event instanceof MessageEvent) {
          source.close();
          try { onError(new Error((JSON.parse(event.data) as { error: string }).error)); }
          catch (error) { onError(error instanceof Error ? error : new Error(String(error))); }
        } else onError(new Error("Trace feed disconnected; reconnecting"));
      });
      return () => source.close();
    },
  };
}
