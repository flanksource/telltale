import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, fireEvent, waitFor, within } from "@testing-library/react";
import { RouterProvider, useMemoryRouter } from "@flanksource/clicky-ui/rpc";
import { afterEach, expect, it, vi } from "vitest";
import { JvmInstrument } from "./JvmInstrument";
import { JvmNotifications } from "./JvmNotifications";
import { JvmTraces } from "./JvmTraces";

afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); window.history.replaceState(null, "", "/"); });

it("instruments the selected overload with consent and preserves the recording selection in the URL", async () => {
  window.history.replaceState(null, "", "/jvm/demo/instrument?class=example.Worker&method=compute(J)D&loader=12ab");
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  const request = vi.fn(async (url: string, options?: RequestInit) => {
    if (url.startsWith("/api/traces") && options?.method === "POST") return new Response(JSON.stringify({ runId: "recording", mount: "/api/traces/recording", status: "running", captures: [{ kind: "jvm_trace", target: { seam: "agent", agent: "demo" }, ref: { kind: "jvm_trace", stream: "stream" } }] }));
    if (url.startsWith("/api/traces")) return new Response(JSON.stringify({ runs: [] }));
    const operation = JSON.parse(options?.body as string).operation;
    if (operation === "jvm.loaders") return new Response(JSON.stringify([{ hash: "12ab", label: "Application", classCount: 1 }]));
    if (operation === "jvm.classes") return new Response(JSON.stringify({ total: 1, classes: [{ name: "example.Worker", loader: "12ab" }] }));
    throw new Error(`Unexpected request: ${url} ${operation}`);
  });
  vi.stubGlobal("fetch", request);
  function Harness() { return <RouterProvider adapter={useMemoryRouter(window.location.pathname + window.location.search)}><JvmInstrument agent="demo" /></RouterProvider>; }
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><Harness /></QueryClientProvider>);
  const start = await screen.findByRole("button", { name: "Instrument method" });
  expect(start).toBeDisabled();
  fireEvent.click(screen.getByLabelText("Allow method instrumentation"));
  await waitFor(() => expect(start).toBeEnabled());
  fireEvent.click(screen.getByRole("button", { name: "Add method" }));
  fireEvent.click(screen.getByRole("button", { name: "Remove method 2" }));
  expect(request).not.toHaveBeenCalledWith("/api/traces", expect.objectContaining({ method: "POST" }));
  fireEvent.click(start);
  await waitFor(() => expect(request).toHaveBeenCalledWith("/api/traces", expect.objectContaining({ method: "POST", body: expect.stringContaining('"method":"compute(J)D"') })));
  expect(new URLSearchParams(window.location.search).get("recording")).toBe("recording");
});

it("sends the root-call filters from the instrument form and blocks a match it cannot evaluate", async () => {
  window.history.replaceState(null, "", "/jvm/demo/instrument?class=example.Worker&method=compute&loader=12ab");
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  const request = vi.fn(async (url: string, options?: RequestInit) => {
    if (url.startsWith("/api/traces") && options?.method === "POST") return new Response(JSON.stringify({ runId: "filtered", mount: "/api/traces/filtered", status: "running", captures: [] }));
    if (url.startsWith("/api/traces")) return new Response(JSON.stringify({ runs: [] }));
    const operation = JSON.parse(options?.body as string).operation;
    if (operation === "jvm.loaders") return new Response(JSON.stringify([{ hash: "12ab", label: "Application", classCount: 1 }]));
    return new Response(JSON.stringify({ total: 0, classes: [] }));
  });
  vi.stubGlobal("fetch", request);
  function Harness() { return <RouterProvider adapter={useMemoryRouter(window.location.pathname + window.location.search)}><JvmInstrument agent="demo" /></RouterProvider>; }
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><Harness /></QueryClientProvider>);
  fireEvent.click(screen.getByLabelText("Allow method instrumentation"));
  fireEvent.change(screen.getByLabelText("Minimum cost"), { target: { value: "250ms" } });
  fireEvent.click(screen.getByRole("button", { name: "Add value match" }));
  const match = screen.getByRole("group", { name: "Match 1" });
  fireEvent.change(within(match).getByLabelText("Text"), { target: { value: "SubmitTask" } });
  fireEvent.click(screen.getByLabelText("Arguments"));
  expect(await screen.findByRole("alert")).toHaveTextContent("record arguments");
  const start = screen.getByRole("button", { name: "Instrument method" });
  expect(start).toBeDisabled();
  fireEvent.click(screen.getByLabelText("Arguments"));
  await waitFor(() => expect(start).toBeEnabled());
  fireEvent.click(start);
  await waitFor(() => expect(request).toHaveBeenCalledWith("/api/traces", expect.objectContaining({ method: "POST", body: expect.stringContaining('"minCost":"250ms","match":[{"on":"arg","op":"equals","value":"SubmitTask","index":0}]') })));
});

