import { describe, expect, it } from "vitest";
import { taskSnapshots, UNGROUPED_ID } from "./taskSnapshots";

const task = (seq: number, id: string, status: string, extra: Record<string, unknown> = {}) =>
  ({ seq, phase: "status", id, name: `task ${id}`, type: "task", status, ...extra });

describe("task capture projection", () => {
  it("keeps each task's latest record under a group rebuilt from its tasks", () => {
    const snapshots = taskSnapshots([
      { seq: 1, phase: "baseline", id: "g1", groupId: "g1", name: "deploy", type: "group", status: "running", total: 9 },
      task(2, "a", "running", { groupId: "g1", group: "deploy" }),
      task(3, "b", "running", { group: "deploy" }),
      task(4, "a", "success", { groupId: "g1", group: "deploy" }),
      task(5, "b", "failed", { group: "deploy", error: "boom" }),
      task(6, "c", "success"),
    ]);
    expect(snapshots).toEqual([
      expect.objectContaining({ id: "g1", groupId: "g1", type: "group", status: "failed", total: 2, completed: 1, failed: 1, running: 0 }),
      { id: UNGROUPED_ID, groupId: UNGROUPED_ID, name: "Tasks", type: "group", status: "success", total: 1, completed: 1, failed: 0, running: 0, canceled: 0 },
      expect.objectContaining({ id: "a", groupId: "g1", status: "success" }),
      expect.objectContaining({ id: "b", groupId: "g1", status: "failed", error: "boom" }),
      expect.objectContaining({ id: "c", groupId: UNGROUPED_ID, status: "success" }),
    ]);
  });

  it("builds a group the capture saw no record of from its tasks' group name", () => {
    const snapshots = taskSnapshots([task(1, "a", "running", { group: "migrate" })]);
    expect(snapshots[0]).toMatchObject({ id: "name:migrate", name: "migrate", type: "group", status: "running", total: 1, running: 1 });
    expect(snapshots[1]).toMatchObject({ id: "a", groupId: "name:migrate" });
  });

  it("refuses a record that is no task snapshot", () => {
    expect(() => taskSnapshots([{ seq: 7, phase: "status", name: "x" }])).toThrow(/task record 7/);
  });
});
