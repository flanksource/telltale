import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { fetchTraceRuns, startRun, stopRun, type RunTraces, type StartRequest } from "../traces/api";
import { agentRecordings } from "./captures";
import { persistFilter } from "./api";

export function useRecordings(agent: string, kind: string) {
  const client = useQueryClient();
  const queryKey = ["trace-runs", "jvm", agent, kind];
  const query = useQuery({ queryKey, queryFn: () => fetchTraceRuns({ kind, limit: 0 }), refetchInterval: 2000, retry: false });
  const recordings = agentRecordings(query.data?.runs ?? [], agent, kind);
  const refresh = () => {
    void client.invalidateQueries({ queryKey: ["trace-runs"] });
    void client.invalidateQueries({ queryKey: ["jvm-class-source", agent] });
    void client.invalidateQueries({ queryKey: ["jvm-patches", agent] });
    void client.invalidateQueries({ queryKey: ["jvm-patch-register", agent] });
  };
  const start = useMutation({ mutationFn: (request: StartRequest) => startRun(request), onSuccess: (run, request) => {
    if (kind === "jvm_trace") {
      const key = `telltale.jvm.${agent}.recent-targets`;
      const previous: unknown = JSON.parse(localStorage.getItem(key) ?? "[]");
      if (!Array.isArray(previous) || previous.some(value => typeof value !== "string")) throw new Error("Invalid recent method targets");
      const captures = request.traces?.jvm_trace;
      if (!captures) throw new Error("Method capture request is missing its jvm_trace capture");
      const submitted = [captures].flat().flatMap(capture => {
        if (!Array.isArray(capture.targets)) throw new Error("Method capture request is missing its targets");
        return capture.targets as unknown[];
      });
      const targets = submitted.map((row: unknown) => {
        if (!row || typeof row !== "object" || !("class" in row) || typeof row.class !== "string" || !("method" in row) || typeof row.method !== "string" || ("loader" in row && typeof row.loader !== "string")) throw new Error("Invalid method capture target");
        return `${"loader" in row ? row.loader : ""}|${row.class}#${row.method}`;
      });
      localStorage.setItem(key, JSON.stringify([...new Set([...targets, ...previous])].slice(0, 25)));
    }
    client.setQueryData(queryKey, (old: { runs: RunTraces[] } | undefined) => ({ runs: [run, ...(old?.runs ?? []).filter(item => item.runId !== run.runId)] }));
    persistFilter("recording", run.runId);
    refresh();
  } });
  const stop = useMutation({ mutationFn: stopRun, onSettled: refresh });
  return { query, recordings, start, stop };
}
