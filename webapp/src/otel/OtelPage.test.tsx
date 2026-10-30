import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { RouterProvider, useMemoryRouter } from "@flanksource/clicky-ui/rpc";
import OtelPage from "./OtelPage";
import { matchRoute } from "../routes";

afterEach(() => {
  vi.unstubAllGlobals();
  window.history.replaceState(null, "", "/");
});
const summary = {
  traceId: "trace-1",
  service: "checkout",
  operation: "POST /orders",
  startedAt: "2026-10-29T10:00:00Z",
  durationMs: 100,
  spanCount: 2,
  errors: 1,
  services: ["checkout", "payments"],
};

it("searches the receiver run of recorded traces and opens a trace's span hierarchy and attributes", async () => {
  const fetchMock = vi.fn((url: string) =>
    Promise.resolve(
      new Response(
        JSON.stringify(
          url.endsWith("/sources")
            ? [{ id: "records", name: "Recorded traces", type: "records" }]
            : url.endsWith("/api/traces?kind=spans")
              ? { runs: [{ runId: "run-2026-10-29T10-00-00Z" }, { runId: "receiver" }] }
              : url.endsWith("/form")
              ? {
                  schema: {
                    type: "object",
                    properties: {
                      service: { type: "string", title: "Service" },
                    },
                  },
                  defaults: { source: "records", lookback: "1h", limit: 100 },
                }
              : url.includes("/trace/")
                ? {
                    summary,
                    missingParents: [],
                    spans: [
                      {
                        traceId: "trace-1",
                        spanId: "root",
                        service: "checkout",
                        operation: "POST /orders",
                        startedAt: summary.startedAt,
                        durationMs: 100,
                        status: "ok",
                      },
                      {
                        traceId: "trace-1",
                        spanId: "child",
                        parentId: "root",
                        service: "payments",
                        operation: "charge",
                        startedAt: summary.startedAt,
                        durationMs: 40,
                        status: "error",
                        attributes: { "http.status_code": 500 },
                        resource: { replicas: 3 },
                        otlp: {
                          resourceSpans: [
                            {
                              scopeSpans: [
                                {
                                  scope: { name: "example-sdk" },
                                  spans: [{ events: [{ name: "declined" }] }],
                                },
                              ],
                            },
                          ],
                        },
                      },
                    ],
                  }
                : {
                    traces: [summary],
                    total: 1,
                    services: ["checkout", "payments"],
                    operations: ["POST /orders"],
                  },
        ),
        { headers: { "Content-Type": "application/json" } },
      ),
    ),
  );
  vi.stubGlobal("fetch", fetchMock);
  function Harness() {
    const router = useMemoryRouter("/otel");
    const route = matchRoute(router.pathname.split("?")[0]!);
    return (
      <RouterProvider adapter={router}>
        <OtelPage traceId={route.page === "otel" ? route.traceId : undefined} />
      </RouterProvider>
    );
  }
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <Harness />
    </QueryClientProvider>,
  );
  fireEvent.change(await screen.findByRole("textbox", { name: "Service" }), {
    target: { value: "checkout" },
  });
  await screen.findByRole("option", { name: "receiver (OTLP receiver)" });
  fireEvent.change(screen.getByRole("combobox", { name: "Run" }), {
    target: { value: "receiver" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Find traces" }));
  expect(await screen.findByText("POST /orders")).toBeInTheDocument();
  await waitFor(() =>
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringMatching(/^\/api\/otel\?(?=.*[?&]run=receiver(&|$))(?=.*[?&]service=checkout(&|$))/),
      undefined,
    ),
  );
  fireEvent.click(screen.getByRole("link", { name: "POST /orders" }));
  expect(await screen.findByText("charge")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Inspect charge" }));
  expect(await screen.findByText("http.status_code")).toBeInTheDocument();
  fireEvent.click(
    screen.getByRole("button", { name: "OpenTelemetry metadata" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Expand value" }));
  fireEvent.click(screen.getByRole("button", { name: "Expand scope" }));
  expect(await screen.findByText('"example-sdk"')).toBeInTheDocument();
});
