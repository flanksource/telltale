import type { CommandGroup, CommandItem } from "@flanksource/clicky-ui/components";
import { UiActivity, UiStack, UiTerminal } from "@flanksource/clicky-ui/icons";
import type { RunTraces } from "../traces/api";
import type { ProcessSample } from "../top/api";

// The pure side of the ⌘K search: the palette's groups for a query, the live
// processes the server matched and the trace runs it holds.

export const PROCESS_LIMIT = 8;
export const PROGRAM_LIMIT = 5;
export const RUN_LIMIT = 8;

export interface SearchSources {
  query: string;
  /** The live processes whose name, command or pid holds the query; undefined until they are read. */
  processes?: ProcessSample[];
  runs?: RunTraces[];
  navigate: (to: string) => void;
}

/** Where the overview selects a run. */
export function runPath(runId: string): string {
  return `/?${new URLSearchParams({ run: runId })}`;
}

function runItem(run: RunTraces, navigate: (to: string) => void): CommandItem {
  const target = run.command?.join(" ") ?? (run.pid ? `pid ${run.pid}` : undefined);
  return {
    id: `run:${run.runId}`,
    label: run.runId,
    icon: UiActivity,
    ...(target ? { description: target } : {}),
    keywords: [run.status, ...(run.pid ? [String(run.pid)] : []), ...(run.captures ?? []).map(capture => capture.kind)],
    trailing: <span className="text-xs text-muted-foreground">{run.status}</span>,
    onSelect: () => navigate(runPath(run.runId)),
  };
}

function programItems(processes: ProcessSample[], navigate: (to: string) => void): CommandItem[] {
  const byName = new Map<string, ProcessSample[]>();
  for (const process of processes) byName.set(process.name, [...(byName.get(process.name) ?? []), process]);
  return [...byName.entries()].map(([name, members]) => ({
    id: `program:${name}`,
    label: name,
    icon: UiStack,
    description: `${members.length} ${members.length === 1 ? "process" : "processes"}`,
    keywords: members.flatMap(member => [String(member.pid), member.command]),
    onSelect: () => navigate(`/top/${encodeURIComponent(name)}`),
  }));
}

/** The palette's groups: a typed pid no live process has, the live processes and programs matching the query, and the trace runs. */
export function searchGroups({ query, processes, runs, navigate }: SearchSources): CommandGroup[] {
  const text = query.trim();
  const groups: CommandGroup[] = [];
  if (/^\d+$/.test(text) && !processes?.some(process => String(process.pid) === text)) {
    groups.push({
      id: "pid",
      heading: "Process id",
      items: [{ id: `pid:${text}`, label: `Process ${text}`, icon: UiTerminal, description: "and the processes under it", keywords: [text], onSelect: () => navigate(`/top/${text}`) }],
    });
  }
  if (text && processes) {
    groups.push({
      id: "processes",
      heading: "Processes",
      limit: PROCESS_LIMIT,
      items: processes.map(process => ({
        id: `process:${process.pid}`,
        label: process.name,
        icon: UiTerminal,
        description: `pid ${process.pid} · ${process.command}`,
        keywords: [String(process.pid)],
        onSelect: () => navigate(`/top/${process.pid}`),
      })),
    });
    groups.push({ id: "programs", heading: "Programs", limit: PROGRAM_LIMIT, items: programItems(processes, navigate) });
  }
  if (runs) groups.push({ id: "runs", heading: "Trace runs", limit: RUN_LIMIT, items: runs.map(run => runItem(run, navigate)) });
  return groups;
}
