import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { RecordedGroups } from "./RecordedGroups";
import { DEFAULT_VIEW } from "./groups";
import { sampleFrame } from "./testFrames";
import type { RunTraces } from "../traces/api";

const run: RunTraces = {
  runId: "run-one", mount: "/api/traces/run-one", dir: "/work/run-one", status: "succeeded",
  captures: [{ kind: "process", ref: { kind: "process", stream: "process-one" } }],
};
const page = (ticks: number[]) => new Response(JSON.stringify({ highSeq: ticks.length * 4, frames: ticks.map(tick => sampleFrame(tick)) }));
afterEach(() => vi.unstubAllGlobals());

describe("derived recorded process groups", () => {
  it("shows complete ticks and moves between an earlier tick and the latest", async () => {
    const fetchMock = vi.fn(() => Promise.resolve(page([1, 2])));
    vi.stubGlobal("fetch", fetchMock);
    render(<RecordedGroups run={run} view={DEFAULT_VIEW} onViewChange={vi.fn()} />);
    expect(await screen.findByText(/tick 2 of 2/)).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith("/api/traces/run-one/process/groups?stream=process-one&afterSeq=0", expect.objectContaining({ signal: expect.any(AbortSignal) }));
    fireEvent.change(screen.getByRole("slider", { name: "Tick" }), { target: { value: "0" } });
    expect(screen.getByText(/tick 1 of 2/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Latest" }));
    expect(screen.getByText(/tick 2 of 2/)).toBeInTheDocument();
  });

  it("surfaces a failed read and retries without advancing the sequence", async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(new Response("storage unavailable", { status: 503 })).mockImplementation(() => Promise.resolve(page([1])));
    vi.stubGlobal("fetch", fetchMock);
    render(<RecordedGroups run={run} view={DEFAULT_VIEW} onViewChange={vi.fn()} />);
    expect(await screen.findByRole("alert")).toHaveTextContent("storage unavailable");
    expect(screen.getByRole("searchbox", { name: "Filter" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText(/tick 1 of 1/)).toBeInTheDocument();
    expect(fetchMock).toHaveBeenLastCalledWith(expect.stringContaining("afterSeq=0"), expect.anything());
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("resets the frame history when the selected run changes and aborts on unmount", async () => {
    const fetchMock = vi.fn(() => Promise.resolve(page([1])));
    vi.stubGlobal("fetch", fetchMock);
    const view = render(<RecordedGroups run={run} view={DEFAULT_VIEW} onViewChange={vi.fn()} />);
    await screen.findByRole("slider");
    const previous = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    view.rerender(<RecordedGroups run={{ ...run, runId: "run-two", mount: "/api/traces/run-two" }} view={DEFAULT_VIEW} onViewChange={vi.fn()} />);
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(expect.stringContaining("/run-two/"), expect.anything()));
    expect(previous[1].signal?.aborted).toBe(true);
    view.unmount();
  });

  it("reports malformed group data without advancing the recording", async () => {
    vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(new Response(JSON.stringify({ highSeq: 1, frames: [{ tick: "bad" }] })))));
    render(<RecordedGroups run={run} view={DEFAULT_VIEW} onViewChange={vi.fn()} />);
    expect(await screen.findByRole("alert")).toHaveTextContent("Invalid process group frames");
  });
});
