import { expect, it } from "vitest";
import { ago, niceStep } from "./TraceResults";

it.each([
  [4_000, "4s ago"],
  [125_000, "2m ago"],
  [7_200_000, "2h ago"],
  [172_800_000, "2d ago"],
])("reads %d ms as %s", (ms, text) => {
  expect(ago(ms)).toBe(text);
});

it.each([
  [7_300, 4, 2_000],
  [100, 4, 50],
  [0.9, 4, 0.5],
])("steps an axis of %d into about %d round steps of %d", (total, ticks, step) => {
  expect(niceStep(total, ticks)).toBeCloseTo(step);
});
