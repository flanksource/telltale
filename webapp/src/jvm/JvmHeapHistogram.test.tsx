import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { JvmHeapHistogram } from "./JvmHeapHistogram";

afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); window.history.replaceState(null, "", "/"); });

const histogram = { collectedAt: "2026-11-14T10:00:00Z", live: false, totalInstances: 14340, totalBytes: 1751776, matched: 2, rows: [
  { rank: 3, className: "example.Cache", instances: 40, bytes: 1048576 },
  { rank: 4, className: "example.Order", instances: 300, bytes: 7200, loader: "app" },
] };

it("reads the histogram with the chosen top, class glob and liveness and lists its classes", async () => {
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  const request = vi.fn(async (_url: string, _options?: RequestInit) => new Response(JSON.stringify(histogram)));
  vi.stubGlobal("fetch", request);
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { mutations: { retry: false } } })}><JvmHeapHistogram agent="demo" /></QueryClientProvider>);
  fireEvent.change(screen.getByLabelText("Rows"), { target: { value: "20" } });
  fireEvent.change(screen.getByLabelText("Class pattern"), { target: { value: "example.*" } });
  fireEvent.click(screen.getByLabelText(/Count only live objects/));
  fireEvent.click(screen.getByRole("button", { name: "Read histogram" }));
  await waitFor(() => expect(request).toHaveBeenCalledWith("/api/agents/demo/call", expect.objectContaining({ body: JSON.stringify({ operation: "jvm.heap.histogram", params: { top: 20, classPattern: "example.*", live: true }, allowMutation: false }) })));
  expect(await screen.findByText("example.Cache")).toBeInTheDocument();
  expect(screen.getByText("app")).toBeInTheDocument();
  expect(screen.getByRole("status")).toHaveTextContent("2 classes matched");
  expect(new URLSearchParams(window.location.search).get("classPattern")).toBe("example.*");
});

it("refuses a non-positive row count before calling the agent", () => {
  const request = vi.fn();
  vi.stubGlobal("fetch", request);
  render(<QueryClientProvider client={new QueryClient()}><JvmHeapHistogram agent="demo" /></QueryClientProvider>);
  fireEvent.change(screen.getByLabelText("Rows"), { target: { value: "0" } });
  expect(screen.getByRole("button", { name: "Read histogram" })).toBeDisabled();
  expect(request).not.toHaveBeenCalled();
});
