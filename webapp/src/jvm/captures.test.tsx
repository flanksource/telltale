import { expect, it } from "vitest";
import { agentRecordings, defaultProbeOptions, filterError, methodCaptureRequest, readMethodTargets, writeMethodTargets, surfaceHref } from "./captures";
import type { RunTraces } from "../traces/api";

it("round trips multiple concrete method selections while preserving unrelated URL filters", () => {
  const targets = [{ class: "example.Worker", method: "compute(J)D", loader: "12ab" }, { class: "example.Service", method: "run*", loader: "" }];
  const search = writeMethodTargets("?opFilter=classes&class=old&method=old&loader=old", targets);
  expect(new URLSearchParams(search).get("opFilter")).toBe("classes");
  expect(new URLSearchParams(search).has("class")).toBe(false);
  expect(readMethodTargets(search)).toEqual(targets);
  expect(readMethodTargets("?class=example.Worker&method=compute(J)D&loader=12ab")).toEqual([targets[0]]);
});

it("rejects malformed target links and incomplete or grouped-loader mutations", () => {
  expect(() => readMethodTargets("?target=invalid")).toThrow("Invalid method target");
  for (const target of [{ class: "", method: "run", loader: "" }, { class: "example.Worker", method: "", loader: "" }, { class: "example.Worker", method: "run", loader: "~group" }]) {
    expect(() => methodCaptureRequest({ agent: "demo", targets: [target], options: defaultProbeOptions, duration: "5m", allowed: true })).toThrow();
  }
});

it("uses native capture parameters with explicit consent, duration and preserved overload identity", () => {
  const targets = [{ class: "example.Worker", method: "compute(J)D", loader: "12ab" }];
  expect(methodCaptureRequest({ agent: "java/demo", targets, options: { ...defaultProbeOptions, calls: true }, duration: "5m", allowed: true })).toEqual({
    duration: "5m", traces: { jvm_trace: { enabled: true, target: { seam: "agent", agent: "java/demo", allowMutation: true }, targets, args: true, return: true, exception: true, calls: true, platform: false, events: 1000, expand: 3, "arg-chars": 16384, gc: false, alloc: false } },
  });
});

it("sends root-call filters only when set, with a return match carrying no index", () => {
  const targets = [{ class: "example.Worker", method: "compute", loader: "" }];
  const options = { ...defaultProbeOptions, minCost: " 250ms ", exceptionsOnly: true, match: [
    { on: "arg" as const, index: 1, path: " customer.name ", op: "regex" as const, value: "^acme" },
    { on: "return" as const, index: 3, path: "", op: "contains" as const, value: "sent" },
  ] };
  expect(methodCaptureRequest({ agent: "demo", targets, options, duration: "5m", allowed: true }).traces?.jvm_trace).toMatchObject({
    minCost: "250ms", exceptionsOnly: true,
    match: [{ on: "arg", index: 1, path: "customer.name", op: "regex", value: "^acme" }, { on: "return", index: 0, op: "contains", value: "sent" }],
  });
  const unfiltered = methodCaptureRequest({ agent: "demo", targets, options: defaultProbeOptions, duration: "5m", allowed: true }).traces?.jvm_trace;
  for (const key of ["minCost", "exceptionsOnly", "match"]) expect(unfiltered).not.toHaveProperty(key);
});

it.each([
  [{ minCost: "fast" }, "Minimum cost"],
  [{ args: false, match: [{ on: "arg" as const, index: 0, path: "", op: "equals" as const, value: "x" }] }, "record arguments"],
  [{ return: false, match: [{ on: "return" as const, index: 0, path: "", op: "equals" as const, value: "x" }] }, "record return values"],
  [{ match: [{ on: "arg" as const, index: -1, path: "", op: "equals" as const, value: "x" }] }, "argument index"],
])("refuses a filter the agent could not evaluate: %o", (override, reason) => {
  const options = { ...defaultProbeOptions, ...override };
  expect(filterError(options)).toContain(reason);
  expect(() => methodCaptureRequest({ agent: "demo", targets: [{ class: "example.Worker", method: "compute", loader: "" }], options, duration: "5m", allowed: true })).toThrow(reason);
});

it("omits an unresolved optional loader so inline definition validation preserves the native target shape", () => {
  const request = methodCaptureRequest({ agent: "demo", targets: [{ class: "example.Worker", method: "compute", loader: "" }], options: defaultProbeOptions, duration: "5m", allowed: true });
  expect(request.traces?.jvm_trace?.targets).toEqual([{ class: "example.Worker", method: "compute" }]);
});

it("keeps recordings scoped to the selected JVM using persisted capture targets", () => {
  const runs: RunTraces[] = ["selected", "other", "unassigned"].map((id, index) => ({ runId: id, dir: id, mount: `/api/traces/${id}`, status: "succeeded", captures: [{ kind: "jvm_trace", ref: { kind: "jvm_trace", stream: id }, ...(index < 2 ? { target: { seam: "agent" as const, agent: index === 0 ? "java/demo" : "java/other" } } : {}) }] }));
  expect(agentRecordings(runs, "java/demo", "jvm_trace").map(row => row.run.runId)).toEqual(["selected"]);
  expect(agentRecordings(runs, "java/demo", "jvm_notifications")).toEqual([]);
});

it("keeps the method capture selection between Instrument and Traces without leaking it into Notifications", () => {
  const search = "?recording=methods&feedKind=jvm_trace&target=12ab%7Cexample.Worker%23compute&objectName=example%3Atype%3DPublisher";
  expect(surfaceHref({ agent: "java/demo", from: "instrument", to: "traces", search })).toContain("recording=methods");
  const href = surfaceHref({ agent: "java/demo", from: "traces", to: "notifications", search });
  expect(href).toContain("/jvm/java%2Fdemo/notifications?");
  expect(href).toContain("objectName=example%3Atype%3DPublisher");
  expect(href).not.toContain("recording=");
  expect(href).not.toContain("feedKind=");
});
