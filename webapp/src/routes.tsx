import { lazy, Suspense } from "react";
import { Panel } from "@flanksource/clicky-ui/components";
import { useRouter } from "@flanksource/clicky-ui/rpc";

const OverviewPage = lazy(() => import("./overview/OverviewPage"));
const TopPage = lazy(() => import("./top/TopPage"));
const ProcessPage = lazy(() => import("./top/ProcessPage"));
const ConnectionsPage = lazy(() => import("./workspace/ConnectionsPage"));
const AgentsPage = lazy(() => import("./workspace/AgentsPage"));
const ArmPage = lazy(() => import("./workspace/ArmPage"));
const QueryPage = lazy(() => import("./workspace/QueryPage"));
const RecordsPage = lazy(() => import("./workspace/RecordsPage"));
const OtelPage = lazy(() => import("./otel/OtelPage"));
const DefinitionsPage = lazy(() => import("./definitions/DefinitionsPage"));

export type Route =
  | { page: "overview" }
  | { page: "definitions"; name?: string }
  | { page: "top" }
  | { page: "process"; target: string }
  | { page: "connections" | "query"; id?: string }
  | { page: "agents" }
  | { page: "arm"; agent?: string; probe?: string }
  | { page: "records"; run: string; kind: string; stream: string }
  | { page: "otel"; traceId?: string }
  | { page: "notFound"; pathname: string };

/** The page a path names: /, /top, or /top/<pid or program name>, the name percent-encoded. */
export function matchRoute(pathname: string): Route {
  const path = pathname.length > 1 ? pathname.replace(/\/+$/, "") : pathname;
  if (path === "/") return { page: "overview" };
  if (path === "/top") return { page: "top" };
  if (path === "/agents") return { page: "agents" };
  if (path === "/arm") return { page: "arm" };
  if (path === "/connections") return { page: "connections" };
  if (path === "/query") return { page: "query" };
  if (path === "/otel") return { page: "otel" };
  if (path === "/definitions") return { page: "definitions" };
  const definition = /^\/definitions\/([^/]+)$/.exec(path);
  const otel = /^\/otel\/([^/]+)$/.exec(path);
  const workspace = /^\/(connections|query)\/([^/]+)$/.exec(path);
  const arm = /^\/arm\/([^/]+)\/([^/]+)$/.exec(path);
  const records = /^\/records\/([^/]+)\/([^/]+)\/([^/]+)$/.exec(path);
  try {
    if (definition) return { page: "definitions", name: decodeURIComponent(definition[1]!) };
    if (otel) return { page: "otel", traceId: decodeURIComponent(otel[1]!) };
    if (workspace) return { page: workspace[1] as "connections" | "query", id: decodeURIComponent(workspace[2]!) };
    if (arm) return { page: "arm", agent: decodeURIComponent(arm[1]!), probe: decodeURIComponent(arm[2]!) };
    if (records) return { page: "records", run: decodeURIComponent(records[1]!), kind: decodeURIComponent(records[2]!), stream: decodeURIComponent(records[3]!) };
  } catch { return { page: "notFound", pathname }; }
  const process = /^\/top\/([^/]+)$/.exec(path);
  if (process) {
    try {
      return { page: "process", target: decodeURIComponent(process[1]!) };
    } catch {
      return { page: "notFound", pathname };
    }
  }
  return { page: "notFound", pathname };
}

/** The page of the current location. */
export function AppRoutes() {
  const { pathname, renderLink } = useRouter();
  const route = matchRoute(pathname);
  switch (route.page) {
    case "definitions": return <Suspense fallback={<p role="status">Loading trace definitions…</p>}><DefinitionsPage {...(route.name ? { name: route.name } : {})} /></Suspense>;
    case "otel": return <Suspense fallback={<p role="status">Loading trace search…</p>}><OtelPage traceId={route.traceId} /></Suspense>;
    case "connections": return <Suspense fallback={<p role="status">Loading connections…</p>}><ConnectionsPage selected={route.id} /></Suspense>;
    case "agents": return <Suspense fallback={<p role="status">Loading agents…</p>}><AgentsPage /></Suspense>;
    case "arm": return <Suspense fallback={<p role="status">Loading arming form…</p>}><ArmPage agent={route.agent} probe={route.probe} /></Suspense>;
    case "query": return <Suspense fallback={<p role="status">Loading query workspace…</p>}><QueryPage selected={route.id} /></Suspense>;
    case "records": return <Suspense fallback={<p role="status">Loading result profile…</p>}><RecordsPage run={route.run} kind={route.kind} stream={route.stream} /></Suspense>;
    case "overview": return <Suspense fallback={<p role="status">Loading overview…</p>}><OverviewPage /></Suspense>;
    case "top": return (
      <Panel title="Process groups">
        <Suspense fallback={<p role="status">Loading process groups…</p>}><TopPage /></Suspense>
      </Panel>
    );
    case "process": return (
      <Panel title="Processes">
        <Suspense fallback={<p role="status">Loading processes…</p>}><ProcessPage target={route.target} /></Suspense>
      </Panel>
    );
    case "notFound": return (
      <div className="space-y-3">
        <p role="alert">No page at {route.pathname}.</p>
        {renderLink({ to: "/", className: "text-sm underline underline-offset-4", children: "Back to the overview" })}
      </div>
    );
  }
}
