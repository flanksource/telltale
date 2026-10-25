import { afterEach, describe, expect, it, vi } from "vitest";
import { fetchTopFrame, fetchTopProcesses, framesFromRecords } from "./api";
import { sampleFrame } from "./testFrames";

afterEach(() => vi.unstubAllGlobals());

describe("framesFromRecords", () => {
  it("groups rows into frames by tick, oldest first, keeping each tick's row order", () => {
    const [first, second] = [sampleFrame(1), sampleFrame(2)];
    const interleaved = [second.groups[0]!, first.groups[0]!, second.groups[1]!, first.groups[1]!, first.groups[2]!, first.groups[3]!];
    const frames = framesFromRecords(interleaved as unknown as Record<string, unknown>[]);
    expect(frames.map(frame => ({ tick: frame.tick, sampledAt: frame.sampledAt, keys: frame.groups.map(g => g.key) }))).toEqual([
      { tick: 1, sampledAt: first.sampledAt, keys: ["claude:1", "vitest:2", "chrome:3", "gavel:4"] },
      { tick: 2, sampledAt: second.sampledAt, keys: ["claude:1", "vitest:2"] },
    ]);
  });

  it("makes no frames of no rows", () => {
    expect(framesFromRecords([])).toEqual([]);
  });

  it("refuses a row without a numeric tick", () => {
    expect(() => framesFromRecords([{ key: "claude:1", tick: "1" }])).toThrow(/no numeric tick/);
  });
});

describe("fetchTopFrame", () => {
  it("asks for the latest frame with the choice of all and net", async () => {
    const frame = sampleFrame();
    const fetchMock = vi.fn(() => Promise.resolve(new Response(JSON.stringify(frame))));
    vi.stubGlobal("fetch", fetchMock);
    expect(await fetchTopFrame({ all: true, net: false })).toEqual(frame);
    expect(fetchMock).toHaveBeenCalledWith("/api/top?all=true&net=false", expect.anything());
  });

  it("reports the server's refusal", async () => {
    vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(new Response("net bytes are unsupported here\n", { status: 501 }))));
    await expect(fetchTopFrame({ all: false, net: true })).rejects.toThrow("read process groups failed (501): net bytes are unsupported here");
  });
});

describe("fetchTopProcesses", () => {
  const reading = { sampledAt: "2026-10-25T10:00:01Z", tick: 1, processes: [] };

  it.each([
    ["a program by name", { name: "Google Chrome Helper" }, "/api/top/processes?name=Google+Chrome+Helper&all=true"],
    ["a process and its subtree by pid", { pid: 4242 }, "/api/top/processes?pid=4242&all=true"],
  ])("asks for %s, with the processes no group holds", async (_what, target, url) => {
    const fetchMock = vi.fn(() => Promise.resolve(new Response(JSON.stringify(reading))));
    vi.stubGlobal("fetch", fetchMock);
    expect(await fetchTopProcesses(target)).toEqual(reading);
    expect(fetchMock).toHaveBeenCalledWith(url, expect.anything());
  });

  it("reports the server's refusal", async () => {
    vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(new Response("give a program's name= or a process's pid=, one of them\n", { status: 400 }))));
    await expect(fetchTopProcesses({ pid: 0 })).rejects.toThrow("read processes failed (400): give a program's name= or a process's pid=, one of them");
  });
});

describe("framesFromRecords origin", () => {
  it("refuses a row recorded without an origin", () => {
    expect(() => framesFromRecords([{ key: "claude:1", tick: 1 }])).toThrow(/has no origin/);
  });
});
