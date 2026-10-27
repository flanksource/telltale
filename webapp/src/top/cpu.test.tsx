import { describe, expect, it } from "vitest";
import { isCPUHog } from "./cpu";

describe("CPU hog threshold across all cores", () => {
  it.each([[201, 8, true], [200, 8, false], [150, 8, false], [26, 1, true], [undefined, 8, false], [300, undefined, false], [300, 0, false]])("classifies %s on %s cores", (cpu, cores, expected) => {
    expect(isCPUHog(cpu as number | undefined, cores as number | undefined)).toBe(expected);
  });
});
