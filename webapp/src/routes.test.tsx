import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import { DensityProvider, ThemeProvider } from "@flanksource/clicky-ui/hooks";
import { RouterProvider, useMemoryRouter } from "@flanksource/clicky-ui/rpc";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { AppRoutes, matchRoute } from "./routes";
import { sampleFrame } from "./top/testFrames";

afterEach(() => vi.unstubAllGlobals());

// The pages load lazily; load them once up front so no test waits on the first import.
beforeAll(async () => {
  await Promise.all([import("./Status"), import("./top/TopPage"), import("./top/ProcessPage"), import("./traces/TracesPage")]);
}, 30000);

describe("matchRoute", () => {
  it.each([
    ["/", { page: "overview" }],
    ["/top", { page: "top" }],
    ["/top/", { page: "top" }],
    ["/top/123", { page: "process", target: "123" }],
    ["/top/launchd", { page: "process", target: "launchd" }],
    ["/top/com.apple.WebKit.Networking", { page: "process", target: "com.apple.WebKit.Networking" }],
    ["/top/Google%20Chrome%20Helper%20(GPU)", { page: "process", target: "Google Chrome Helper (GPU)" }],
    ["/top/a%2Fb", { page: "process", target: "a/b" }],
    ["/top/a/b", { page: "notFound", pathname: "/top/a/b" }],
    ["/top/%E0%A4%A", { page: "notFound", pathname: "/top/%E0%A4%A" }],
    ["/nope", { page: "notFound", pathname: "/nope" }],
  ])("reads %s", (pathname, route) => {
    expect(matchRoute(pathname)).toEqual(route);
  });
});

function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
}

const processes = {
  sampledAt: "2026-10-25T10:00:01Z", tick: 1,
  processes: [{ key: "123:1", pid: 123, ppid: 1, name: "node", command: "node server.js", groupKey: "other:user:node", origin: { owner: "moshe", scope: "user" }, processes: 1, rssBytes: 1024 }],
};

function serve() {
  const fetchMock = vi.fn((url: string) => {
    if (url.startsWith("/api/top/processes")) return Promise.resolve(json(processes));
    if (url.startsWith("/api/top/facets")) return Promise.resolve(new Response("pid 123: not listed at the latest tick", { status: 404 }));
    if (url.startsWith("/api/top")) return Promise.resolve(json(sampleFrame()));
    if (url === "/health") return Promise.resolve(json({ status: "healthy", version: "test" }));
    if (url.startsWith("/api/traces")) return Promise.resolve(json({ runs: [] }));
    return Promise.resolve(new Response("", { status: 404 }));
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

function renderAt(initialPath: string) {
  function Harness() {
    const router = useMemoryRouter(initialPath);
    return <RouterProvider adapter={router}><output data-testid="path">{router.pathname}</output><AppRoutes /></RouterProvider>;
  }
  return render(<QueryClientProvider client={new QueryClient()}><Harness /></QueryClientProvider>);
}

describe("AppRoutes", () => {
  beforeEach(() => window.history.replaceState(null, "", "/"));

  it("renders the process page at /top/123, reading the pid", async () => {
    const fetchMock = serve();
    renderAt("/top/123");
    expect(await screen.findByRole("heading", { name: "Process 123 and those under it" })).toBeInTheDocument();
    expect(await screen.findByRole("row", { name: /node server\.js/ })).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith("/api/top/processes?pid=123&all=true", expect.anything());
  });

  it("renders the process page at /top/<name> with the name decoded", async () => {
    const fetchMock = serve();
    renderAt("/top/Google%20Chrome%20Helper");
    expect(await screen.findByRole("heading", { name: "Google Chrome Helper" })).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith("/api/top/processes?name=Google+Chrome+Helper&all=true", expect.anything());
  });

  it("renders the process groups at /top", async () => {
    serve();
    renderAt("/top");
    expect(await screen.findByRole("row", { name: /^claude/ })).toBeInTheDocument();
  });

  it("renders the status and trace runs at /", async () => {
    serve();
    renderAt("/");
    expect(await screen.findByText("Backend connection")).toBeInTheDocument();
    expect(await screen.findByText("Trace runs")).toBeInTheDocument();
    expect(screen.queryByText("Process groups")).not.toBeInTheDocument();
  });

  it("says when no page matches, with a way back", async () => {
    serve();
    renderAt("/nope");
    expect(await screen.findByRole("alert")).toHaveTextContent("No page at /nope.");
    fireEvent.click(screen.getByRole("link", { name: "Back to the overview" }));
    expect(screen.getByTestId("path")).toHaveTextContent("/");
  });

  it("goes from a group's link on /top to its process page", async () => {
    serve();
    renderAt("/top");
    fireEvent.click(await screen.findByRole("link", { name: "vitest" }));
    expect(screen.getByTestId("path")).toHaveTextContent("/top/2");
    expect(await screen.findByRole("heading", { name: "Process 2 and those under it" })).toBeInTheDocument();
  });
});

describe("App", () => {
  it("navigates between Overview and Top in the browser, marking the current one", async () => {
    window.history.replaceState(null, "", "/top/123");
    serve();
    render(
      <ThemeProvider><DensityProvider><QueryClientProvider client={new QueryClient()}><App /></QueryClientProvider></DensityProvider></ThemeProvider>,
    );
    expect(await screen.findByRole("heading", { name: "Process 123 and those under it" })).toBeInTheDocument();
    const top = screen.getAllByRole("link", { name: "Top" })[0]!;
    expect(top).toHaveAttribute("href", "/top");
    expect(top).toHaveClass("bg-sidebar-accent");
    expect(screen.getAllByRole("link", { name: "Overview" })[0]).not.toHaveClass("bg-sidebar-accent");
    fireEvent.click(screen.getAllByRole("link", { name: "Overview" })[0]!);
    expect(window.location.pathname).toBe("/");
    expect(await screen.findByText("Backend connection")).toBeInTheDocument();
  });
});
