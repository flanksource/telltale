import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { RunTraces } from "../traces/api";
import type { Frame, HostUsage } from "./api";
import { othersFrame, sampleFrame } from "./testFrames";
import TopPage from "./TopPage";

beforeEach(() => window.history.replaceState(null, "", "/"));
afterEach(() => vi.unstubAllGlobals());

function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
}

function renderPage() {
  return render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <TopPage />
    </QueryClientProvider>,
  );
}

const cpuOf = (name: RegExp) => within(screen.getByRole("row", { name })).getAllByRole("cell")[2]!.textContent;

describe("live process groups", () => {
  it("shows the latest frame as a tree table", async () => {
    vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(json(sampleFrame()))));
    renderPage();
    expect(await screen.findByRole("row", { name: /^claude/ })).toBeInTheDocument();
    expect(screen.getAllByRole("row").slice(1).map(row => row.getAttribute("data-depth"))).toEqual(["0", "1", "1", "0"]);
    expect(screen.getByText(/tick 1/)).toBeInTheDocument();
  });

  it("keeps the filter controls when nothing matches, and restores the rows when the filter is cleared", async () => {
    vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(json(sampleFrame()))));
    renderPage();
    await screen.findByRole("row", { name: /^claude/ });
    fireEvent.change(screen.getByRole("searchbox", { name: "Filter" }), { target: { value: "no-such-tool" } });
    expect(screen.getByText("No group matches these filters.")).toBeInTheDocument();
    expect(screen.getByRole("searchbox", { name: "Filter" })).toHaveValue("no-such-tool");
    expect(screen.getByRole("combobox", { name: "Kind" })).toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: "Show other processes" })).toBeInTheDocument();
    expect(window.location.search).toBe("?topQ=no-such-tool");

    fireEvent.change(screen.getByRole("searchbox", { name: "Filter" }), { target: { value: "" } });
    expect(await screen.findByRole("row", { name: /^claude/ })).toBeInTheDocument();
  });

  it("keeps the filter controls while the first frame is read, and when it fails", async () => {
    vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(new Response("boom", { status: 500 }))));
    renderPage();
    expect(screen.getByRole("searchbox", { name: "Filter" })).toBeInTheDocument();
    expect(await screen.findByRole("alert")).toHaveTextContent("read process groups failed (500): boom");
    expect(screen.getByRole("searchbox", { name: "Filter" })).toBeInTheDocument();
  });

  it("retries a refused reading", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response("net bytes are unsupported here", { status: 501 }))
      .mockResolvedValue(json(sampleFrame()));
    vi.stubGlobal("fetch", fetchMock);
    renderPage();
    expect(await screen.findByRole("alert")).toHaveTextContent("(501)");
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByRole("row", { name: /^claude/ })).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("reads the other processes and the network only when asked to", async () => {
    const fetchMock = vi.fn(() => Promise.resolve(json(sampleFrame())));
    vi.stubGlobal("fetch", fetchMock);
    renderPage();
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith("/api/top?all=false&net=false", expect.anything()));
    fireEvent.click(screen.getByRole("checkbox", { name: "Network bytes (nettop, costly)" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith("/api/top?all=false&net=true", expect.anything()));
    fireEvent.click(screen.getByRole("checkbox", { name: "Show other processes" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith("/api/top?all=true&net=true", expect.anything()));
    expect(window.location.search).toBe("?topOther=1&topNet=1");
  });

  it("opens with the filters and sort the URL names", async () => {
    window.history.replaceState(null, "", "/?topQ=vitest&topSort=rss&topDir=asc");
    vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(json(sampleFrame()))));
    renderPage();
    expect(await screen.findByRole("row", { name: /^vitest/ })).toBeInTheDocument();
    expect(screen.getByRole("searchbox", { name: "Filter" })).toHaveValue("vitest");
    expect(screen.getByRole("columnheader", { name: /RSS/ })).toHaveAttribute("aria-sort", "ascending");
  });
});

const mount = "/api/traces/run-2026-10-25T10-00-00Z";
const recording: RunTraces = {
  runId: "run-2026-10-25T10-00-00Z",
  mount,
  dir: "/work/.telltale/traces/run-2026-10-25T10-00-00Z",
  status: "succeeded",
  captures: [{ kind: "process_groups", ref: { kind: "process_groups", stream: "groups-1", toSeq: 8 } }],
};

function recordedFrames(): Frame[] {
  const [first, second] = [sampleFrame(1), sampleFrame(2)];
  first.groups[0]!.total.cpuPercent = 77;
  return [first, second];
}

