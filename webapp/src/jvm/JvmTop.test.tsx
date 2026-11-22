import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { JvmTop } from "./JvmTop";

afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); window.history.replaceState(null, "", "/"); });

it("shows snapshot metrics, unavailable CPU and deadlocks and freezes on stack inspection", async () => {
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  const frame = { agentId: "fixture", sampledAt: "2026-11-17T10:00:00Z", metrics: {
    runtime: { javaVersion: "21", uptime: 60000 }, processCpuPercent: null,
    memory: { heap: [{ name: "Eden", used: 128, committed: 256, max: -1 }], nonHeap: [], buffer: [] },
    garbageCollectors: [{ name: "Young", collectionCount: 2, collectionTime: 12 }],
  }, threads: { cpuTimeEnabled: false, deadlocked: [7], threads: [{ id: 7, name: "worker", state: "BLOCKED", cpuPercent: null, cpuTimeMs: null, blockedCount: 1, waitedCount: 2, lockOwnerId: null }] } };
  const request = vi.fn(async (url: string) => new Response(JSON.stringify(url.includes("/top") ? { success: true, output: JSON.stringify(frame) } : { thread: frame.threads.threads[0], text: "worker stack", frames: [] })));
  vi.stubGlobal("fetch", request);
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><JvmTop agent="fixture" /></QueryClientProvider>);
  expect(await screen.findByText("worker")).toBeInTheDocument();
  expect(screen.getByText(/Deadlock detected/)).toBeInTheDocument();
  fireEvent.click(screen.getByText("worker"));
  expect(await screen.findByText("worker stack")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Resume live" })).toBeInTheDocument();
  expect(window.location.search).toContain("thread=7");
});

it("surfaces failures while leaving filter controls available", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => new Response("unavailable", { status: 503 })));
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><JvmTop agent="missing" /></QueryClientProvider>);
  expect(await screen.findByRole("alert")).toHaveTextContent("unavailable");
  expect(screen.getByLabelText("Filter threads")).toBeInTheDocument();
});

it("loads a dashboard snapshot when opened with a selected thread in the URL", async () => {
  window.history.replaceState(null, "", "/jvm/fixture/top?thread=7");
  const request = vi.fn(async (_url: string) => new Response("target unavailable", { status: 503 }));
  vi.stubGlobal("fetch", request);
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><JvmTop agent="fixture" /></QueryClientProvider>);
  await waitFor(() => expect(request.mock.calls.some(call => String(call[0]).includes("/api/v1/jvm/top"))).toBe(true));
});
