import { useSyncExternalStore } from "react";
import { useIsFetching, useQueryClient, type Query } from "@tanstack/react-query";
import { Button } from "@flanksource/clicky-ui/components";

const STORAGE_KEY = "telltale.refreshIntervalMs";
const CHANGE_EVENT = "telltale-refresh-interval";
const INTERVALS = [0, 1000, 2000, 5000, 10000, 30000, 60000];
const monitoringQuery = (query: Query) => /^(top-|rules-)/.test(String(query.queryKey[0]));

function readInterval(): number {
  const saved = localStorage.getItem(STORAGE_KEY);
  const interval = saved === null ? 1000 : Number(saved);
  if (!INTERVALS.includes(interval)) throw new Error(`Invalid saved refresh interval: ${saved}`);
  return interval;
}
function subscribe(onChange: () => void): () => void {
  window.addEventListener(CHANGE_EVENT, onChange);
  window.addEventListener("storage", onChange);
  return () => { window.removeEventListener(CHANGE_EVENT, onChange); window.removeEventListener("storage", onChange); };
}
export function useRefreshInterval(): number | false {
  const interval = useSyncExternalStore(subscribe, readInterval);
  return interval === 0 ? false : interval;
}
export function RefreshControl() {
  const interval = useRefreshInterval();
  const client = useQueryClient();
  const fetching = useIsFetching({ predicate: monitoringQuery });
  return (
    <div className="flex flex-wrap items-center gap-2 text-sm">
      <label className="flex items-center gap-2">
        Refresh interval
        <select className="rounded border border-border bg-background px-2 py-1" value={interval || 0} onChange={event => {
          localStorage.setItem(STORAGE_KEY, event.target.value);
          window.dispatchEvent(new Event(CHANGE_EVENT));
        }}>
          {INTERVALS.map(value => <option key={value} value={value}>{value === 0 ? "Paused" : `${value / 1000}s`}</option>)}
        </select>
      </label>
      <Button variant="outline" size="sm" disabled={fetching > 0} onClick={() => void client.refetchQueries({ type: "active", predicate: monitoringQuery })}>Refresh now</Button>
    </div>
  );
}
