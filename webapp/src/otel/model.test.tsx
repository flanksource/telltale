import { describe, expect, it } from "vitest";
import { spanRows } from "./model";
import type { Span } from "./api";

const span = (id: string, parentId = ""): Span => ({
  traceId: "trace",
  spanId: id,
  parentId,
  service: "checkout",
  operation: id,
  startedAt: "2026-10-29T10:00:00Z",
  durationMs: 10,
  status: "ok",
});

describe("trace waterfall rows", () => {
  it("keeps children under their parents and preserves missing-parent roots", () => {
    expect(
      spanRows(
        [span("child", "root"), span("orphan", "absent"), span("root")],
        new Set(),
      ).map((row) => [row.spanId, row.depth]),
    ).toEqual([
      ["orphan", 0],
      ["root", 0],
      ["child", 1],
    ]);
  });
  it("collapses descendants without removing their parent", () => {
    expect(
      spanRows([span("root"), span("child", "root")], new Set(["root"])).map(
        (row) => row.spanId,
      ),
    ).toEqual(["root"]);
  });
  it("refuses cycles and duplicate identities", () => {
    expect(() =>
      spanRows([span("root", "child"), span("child", "root")], new Set()),
    ).toThrow(/cycle/);
    expect(() => spanRows([span("root"), span("root")], new Set())).toThrow(
      /duplicate/,
    );
  });
});
