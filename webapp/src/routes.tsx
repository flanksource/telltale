import { lazy, Suspense } from "react";
import { Panel } from "@flanksource/clicky-ui/components";
import { useRouter } from "@flanksource/clicky-ui/rpc";

const Status = lazy(() => import("./Status"));
const TopPage = lazy(() => import("./top/TopPage"));
const ProcessPage = lazy(() => import("./top/ProcessPage"));
const TracesPage = lazy(() => import("./traces/TracesPage"));

export type Route =
  | { page: "overview" }
  | { page: "top" }
  | { page: "process"; target: string }
  | { page: "notFound"; pathname: string };

/** The page a path names: /, /top, or /top/<pid or program name>, the name percent-encoded. */
export function matchRoute(pathname: string): Route {
  const path = pathname.length > 1 ? pathname.replace(/\/+$/, "") : pathname;
  if (path === "/") return { page: "overview" };
  if (path === "/top") return { page: "top" };
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

function Overview() {
  return (
    <div className="space-y-8">
      <div className="space-y-3">
        <h1 className="text-3xl font-semibold tracking-tight">Telltale</h1>
        <p className="text-muted-foreground">Your workspace is ready.</p>
      </div>
      <Panel title="Backend connection">
        <Suspense fallback={<p role="status">Connecting to the backend…</p>}><Status /></Suspense>
      </Panel>
      <Panel title="Trace runs">
        <Suspense fallback={<p role="status">Loading trace runs…</p>}><TracesPage /></Suspense>
      </Panel>
      <a className="text-sm underline underline-offset-4" href="/api/openapi.json">View the OpenAPI specification</a>
    </div>
  );
}

/** The page of the current location. */
export function AppRoutes() {
  const { pathname, renderLink } = useRouter();
  const route = matchRoute(pathname);
  switch (route.page) {
    case "overview": return <Overview />;
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
