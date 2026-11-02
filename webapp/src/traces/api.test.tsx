import { afterEach, describe, expect, it, vi } from "vitest";
import { followTraceRecords, traceRecordsUrl } from "./api";

class Source extends EventTarget {
  static instances: Source[] = [];
  close = vi.fn();
  constructor(readonly url: string) { super(); Source.instances.push(this); }
}

afterEach(() => { Source.instances = []; vi.unstubAllGlobals(); });

describe("record follow client", () => {
  const ref = { kind: "process_groups", stream: "group / one", afterSeq: 2, toSeq: 8 };

  it("uses the records route with the capture window", () => {
    expect(traceRecordsUrl("/api/traces/run-one", ref, { follow: "1" })).toBe("/api/traces/run-one/traces/process_groups/records?stream=group+%2F+one&afterSeq=2&toSeq=8&follow=1");
  });

  it("delivers JSON rows once across native reconnects and closes on end", () => {
    vi.stubGlobal("EventSource", Source);
    const onRow = vi.fn();
    const onError = vi.fn();
    const onEnd = vi.fn();
    const onOpen = vi.fn();
    const close = followTraceRecords({ mount: "/api/traces/run-one", ref, onRow, onError, onEnd, onOpen });
    const source = Source.instances[0]!;
    source.dispatchEvent(new Event("open"));
    for (const seq of [3, 3, 4]) source.dispatchEvent(new MessageEvent("message", { data: JSON.stringify({ seq, tick: seq }), lastEventId: String(seq) }));
    source.dispatchEvent(new Event("end"));
    expect(onOpen).toHaveBeenCalledOnce();
    expect(onRow.mock.calls).toEqual([[{ seq: 3, tick: 3 }], [{ seq: 4, tick: 4 }]]);
    expect(onError).not.toHaveBeenCalled();
    expect(onEnd).toHaveBeenCalledOnce();
    expect(source.close).toHaveBeenCalled();
    close();
  });

  it("surfaces transport errors while preserving native EventSource resume", () => {
    vi.stubGlobal("EventSource", Source);
    const onError = vi.fn();
    const close = followTraceRecords({ mount: "/api/traces/run-one", ref, onRow: vi.fn(), onError, onEnd: vi.fn(), onOpen: vi.fn() });
    const source = Source.instances[0]!;
    source.dispatchEvent(new Event("error"));
    expect(onError).toHaveBeenCalledWith(expect.objectContaining({ message: expect.stringContaining("process_groups") }));
    expect(source.close).not.toHaveBeenCalled();
    expect(Source.instances).toHaveLength(1);
    close();
    expect(source.close).toHaveBeenCalledOnce();
  });

  it.each([
    ["invalid JSON", "message", "{", "JSON"],
    ["missing seq", "message", '{"tick":1}', "seq"],
    ["server error", "error", '{"error":"read failed"}', "read failed"],
  ])("closes and reports %s", (_name, type, data, message) => {
    vi.stubGlobal("EventSource", Source);
    const onError = vi.fn();
    followTraceRecords({ mount: "/api/traces/run-one", ref, onRow: vi.fn(), onError, onEnd: vi.fn(), onOpen: vi.fn() });
    const source = Source.instances[0]!;
    source.dispatchEvent(new MessageEvent(type, { data, lastEventId: "3" }));
    expect(onError).toHaveBeenCalledWith(expect.objectContaining({ message: expect.stringContaining(message) }));
    expect(source.close).toHaveBeenCalledOnce();
  });
});
