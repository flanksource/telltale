import { useMemo, useState } from "react";
import { AppShell, Button } from "@flanksource/clicky-ui/components";
import { useTheme } from "@flanksource/clicky-ui/hooks";
import { RouterProvider, useBrowserRouter, useRouter } from "@flanksource/clicky-ui/rpc";
import { AppRoutes, matchRoute } from "./routes";
import { CommandSearch } from "./search/CommandSearch";
import TracesPage from "./traces/TracesPage";
import { RunShellContext } from "./layout/run-shell";

function Shell() {
  const { resolvedTheme, setTheme } = useTheme();
  const { pathname } = useRouter();
  const page = matchRoute(pathname).page;
  const params = new URLSearchParams(window.location.search);
  const viewer = params.get("viewer") === "1";
  const framed = viewer || (page === "overview" && !!params.get("run"));
  const [nav, setNav] = useState<HTMLElement | null>(null);
  const [header, setHeader] = useState<HTMLElement | null>(null);
  const [actions, setActions] = useState<HTMLElement | null>(null);
  const targets = useMemo(() => framed ? { nav, header, actions } : null, [framed, nav, header, actions]);
  return <RunShellContext.Provider value={targets}>
    <AppShell
      brand={<span className="font-semibold">Telltale</span>}
      nav={framed ? <div ref={setNav} className="flex min-w-0 items-center gap-3" /> : page === "otel" ? <h1 className="whitespace-nowrap text-sm font-semibold">OpenTelemetry traces</h1> : undefined}
      contentWidth={framed ? "full" : "contained"}
      contentClassName={framed ? "flex min-h-0 flex-col overflow-hidden" : ""}
      bodyHeader={framed ? <div ref={setHeader} className="min-w-0" /> : undefined}
      bodyActions={framed ? <div ref={setActions} className="flex min-w-0 flex-1 flex-wrap items-center justify-between gap-2" /> : undefined}
      bodyHeaderClassName={framed ? "[&>[data-slot=app-shell-body-header-content]]:flex-col [&>[data-slot=app-shell-body-header-content]]:items-stretch" : ""}
      search={viewer ? undefined : <CommandSearch />}
      navSections={viewer ? [] : [{
        items: [
          { key: "overview", label: "Overview", to: "/", active: page === "overview" },
          { key: "definitions", label: "Definitions", to: "/definitions", active: page === "definitions" },
          { key: "top", label: "Top", to: "/top", active: page === "top" || page === "process" },
          { key: "connections", label: "Connections", to: "/connections", active: page === "connections" },
          { key: "agents", label: "Agents", to: "/agents", active: page === "agents" },
          { key: "arm", label: "Arm captures", to: "/arm", active: page === "arm" },
          { key: "query", label: "Query", to: "/query", active: page === "query" || page === "records" },
          { key: "otel", label: "Traces", to: "/otel", active: page === "otel" },
        ],
      }]}
      actions={<Button variant="outline" onClick={() => setTheme(resolvedTheme === "dark" ? "light" : "dark")}>Switch to {resolvedTheme === "dark" ? "light" : "dark"} theme</Button>}
    >
      <div className={framed ? "flex h-full min-h-0 min-w-0 flex-col overflow-hidden p-3" : "space-y-8 px-4 py-12 md:px-8"}>
        {viewer ? <TracesPage /> : <AppRoutes />}
      </div>
    </AppShell>
  </RunShellContext.Provider>;
}

export default function App() {
  const router = useBrowserRouter();
  return <RouterProvider adapter={router}><Shell /></RouterProvider>;
}
