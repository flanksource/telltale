import type { QueryKey } from "@tanstack/react-query";
import { useSnapshotQuery } from "../monitoring/useSnapshotQuery";
import { fetchTraceRuns, runFilterQuery, TRACES_ENDPOINT, type RunFilter } from "./api";

export function useRunList(key: QueryKey, filter: RunFilter = {}) {
  const params = new URLSearchParams(runFilterQuery(filter));
  params.set("follow", "1");
  return useSnapshotQuery({ key, read: () => fetchTraceRuns(filter), stream: `${TRACES_ENDPOINT}?${params}` });
}
