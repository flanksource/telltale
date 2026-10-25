import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { RouterProvider, useMemoryRouter } from "@flanksource/clicky-ui/rpc";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { HostUsage, ProcessFacets, ProcessSample, TopProcesses } from "./api";
import ProcessPage from "./ProcessPage";
import { filterProcesses, metricSeries, parseTarget, sortProcesses, sumProcesses } from "./processes";

afterEach(() => vi.unstubAllGlobals());

const GiB = 1024 * 1024 * 1024;

const host: HostUsage = {
  cores: 8, cpuUserPercent: 20, cpuSystemPercent: 5, cpuIdlePercent: 75,
  memoryUsedBytes: 8 * GiB, memoryTotalBytes: 16 * GiB, swapUsedBytes: 0, swapTotalBytes: 0, load1: 1, load5: 2, load15: 3,
};

function processSample(overrides: Partial<ProcessSample> & Pick<ProcessSample, "pid" | "name">): ProcessSample {
  return {
    key: `${overrides.pid}:1`, ppid: 1, command: `/usr/bin/${overrides.name}`, groupKey: "other:user:launchd",
    origin: { owner: "moshe", scope: "user" }, processes: 1, rssBytes: 0, ...overrides,
  };
}

/** Two processes of one program, 150% and 50% CPU, one with every rate known and one with only CPU and memory. */
function reading(tick = 1): TopProcesses {
  return {
    sampledAt: `2026-10-25T10:00:0${tick}Z`, tick, host,
    processes: [
      processSample({
        pid: 101, ppid: 1, name: "launchd", cpuPercent: 150, rssBytes: 2 * GiB, diskReadBps: 2048, diskWriteBps: 1024, netRxBps: 4096, netTxBps: 512, sockets: 3,
        origin: { owner: "root", scope: "system", source: "launchd", label: "com.example.launchd" },
      }),
      processSample({ pid: 202, ppid: 101, name: "launchd", cpuPercent: 50, rssBytes: 1 * GiB, command: "launchd --child" }),
    ],
  };
}

function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
}

const SESSION_TITLE = "Fix the flaky acme login test";

/** pid 101 in a named claude session and cmux workspace, listening on one port, its token masked. */
function facetsOf(pid: number): ProcessFacets {
  return {
    pid, key: `${pid}:1`, name: "claude", command: "claude --session-id 5f0c3a52-acme", readAt: "2026-10-25T10:00:05Z",
    context: {
      repo: "/work/acme-api", branch: "main", agent: "claude", session: "5f0c3a52-acme", sessionTitle: SESSION_TITLE,
      workspace: "8E1C7A10-ACME", workspaceTitle: "acme-api", surface: "B2D4-ACME", surfaceTitle: "claude · login fix",
    },
    environment: { ACME_REGION: "eu", ACME_API_TOKEN: "****" },
    descriptors: {
      openFiles: [{ fd: "3r", type: "REG", name: "/work/acme-api/go.mod" }],
      sockets: [{ fd: "7u", type: "IPv4", name: "localhost:8080", protocol: "TCP", state: "LISTEN", localAddr: "127.0.0.1:8080", port: 8080 }],
      ports: [{ fd: "7u", type: "IPv4", name: "localhost:8080", protocol: "TCP", state: "LISTEN", localAddr: "127.0.0.1:8080", port: 8080 }],
      connections: [],
    },
  };
}

/** Answers the processes and the facets routes, each with its own reading. */
function routed(processes: TopProcesses = reading()) {
  return vi.fn((url: string) => Promise.resolve(url.startsWith("/api/top/facets?")
    ? json(facetsOf(Number(new URLSearchParams(url.split("?")[1]).get("pid"))))
    : json(processes)));
}

function renderPage(target: string) {
  function Harness() {
    const router = useMemoryRouter(`/top/${target}`);
    return (
      <RouterProvider adapter={router}>
        <output data-testid="path">{router.pathname}</output>
        <ProcessPage target={target} />
      </RouterProvider>
    );
  }
  return render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <Harness />
    </QueryClientProvider>,
  );
}

const tile = (metric: string) => within(document.querySelector<HTMLElement>(`[data-metric="${metric}"]`)!);

describe("parseTarget", () => {
  it.each([
    ["123", { pid: 123 }],
    ["launchd", { name: "launchd" }],
    ["com.apple.WebKit.Networking", { name: "com.apple.WebKit.Networking" }],
    ["12abc", { name: "12abc" }],
    ["Google Chrome Helper", { name: "Google Chrome Helper" }],
  ])("reads %s", (target, expected) => {
    expect(parseTarget(target)).toEqual(expected);
  });
});

