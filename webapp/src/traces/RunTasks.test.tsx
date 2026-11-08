import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { TracesPanel } from "./TracesPanel";
import type { RunTraces } from "./api";

const run: RunTraces = {
  runId: "clicky-run", mount: "/api/traces/clicky-run", dir: "/clicky-run", status: "succeeded",
  captures: [{ kind: "task", ref: { kind: "task", stream: "task-1" } }],
};

const records = [
  { seq: 1, phase: "status", id: "t1", name: "deploy api", type: "task", status: "running" },
  { seq: 2, phase: "progress", id: "t1", name: "deploy api", type: "task", status: "running", progress: 1, maxValue: 2 },
  { seq: 3, phase: "status", id: "t1", name: "deploy api", type: "task", status: "success", progress: 2, maxValue: 2 },
  { seq: 4, phase: "status", id: "t2", name: "migrate db", type: "task", status: "failed", error: "lock timeout" },
];

beforeEach(() => {
  window.history.replaceState(null, "", "/");
  vi.stubGlobal("fetch", vi.fn(async (url: string) => {
    const request = new URL(url, window.location.origin);
    if (request.pathname.endsWith("/task/export")) return new Response(records.map(row => JSON.stringify(row)).join("\n"));
    if (request.pathname.endsWith("/processes")) return Response.json({ armed: false, rows: [] });
    if (request.pathname.endsWith("/records")) return Response.json({ rows: [], nextCursor: "" });
    return new Response("Unexpected request", { status: 404 });
  }));
});
afterEach(() => vi.unstubAllGlobals());

describe("a run with a clicky task capture", () => {
  it("shows its tasks at their latest records in the clicky-ui task viewer", async () => {
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <TracesPanel run={run} />
    </QueryClientProvider>);
    fireEvent.click(screen.getByRole("tab", { name: "Tasks" }));
    const panel = await screen.findByRole("tabpanel", { name: "Tasks" });
    expect(await screen.findByText("deploy api")).toBeInTheDocument();
    expect(panel).toHaveTextContent("migrate db");
    expect(panel).toHaveTextContent("lock timeout");
    expect(window.location.search).toContain("runView=tasks");
  });
});
