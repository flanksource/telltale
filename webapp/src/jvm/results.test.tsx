import { expect, it } from "vitest";
import { executionNodes, graphNode } from "./results";

it("preserves opaque graph explanations and server identity", () => {
  expect(graphNode({ id: "node", label: "cache", kind: "opaque", raw: "not expanded", path: ".cache", type: "Cache" })).toMatchObject({ id: "node", label: "cache", kind: "opaque", raw: "not expanded", path: ".cache", type: "Cache" });
});

it("retains values, failures and thread identity and marks incomplete snapshots", () => {
  const roots = executionNodes([
    { kind: "enter", frame: 1, threadId: 7, probeSeq: 1, method: "run", args: [{ label: "arg", value: "order" }] },
    { kind: "throw", frame: 1, threadId: 7, probeSeq: 2, error: "Out of stock" },
    { kind: "enter", frame: 2, threadId: 8, probeSeq: 3, method: "pending" },
  ]);
  expect(roots[0]).toMatchObject({ status: "error", detail: { thread: "7" }, args: [{ label: "arg", value: "order" }], error: "Out of stock" });
  expect(roots[1]).toMatchObject({ status: "warning", detail: { thread: "8", completeness: "Closing event missing from this snapshot" } });
});

it("orders events before nesting and retains orphaned outcomes and truncation warnings", () => {
  expect(executionNodes([
    { kind: "return", frame: 2, parent: 1, threadId: 7, probeSeq: 3, value: "child" },
    { kind: "call", frame: 2, parent: 1, threadId: 7, probeSeq: 2, method: "child", truncated: true },
    { kind: "enter", frame: 1, threadId: 7, probeSeq: 1, method: "root" },
    { kind: "throw", frame: 3, threadId: 7, probeSeq: 4, method: "orphan", error: "Dropped entry" },
  ])).toMatchObject([
    { label: "root", children: [{ label: "child", value: "child", detail: { truncation: "Captured values were truncated" } }] },
    { label: "orphan", error: "Dropped entry", detail: { completeness: "Entry event missing from this snapshot" } },
  ]);
});
