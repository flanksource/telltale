import { describe, expect, it } from "vitest";
import { durationOf, durationRequest, isTraceId, rangeOf, shortOperation, windowOf } from "./query";

const now = Date.parse("2026-10-29T10:00:00Z");

describe("the time range", () => {
  it.each([
    [{ lookback: "1h" }, { from: "now-1h", to: "now" }],
    [{}, { from: "", to: "now" }],
    [{ from: "2026-10-29T09:00:00Z" }, { from: "2026-10-29T09:00:00Z", to: "now" }],
    [{ from: "2026-10-29T09:00:00Z", until: "2026-10-29T09:30:00Z", lookback: "1h" }, { from: "2026-10-29T09:00:00Z", to: "2026-10-29T09:30:00Z" }],
  ])("shows the request %j as %j", (request, range) => {
    expect(rangeOf(request)).toEqual(range);
  });

  it.each([
    ["now-15m", "now", { lookback: "15m" }],
    ["now-7d", "now", { lookback: "168h" }],
    ["now-1M", "", { lookback: "720h" }],
    ["", "now", { lookback: undefined }],
    ["now-2h", "now-1h", { from: "2026-10-29T08:00:00.000Z", until: "2026-10-29T09:00:00.000Z" }],
    ["2026-10-29T08:30:00Z", "now", { from: "2026-10-29T08:30:00.000Z", until: undefined }],
  ])("requests %s → %s as %j", (from, to, window) => {
    expect(windowOf(from, to, now)).toEqual({ lookback: undefined, from: undefined, until: undefined, ...window });
  });

  it("refuses a time it cannot read", () => {
    expect(() => windowOf("yesterday", "now", now)).toThrow(/Could not read the time "yesterday"/);
  });
});

describe("the duration range", () => {
  it("round-trips bounds through the request", () => {
    const control = durationOf({ minDuration: "500ms", maxDuration: "2.5s" });
    expect(control).toEqual({ min: "500", minUnit: "ms", max: "2.5", maxUnit: "s" });
    expect(durationRequest(control)).toEqual({ minDuration: "500ms", maxDuration: "2.5s" });
  });

  it("reads a bare bound in seconds and drops an empty one", () => {
    expect(durationRequest({ min: "3", max: " " })).toEqual({ minDuration: "3s", maxDuration: undefined });
  });

  it("refuses a request duration the control cannot show", () => {
    expect(() => durationOf({ minDuration: "1h30m" })).toThrow(/minDuration/);
  });
});

it("shortens a prompt path to the prompt's name under prompts/", () => {
  expect(shortOperation("invoke_agent /Users/acme/.captain/prompts/jira/classify.prompt")).toBe("invoke_agent jira/classify.prompt");
  expect(shortOperation("POST /orders")).toBe("POST /orders");
});

it.each([
  ["8d7616992490c496e2ca9b9d9db42c7f", true],
  ["8d7616992490c496", true],
  ["8d76169924", false],
  ["zz7616992490c496e2ca9b9d9db42c7f", false],
])("reads %s as a trace ID: %s", (text, valid) => {
  expect(isTraceId(text)).toBe(valid);
});