describe("process totals", () => {
  it("sums what is known and leaves a metric unknown when no process reports it", () => {
    const total = sumProcesses(reading().processes);
    expect(total).toEqual({ processes: 2, rssBytes: 3 * GiB, cpuPercent: 200, diskReadBps: 2048, diskWriteBps: 1024, netRxBps: 4096, netTxBps: 512, sockets: 3 });
    expect(sumProcesses([processSample({ pid: 1, name: "x" })])).toEqual({ processes: 1, rssBytes: 0 });
  });

  it("builds a metric's series over the readings, skipping those where it was unknown", () => {
    const unknown = reading(2);
    unknown.processes = [processSample({ pid: 5, name: "x", rssBytes: 7 })];
    expect(metricSeries([reading(1), unknown, reading(3)], "cpu")).toEqual([200, 200]);
    expect(metricSeries([reading(1), unknown, reading(3)], "rss")).toEqual([3 * GiB, 7, 3 * GiB]);
  });

  it("sorts by CPU or RSS, highest first, and filters by name, command, owner and origin", () => {
    const processes = reading().processes;
    expect(sortProcesses(processes, "cpu").map(p => p.pid)).toEqual([101, 202]);
    expect(sortProcesses(processes, "rss").map(p => p.pid)).toEqual([101, 202]);
    expect(sortProcesses([processes[1]!, { ...processes[0]!, rssBytes: 1 }], "rss").map(p => p.pid)).toEqual([202, 101]);
    expect(filterProcesses(processes, "--CHILD").map(p => p.pid)).toEqual([202]);
    expect(filterProcesses(processes, "root").map(p => p.pid)).toEqual([101]);
    expect(filterProcesses(processes, "launchd:com.example").map(p => p.pid)).toEqual([101]);
    expect(filterProcesses(processes, "  ")).toEqual(processes);
  });
});

