import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { TracesPanel } from "./TracesPanel";
import type { RunTraces } from "./api";

const processKey = "9508@1794140150";
const observedAt = "2026-11-08T12:15:51Z";
const run: RunTraces = {
  runId: "recorded-run", mount: "/api/traces/recorded-run", dir: "/recorded-run", status: "succeeded",
  captures: [],
  process: {
    tree: { kind: "process", stream: "process-1" },
    samples: { kind: "process_metrics", stream: "metrics-1" },
    facets: { kind: "process_facets", stream: "facets-1" },
    stacks: { kind: "stacks", stream: "stacks-1" },
  },
};

beforeEach(() => {
  window.history.replaceState(null, "", "/?viewer=1&feedSort=timestamp#capture");
  vi.stubGlobal("fetch", vi.fn(async (url: string) => {
    const request = new URL(url, window.location.origin);
    if (request.pathname.endsWith("/seq")) return Response.json({ seq: request.searchParams.get("stream") === "metrics-1" ? 2 : 0 });
    if (request.pathname.endsWith("/process/export")) return new Response(JSON.stringify({ key: processKey, pid: 9508, ppid: 1, name: "demo", command: "demo --record", startedAt: observedAt, firstObservedAt: observedAt }));
    if (request.pathname.endsWith("/process_metrics/export")) return new Response([
      { entity: processKey, observedAt, tick: 1, cpuPercent: 31, rssBytes: 2048, memoryPercent: 4 },
      { entity: processKey, observedAt: "2026-11-08T12:15:52Z", tick: 2, cpuPercent: 1.6, rssBytes: 4096, memoryPercent: 8 },
    ].map(row => JSON.stringify(row)).join("\n"));
    if (request.pathname.endsWith("/export")) return new Response("");
    if (request.pathname.endsWith("/processes")) return Response.json({ armed: true, rows: [] });
    if (request.pathname.endsWith("/records")) return Response.json({ rows: [], nextCursor: "" });
    return new Response("Unexpected request", { status: 404 });
  }));
});
afterEach(() => vi.unstubAllGlobals());

function renderPanel() {
  return render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
    <TracesPanel run={run} />
  </QueryClientProvider>);
}

describe("run trace layout", () => {
  it("shows a capture-catalog error and lets the user recover the Top tab", async () => {
    let catalog = 0;
    vi.stubGlobal("fetch", vi.fn(async (url: string, init?: RequestInit) => {
      const request = new URL(url, window.location.origin);
      if (request.pathname.endsWith("/kinds")) return ++catalog === 1 ? new Response("Store is busy", { status: 500 }) : Response.json([
        { name: "sql_top", title: "SQL", top: { identity: ["operationId"] } },
      ]);
      if (request.pathname.endsWith("/processes")) return Response.json({ armed: false, rows: [] });
      if (request.searchParams.has("view")) {
        if (request.searchParams.get("view") === "frames") return Response.json([]);
        if ((init?.headers as Record<string, string>)?.Accept === "application/schema+json") return Response.json({ columns: [] });
        return new Response("No complete samples", { status: 409 });
      }
      return Response.json({ rows: [], nextCursor: "" });
    }));
    const recorded: RunTraces = { runId: run.runId, mount: run.mount, dir: run.dir, status: "succeeded", captures: [{ kind: "sql_top", ref: { kind: "sql_top", stream: "sql" } }] };
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><TracesPanel run={recorded} /></QueryClientProvider>);
    expect(await screen.findByRole("alert")).toHaveTextContent("Store is busy");
    fireEvent.click(screen.getByRole("button", { name: "Retry capture catalog" }));
    expect(await screen.findByRole("tab", { name: "Top" })).toHaveAttribute("aria-selected", "true");
  });

  it("keeps protocol Top captures out of the event tabs while retaining the Top view", async () => {
    const topKinds = ["http_top", "sql_top", "jms_top"];
    window.history.replaceState(null, "", "/?viewer=1&runView=events");
    vi.stubGlobal("fetch", vi.fn(async (url: string) => {
      const path = new URL(url, window.location.origin).pathname;
      if (path.endsWith("/kinds")) return Response.json(["http", ...topKinds].map(name => ({ name, title: name, exportFormats: [], ...(topKinds.includes(name) ? { top: { identity: ["id"] } } : {}) })));
      if (path.endsWith("/processes")) return Response.json({ armed: false, rows: [] });
      if (path.endsWith("/records")) return Response.json({ rows: [], nextCursor: "" });
      return new Response("Unexpected request", { status: 404 });
    }));
    const recorded: RunTraces = { runId: run.runId, mount: run.mount, dir: run.dir, status: "succeeded", captures: ["http", ...topKinds].map(kind => ({ kind, ref: { kind, stream: kind } })) };
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><TracesPanel run={recorded} /></QueryClientProvider>);
    await screen.findByRole("tab", { name: "Top" });
    expect(screen.getByRole("tab", { name: /^http(?:\s|$)/ })).toBeInTheDocument();
    for (const kind of topKinds) expect(screen.queryByRole("tab", { name: new RegExp(`^${kind}(?:\\s|$)`) })).not.toBeInTheDocument();
  });

  it("keeps the existing process tree row above the tabs and opens its details on selection", async () => {
    renderPanel();
    const row = await screen.findByRole("treeitem");
    const events = screen.getByRole("tab", { name: "History" });
    expect(row).toHaveTextContent("demo");
    expect(row).toHaveTextContent("pid 9508");
    expect(row).toHaveTextContent("demo --record");
    expect(row.compareDocumentPosition(events) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(screen.getByRole("img", { name: "CPU % over 2 samples" })).toBeInTheDocument();
    expect(screen.getByRole("progressbar", { name: "CPU for pid 9508" })).toHaveAttribute("aria-valuenow", "1.6");
    expect(screen.getByRole("progressbar", { name: "RAM for pid 9508" })).toHaveAttribute("aria-valuenow", "8");
    expect(screen.queryByText("Select a process to view its trace")).not.toBeInTheDocument();
    fireEvent.click(screen.getByText("demo"));
    expect(screen.getByRole("tab", { name: "Process details" })).toHaveAttribute("aria-selected", "true");
    expect(await screen.findByText("Command")).toBeInTheDocument();
    fireEvent.click(events);
    expect(screen.getByRole("treeitem")).toBe(row);
    expect(events).toHaveAttribute("aria-selected", "true");
    expect(screen.queryByText("Command")).not.toBeInTheDocument();
  });

  it("restores the details tab from navigation and preserves the feed and fragment", async () => {
    renderPanel();
    await screen.findByRole("treeitem");
    act(() => {
      window.history.pushState(null, "", "/?viewer=1&feedSort=timestamp&runView=process#capture");
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
    expect(await screen.findByText("Select a process to view its trace")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("tab", { name: "History" }));
    await waitFor(() => expect(new URLSearchParams(window.location.search).has("runView")).toBe(false));
    expect(new URLSearchParams(window.location.search).get("feedSort")).toBe("timestamp");
    expect(window.location.hash).toBe("#capture");
  });
});
