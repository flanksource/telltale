import { useCallback, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { CacheBrowser, type QueryBrowserRequest, type QueryBrowserResult, type DataTableFilterLookupResult } from "@flanksource/clicky-ui/data";
import { ConnectionQueryWorkspace, fetchJSON, mergeProviderOptions, queryBrowserOptionsSchema, useInspection, type BrowserDescriptor, type CatalogNode, type EsSearch, type QueryModeTransition } from "@flanksource/clicky-ui/profiles";
import { useRouter } from "@flanksource/clicky-ui/rpc";
import { useConnectionDashboard } from "@flanksource/clicky-ui/connections";

function QueryWorkspace({ id, descriptor }: { id: string; descriptor: BrowserDescriptor }) {
  const baseUrl = `/api/v1/connection/${encodeURIComponent(id)}/browser`;
  const [query, setQuery] = useState(() => new URLSearchParams(window.location.search).get("q") ?? descriptor.defaultQuery ?? "");
  const [selection, setSelection] = useState<Record<string, unknown>>({});
  const [options, setOptions] = useState<Record<string, unknown>>(descriptor.initialOptions ?? {});
  const [database, setDatabase] = useState("");
  const [search, setSearch] = useState<EsSearch>();
  const inspection = useInspection({
    cacheKey: "query-inspection", id, baseUrl, enabled: descriptor.catalog === true,
    database, fallbackDatabase: String(descriptor.initialOptions?.database ?? ""),
    target: descriptor.target?.kind === "index" ? String(options[descriptor.target.option] ?? "") : "",
    ...(typeof options.targetKind === "string" ? { targetKind: options.targetKind } : {}),
  });
  const sqlite = inspection.data && "driver" in inspection.data && inspection.data.driver === "sqlite";
  const sqlDatabase = sqlite ? "" : inspection.sqlDatabase;
  const activeOptions = useMemo(() => mergeProviderOptions({ layers: [descriptor.initialOptions, selection], database: sqlDatabase, keepTargetKind: true }), [descriptor.initialOptions, selection, sqlDatabase]);
  const execute = useCallback((request: QueryBrowserRequest) => fetchJSON<QueryBrowserResult>(`${baseUrl}/query`, {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ ...request, options: mergeProviderOptions({ layers: [request.options], database: sqlDatabase, keepTargetKind: true }) }),
  }), [baseUrl, sqlDatabase]);
  const changeQuery = (value: string) => {
    setQuery(value);
    const params = new URLSearchParams(window.location.search);
    if (value) params.set("q", value); else params.delete("q");
    window.history.replaceState(null, "", `${window.location.pathname}${params.size ? `?${params}` : ""}`);
  };
  return <ConnectionQueryWorkspace
    id={id} title="Query" descriptor={descriptor} inspection={inspection}
    onDatabaseChange={next => { setDatabase(next); setSelection({}); setOptions({}); }} query={query} onQueryChange={changeQuery}
    options={activeOptions} onOptionsChange={setOptions} optionsSchema={queryBrowserOptionsSchema(descriptor)}
    onCatalogSelect={(node: CatalogNode) => { changeQuery(node.query ?? ""); setSelection(node.options ?? {}); setOptions(node.options ?? {}); }}
    search={search} onSearchChange={(transition: QueryModeTransition) => { setSearch(transition.search); changeQuery(transition.query); }}
    compileBaseUrl={baseUrl} execute={execute}
    lookupFilterValues={request => fetchJSON<DataTableFilterLookupResult>(`${baseUrl}/filters/values`, {
      method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(request),
    })}
    className="min-h-[32rem] h-[70vh]"
  />;
}

function SelectedConnection({ id }: { id: string }) {
  const baseUrl = `/api/v1/connection/${encodeURIComponent(id)}/browser`;
  const descriptor = useQuery({ queryKey: ["query-descriptor", id], queryFn: () => fetchJSON<BrowserDescriptor>(baseUrl), retry: false });
  if (descriptor.isPending) return <p role="status">Loading query capabilities…</p>;
  if (descriptor.error) return <p role="alert">{descriptor.error.message}</p>;
  if (descriptor.data.kind === "cache") return <CacheBrowser baseUrl={baseUrl} className="h-[70vh]" />;
  return <QueryWorkspace key={id} id={id} descriptor={descriptor.data} />;
}

export default function QueryPage({ selected }: { selected?: string }) {
  const { navigate, renderLink } = useRouter();
  const connections = useConnectionDashboard({ apiBase: "/api/v1" });
  return <div className="space-y-4 min-w-0">
    <h1 className="text-2xl font-semibold">Query</h1>
    <p className="text-sm text-muted-foreground">Explore a connection using its provider’s query, catalog, and schema.</p>
    <label className="flex flex-wrap items-center gap-2 text-sm">Connection
      <select className="max-w-full rounded border border-border bg-background px-2 py-1" value={selected ?? ""} onChange={event => navigate(event.target.value ? `/query/${encodeURIComponent(event.target.value)}` : "/query")}>
        <option value="">Choose a connection</option>
        {connections.data?.connections.map(connection => <option key={connection.id} value={connection.id}>{connection.namespace ? `${connection.namespace}/` : ""}{connection.name} ({connection.type})</option>)}
      </select>
    </label>
    {connections.error && <p role="alert">{connections.error.message}</p>}
    {!selected && connections.data?.connections.length === 0 && <p>No connections configured. {renderLink({ to: "/connections", children: "Add a connection", className: "underline" })} to begin querying.</p>}
    {selected && <SelectedConnection key={selected} id={selected} />}
  </div>;
}
