import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { RecordedGroups } from "./RecordedGroups";
import { DEFAULT_VIEW } from "./groups";
import { sampleFrame } from "./testFrames";
import type { RunTraces } from "../traces/api";

class Source extends EventTarget {
  static instances: Source[] = [];
  close = vi.fn();
  constructor(readonly url: string) { super(); Source.instances.push(this); }
}

const run: RunTraces = {
  runId: "run-one", mount: "/api/traces/run-one", dir: "/work/run-one", status: "running",
  captures: [{ kind: "process_groups", ref: { kind: "process_groups", stream: "groups-one" } }],
};

function frame(source: Source, tick: number, after = (tick - 1) * 4) {
  act(() => {
    sampleFrame(tick).groups.forEach((row, index) => source.dispatchEvent(new MessageEvent("message", {
      data: JSON.stringify({ ...row, seq: after + index + 1 }), lastEventId: String(after + index + 1),
    })));
  });
}

beforeEach(() => { vi.stubGlobal("EventSource", Source); vi.stubGlobal("fetch", vi.fn()); });
afterEach(() => { Source.instances = []; vi.unstubAllGlobals(); });

describe("recorded groups follow", () => {
  it("streams frames, publishes the final tick on end, and closes on unmount", () => {
    const view = render(<RecordedGroups run={run} view={DEFAULT_VIEW} onViewChange={vi.fn()} />);
    const source = Source.instances[0]!;
    expect(source.url).toBe("/api/traces/run-one/traces/process_groups/records?stream=groups-one&follow=1");
    frame(source, 1);
    frame(source, 2);
    expect(screen.getByText(/tick 1 of 1/)).toBeInTheDocument();
    act(() => source.dispatchEvent(new Event("end")));
    expect(screen.getByText(/tick 2 of 2/)).toBeInTheDocument();
    expect(source.close).toHaveBeenCalled();
    expect(fetch).not.toHaveBeenCalled();
    fireEvent.change(screen.getByRole("slider", { name: "Tick" }), { target: { value: "0" } });
    expect(screen.getByText(/tick 1 of 2/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Latest" }));
    expect(screen.getByText(/tick 2 of 2/)).toBeInTheDocument();
    view.unmount();
    expect(source.close).toHaveBeenCalled();
  });

  it("surfaces errors without polling and retries after the last received sequence", () => {
    render(<RecordedGroups run={run} view={DEFAULT_VIEW} onViewChange={vi.fn()} />);
    const source = Source.instances[0]!;
    frame(source, 1);
    act(() => source.dispatchEvent(new Event("error")));
    expect(screen.getByRole("alert")).toHaveTextContent("process_groups");
    expect(screen.getByRole("searchbox", { name: "Filter" })).toBeInTheDocument();
    expect(fetch).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(source.close).toHaveBeenCalledOnce();
    const resumed = Source.instances[1]!;
    expect(resumed.url).toContain("afterSeq=4");
    frame(resumed, 2);
    act(() => resumed.dispatchEvent(new Event("end")));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.getByRole("slider", { name: "Tick" })).toHaveAttribute("max", "1");
  });

  it("closes and resets rows when the selected run changes", () => {
    const view = render(<RecordedGroups run={run} view={DEFAULT_VIEW} onViewChange={vi.fn()} />);
    const source = Source.instances[0]!;
    frame(source, 1);
    view.rerender(<RecordedGroups run={{ ...run, runId: "run-two", mount: "/api/traces/run-two" }} view={DEFAULT_VIEW} onViewChange={vi.fn()} />);
    expect(source.close).toHaveBeenCalledOnce();
    expect(screen.queryByRole("slider")).not.toBeInTheDocument();
    expect(Source.instances[1]!.url).toContain("/run-two/");
  });

  it("reports invalid frame data through the existing error state", () => {
    render(<RecordedGroups run={run} view={DEFAULT_VIEW} onViewChange={vi.fn()} />);
    const source = Source.instances[0]!;
    act(() => source.dispatchEvent(new MessageEvent("message", { data: '{"seq":1,"tick":"invalid"}', lastEventId: "1" })));
    expect(screen.getByRole("alert")).toHaveTextContent("no numeric tick");
    expect(source.close).toHaveBeenCalledOnce();
  });
});
