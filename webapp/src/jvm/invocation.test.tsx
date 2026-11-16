import { afterEach, expect, it, vi } from "vitest";
import { loadInvocation } from "./invocation";

afterEach(() => vi.unstubAllGlobals());

it("loads an invocation across result pages, with its parent and closing events", async () => {
  const seed = { key: "probe", threadId: 7, frame: 2, kind: "call", probeSeq: 2, parent: 1 };
  const request = vi.fn(async (input: string) => {
    const url = new URL(input, window.location.origin);
    if (url.pathname.endsWith("/seq")) return new Response(JSON.stringify({ seq: 50 }));
    expect(url.searchParams.get("q")).toBeNull();
    expect(url.searchParams.get("where")).toBe('row.threadId == 7 && row.key == "probe"');
    expect(url.searchParams.get("toSeq")).toBe("50");
    return new Response(JSON.stringify(url.searchParams.get("cursor") ? {
      rows: [{ ...seed, kind: "return", probeSeq: 3, value: "result" }, { ...seed, frame: 1, parent: 0, kind: "return", probeSeq: 4 }], nextCursor: "",
    } : { rows: [{ ...seed, frame: 1, parent: 0, kind: "enter", probeSeq: 1 }, seed, { ...seed, frame: 9, parent: 0, kind: "enter", probeSeq: 10 }], nextCursor: "next" }));
  });
  vi.stubGlobal("fetch", request);
  expect((await loadInvocation({ mount: "/api/traces/run", stream: "events", seed, signal: new AbortController().signal })).map(event => [event.frame, event.kind])).toEqual([[1, "enter"], [2, "call"], [2, "return"], [1, "return"]]);
  expect(request).toHaveBeenCalledTimes(3);
});

it("reports a missing invocation rather than displaying an empty tree as complete", async () => {
  vi.stubGlobal("fetch", vi.fn(async (url: string) => new Response(JSON.stringify(url.includes("/seq?") ? { seq: 1 } : { rows: [], nextCursor: "" }))));
  await expect(loadInvocation({ mount: "/api/traces/run", stream: "events", seed: { threadId: 7, frame: 2, kind: "return", probeSeq: 2 }, signal: new AbortController().signal })).rejects.toThrow("Entry event is missing");
});
