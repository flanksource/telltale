import { Button } from "@flanksource/clicky-ui/components";
import { Badge } from "@flanksource/clicky-ui/data";
import type { Hook } from "./api";
import type { useRulesStatus } from "./useRules";

export function StateBadge({ state }: { state: string }) {
  const status = ["failed", "breached", "error"].includes(state) ? "error" : ["pending", "recovering", "unknown"].includes(state) ? "warning" : ["succeeded", "closed", "clear"].includes(state) ? "success" : "info";
  return <Badge variant="status" status={status}>{state}</Badge>;
}
export function HookLabel({ hook }: { hook: Hook }) {
  return <span>{hook.type === "renice" ? `renice → ${hook.nice}` : hook.type === "signal" ? hook.signal : hook.type === "session-interrupt" ? `${hook.via} interrupt${hook.key ? ` · ${hook.key}` : ""}` : hook.command?.join(" ")}</span>;
}
export function QueryError({ error, retry }: { error: Error; retry: () => void }) {
  return <div className="flex flex-wrap items-center gap-3"><p role="alert" className="text-sm text-destructive">{error.message}</p><Button variant="outline" size="sm" onClick={retry}>Retry</Button></div>;
}
export function RulesStatus({ query }: { query: ReturnType<typeof useRulesStatus> }) {
  if (query.isPending) return <p role="status">Reading monitoring status…</p>;
  if (query.isError) return <QueryError error={query.error} retry={() => void query.refetch()} />;
  const status = query.data;
  if (!status.enabled) return <p className="rounded border border-border bg-muted/30 p-4 text-sm">Start Telltale with <code>serve --rules &lt;file&gt;</code> to observe resource rules. Add <code>--enforce-rules</code> to execute configured hooks.</p>;
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-3 text-sm">
        <Badge variant="status" status={status.enforcing ? "warning" : "info"}>{status.enforcing ? "Enforcing" : "Observation only"}</Badge>
        <span>Active incidents: {status.incidents.filter(incident => incident.state !== "closed").length}</span>
        <time aria-label="Latest rule sample" dateTime={status.sampledAt} className="text-muted-foreground">{status.sampledAt?.startsWith("0001") ? "Waiting for first sample" : `Sampled ${new Date(status.sampledAt).toLocaleTimeString()}`}</time>
      </div>
      <p className="text-xs text-muted-foreground">Refresh controls update this view. Background rule evaluation continues while refresh is paused.</p>
      {status.error && <p role="alert" className="text-sm text-destructive">{status.error}</p>}
    </div>
  );
}
