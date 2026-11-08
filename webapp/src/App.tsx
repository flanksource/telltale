import { AppShell, Button } from "@flanksource/clicky-ui/components";
import { useTheme } from "@flanksource/clicky-ui/hooks";
import { RouterProvider, useBrowserRouter, useRouter } from "@flanksource/clicky-ui/rpc";
import { AppRoutes, matchRoute } from "./routes";
import { CommandSearch } from "./search/CommandSearch";
import TracesPage from "./traces/TracesPage";

function Shell() {
  const { resolvedTheme, setTheme } = useTheme();
  const { pathname } = useRouter();
  const page = matchRoute(pathname).page;
  if (new URLSearchParams(window.location.search).get("viewer") === "1") return <AppShell brand="Telltale" contentWidth="contained"><TracesPage /></AppShell>;
  return (
    <AppShell
      brand={<span className="font-semibold">Telltale</span>}
      nav={page === "otel" ? <h1 className="whitespace-nowrap text-sm font-semibold">OpenTelemetry traces</h1> : undefined}
      contentWidth="contained"
      search={<CommandSearch />}
      navSections={[{
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
      <div className="space-y-8 px-4 py-12 md:px-8">
        <AppRoutes />
      </div>
    </AppShell>
  );
}

export default function App() {
  const router = useBrowserRouter();
  return <RouterProvider adapter={router}><Shell /></RouterProvider>;
}
