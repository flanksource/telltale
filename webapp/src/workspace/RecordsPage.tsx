import { useQuery } from "@tanstack/react-query";
import { QueryBrowser } from "@flanksource/clicky-ui/data";
import type { JsonSchemaObject } from "@flanksource/clicky-ui/components";
import { useRouter } from "@flanksource/clicky-ui/rpc";
import { fetchJSON } from "@flanksource/clicky-ui/profiles";
import { traceRecordsUrl, type TraceRef } from "../traces/api";
import { executeRecordQuery, recordQueryColumns, type RecordProfileColumn } from "../traces/recordQuery";

export default function RecordsPage({ run, kind, stream }: { run: string; kind: string; stream: string }) {
  const { renderLink } = useRouter();
  const params = new URLSearchParams(window.location.search);
  const mount = `/api/traces/${encodeURIComponent(run)}`;
  const ref: TraceRef = { kind, stream, afterSeq: Number(params.get("afterSeq") ?? 0), toSeq: Number(params.get("toSeq") ?? 0) };
  const url = traceRecordsUrl(mount, ref);
  const schema = useQuery({
    queryKey: ["record-query-schema", url],
    queryFn: async () => {
      const schema = await fetchJSON<{ optionsSchema: JsonSchemaObject; columns: RecordProfileColumn[] }>(url, { headers: { Accept: "application/schema+json" } });
      return { ...schema, columns: recordQueryColumns(schema.columns) };
    }, retry: false,
  });
  return <div className="space-y-4 min-w-0">
    <h1 className="text-2xl font-semibold">Query {kind} records</h1>
    <p className="text-sm text-muted-foreground break-all">{run} · {stream}</p>
    {renderLink({ to: `/?run=${encodeURIComponent(run)}`, children: "Back to trace run", className: "text-sm underline" })}
    {schema.isPending && <p role="status">Loading result profile…</p>}
    {schema.error && <p role="alert">{schema.error.message}</p>}
    {schema.data && <QueryBrowser
      key={url} id={url} title="Records" language="text" queryLabel="Search" allowEmptyQuery
      initialQuery={params.get("q") ?? ""}
      onQueryChange={q => {
        const next = new URLSearchParams(window.location.search);
        if (q) next.set("q", q); else next.delete("q");
        window.history.replaceState(null, "", `${window.location.pathname}${next.size ? `?${next}` : ""}`);
      }}
      optionsSchema={schema.data.optionsSchema}
      execute={request => executeRecordQuery({ mount, ref, columns: schema.data.columns, request })}
      className="h-[70vh] min-h-[32rem]"
    />}
  </div>;
}
