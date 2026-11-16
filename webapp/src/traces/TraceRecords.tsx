import { useQuery } from "@tanstack/react-query";
import { QueryBrowser } from "@flanksource/clicky-ui/data";
import type { JsonSchemaObject } from "@flanksource/clicky-ui/components";
import { fetchJSON } from "@flanksource/clicky-ui/profiles";
import { runMount, traceRecordsUrl, type TraceRef } from "./api";
import { executeRecordQuery, recordQueryColumns, type RecordProfileColumn } from "./recordQuery";
import { InvocationView } from "../jvm/InvocationView";

export function TraceRecords({ run, kind, stream }: { run: string; kind: string; stream: string }) {
  const params = new URLSearchParams(window.location.search);
  const mount = runMount(run);
  const ref: TraceRef = { kind, stream, afterSeq: Number(params.get("afterSeq") ?? 0), toSeq: Number(params.get("toSeq") ?? 0) };
  const url = traceRecordsUrl(mount, ref);
  const schema = useQuery({ queryKey: ["record-query-schema", url], queryFn: async () => {
    const profile = await fetchJSON<{ optionsSchema: JsonSchemaObject; columns: RecordProfileColumn[] }>(url, { headers: { Accept: "application/schema+json" } });
    return { ...profile, columns: recordQueryColumns(profile.columns) };
  }, retry: false });
  return <div className="flex h-full min-h-0 min-w-0 flex-col">
    {schema.isPending && <p role="status">Loading result profile…</p>}{schema.error && <p role="alert">{schema.error.message}</p>}
    {schema.data && <QueryBrowser key={url} id={url} title="Records" language="text" queryLabel="Search" allowEmptyQuery initialQuery={params.get("q") ?? ""}
      onQueryChange={q => { const next = new URLSearchParams(window.location.search); if (q) next.set("q", q); else next.delete("q"); window.history.replaceState(null, "", `${window.location.pathname}${next.size ? `?${next}` : ""}`); }}
      optionsSchema={schema.data.optionsSchema} execute={request => executeRecordQuery({ mount, ref, columns: schema.data.columns, request })}
      {...(kind === "jvm_trace" ? { renderResults: ({ result, defaultView }) => <div className="flex min-h-0 flex-1 flex-col"><InvocationView mount={mount} stream={stream} rows={result.rows ?? []} />{defaultView}</div> } : {})}
      className="min-h-0 flex-1"
    />}
  </div>;
}
