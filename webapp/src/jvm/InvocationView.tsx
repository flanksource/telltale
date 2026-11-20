import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Button, Select } from "@flanksource/clicky-ui/components";
import { loadInvocation } from "./invocation";
import { MethodTree } from "./MethodTree";
import type { MethodEvent } from "./results";

export function InvocationView({ mount, stream, rows }: { mount: string; stream: string; rows: Record<string, unknown>[] }) {
  const [selected, setSelected] = useState<MethodEvent>();
  const choices: Array<[string, MethodEvent]> = [...new Map(rows.filter((row): row is Record<string, unknown> & MethodEvent => typeof row.frame === "number" && typeof row.threadId === "number" && typeof row.kind === "string").map(event => [`${event.key}:${event.threadId}:${event.frame}`, event])).entries()];
  if (selected && !choices.some(([id]) => id === `${selected.key}:${selected.threadId}:${selected.frame}`)) choices.unshift([`${selected.key}:${selected.threadId}:${selected.frame}`, selected]);
  const query = useQuery({ queryKey: ["jvm-invocation", mount, stream, selected?.key, selected?.threadId, selected?.frame], queryFn: ({ signal }) => loadInvocation({ mount, stream, seed: selected!, signal }), enabled: !!selected, retry: false });
  return <details open className="max-h-96 shrink-0 overflow-auto border-b p-2"><summary className="cursor-pointer text-sm font-medium">Method invocation</summary>
    <div className="space-y-2 py-2"><label className="block text-sm">Load a call from this result page<Select value={selected ? `${selected.key}:${selected.threadId}:${selected.frame}` : ""} onChange={event => setSelected(choices.find(([id]) => id === event.target.value)?.[1])}><option value="">Choose a call</option>{choices.map(([id, event]) => <option key={id} value={id}>{event.class}.{event.method} · thread {event.threadId} · frame {event.frame}</option>)}</Select></label>
      <p className="text-xs text-muted-foreground">Loads the parent invocation and nested calls across all result pages, independently of search and filters. Missing events and truncated values remain marked.</p>
      {query.isFetching && <p role="status">Loading invocation snapshot…</p>}{query.error && <p role="alert">{query.error.message}</p>}
      {selected && <Button size="sm" variant="outline" disabled={query.isFetching} onClick={() => void query.refetch()}>Refresh invocation</Button>}
      {query.data && <MethodTree events={query.data} />}
    </div>
  </details>;
}
