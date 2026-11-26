import { useEffect, useRef } from "react";
import { useQuery, useQueryClient, type QueryKey, type QueryFunction } from "@tanstack/react-query";
import { useLiveSnapshot } from "@flanksource/clicky-ui/hooks";

export function useSnapshotQuery<T, Change = T>({ key, read, stream, enabled = true, merge }: {
  key: QueryKey;
  read: QueryFunction<T>;
  stream: string | null;
  enabled?: boolean;
  merge?: (previous: T | undefined, change: Change) => T;
}) {
  const client = useQueryClient();
  const query = useQuery({ queryKey: key, queryFn: read, enabled, retry: false, refetchOnWindowFocus: false, refetchOnReconnect: false });
  const updates = useLiveSnapshot<T, Change>(enabled ? stream : null, merge);
  const keyRef = useRef(key);
  keyRef.current = key;
  useEffect(() => {
    if (!enabled || !stream || updates.data === undefined) return;
    void client.cancelQueries({ queryKey: keyRef.current, exact: true }, { revert: false });
    client.setQueryData(keyRef.current, updates.data);
  }, [client, enabled, stream, updates.data]);
  const error = updates.error ?? query.error;
  return { ...query, error, isError: !!error, refetch: () => {
    if (updates.error && stream) updates.refresh();
    return query.refetch();
  } };
}
