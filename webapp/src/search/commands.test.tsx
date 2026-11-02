import { describe, expect, it, vi } from "vitest";
import type { ProcessSample } from "../top/api";
import type { RunTraces } from "../traces/api";
import { runPath, searchGroups } from "./commands";

function proc(pid: number, name: string, command: string): ProcessSample {
  return { key: `${pid}:1`, pid, ppid: 1, name, command, groupKey: `other:user:${name}`, origin: { owner: "acme", scope: "user" }, processes: 1, rssBytes: 1024 };
}

const node = proc(4242, "node", "node server.js");
const nodeWorker = proc(4243, "node", "node worker.js");

const attached: RunTraces = {
  runId: "run-attached", mount: "/api/traces/run-attached", dir: "/data/run-attached", status: "succeeded", pid: 4242,
  captures: [{ kind: "http", ref: { kind: "http", stream: "proxy-1" } }],
};

function shape(groups: ReturnType<typeof searchGroups>) {
  return groups.map(group => ({ id: group.id, items: group.items.map(item => [item.id, item.label, item.description, item.keywords]) }));
}

describe("searchGroups", () => {
  it("offers a typed pid that no live process has, the live processes and programs, and the runs naming it", () => {
    const groups = searchGroups({ query: " 42 ", processes: [node, nodeWorker], runs: [attached], navigate: vi.fn() });

    expect(shape(groups)).toEqual([
      { id: "pid", items: [["pid:42", "Process 42", "and the processes under it", ["42"]]] },
      { id: "processes", items: [
        ["process:4242", "node", "pid 4242 · node server.js", ["4242"]],
        ["process:4243", "node", "pid 4243 · node worker.js", ["4243"]],
      ] },
      { id: "programs", items: [["program:node", "node", "2 processes", ["4242", "node server.js", "4243", "node worker.js"]]] },
      { id: "runs", items: [["run:run-attached", "run-attached", "pid 4242", ["succeeded", "4242", "http"]]] },
    ]);
  });

  it("drops the pid shortcut when a live process has exactly that pid", () => {
    const groups = searchGroups({ query: "4242", processes: [node], navigate: vi.fn() });
    expect(groups.map(group => group.id)).toEqual(["processes", "programs"]);
  });

  it("shows only the runs before anything is typed", () => {
    const groups = searchGroups({ query: "", processes: [node], runs: [attached], navigate: vi.fn() });
    expect(groups.map(group => group.id)).toEqual(["runs"]);
  });

  it.each([
    ["pid", "pid:42", "42", "/top/42"],
    ["process", "process:4242", "4242x", "/top/4242"],
    ["program", "program:Google Chrome Helper (GPU)", "chrome", "/top/Google%20Chrome%20Helper%20(GPU)"],
    ["run", "run:run-attached", "run", "/?run=run-attached"],
  ])("opens a %s's page", (_, id, query, to) => {
    const navigate = vi.fn();
    const chrome = proc(9001, "Google Chrome Helper (GPU)", "/Applications/Chrome");
    const item = searchGroups({ query, processes: [node, chrome], runs: [attached], navigate })
      .flatMap(group => group.items).find(candidate => candidate.id === id);

    item!.onSelect!({ item: item!, query, close: vi.fn() });

    expect(navigate).toHaveBeenCalledWith(to);
  });

  it("puts the run id in the overview's run parameter, escaped", () => {
    expect(runPath("run a&b")).toBe("/?run=run+a%26b");
  });
});
