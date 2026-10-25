import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { parseNdjson, traceExportUrl, type RunTraces, type TracesStatus } from "./api";
import { captureWindows } from "./TracesPanel";
import TracesPage from "./TracesPage";

afterEach(() => vi.unstubAllGlobals());

const mount = "/api/traces/run-2026-10-24T10-00-00Z";

const finished: RunTraces = {
  runId: "run-2026-10-24T10-00-00Z",
  mount,
  run: {
    runId: "run-2026-10-24T10-00-00Z",
    dir: "/work/.telltale/traces/run-2026-10-24T10-00-00Z",
    command: ["curl", "https://example.com"],
    exitCode: 0,
    captures: [{ kind: "http_proxy", ref: { kind: "http_proxy", stream: "proxy-1", toSeq: 2 }, summary: { count: 2 } }],
    process: {
      tree: { kind: "process_tree", stream: "tree-1" },
      samples: { kind: "process_samples", stream: "samples-1" },
      stacks: { kind: "stacks", stream: "stacks-1" },
    },
  },
};

function json(value: unknown): Response {
  return new Response(JSON.stringify(value), { headers: { "Content-Type": "application/json" } });
}

// serve answers the trace list with status and every store route with an empty stream.
function serve(status: TracesStatus) {
  vi.stubGlobal("fetch", vi.fn((url: string) => {
    if (url === "/api/traces") return Promise.resolve(json(status));
    if (url.endsWith("/traces/kinds")) return Promise.resolve(json([{ name: "http_proxy", title: "HTTP proxy traffic", exportFormats: [{ name: "har", ext: "har", mime: "application/json" }] }]));
    if (url.includes("/seq?")) return Promise.resolve(json({ seq: 0 }));
    return Promise.resolve(new Response("", { status: 404 }));
  }));
}

function renderPage() {
  return render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <TracesPage />
    </QueryClientProvider>,
  );
}

describe("trace runs", () => {
  it("tells how to record a run when none is stored", async () => {
    serve({ runs: [] });
    renderPage();
    expect(await screen.findByText(/No runs recorded yet/)).toHaveTextContent("telltale trace run -- <command>");
  });

  it("shows the newest run's command, exit code and captures", async () => {
    serve({ runs: [finished] });
    renderPage();
    expect(await screen.findByText("curl https://example.com")).toBeInTheDocument();
    expect(screen.getByText(/exited with code 0/)).toBeInTheDocument();
    expect(screen.getByRole("option", { name: finished.runId })).toBeInTheDocument();
  });

  it("says why a run that did not finish cannot be shown", async () => {
    serve({ runs: [{ runId: "run-crashed", mount: "/api/traces/run-crashed", unavailable: "the run did not finish: it left no run.json" }] });
    renderPage();
    expect(await screen.findByRole("alert")).toHaveTextContent("left no run.json");
  });

  it("surfaces a failed run listing", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("store unreadable", { status: 500 })));
    renderPage();
    expect(await screen.findByRole("alert")).toHaveTextContent("list trace runs failed (500): store unreadable");
  });
});

describe("trace windows", () => {
  it("names a capture's window in its run, counted from its summary", () => {
    expect(captureWindows(finished)).toEqual([
      { kind: "http_proxy", stream: "proxy-1", toSeq: 2, run_id: finished.runId, count: 2, summary: { count: 2 } },
    ]);
  });

  it("builds a window's export URL under the run's mount", () => {
    expect(traceExportUrl(mount, { kind: "http_proxy", stream: "proxy-1", afterSeq: 3, toSeq: 9 }, "har"))
      .toBe(`${mount}/api/v1/traces/http_proxy/export?stream=proxy-1&afterSeq=3&toSeq=9&format=har`);
  });

  it("reads one record per non-empty NDJSON line", () => {
    expect(parseNdjson('{"seq":1}\n\n{"seq":2}\n')).toEqual([{ seq: 1 }, { seq: 2 }]);
  });
});
