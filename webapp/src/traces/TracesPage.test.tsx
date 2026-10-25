import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { parseNdjson, runFilterQuery, traceExportUrl, type RunTraces, type StartForm, type TracesStatus } from "./api";
import { sampleFrame } from "../top/testFrames";
import { captureWindows } from "./TracesPanel";
import TracesPage from "./TracesPage";

beforeEach(() => window.history.replaceState(null, "", "/"));
afterEach(() => vi.unstubAllGlobals());

const runId = "run-2026-10-24T10-00-00Z";
const mount = `/api/traces/${runId}`;
const processRefs = {
  tree: { kind: "process_tree", stream: "tree-1" },
  samples: { kind: "process_samples", stream: "samples-1" },
  stacks: { kind: "stacks", stream: "stacks-1" },
};

const finished: RunTraces = {
  runId,
  mount,
  dir: `/work/.telltale/traces/${runId}`,
  status: "succeeded",
  command: ["curl", "https://example.com"],
  exitCode: 0,
  captures: [{ kind: "http_proxy", ref: { kind: "http_proxy", stream: "proxy-1", toSeq: 2 }, summary: { count: 2 } }],
  process: processRefs,
};

const running: RunTraces = {
  runId: "run-2026-10-24T11-00-00Z",
  mount: "/api/traces/run-2026-10-24T11-00-00Z",
  dir: "/work/.telltale/traces/run-2026-10-24T11-00-00Z",
  status: "running",
  command: ["sh", "-c", "sleep 60"],
  captures: [],
  process: processRefs,
  live: { sessions: { tree: "s-tree", samples: "s-samples", stacks: "s-stacks" } },
};

const startForm: StartForm = {
  schema: { type: "object", required: ["command"], properties: { command: { type: "string", title: "Command" } } },
  defaults: { command: "" },
};

function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
}

