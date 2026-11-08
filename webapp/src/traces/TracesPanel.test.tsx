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
  it("keeps the existing process tree row above the tabs and opens its details on selection", async () => {
    renderPanel();
    const row = await screen.findByRole("treeitem");
    const events = screen.getByRole("tab", { name: "Events" });
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
    fireEvent.click(screen.getByRole("tab", { name: "Events" }));
    await waitFor(() => expect(new URLSearchParams(window.location.search).has("runView")).toBe(false));
    expect(new URLSearchParams(window.location.search).get("feedSort")).toBe("timestamp");
    expect(window.location.hash).toBe("#capture");
  });
});
