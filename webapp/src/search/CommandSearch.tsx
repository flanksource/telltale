import { useEffect, useState } from "react";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { CommandPalette, CommandPaletteTrigger } from "@flanksource/clicky-ui/components";
import { useRouter } from "@flanksource/clicky-ui/rpc";
import { fetchTopProcesses } from "../top/api";
import { fetchTraceRuns } from "../traces/api";
import { searchGroups } from "./commands";

const SEARCH_DEBOUNCE_MS = 150;

function useDebounced(value: string, ms: number): string {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(value), ms);
    return () => clearTimeout(timer);
  }, [value, ms]);
  return debounced;
}

/** ⌘K search over the live processes, by name, command or pid, and the trace runs, by id, command, pid, status or capture kind. */
export function CommandSearch() {
  const { navigate } = useRouter();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const text = useDebounced(query.trim(), SEARCH_DEBOUNCE_MS);
  const processes = useQuery({
    queryKey: ["top-search", text],
    queryFn: ({ signal }) => fetchTopProcesses({ q: text }, signal),
    enabled: open && text !== "",
    retry: false,
    placeholderData: keepPreviousData,
  });
  const runs = useQuery({ queryKey: ["trace-runs", {}], queryFn: () => fetchTraceRuns(), enabled: open, retry: false });
  const errors = [processes.error, runs.error].filter((error): error is Error => !!error);
  return (
    <>
      <CommandPaletteTrigger onClick={() => setOpen(true)} open={open} label="Search processes, pids and trace runs…" />
      <CommandPalette
        open={open}
        onOpenChange={setOpen}
        query={query}
        onQueryChange={setQuery}
        placeholder="Process name, command or pid, or a trace run…"
        loading={runs.isPending || (text !== "" && processes.isPending)}
        groups={searchGroups({
          query,
          ...(text && processes.data ? { processes: processes.data.processes } : {}),
          ...(runs.data ? { runs: runs.data.runs } : {}),
          navigate,
        })}
        footer={errors.length > 0
          ? <div role="alert" className="space-y-1 text-destructive">{errors.map(error => <p key={error.message}>{error.message}</p>)}</div>
          : "↑↓ navigate · ↵ open · esc close"}
      />
    </>
  );
}
