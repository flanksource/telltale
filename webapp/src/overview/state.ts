import { useEffect, useState } from "react";

interface OverviewState {
  activity: string;
  activityQ: string;
  otelSource: string;
  otelWindow: string;
  run: string;
}

const KEYS = ["activity", "activityQ", "otelSource", "otelWindow", "run"] as const;

function readState(): OverviewState {
  const params = new URLSearchParams(window.location.search);
  return {
    activity: params.get("activity") ?? "all", activityQ: params.get("activityQ") ?? "",
    otelSource: params.get("otelSource") ?? "", otelWindow: params.get("otelWindow") ?? "", run: params.get("run") ?? "",
  };
}

export function useOverviewState() {
  const [state, setState] = useState(readState);
  useEffect(() => {
    const restore = () => setState(readState());
    window.addEventListener("popstate", restore);
    return () => window.removeEventListener("popstate", restore);
  }, []);
  const params = new URLSearchParams(window.location.search);
  for (const key of KEYS) {
    if (state[key] && !(key === "activity" && state[key] === "all")) params.set(key, state[key]); else params.delete(key);
  }
  const search = params.toString();
  useEffect(() => {
    window.history.replaceState(null, "", `${window.location.pathname}${search ? `?${search}` : ""}${window.location.hash}`);
  }, [search]);
  return [state, (next: Partial<OverviewState>) => setState(previous => ({ ...previous, ...next })), search] as const;
}