it("records the MBean handed off from JMX and stops a capture restored from its URL", async () => {
  window.history.replaceState(null, "", "/jvm/demo/notifications?objectName=example:type=Publisher&recording=existing");
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  vi.stubGlobal("EventSource", class { onmessage = null; addEventListener() {} close() {} });
  const existing = { runId: "existing", mount: "/api/traces/existing", status: "running", captures: [{ kind: "jvm_notifications", target: { seam: "agent", agent: "demo" }, ref: { kind: "jvm_notifications", stream: "events" } }] };
  const request = vi.fn(async (url: string, options?: RequestInit) => {
    if (options?.method === "DELETE") return new Response(null, { status: 204 });
    if (url === "/api/traces" && options?.method === "POST") return new Response(JSON.stringify(existing));
    if (url.startsWith("/api/traces/existing/records")) return new Response(JSON.stringify({ rows: [], nextCursor: "" }));
    if (url.startsWith("/api/traces")) return new Response(JSON.stringify({ runs: [existing] }));
    return new Response("[]");
  });
  vi.stubGlobal("fetch", request);
  function Harness() { return <RouterProvider adapter={useMemoryRouter(window.location.pathname + window.location.search)}><JvmNotifications agent="demo" /></RouterProvider>; }
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><Harness /></QueryClientProvider>);
  expect(screen.getByLabelText("MBean object name or pattern")).toHaveValue("example:type=Publisher");
  fireEvent.change(screen.getByLabelText("Notification type prefixes"), { target: { value: "example.created, example.changed" } });
  fireEvent.click(screen.getByRole("button", { name: "Arm recording" }));
  await waitFor(() => expect(request).toHaveBeenCalledWith("/api/traces", expect.objectContaining({ method: "POST", body: JSON.stringify({ duration: "5m", traces: { jvm_notifications: { enabled: true, target: { seam: "agent", agent: "demo" }, mbean: "example:type=Publisher", types: ["example.created", "example.changed"], capacity: 500 } } }) })));
  fireEvent.click(await screen.findByRole("button", { name: "Stop recording" }));
  await waitFor(() => expect(request).toHaveBeenCalledWith("/api/traces/existing", { method: "DELETE" }));
});

it("shows only this JVM's recorded traces and keeps catalog filters visible with no matching rows", async () => {
  window.history.replaceState(null, "", "/jvm/demo/traces");
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  vi.stubGlobal("fetch", vi.fn(async (url: string) => {
    if (!url.startsWith("/api/traces")) return new Response(JSON.stringify({ keys: [] }));
    return new Response(JSON.stringify({ runs: ["demo", "other"].map(agent => ({ runId: `${agent}-capture`, mount: `/api/traces/${agent}-capture`, status: "succeeded", captures: [{ kind: "jvm_trace", target: { seam: "agent", agent }, ref: { kind: "jvm_trace", stream: agent } }] })) }));
  }));
  function Harness() { return <RouterProvider adapter={useMemoryRouter(window.location.pathname)}><JvmTraces agent="demo" /></RouterProvider>; }
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><Harness /></QueryClientProvider>);
  expect(await screen.findByRole("button", { name: "Open recording demo-capture" })).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Open recording other-capture" })).not.toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("Filter recordings"), { target: { value: "no-match" } });
  expect(await screen.findByText("No recordings match this JVM and these filters")).toBeInTheDocument();
  expect(screen.getByLabelText("Recording state")).toBeInTheDocument();
  expect(new URLSearchParams(window.location.search).get("recordingFilter")).toBe("no-match");
});
