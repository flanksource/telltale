import { describe, expect, it, vi } from "vitest";
import { captureParameters, persistFilter, persistSearch, selectedParameters } from "./api";
import { executionNodes } from "./results";

describe("JVM diagnostics", () => {
  it("notifies navigation when recording or target selections change", () => {
    const listener = vi.fn();
    window.addEventListener("popstate", listener);
    try {
      persistFilter("recording", "capture");
      persistSearch("target=loader%7Cexample.Target%23compute&recording=capture");
      expect(listener).toHaveBeenCalledTimes(2);
      expect(new URLSearchParams(window.location.search).get("recording")).toBe("capture");
      expect(new URLSearchParams(window.location.search).get("target")).toBe("loader|example.Target#compute");
    } finally { window.removeEventListener("popstate", listener); window.history.replaceState(null, "", "/"); }
  });
  it("keeps class and loader selection without persisting executable source", () => {
    expect(selectedParameters("?class=example.Target&loader=4ac&method=compute&source=secret")).toEqual({ class: "example.Target", loader: "4ac", method: "compute" });
  });
  it("carries the selected method and loader into a capture without unrelated filters", () => {
    const selection = { class: "example.Target", loader: "4ac", method: "compute(I)I", query: "Target" };
    expect(captureParameters("jvm_trace", selection)).toEqual({ targets: [{ class: "example.Target", loader: "4ac", method: "compute(I)I" }] });
    expect(captureParameters("jvm_jfr", selection)).toEqual({});
    expect(captureParameters("jvm_notifications", { objectName: "java.lang:type=Memory" })).toEqual({ mbean: "java.lang:type=Memory" });
  });
  it("keeps frames from different threads separate and attaches costs to the correct call", () => {
    expect(executionNodes([
      { kind: "enter", threadId: 1, frame: 1, probeSeq: 1, method: "first" },
      { kind: "enter", threadId: 2, frame: 1, probeSeq: 2, method: "second" },
      { kind: "return", threadId: 1, frame: 1, probeSeq: 3, durationNs: 2000000 },
    ])).toMatchObject([{ label: "first", status: "ok", cost: 2 }, { label: "second", status: "warning", detail: { completeness: "Closing event missing from this snapshot" } }]);
  });
  it("retains an exception when the following exit supplies its duration", () => {
    expect(executionNodes([
      { kind: "enter", threadId: 1, frame: 1, probeSeq: 1, method: "compute" },
      { kind: "throw", threadId: 1, frame: 1, probeSeq: 2, error: "exception" },
      { kind: "exit", threadId: 1, frame: 1, probeSeq: 3, durationNs: 2000000 },
    ])).toMatchObject([{ label: "compute", status: "error", cost: 2 }]);
  });
});
