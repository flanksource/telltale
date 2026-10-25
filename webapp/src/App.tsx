import { AppShell, Button } from "@flanksource/clicky-ui/components";
import { useTheme } from "@flanksource/clicky-ui/hooks";
import { RouterProvider, useBrowserRouter, useRouter } from "@flanksource/clicky-ui/rpc";
import { AppRoutes, matchRoute } from "./routes";

function Shell() {
  const { resolvedTheme, setTheme } = useTheme();
  const { pathname } = useRouter();
  const page = matchRoute(pathname).page;
  return (
    <AppShell
      brand={<span className="font-semibold">Telltale</span>}
      contentWidth="contained"
      navSections={[{
        items: [
          { key: "overview", label: "Overview", to: "/", active: page === "overview" },
          { key: "top", label: "Top", to: "/top", active: page === "top" || page === "process" },
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