describe("ProcessPage", () => {
  it("polls the processes of a program by name with all=true, and shows its totals as gauges with trends", async () => {
    const fetchMock = vi.fn(() => Promise.resolve(json(reading())));
    vi.stubGlobal("fetch", fetchMock);
    renderPage("launchd");
    expect(await screen.findByRole("heading", { name: "launchd" })).toBeInTheDocument();
    await screen.findByText(/tick 1/);
    expect(fetchMock).toHaveBeenCalledWith("/api/top/processes?name=launchd&all=true", expect.anything());
    expect(screen.getByText(/2 processes/)).toBeInTheDocument();
    expect(tile("cpu").getByText("200.0")).toBeInTheDocument();
    // 200% of 8 cores' 800%.
    expect(tile("cpu").getByRole("meter")).toHaveAttribute("aria-valuemax", "800");
    expect(tile("cpu").getByRole("meter")).toHaveAttribute("aria-valuenow", "200");
    expect(tile("rss").getByText("3 GiB")).toBeInTheDocument();
    expect(tile("rss").getByRole("meter")).toHaveAttribute("aria-valuemax", String(16 * GiB));
    expect(tile("diskRead").getByText("2 KiB/s")).toBeInTheDocument();
    expect(tile("diskWrite").getByText("1 KiB/s")).toBeInTheDocument();
    expect(tile("netRx").getByText("4 KiB/s")).toBeInTheDocument();
    expect(tile("netTx").getByText("512 B/s")).toBeInTheDocument();
    expect(tile("sockets").getByText("3")).toBeInTheDocument();
    expect(tile("cpu").getByText("CPU % trend")).toBeInTheDocument();
  });

  it("asks for a process and its subtree by pid when the target is numeric", async () => {
    const fetchMock = routed();
    vi.stubGlobal("fetch", fetchMock);
    renderPage("101");
    expect(await screen.findByRole("heading", { name: "Process 101 and those under it" })).toBeInTheDocument();
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith("/api/top/processes?pid=101&all=true", expect.anything()));
  });

  it("shows a pid's session, cmux workspace and surface by name, and its environment and descriptors by facet", async () => {
    const fetchMock = routed();
    vi.stubGlobal("fetch", fetchMock);
    renderPage("101");
    const panel = await screen.findByRole("region", { name: "Process 101" });
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith("/api/top/facets?pid=101&all=true", expect.anything()));
    const list = await within(panel).findByLabelText("Context");
    expect(list).toHaveTextContent(`claude session${SESSION_TITLE}`);
    expect(list).toHaveTextContent("cmux workspaceacme-api");
    expect(list).toHaveTextContent("cmux surfaceclaude · login fix");
    expect(list).toHaveTextContent("Checkout/work/acme-api @ main");
    expect(within(panel).getByText("ACME_API_TOKEN").closest("tr")).toHaveTextContent("ACME_API_TOKEN****");

    fireEvent.click(within(panel).getByRole("tab", { name: /Ports/ }));
    expect(await within(panel).findByText("127.0.0.1:8080")).toBeInTheDocument();
  });

  it("shows a program's facets only for the process chosen in its table", async () => {
    vi.stubGlobal("fetch", routed());
    renderPage("launchd");
    await screen.findByRole("row", { name: /--child/ });
    expect(screen.queryByRole("region", { name: /^Process \d+$/ })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("row", { name: /--child/ }));

    expect(await screen.findByRole("region", { name: "Process 202" })).toBeInTheDocument();
    expect(screen.getByRole("row", { name: /--child/ })).toHaveAttribute("aria-selected", "true");
  });

  it("lists each process with its owner, scope, origin and usage, a dash for what is unknown", async () => {
    vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(json(reading()))));
    renderPage("launchd");
    await screen.findByRole("row", { name: /--child/ });
    const cells = (name: RegExp) => within(screen.getByRole("row", { name })).getAllByRole("cell").map(cell => cell.textContent);
    // PID, PPID, Name, Owner, Scope, Origin, CPU %, RSS, Disk read/s, Disk write/s, Net in/s, Net out/s, Sockets, Command
    expect(cells(/^101/)).toEqual(["101", "1", "launchd", "root", "system", "launchd:com.example.launchd root", "150.0", "2 GiB", "2 KiB/s", "1 KiB/s", "4 KiB/s", "512 B/s", "3", "/usr/bin/launchd"]);
    expect(cells(/^202/)).toEqual(["202", "101", "launchd", "moshe", "user", "", "50.0", "1 GiB", "–", "–", "–", "–", "–", "launchd --child"]);
    expect(screen.getAllByRole("row").slice(1).map(row => within(row).getAllByRole("cell")[0]!.textContent)).toEqual(["101", "202"]);
  });

  it("links a pid to its own subtree page", async () => {
    vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(json(reading()))));
    renderPage("launchd");
    fireEvent.click(await screen.findByRole("link", { name: "202" }));
    expect(screen.getByTestId("path")).toHaveTextContent("/top/202");
  });

  it("links back to the groups", async () => {
    vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(json(reading()))));
    renderPage("launchd");
    expect(await screen.findByRole("link", { name: /Process groups/ })).toHaveAttribute("href", "/top");
  });

  it("sorts by RSS when its header is clicked", async () => {
    const swapped = reading();
    swapped.processes[1]!.rssBytes = 4 * GiB;
    vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(json(swapped))));
    renderPage("launchd");
    await screen.findByRole("row", { name: /--child/ });
    expect(screen.getAllByRole("row").slice(1).map(row => within(row).getAllByRole("cell")[0]!.textContent)).toEqual(["101", "202"]);
    fireEvent.click(screen.getByRole("button", { name: "RSS" }));
    expect(screen.getAllByRole("row").slice(1).map(row => within(row).getAllByRole("cell")[0]!.textContent)).toEqual(["202", "101"]);
    expect(screen.getByRole("columnheader", { name: "RSS" })).toHaveAttribute("aria-sort", "descending");
  });

  it("filters the table, and says when the filter leaves no process while keeping the gauges", async () => {
    vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(json(reading()))));
    renderPage("launchd");
    await screen.findByRole("row", { name: /--child/ });
    fireEvent.change(screen.getByRole("searchbox", { name: "Filter" }), { target: { value: "--child" } });
    expect(screen.getAllByRole("row")).toHaveLength(2);
    fireEvent.change(screen.getByRole("searchbox", { name: "Filter" }), { target: { value: "no-such" } });
    expect(screen.getByText("No process matches these filters.")).toBeInTheDocument();
    expect(screen.getByRole("searchbox", { name: "Filter" })).toHaveValue("no-such");
    expect(tile("cpu").getByText("200.0")).toBeInTheDocument();
  });

  it("says no process matches, keeping the header and the filter", async () => {
    vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(json({ ...reading(), processes: [] }))));
    renderPage("nothing-runs-this");
    expect(await screen.findByText("No process runs nothing-runs-this right now. This page keeps polling.")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "nothing-runs-this" })).toBeInTheDocument();
    expect(screen.getByRole("searchbox", { name: "Filter" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Process groups/ })).toBeInTheDocument();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
    expect(document.querySelector("[data-metric]")).toBeNull();
  });

  it("says no process is running for a pid that is gone", async () => {
    vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(json({ ...reading(), processes: [] }))));
    renderPage("424242");
    expect(await screen.findByText("No process 424242 is running right now. This page keeps polling.")).toBeInTheDocument();
  });

  it("reports a refusal with a Retry, keeping the header and filter, and recovers on Retry", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response("pid=\"0\" is not a process id", { status: 400 }))
      .mockResolvedValue(json(reading()));
    vi.stubGlobal("fetch", fetchMock);
    renderPage("launchd");
    expect(await screen.findByRole("alert")).toHaveTextContent('read processes failed (400): pid="0" is not a process id');
    expect(screen.getByRole("heading", { name: "launchd" })).toBeInTheDocument();
    expect(screen.getByRole("searchbox", { name: "Filter" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByRole("row", { name: /--child/ })).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("falls back to a unit scale when the reading holds no host, and shows unknown rates as dashes", async () => {
    const noHost = reading();
    delete noHost.host;
    noHost.processes = [processSample({ pid: 9, name: "x", cpuPercent: 40, rssBytes: 5 })];
    vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(json(noHost))));
    renderPage("x");
    await screen.findByText(/tick 1/);
    expect(tile("cpu").getByRole("meter")).toHaveAttribute("aria-valuemax", "100");
    expect(tile("diskRead").getByText("–")).toBeInTheDocument();
    expect(tile("sockets").getByText("–")).toBeInTheDocument();
  });
});
