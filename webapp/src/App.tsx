import { lazy, Suspense } from "react";
import { AppShell, Button, Panel } from "@flanksource/clicky-ui/components";
import { useTheme } from "@flanksource/clicky-ui/hooks";

const Status = lazy(() => import("./Status"));

export default function App() {
  const { resolvedTheme, setTheme } = useTheme();
  return (
    <AppShell
      brand={<span className="font-semibold">Telltale</span>}
      contentWidth="contained"
      actions={<Button variant="outline" onClick={() => setTheme(resolvedTheme === "dark" ? "light" : "dark")}>Switch to {resolvedTheme === "dark" ? "light" : "dark"} theme</Button>}
    >
      <div className="space-y-8 px-4 py-12 md:px-8">
        <div className="space-y-3">
          <h1 className="text-3xl font-semibold tracking-tight">Telltale</h1>
          <p className="text-muted-foreground">Your workspace is ready.</p>
        </div>
        <Panel title="Backend connection">
          <Suspense fallback={<p role="status">Connecting to the backend…</p>}><Status /></Suspense>
        </Panel>
        <a className="text-sm underline underline-offset-4" href="/api/openapi.json">View the OpenAPI specification</a>
      </div>
    </AppShell>
  );
}
