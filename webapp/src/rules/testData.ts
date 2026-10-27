import { vi } from "vitest";
import type { Incident, RuleConfig, RuleEvent, RulePreview, RuleStatus } from "./api";

export const incident: Incident = { id: "incident-1", rule: "browser-limit", group: "host", revision: "revision-1", state: "breached", step: 1, since: "2026-10-27T12:00:00Z", readyAt: "2026-10-27T12:00:45Z", recoveringAt: "0001-01-01T00:00:00Z", updatedAt: "2026-10-27T12:00:15Z" };
export const config: RuleConfig = { revision: "revision-1", bindings: {}, config: { version: 1, rules: [{ name: "browser-limit", groupBy: "host", match: "group.members.size() > 4", for: "15s", recoverFor: "10s", steps: [{ wait: "0s", hook: { type: "renice", nice: 10, targets: "group.members" } }, { wait: "30s", hook: { type: "signal", signal: "SIGTERM", targets: "group.members" } }] }] } };
export const status: RuleStatus = { enabled: true, enforcing: false, revision: "revision-1", sampledAt: "2026-10-27T12:00:15Z", incidents: [incident] };
export const preview: RulePreview[] = [{ rule: "browser-limit", known: true, matched: true, group: { key: "host", name: "Host", kind: "host", context: {}, cores: 2, cpu: 25, rss: 1024, mem: 1, members: [], sessions: [] }, steps: [] }];
export const events: RuleEvent[] = [{ kind: "result", at: "2026-10-27T12:00:15Z", mode: "observe", incident, result: { status: "observed", stdout: "hook output" } }];

export function serveRules(): void {
  vi.stubGlobal("fetch", vi.fn((url: string) => {
    const body = url === "/api/rules/status" ? status : url === "/api/rules" ? config : url === "/api/rules/preview" ? preview : url.startsWith("/api/rules/events?") ? events : undefined;
    return Promise.resolve(new Response(JSON.stringify(body), { status: body ? 200 : 404, headers: { "Content-Type": "application/json" } }));
  }));
}
