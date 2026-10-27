import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Button, Tabs } from "@flanksource/clicky-ui/components";
import { ProcessFacetTable, SAMPLE_FACET_ORDER, SAMPLE_FACETS, type ProcessSampleFacet } from "@flanksource/clicky-ui/data";
import { fetchTopFacets, type GroupContext } from "./api";
import { useRefreshInterval } from "../monitoring/RefreshControl";

/** How often a process's facets are read again: each reading runs lsof over it. */
export const FACETS_POLL_MS = 5000;

type FacetTab = "env" | ProcessSampleFacet;

/** Where a process runs and on whose behalf, by name where it has one: its agent session, cmux workspace and surface, and its checkout. */
function ContextList({ context }: { context: GroupContext }) {
  const rows: Array<[string, string, string | undefined]> = [];
  if (context.session) rows.push([`${context.agent ?? "agent"} session`, context.sessionTitle || context.session, context.session]);
  if (context.workspace) rows.push(["cmux workspace", context.workspaceTitle || context.workspace, context.workspace]);
  if (context.surface) rows.push(["cmux surface", context.surfaceTitle || context.surface, context.surface]);
  if (context.repo) rows.push(["Checkout", context.branch ? `${context.repo} @ ${context.branch}` : context.repo, undefined]);
  else if (context.cwd) rows.push(["Directory", context.cwd, undefined]);
  return (
    <div className="space-y-1">
      {rows.length > 0 && (
        <dl aria-label="Context" className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 text-sm">
          {rows.map(([label, value, id]) => (
            <div key={label} className="contents">
              <dt className="text-muted-foreground">{label}</dt>
              <dd className="min-w-0 truncate" title={id}>{value}</dd>
            </div>
          ))}
        </dl>
      )}
      {context.error && <p className="text-xs text-muted-foreground" role="note">Partly read: {context.error}</p>}
    </div>
  );
}

/** One process's context and facets, as process_tree and process_samples record them: its environment (secrets masked) and its open files, sockets, ports and connections, read live every few seconds. */
export function FacetsPanel({ pid }: { pid: number }) {
  const interval = useRefreshInterval();
  const [tab, setTab] = useState<FacetTab>("env");
  const query = useQuery({
    queryKey: ["top-facets", pid],
    queryFn: ({ signal }) => fetchTopFacets(pid, signal),
    retry: false,
    refetchInterval: current => current.state.status === "error" || interval === false ? false : Math.max(FACETS_POLL_MS, interval),
  });
  const facets = query.data;
  const tabs = [
    { id: "env", label: "Environment", count: facets?.environment ? Object.keys(facets.environment).length : 0 },
    ...SAMPLE_FACET_ORDER.map(facet => ({ id: facet, label: SAMPLE_FACETS[facet].label, count: facets?.descriptors[facet].length ?? 0 })),
  ];
  return (
    <section aria-label={`Process ${pid}`} className="space-y-3 rounded-lg border border-border bg-card p-3">
      <div className="flex flex-wrap items-baseline gap-3">
        <h3 className="font-semibold">{facets ? `${facets.name} · pid ${pid}` : `pid ${pid}`}</h3>
        {facets && <span className="text-xs text-muted-foreground">read {new Date(facets.readAt).toLocaleTimeString()}</span>}
      </div>
      {query.isPending && <p role="status">Reading process {pid}…</p>}
      {query.isError && (
        <div className="space-y-2">
          <p role="alert">{query.error.message}</p>
          <Button onClick={() => void query.refetch()}>Retry</Button>
        </div>
      )}
      {facets && (
        <>
          <ContextList context={facets.context} />
          <Tabs tabs={tabs} value={tab} onChange={id => setTab(id as FacetTab)} />
          {tab === "env"
            ? <ProcessFacetTable facet="env" environment={facets.environment} error={facets.environmentError} />
            : <ProcessFacetTable facet={tab} reading={{ rows: facets.descriptors[tab], readAt: facets.readAt }} />}
        </>
      )}
    </section>
  );
}
