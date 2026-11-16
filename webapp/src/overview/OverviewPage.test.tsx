import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { RouterProvider, useBrowserRouter } from "@flanksource/clicky-ui/rpc";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import OverviewPage from "./OverviewPage";
import type { RunTraces } from "../traces/api";

const run: RunTraces = {
  runId: "recorded-build", mount: "/api/traces/recorded-build", dir: "/recorded-build", status: "succeeded",
  command: ["make", "build"], startedAt: "2026-11-08T12:00:00Z", endedAt: "2026-11-08T12:00:03Z", exitCode: 0,
  captures: [{ kind: "http", ref: { kind: "http", stream: "proxy" } }],
};
const trace = {
  traceId: "checkout-trace", service: "checkout", operation: "POST /orders", startedAt: "2026-11-08T12:00:01Z",
  durationMs: 125, spanCount: 4, errors: 1, services: ["checkout", "payments"],
};

function serve({ traceError = false, empty = false } = {}) {
  const fetchMock = vi.fn(async (url: string) => {
    if (url === "/health") return Response.json({ status: "healthy", version: "test" });
    if (url === "/api/otel/form") return Response.json({ schema: {}, defaults: { source: "records", lookback: "1h", minSpans: 2, limit: 100 } });
    if (url === "/api/otel/sources") return Response.json([{ id: "records", name: "Recorded traces", type: "records" }, { id: "remote", name: "Remote telemetry", type: "tempo" }]);
    if (url.startsWith("/api/otel?")) return traceError
      ? new Response("Trace source unavailable", { status: 503 })
      : Response.json({ traces: empty ? [] : [trace], total: empty ? 0 : 1, services: [], rootOperations: [], attributes: [] });
    if (url === "/api/traces") return Response.json({ runs: empty ? [] : [run, { ...run, runId: "receiver", standing: true, status: "running" }] });
    if (url.endsWith("/processes")) return Response.json({ armed: false, rows: [] });
    if (url.includes("/records?")) return Response.json({ rows: [], nextCursor: "" });
    if (url === "/api/traces/start") return Response.json({ schema: { type: "object", required: ["command"], properties: { command: { type: "string", title: "Command" } } }, defaults: { command: "" } });
    if (url === "/api/traces/definitions") return Response.json([]);
    return new Response(`Unexpected request: ${url}`, { status: 404 });
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

function renderPage() {
  function Harness() {
    const router = useBrowserRouter();
    return <RouterProvider adapter={router}><OverviewPage /></RouterProvider>;
  }
  return render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><Harness /></QueryClientProvider>);
}

beforeEach(() => window.history.replaceState(null, "", "/"));
afterEach(() => vi.unstubAllGlobals());

describe("overview activity", () => {
  it("interleaves OpenTelemetry traces and recorded runs newest first with their own detail links", async () => {
    serve();
    renderPage();
    await screen.findByText("POST /orders");
    const rows = screen.getAllByRole("row").slice(1);
    expect(rows).toHaveLength(2);
    expect(rows[0]).toHaveTextContent("POST /orders");
    expect(rows[0]).toHaveTextContent("OpenTelemetry");
    expect(rows[1]).toHaveTextContent("make build");
    expect(rows[1]).toHaveTextContent("Recorded run");
    const links = screen.getAllByRole("link").map(link => link.getAttribute("href"));
    expect(links).toContain("/otel/checkout-trace?source=records&lookback=1h&minSpans=2&limit=100");
    expect(links).toContain("/?run=recorded-build");
    expect(screen.queryByText("receiver")).not.toBeInTheDocument();
  });

  it("persists type and text filters and restores them on navigation without hiding the controls", async () => {
    serve();
    renderPage();
    await screen.findByText("POST /orders");
    fireEvent.click(screen.getByRole("tab", { name: /Recorded runs/ }));
    fireEvent.change(screen.getByRole("searchbox", { name: "Filter activity" }), { target: { value: "build" } });
    expect(screen.getAllByRole("link").some(link => link.getAttribute("href") === "/?activity=run&activityQ=build&run=recorded-build")).toBe(true);
    fireEvent.change(screen.getByRole("searchbox", { name: "Filter activity" }), { target: { value: "missing" } });
    expect(await screen.findByText("No activity matches these filters.")).toBeInTheDocument();
    expect(new URLSearchParams(window.location.search).get("activity")).toBe("run");
    expect(new URLSearchParams(window.location.search).get("activityQ")).toBe("missing");
    act(() => {
      window.history.pushState(null, "", "/?activity=otel&activityQ=checkout");
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
    expect(await screen.findByText("POST /orders")).toBeInTheDocument();
    expect(screen.queryByText("make build")).not.toBeInTheDocument();
    expect(screen.getByRole("searchbox", { name: "Filter activity" })).toHaveValue("checkout");
  });

  it("passes the selected telemetry source and window to search and detail links", async () => {
    const fetchMock = serve();
    renderPage();
    await screen.findByText("POST /orders");
    fireEvent.change(screen.getByRole("combobox", { name: "Trace source" }), { target: { value: "remote" } });
    fireEvent.change(screen.getByRole("combobox", { name: "Trace window" }), { target: { value: "24h" } });
    await waitFor(() => expect(fetchMock.mock.calls.some(([url]) => url.startsWith("/api/otel?") && new URL(url, window.location.origin).searchParams.get("source") === "remote" && new URL(url, window.location.origin).searchParams.get("lookback") === "24h")).toBe(true));
    await waitFor(() => expect(screen.getAllByRole("link").some(link => link.getAttribute("href")?.includes("/otel/checkout-trace?source=remote&lookback=24h"))).toBe(true));
  });

  it("shows recorded runs and an explicit retry when telemetry fails", async () => {
    serve({ traceError: true });
    renderPage();
    expect(await screen.findByRole("alert")).toHaveTextContent("Trace source unavailable");
    expect(await screen.findByText("make build")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Retry OpenTelemetry" })).toBeInTheDocument();
  });

  it("opens the existing run view from a deep link and returns to the overview", async () => {
    window.history.replaceState(null, "", "/?run=recorded-build&activity=run");
    serve();
    renderPage();
    expect(await screen.findByText(/exited with code 0/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Back to overview" })).toHaveAttribute("href", "/?activity=run");
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("link", { name: "Back to overview" }));
    expect(await screen.findByRole("tab", { name: "Recorded runs" })).toHaveAttribute("aria-selected", "true");
    expect(await screen.findByText("make build")).toBeInTheDocument();
  });

  it("keeps filters and the record action available when there is no activity", async () => {
    serve({ empty: true });
    renderPage();
    expect(await screen.findByText("No traces or recorded runs yet.")).toBeInTheDocument();
    expect(screen.getByRole("searchbox", { name: "Filter activity" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Record a run" }));
    expect(await screen.findByRole("textbox", { name: /Command/ })).toBeInTheDocument();
  });
});
