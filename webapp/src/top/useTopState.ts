import { useEffect, useState } from "react";
import { GROUP_KINDS, SCOPES, type GroupKind, type Scope } from "./api";
import { DEFAULT_VIEW, SORT_KEYS, type GroupsView, type SortDir, type SortKey } from "./groups";
import { GROUPINGS, type GroupBy } from "./grouping";

export type TopTab = "live" | "recorded" | "rules" | "events";

/** The process groups panel's state, which lives in the URL: the tab, the view, the network choice and the selected recording. */
export interface TopState extends GroupsView {
  tab: TopTab;
  /** Read each process's network bytes (live only). */
  net: boolean;
  run?: string;
}

// The parameters are prefixed so they stay clear of the trace runs' own (q, status, kind, run).
const PARAMS = ["topTab", "topQ", "topKind", "topScope", "topOther", "topSort", "topDir", "topNet", "topRun", "topGroupBy"];

function readTopState(): TopState {
  const params = new URLSearchParams(window.location.search);
  const kind = params.get("topKind") ?? "";
  const scope = params.get("topScope") ?? "";
  const sort = params.get("topSort") ?? "";
  const dir = params.get("topDir") ?? "";
  const run = params.get("topRun");
  const groupBy = params.get("topGroupBy") ?? "tree";
  if (!(GROUPINGS as readonly string[]).includes(groupBy)) throw new Error(`Unknown topGroupBy "${groupBy}"`);
  return {
    groupBy: groupBy as GroupBy,
    tab: ["recorded", "rules", "events"].includes(params.get("topTab") ?? "") ? params.get("topTab") as TopTab : "live",
    q: params.get("topQ") ?? "",
    kind: (GROUP_KINDS as readonly string[]).includes(kind) ? kind as GroupKind : "",
    scope: (SCOPES as readonly string[]).includes(scope) ? scope as Scope : "",
    showOther: params.get("topOther") === "1",
    sort: (SORT_KEYS as readonly string[]).includes(sort) ? sort as SortKey : DEFAULT_VIEW.sort,
    dir: dir === "asc" || dir === "desc" ? dir as SortDir : DEFAULT_VIEW.dir,
    net: params.get("topNet") === "1",
    ...(run ? { run } : {}),
  };
}

/** The URL parameters of the choices that differ from the defaults. */
function topParams(state: TopState): Array<[string, string]> {
  const entries: Array<[string, string | false]> = [
    ["topTab", state.tab !== "live" && state.tab],
    ["topQ", state.q],
    ["topKind", state.kind],
    ["topScope", state.scope],
    ["topOther", state.showOther && "1"],
    ["topSort", state.sort !== DEFAULT_VIEW.sort && state.sort],
    ["topDir", state.dir !== DEFAULT_VIEW.dir && state.dir],
    ["topNet", state.net && "1"],
    ["topRun", state.run ?? ""],
    ["topGroupBy", state.groupBy !== "tree" && state.groupBy],
  ];
  return entries.filter((entry): entry is [string, string] => !!entry[1]);
}

export function useTopState(): [TopState, (next: Partial<TopState>) => void] {
  const [state, setState] = useState(readTopState);
  useEffect(() => {
    const onPop = () => setState(readTopState());
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);
  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    for (const name of PARAMS) params.delete(name);
    for (const [name, value] of topParams(state)) params.set(name, value);
    const search = params.toString();
    window.history.replaceState(null, "", `${window.location.pathname}${search ? `?${search}` : ""}${window.location.hash}`);
  }, [state]);
  return [state, next => setState(previous => ({ ...previous, ...next }))];
}
