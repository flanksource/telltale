import type { GroupContext } from "../top/api";
import { failure } from "../traces/api";

export interface RuleResources { cores: number | null; cpu: number | null; rss: number; mem: number }
export interface RuleProcess extends RuleResources {
  key: string; pid: number; ppid: number; name: string; command: string; context: GroupContext;
  groupName: string; groupRoot: boolean; groupPath: string[];
}
export interface RuleSession { key: string; agent: string; id: string; workspace: string; surface: string; anchors: string[]; error?: string }
export interface RuleGroup extends RuleResources { key: string; name: string; kind: string; context: GroupContext; members: RuleProcess[]; sessions: RuleSession[] }
export interface Hook {
  type: "renice" | "signal" | "session-interrupt" | "exec"; targets: string;
  nice?: number; signal?: string; via?: string; key?: string; endpoint?: string; command?: string[]; timeout?: string;
}
export interface Rule { name: string; groupBy: string; match: string; for: string; recover?: string; recoverFor: string; steps: Array<{ wait: string; hook: Hook }> }
export interface RuleConfig { config: { version: number; rules: Rule[] }; revision: string; bindings: Record<string, string> }
export interface Incident {
  id: string; rule: string; group: string; revision: string; state: string; step: number;
  since: string; readyAt: string; recoveringAt: string; updatedAt: string; error?: string;
}
export interface RuleStatus { enabled: boolean; enforcing: boolean; revision: string; sampledAt: string; error?: string; incidents: Incident[] }
export interface Targets { processes?: RuleProcess[]; sessions?: RuleSession[] }
export interface RulePreview { rule: string; group: RuleGroup; matched: boolean; known: boolean; error?: string; steps: Array<{ hook: Hook; targets: Targets; error?: string }> }
export interface Decision { incident: Incident; group: RuleGroup; host: { cores: number; memoryTotalBytes: number }; hook: Hook; targets: Targets; sampledAt: string }
export interface HookResult { status: string; coalesced?: boolean; targets?: Array<{ key: string; error?: string }>; exitCode?: number; stdout?: string; stderr?: string; error?: string }
export interface RuleEvent { kind: string; at: string; incident: Incident; decision?: Decision; result?: HookResult; mode?: "observe" | "enforce" }
export interface EventFilters { rule: string; group: string; incident: string; since: string; limit: string }

export async function fetchRules<T>(operation: string, signal?: AbortSignal): Promise<T> {
  const response = await fetch(`/api/rules${operation}`, { headers: { Accept: "application/json" }, ...(signal ? { signal } : {}) });
  if (!response.ok) throw await failure(response, "read resource rules");
  return response.json() as Promise<T>;
}

export function fetchRuleEvents(filters: EventFilters, signal?: AbortSignal): Promise<RuleEvent[]> {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(filters)) if (value) query.set(key, value);
  return fetchRules(`/events?${query}`, signal);
}
