import { afterEach, expect, it, vi } from "vitest";
import { serverFiltersToFilterBar } from "@flanksource/clicky-ui/data";
import { executeRecordQuery, recordQueryColumns } from "./recordQuery";

afterEach(() => vi.unstubAllGlobals());

it("projects profile filter keys and match controls into the shared query browser contract", () => {
  const columns = recordQueryColumns([{ name: "host", filter: { key: "filter.host", kind: "match", lookup: true } }]);
  expect(columns).toMatchObject([{ name: "host", filterKey: "filter.host", filter: { kind: "terms" } }]);
  expect(() => serverFiltersToFilterBar(columns, {}, vi.fn())).not.toThrow();
});

it("queries the profile within its trace window with filters and a continuation cursor", async () => {
  const fetchMock = vi.fn((_url: string, _options?: RequestInit) => Promise.resolve(new Response(JSON.stringify({ rows: [{ value: 7 }], nextCursor: "next-page" }))));
  vi.stubGlobal("fetch", fetchMock);
  const result = await executeRecordQuery({ mount: "/api/traces/run-1", ref: { kind: "http", stream: "http-1", afterSeq: 2, toSeq: 9 }, columns: [], request: {
    query: "worker", options: { where: "row.value > 0" }, filters: { "filter.value": ">=7" }, pagination: { limit: 25, cursor: "previous-page" },
  } });
  const url = new URL(fetchMock.mock.calls[0]![0]!, "http://localhost");
  expect(Object.fromEntries(url.searchParams)).toEqual({ stream: "http-1", afterSeq: "2", toSeq: "9", q: "worker", where: "row.value > 0", filter: "value=>=7", limit: "25", cursor: "previous-page" });
  expect(result).toMatchObject({ rows: [{ value: 7 }], pagination: { mode: "cursor", limit: 25, hasMore: true, nextCursor: "next-page" } });
});
