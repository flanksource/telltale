import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useHistoryRoute } from "@flanksource/clicky-ui/hooks";
import { useRefreshInterval } from "../monitoring/RefreshControl";
import { fetchRules, type EventFilters, type RuleConfig, type RuleStatus } from "./api";

export function useRulesStatus() {
  const interval = useRefreshInterval();
  return useQuery({ queryKey: ["rules-status"], queryFn: ({ signal }) => fetchRules<RuleStatus>("/status", signal), retry: false, refetchInterval: interval });
}
export function useRuleConfig(status: RuleStatus | undefined) {
  return useQuery({ queryKey: ["rules-config", status?.revision], queryFn: ({ signal }) => fetchRules<RuleConfig>("", signal), enabled: status?.enabled === true, retry: false, staleTime: Infinity });
}
export interface RuleViewState extends EventFilters { selectedEvent: string }
const fields = { rule: "rulesRule", group: "rulesGroup", incident: "rulesIncident", since: "rulesSince", limit: "rulesLimit", selectedEvent: "rulesEvent" } as const;
function parseFilters(_pathname: string, search: string): RuleViewState {
  const params = new URLSearchParams(search);
  return { rule: params.get(fields.rule) ?? "", group: params.get(fields.group) ?? "", incident: params.get(fields.incident) ?? "", since: params.get(fields.since) ?? "", limit: params.get(fields.limit) ?? "100", selectedEvent: params.get(fields.selectedEvent) ?? "" };
}
function buildFilters(filters: RuleViewState): string {
  const params = new URLSearchParams(window.location.search);
  for (const key of Object.keys(fields) as Array<keyof RuleViewState>) {
    if (filters[key] && !(key === "limit" && filters[key] === "100")) params.set(fields[key], filters[key]);
    else params.delete(fields[key]);
  }
  return `${window.location.pathname}${params.size ? `?${params}` : ""}${window.location.hash}`;
}
export function useRuleFilters() { return useHistoryRoute({ parse: parseFilters, build: buildFilters }); }
export function useDebouncedFilters(filters: EventFilters): EventFilters {
  const [debounced, setDebounced] = useState(filters);
  useEffect(() => {
    const timer = window.setTimeout(() => setDebounced(filters), 250);
    return () => window.clearTimeout(timer);
  }, [filters]);
  return debounced;
}
