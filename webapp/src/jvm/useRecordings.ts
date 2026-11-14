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
  const start = useMutation({ mutationFn: (request: StartRequest) => startRun(request), onSuccess: run => {
    client.setQueryData(queryKey, (old: { runs: RunTraces[] } | undefined) => ({ runs: [run, ...(old?.runs ?? []).filter(item => item.runId !== run.runId)] }));
    persistFilter("recording", run.runId);
    refresh();
  } });
  const stop = useMutation({ mutationFn: stopRun, onSettled: refresh });
  return { query, recordings, start, stop };
}
