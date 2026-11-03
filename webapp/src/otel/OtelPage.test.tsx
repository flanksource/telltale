import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { RouterProvider, useMemoryRouter } from "@flanksource/clicky-ui/rpc";
import OtelPage from "./OtelPage";
import { matchRoute } from "../routes";
import type { SearchResult } from "./api";

afterEach(() => {
  vi.unstubAllGlobals();
  window.history.replaceState(null, "", "/");
});

const summary = {
  traceId: "8d7616992490c496e2ca9b9d9db42c7f",
  service: "checkout",
  operation: "POST /orders",
  startedAt: "2026-10-29T10:00:00Z",
  durationMs: 100,
  spanCount: 2,
  errors: 1,
  services: ["checkout", "payments"],
};

const searched: SearchResult = {
  traces: [summary],
  total: 1,
  services: ["checkout", "payments"],
  rootOperations: [
    { operation: "POST /orders", count: 1, p50Ms: 100, p95Ms: 100 },
    { operation: "gorm.Query", count: 98, singleSpan: true, p50Ms: 0.4, p95Ms: 2 },
  ],
  attributes: [{ value: "http.method=POST", count: 1 }],
};

const detail = {
  summary,
  missingParents: [],
  spans: [
    { traceId: summary.traceId, spanId: "root", service: "checkout", operation: "POST /orders", startedAt: summary.startedAt, durationMs: 100, status: "ok" },
    {
      traceId: summary.traceId, spanId: "child", parentId: "root", service: "payments", operation: "charge", startedAt: summary.startedAt, durationMs: 40, status: "error",
      attributes: { "http.status_code": 500 }, resource: { replicas: 3 },
      otlp: { resourceSpans: [{ scopeSpans: [{ scope: { name: "example-sdk" }, spans: [{ events: [{ name: "declined" }] }] }] }] },
    },
  ],
};

function respond(url: string): unknown {
  if (url.endsWith("/sources")) return [{ id: "records", name: "Recorded traces", type: "records" }];
  if (url.endsWith("/api/traces?kind=spans")) return { runs: [{ runId: "run-2026-10-29T10-00-00Z" }, { runId: "receiver" }] };
  if (url.endsWith("/form")) return { schema: { type: "object", properties: {} }, defaults: { source: "records", lookback: "1h", limit: 100, minSpans: 2 } };
  if (url.includes("/trace/")) return detail;
  return searched;
}

function serve() {
  const fetchMock = vi.fn((url: string) => Promise.resolve(new Response(JSON.stringify(respond(url)), { headers: { "Content-Type": "application/json" } })));
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

/** The query of the latest search request. */
function lastSearch(fetchMock: ReturnType<typeof serve>): URLSearchParams {
  const url = fetchMock.mock.calls.map(call => call[0]).filter(url => url.startsWith("/api/otel?")).at(-1);
  if (!url) throw new Error("no search request");
  return new URLSearchParams(url.slice("/api/otel?".length));
}

function renderPage(path = "/otel") {
  function Harness() {
    const router = useMemoryRouter(path);
    const route = matchRoute(router.pathname.split("?")[0]!);
    return (
      <RouterProvider adapter={router}>
        <OtelPage traceId={route.page === "otel" ? route.traceId : undefined} />
      </RouterProvider>
    );
  }
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <Harness />
    </QueryClientProvider>,
  );
}

describe("trace search", () => {
  it("searches on load with single-span traces hidden, and lists root operations with their percentiles", async () => {
    const fetchMock = serve();
    renderPage();

    expect(await screen.findByRole("button", { name: /POST \/orders.*p50 100 ms · p95 100 ms/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /gorm\.Query.*98 single-span/ })).toHaveClass("opacity-50");
    expect(Object.fromEntries(lastSearch(fetchMock))).toEqual({ source: "records", lookback: "1h", limit: "100", minSpans: "2" });
    expect(screen.getByText("1 error")).toBeInTheDocument();
    expect(screen.getByRole("figure", { name: "Duration over time" })).toBeInTheDocument();
  });

  it("filters to a root operation chosen from the list, and back to all", async () => {
    const fetchMock = serve();
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: /POST \/orders.*p50/ }));

    await waitFor(() => expect(lastSearch(fetchMock).get("operations")).toBe(JSON.stringify({ "POST /orders": "include" })));
    expect(await screen.findByRole("button", { name: /POST \/orders.*p50/, pressed: true })).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /All operations/ }));
    await waitFor(() => expect(lastSearch(fetchMock).has("operations")).toBe(false));
  });

  it("shows single-span traces when a single-span operation is chosen", async () => {
    const fetchMock = serve();
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: /gorm\.Query/ }));
    await waitFor(() => expect(lastSearch(fetchMock).get("operations")).toBe(JSON.stringify({ "gorm.Query": "include" })));
    expect(lastSearch(fetchMock).has("minSpans")).toBe(false);
  });

  it("searches the receiver run when Received is chosen", async () => {
    const fetchMock = serve();
    renderPage();
    fireEvent.click(await screen.findByRole("radio", { name: "Received" }));
    await waitFor(() => expect(lastSearch(fetchMock).get("run")).toBe("receiver"));
  });

  it("opens a trace's span hierarchy and attributes from its row", async () => {
    serve();
    renderPage();
    fireEvent.click(await screen.findByRole("link", { name: /POST \/orders/ }));
    expect(await screen.findByText("charge")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /charge.*40 ms/ }));
    expect(await screen.findByText("http.status_code")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "OpenTelemetry metadata" }));
    fireEvent.click(screen.getByRole("button", { name: "Expand value" }));
    fireEvent.click(screen.getByRole("button", { name: "Expand scope" }));
    expect(await screen.findByText('"example-sdk"')).toBeInTheDocument();
  });

  it("jumps to a trace by its ID and refuses one that is not", async () => {
    const fetchMock = serve();
    renderPage();
    const input = await screen.findByRole("searchbox", { name: "Trace ID" });
    fireEvent.change(input, { target: { value: "not-a-trace" } });
    fireEvent.click(screen.getByRole("button", { name: "Open" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("A trace ID is 16 or 32 hex characters.");

    fireEvent.change(input, { target: { value: summary.traceId.toUpperCase() } });
    fireEvent.click(screen.getByRole("button", { name: "Open" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(expect.stringContaining(`/api/otel/trace/${summary.traceId}?`), undefined));
  });

  it("refuses a search URL whose filter is not an include/exclude object", async () => {
    serve();
    window.history.replaceState(null, "", `/otel?services=${encodeURIComponent('{"checkout":"only"}')}`);
    renderPage();
    expect(await screen.findByRole("alert")).toHaveTextContent("Invalid trace search URL: services must be a JSON object of values to include or exclude");
  });
});
