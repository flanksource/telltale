import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { JvmMetrics } from "./JvmMetrics";

afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); });

it("reuses workspace panes and shared metric loaders while batching the initial snapshot request", async () => {
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  const request = vi.fn(async () => new Response(JSON.stringify({
    capturedAt: "2026-11-13T10:00:00Z",
    runtime: { javaVersion: "21", osName: "Example OS", osVersion: "1", processors: 4, uptime: 60000, systemLoadAverage: 1.5 },
    processCpuPercent: 25,
    memory: { heap: [{ name: "Eden", used: 100, committed: 200, max: -1 }], nonHeap: [], buffer: [] },
    garbageCollectors: [{ name: "Young", collectionCount: 2, collectionTime: 12 }], connectors: [], dataSources: [],
  })));
  vi.stubGlobal("fetch", request);
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><JvmMetrics agent="fixture" /></QueryClientProvider>);
  expect(await screen.findByText("Example OS 1")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Collapse Runtime" })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Collapse Memory pools" })).toBeInTheDocument();
  expect(screen.getByText("Live samples collected while this page is open, up to one hour.")).toBeInTheDocument();
  await waitFor(() => expect(screen.getAllByText("Collecting data…")).toHaveLength(3));
  expect(request).toHaveBeenCalledOnce();
  fireEvent.click(screen.getByRole("button", { name: "Pause polling" }));
  expect(screen.getByRole("button", { name: "Resume polling" })).toBeInTheDocument();
});
