import { describe, expect, it } from "vitest";
import fixture from "../../../testdata/top-grouping.json";
import type { Frame } from "./api";
import { DEFAULT_VIEW, groupRows } from "./groups";

describe("grouped view", () => {
  const groups = fixture.frame.groups as Frame["groups"];
  it("sorts repository buckets by aggregate CPU", () => {
    const rows = groupRows(groups, { ...DEFAULT_VIEW, groupBy: "repo" });
    expect(rows.filter(row => row.group.kind === "bucket").map(row => row.group.total.cpuPercent)).toEqual([70, 30, 5]);
  });

  it("keeps bucket ancestry and recomputes totals from the matching tool groups", () => {
    const rows = groupRows(groups, { ...DEFAULT_VIEW, groupBy: "repo", kind: "test" });
    expect(rows.map(row => row.group.key)).toEqual(["bucket:repo:/work/acme/.git", "agent-a", "tests"]);
    expect(rows[0]!.group.total.cpuPercent).toBe(30);
  });

  it("finds named sessions and inherited tools without hiding matches in folded buckets", () => {
    const rows = groupRows(groups, { ...DEFAULT_VIEW, groupBy: "claude-session", q: "Fix login" });
    expect(rows.filter(row => row.group.kind !== "bucket").map(row => row.group.key)).toEqual(["agent-b", "build", "agent-a", "tests"]);
  });
});
