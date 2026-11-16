import type { TaskSnapshot } from "@flanksource/clicky-ui/data";
import type { TraceRecord } from "./api";

/** The group a clicky task started outside any group is shown under. */
export const UNGROUPED_ID = "telltale:ungrouped";

const TERMINAL = new Set(["success", "failed", "warning", "canceled"]);

function snapshotOf(record: TraceRecord): TaskSnapshot {
  const { id, name, type, status } = record;
  if (typeof id !== "string" || !id || typeof name !== "string" || (type !== "task" && type !== "group") || typeof status !== "string") {
    throw new Error(`task record ${String(record.seq)}: expected id, name, type (task|group) and status`);
  }
  return record as unknown as TaskSnapshot;
}

function groupStatus(tasks: TaskSnapshot[]): string {
  if (tasks.some(task => !TERMINAL.has(task.status))) return "running";
  if (tasks.some(task => task.status === "failed")) return "failed";
  if (tasks.some(task => task.status === "warning")) return "warning";
  if (tasks.length > 0 && tasks.every(task => task.status === "canceled")) return "canceled";
  return "success";
}

/**
 * The task records of a clicky agent's task capture as the snapshots
 * clicky-ui's TaskProgress renders: each task at its latest record, under the
 * group it names. A group the capture saw no record of, or the tasks started
 * outside a group, get a group built from their tasks; every group's counts
 * and status are recomputed from its tasks' latest records, as a recorded
 * group snapshot is only as fresh as the moment the capture armed.
 */
export function taskSnapshots(records: TraceRecord[]): TaskSnapshot[] {
  const latest = new Map<string, TaskSnapshot>();
  for (const record of records) {
    const snapshot = snapshotOf(record);
    latest.set(snapshot.id, snapshot);
  }
  const groups = new Map<string, TaskSnapshot>();
  const groupByName = new Map<string, string>();
  for (const snapshot of latest.values()) {
    if (snapshot.type !== "group") continue;
    const key = snapshot.groupId || snapshot.id;
    groups.set(key, { ...snapshot, groupId: key });
    groupByName.set(snapshot.name, key);
  }
  const members = new Map<string, TaskSnapshot[]>();
  const tasks: TaskSnapshot[] = [];
  for (const snapshot of latest.values()) {
    if (snapshot.type !== "task") continue;
    const key = snapshot.groupId || (snapshot.group ? groupByName.get(snapshot.group) ?? `name:${snapshot.group}` : UNGROUPED_ID);
    if (!groups.has(key)) {
      const name = key === UNGROUPED_ID ? "Tasks" : snapshot.group ?? key;
      groups.set(key, { id: key, groupId: key, name, type: "group", status: "running" });
    }
    const task = { ...snapshot, groupId: key };
    members.set(key, [...members.get(key) ?? [], task]);
    tasks.push(task);
  }
  for (const [key, group] of groups) {
    const own = members.get(key) ?? [];
    groups.set(key, {
      ...group,
      status: groupStatus(own),
      total: own.length,
      completed: own.filter(task => task.status === "success" || task.status === "warning").length,
      failed: own.filter(task => task.status === "failed").length,
      running: own.filter(task => task.status === "running").length,
      canceled: own.filter(task => task.status === "canceled").length,
    });
  }
  return [...groups.values(), ...tasks];
}