function serveRecording(runs: RunTraces[]) {
  const rows = recordedFrames().flatMap(frame => frame.groups);
  const fetchMock = vi.fn((url: string) => {
    if (url.startsWith("/api/top")) return Promise.resolve(json(sampleFrame()));
    if (url.startsWith("/api/traces?")) return Promise.resolve(json({ runs }));
    if (url.includes("/seq?")) return Promise.resolve(json({ seq: 8 }));
    if (url.includes("/export?")) return Promise.resolve(new Response(rows.map(row => JSON.stringify(row)).join("\n")));
    return Promise.resolve(new Response("", { status: 404 }));
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

describe("recorded process groups", () => {
  it("lists the recordings and shows the latest tick of the selected one, with a CPU chart", async () => {
    window.history.replaceState(null, "", "/?topTab=recorded");
    const fetchMock = serveRecording([recording]);
    renderPage();
    expect(await screen.findByRole("option", { name: `${recording.runId} (succeeded)` })).toBeInTheDocument();
    expect(await screen.findByRole("row", { name: /^claude/ })).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith("/api/traces?kind=process_groups", expect.anything());
    expect(fetchMock).toHaveBeenCalledWith(`${mount}/api/v1/traces/process_groups/export?stream=groups-1&toSeq=8&format=ndjson`, expect.anything());
    expect(screen.getByRole("slider", { name: "Tick" })).toHaveValue("1");
    expect(screen.getByText(/tick 2 of 2/)).toBeInTheDocument();
    expect(cpuOf(/^claude/)).toBe("12.5");
    expect(screen.getByRole("img", { name: /Total CPU % of the 4 busiest groups over 2 ticks/ })).toBeInTheDocument();
  });

  it("shows an earlier tick when the slider moves to it, and the latest again on Latest", async () => {
    window.history.replaceState(null, "", "/?topTab=recorded");
    serveRecording([recording]);
    renderPage();
    await screen.findByRole("row", { name: /^claude/ });
    fireEvent.change(screen.getByRole("slider", { name: "Tick" }), { target: { value: "0" } });
    expect(screen.getByText(/tick 1 of 2/)).toBeInTheDocument();
    expect(cpuOf(/^claude/)).toBe("77.0");
    fireEvent.click(screen.getByRole("button", { name: "Latest" }));
    expect(cpuOf(/^claude/)).toBe("12.5");
  });

  it("says how to record when there are no recordings, keeping the tabs", async () => {
    window.history.replaceState(null, "", "/?topTab=recorded");
    serveRecording([]);
    renderPage();
    expect(await screen.findByText(/No process groups recorded yet/)).toHaveTextContent("telltale top --record");
    expect(screen.getByRole("tab", { name: "Live" })).toBeInTheDocument();
  });

  it("switches between the tabs and keeps the choice in the URL", async () => {
    serveRecording([recording]);
    renderPage();
    expect(await screen.findByRole("row", { name: /^claude/ })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("tab", { name: "Recorded" }));
    expect(window.location.search).toBe("?topTab=recorded");
    expect(await screen.findByRole("option", { name: /succeeded/ })).toBeInTheDocument();
  });
});

const GiB = 1024 * 1024 * 1024;

const host: HostUsage = {
  cores: 8, cpuUserPercent: 12.34, cpuSystemPercent: 5.5, cpuIdlePercent: 82.16,
  memoryUsedBytes: 8 * GiB, memoryTotalBytes: 16 * GiB, swapUsedBytes: 1 * GiB, swapTotalBytes: 4 * GiB,
  diskReadOps: 120.4, diskWriteOps: 80, diskReadBps: 3 * 1024 * 1024, diskWriteBps: 512 * 1024, load1: 1.5, load5: 2.25, load15: 3,
};

// A label is shown by a gauge card and by a stat cell: find the card, or the cell, of the label.
const cardOf = (strip: HTMLElement, label: string, selector: string) => {
  const found = within(strip).getAllByText(label).map(element => element.closest(selector)).find(Boolean);
  if (!found) throw new Error(`no ${selector} labelled ${label}`);
  return found;
};
const gauge = (strip: HTMLElement, label: string) => cardOf(strip, label, "div.min-h-28");
const stat = (strip: HTMLElement, label: string) => cardOf(strip, label, 'div[class*="px-density-3"]');

describe("host usage", () => {
  it("shows the live frame's CPU split, cores, load, memory and swap above the tabs", async () => {
    vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(json({ ...sampleFrame(), host }))));
    renderPage();
    const strip = await screen.findByRole("region", { name: "Host" });
    expect(strip.compareDocumentPosition(screen.getByRole("tab", { name: "Live" })) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(gauge(strip, "CPU user")).toHaveTextContent("CPU user12.3%of all cores");
    expect(gauge(strip, "CPU kernel")).toHaveTextContent("CPU kernel5.5%of all cores");
    expect(gauge(strip, "Memory")).toHaveTextContent("Memory50%8 GiB of 16 GiB");
    expect(gauge(strip, "Swap")).toHaveTextContent("Swap25%1 GiB of 4 GiB");
    expect(stat(strip, "CPU user")).toHaveTextContent("CPU user12.3%");
    expect(stat(strip, "CPU kernel")).toHaveTextContent("CPU kernel5.5%");
    expect(stat(strip, "CPU idle")).toHaveTextContent("CPU idle82.2%");
    expect(stat(strip, "Disk read IOPS")).toHaveTextContent("Disk read IOPS120");
    expect(stat(strip, "Disk write IOPS")).toHaveTextContent("Disk write IOPS80");
    expect(stat(strip, "Disk read/s")).toHaveTextContent("Disk read/s3 MiB/s");
    expect(stat(strip, "Disk write/s")).toHaveTextContent("Disk write/s512 KiB/s");
    expect(stat(strip, "Cores")).toHaveTextContent("Cores8");
    expect(stat(strip, "Load")).toHaveTextContent("Load1.50 2.25 3.001 / 5 / 15 min");
    expect(within(strip).queryByText("telltale overhead")).not.toBeInTheDocument();
  });

  it("shows what the tick cost telltale itself", async () => {
    vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(json({ ...sampleFrame(), host, overhead: { cpuPercent: 4.25, tickMillis: 212.4 } }))));
    renderPage();
    const strip = await screen.findByRole("region", { name: "Host" });
    expect(stat(strip, "telltale overhead")).toHaveTextContent("telltale overhead4.3%CPU · 212 ms/tick");
  });

  it("shows dashes, not zero, for the CPU shares of the first tick", async () => {
    const first: HostUsage = { ...host };
    delete first.cpuUserPercent;
    delete first.cpuSystemPercent;
    delete first.cpuIdlePercent;
    delete first.diskReadOps;
    delete first.diskWriteOps;
    delete first.diskReadBps;
    delete first.diskWriteBps;
    vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(json({ ...sampleFrame(), host: first }))));
    renderPage();
    const strip = await screen.findByRole("region", { name: "Host" });
    for (const label of ["CPU user", "CPU kernel", "CPU idle", "Disk read IOPS", "Disk write IOPS", "Disk read/s", "Disk write/s"]) expect(stat(strip, label)).toHaveTextContent(`${label}–`);
    expect(within(strip).queryByText("of all cores")).not.toBeInTheDocument();
  });

  it("shows no host row for a frame without a host, and none on the recorded tab", async () => {
    vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(json(sampleFrame()))));
    renderPage();
    await screen.findByRole("row", { name: /^claude/ });
    expect(screen.queryByRole("region", { name: "Host" })).not.toBeInTheDocument();
  });

  it("hides the host row on the recorded tab, whose frames hold no host", async () => {
    window.history.replaceState(null, "", "/?topTab=recorded");
    const fetchMock = vi.fn((url: string) => url.startsWith("/api/traces?") ? Promise.resolve(json({ runs: [] })) : Promise.resolve(json({ ...sampleFrame(), host })));
    vi.stubGlobal("fetch", fetchMock);
    renderPage();
    await screen.findByText(/No process groups recorded yet/);
    expect(screen.queryByRole("region", { name: "Host" })).not.toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalledWith(expect.stringMatching(/^\/api\/top\?/), expect.anything());
  });
});

describe("scope and service filters", () => {
  it("filters by scope and offers the service kind, keeping both in the URL", async () => {
    vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(json(othersFrame()))));
    window.history.replaceState(null, "", "/?topOther=1");
    renderPage();
    await screen.findByRole("row", { name: /^postgresql@17/ });
    expect(within(screen.getByRole("combobox", { name: "Kind" })).getByRole("option", { name: "service" })).toBeInTheDocument();
    fireEvent.change(screen.getByRole("combobox", { name: "Scope" }), { target: { value: "system" } });
    expect(screen.queryByRole("row", { name: /^postgresql@17/ })).not.toBeInTheDocument();
    expect(screen.getByRole("row", { name: /^system/ })).toBeInTheDocument();
    expect(window.location.search).toBe("?topScope=system&topOther=1");
  });

  it("opens with the scope the URL names", async () => {
    window.history.replaceState(null, "", "/?topScope=user&topOther=1");
    vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(json(othersFrame()))));
    renderPage();
    await screen.findByRole("row", { name: /^other/ });
    expect(screen.getByRole("combobox", { name: "Scope" })).toHaveValue("user");
    expect(screen.queryByRole("row", { name: /^system/ })).not.toBeInTheDocument();
  });
});