// serve answers the run list with status, the start form with startForm, a
// start with started, and every store route with an empty stream.
function serve(status: TracesStatus, started: Response = json(running, 201)) {
  const fetchMock = vi.fn((url: string, init?: RequestInit) => {
    if (url === "/api/traces" && init?.method === "POST") return Promise.resolve(started);
    if (url === "/api/traces" || url.startsWith("/api/traces?")) return Promise.resolve(json(status));
    if (url === "/api/traces/start") return Promise.resolve(json(startForm));
    if (init?.method === "DELETE") return Promise.resolve(new Response(null, { status: 202 }));
    if (url.endsWith("/traces/kinds")) return Promise.resolve(json([{ name: "http_proxy", title: "HTTP proxy traffic", exportFormats: [{ name: "har", ext: "har", mime: "application/json" }] }]));
    if (url.includes("/seq?")) return Promise.resolve(json({ seq: 0 }));
    return Promise.resolve(new Response("", { status: 404 }));
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

function renderPage() {
  return render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <TracesPage />
    </QueryClientProvider>,
  );
}

describe("trace runs", () => {
  it("tells how to start or record a run when none is stored", async () => {
    serve({ runs: [] });
    renderPage();
    expect(await screen.findByText(/No runs recorded yet/)).toHaveTextContent("telltale trace run -- <command>");
  });

  it("shows the newest run's command, exit code and captures", async () => {
    serve({ runs: [finished] });
    renderPage();
    expect(await screen.findByText("curl https://example.com")).toBeInTheDocument();
    expect(screen.getByText(/exited with code 0/)).toBeInTheDocument();
    expect(screen.getByRole("option", { name: `${runId} (succeeded)` })).toBeInTheDocument();
  });

  it("names an attached run by its pid and lists the checkpoints that failed", async () => {
    const attached: RunTraces = {
      ...finished, command: undefined, pid: 4242, status: "failed",
      checkpointErrors: ["stacks stop checkpoint: process 4242 has no gops address"],
    };
    serve({ runs: [attached] });
    renderPage();
    expect(await screen.findByText("pid 4242")).toBeInTheDocument();
    expect(screen.getByText(/attached run ended/)).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent("stacks stop checkpoint: process 4242 has no gops address");
  });

  it("says why a run that did not finish cannot be shown", async () => {
    serve({ runs: [{ runId: "run-crashed", mount: "/api/traces/run-crashed", dir: "/work/run-crashed", status: "unfinished", unavailable: "the run did not finish: it left no run.json" }] });
    renderPage();
    expect(await screen.findByText(/left no run.json/)).toHaveAttribute("role", "alert");
  });

  it("surfaces a failed run listing", async () => {
    vi.stubGlobal("fetch", vi.fn((url: string) => Promise.resolve(url === "/api/traces/start" ? json(startForm) : new Response("store unreadable", { status: 500 }))));
    renderPage();
    expect(await screen.findByText(/list trace runs failed/)).toHaveTextContent("list trace runs failed (500): store unreadable");
  });

  it("lists with the filters it keeps in the URL", async () => {
    window.history.replaceState(null, "", "/?status=failed&q=curl");
    const fetchMock = serve({ runs: [] });
    renderPage();
    expect(await screen.findByText("No run matches these filters.")).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith("/api/traces?q=curl&status=failed", expect.anything());

    fireEvent.change(screen.getByRole("combobox", { name: "Status" }), { target: { value: "running" } });
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith("/api/traces?q=curl&status=running", expect.anything()));
    expect(window.location.search).toBe("?q=curl&status=running");
  });

  it("shows a live run as running with a Stop that stops it", async () => {
    const fetchMock = serve({ runs: [running] });
    renderPage();
    expect(await screen.findByText("sh -c sleep 60")).toBeInTheDocument();
    expect(screen.getByText("running", { selector: "[role=status]" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Stop" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(running.mount, { method: "DELETE" }));
  });
});

describe("host runs recording process groups", () => {
  const groupsRef = { kind: "process_groups", stream: "groups-1" };
  const hostRun: RunTraces = {
    runId: "run-2026-10-25T12-00-00Z",
    mount: "/api/traces/run-2026-10-25T12-00-00Z",
    dir: "/work/.telltale/traces/run-2026-10-25T12-00-00Z",
    status: "running",
    captures: [{ kind: "process_groups", ref: groupsRef }],
  };

  // serveGroups answers the run list with runs and the run's group rows with one tick of sampleFrame.
  function serveGroups(runs: RunTraces[]) {
    const rows = sampleFrame().groups.map(row => JSON.stringify(row)).join("\n");
    const fetchMock = vi.fn((url: string, init?: RequestInit) => {
      if (url === "/api/traces" || url.startsWith("/api/traces?")) return Promise.resolve(json({ runs }));
      if (url === "/api/traces/start") return Promise.resolve(json(startForm));
      if (init?.method === "DELETE") return Promise.resolve(new Response(null, { status: 202 }));
      if (url.includes("/seq?")) return Promise.resolve(json({ seq: 4 }));
      if (url.includes("/export?")) return Promise.resolve(new Response(rows));
      return Promise.resolve(new Response("", { status: 404 }));
    });
    vi.stubGlobal("fetch", fetchMock);
    return fetchMock;
  }

  it("shows a running host run's groups, as a host run with a Stop that stops it", async () => {
    const fetchMock = serveGroups([hostRun]);
    renderPage();
    expect(await screen.findByText("host run")).toBeInTheDocument();
    expect(screen.getByText("running", { selector: "[role=status]" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Stop" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(hostRun.mount, { method: "DELETE" }));
  });

  it("shows a stopped host run's groups instead of saying it is unavailable", async () => {
    serveGroups([{ ...hostRun, status: "succeeded", captures: [{ kind: "process_groups", ref: { ...groupsRef, toSeq: 4 } }] }]);
    renderPage();
    expect(await screen.findByRole("row", { name: /^claude/ })).toBeInTheDocument();
    expect(screen.getByText("host run")).toBeInTheDocument();
    expect(screen.getByText(/ended/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Stop" })).not.toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("leaves the process groups panel's URL parameters alone", async () => {
    window.history.replaceState(null, "", "/?topTab=recorded&topQ=vitest");
    serveGroups([]);
    renderPage();
    fireEvent.change(await screen.findByRole("combobox", { name: "Status" }), { target: { value: "failed" } });
    await waitFor(() => expect(window.location.search).toBe("?topTab=recorded&topQ=vitest&status=failed"));
  });
});

describe("starting a run", () => {
  it("posts the form's value and selects the run the server started", async () => {
    const fetchMock = serve({ runs: [running, finished] });
    renderPage();
    fireEvent.change(await screen.findByRole("textbox", { name: /Command/ }), { target: { value: "sleep 60" } });
    fireEvent.click(screen.getByRole("button", { name: "Start run" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith("/api/traces", expect.objectContaining({
      method: "POST", body: JSON.stringify({ command: "sleep 60" }),
    })));
    await waitFor(() => expect(window.location.search).toBe(`?run=${running.runId}`));
  });

  it("shows the errors of a refused start", async () => {
    serve({ runs: [] }, json({ errors: [{ instancePath: "", message: "start pg_proxy trace: connection refused" }] }, 400));
    renderPage();
    fireEvent.change(await screen.findByRole("textbox", { name: /Command/ }), { target: { value: "psql" } });
    fireEvent.click(screen.getByRole("button", { name: "Start run" }));
    expect(await screen.findByText("start pg_proxy trace: connection refused")).toHaveAttribute("role", "alert");
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

  it("puts only a filter's non-empty fields in its query", () => {
    expect(runFilterQuery({ q: "", status: "failed", kind: "http_proxy" })).toBe("status=failed&kind=http_proxy");
  });
});
