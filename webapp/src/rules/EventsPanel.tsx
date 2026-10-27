import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Button } from "@flanksource/clicky-ui/components";
import { CodeBlock, DataTable, type DataTableColumn } from "@flanksource/clicky-ui/data";
import { useRefreshInterval } from "../monitoring/RefreshControl";
import { fetchRuleEvents, type EventFilters, type RuleEvent } from "./api";
import { useDebouncedFilters, useRuleConfig, useRuleFilters, useRulesStatus } from "./useRules";
import { HookLabel, QueryError, RulesStatus, StateBadge } from "./RuleStatus";
import { TargetsView } from "./TargetsView";

type EventRow = { id: string; at: string; rule: string; group: string; incident: string; kind: string; outcome: string; mode: string; step: string; event: RuleEvent };
const input = "min-w-0 rounded border border-border bg-background px-2 py-1";

export function EventsPanel() {
  const status = useRulesStatus();
  const config = useRuleConfig(status.data);
  const interval = useRefreshInterval();
  const [filters, setFilters] = useRuleFilters();
  const criteria = useMemo<EventFilters>(() => ({ rule: filters.rule, group: filters.group, incident: filters.incident, since: filters.since, limit: filters.limit }), [filters.rule, filters.group, filters.incident, filters.since, filters.limit]);
  const debounced = useDebouncedFilters(criteria);
  const [payloadOpen, setPayloadOpen] = useState(false);
  const query = useQuery({ queryKey: ["rules-events", debounced], queryFn: ({ signal }) => fetchRuleEvents(debounced, signal), enabled: status.data?.enabled === true, retry: false, refetchInterval: interval });
  const rows: EventRow[] = (query.data ?? []).map(event => ({ id: `${event.mode}:${event.kind}:${event.incident.id}:${event.incident.step}:${event.at}`, at: event.at, rule: event.incident.rule, group: event.incident.group, incident: event.incident.id, kind: event.kind, outcome: event.result?.status || event.incident.state, mode: event.mode || "unknown", step: event.decision ? String(event.decision.incident.step + 1) : "—", event })).reverse();
  const selected = rows.find(row => row.id === filters.selectedEvent)?.event;
  const columns: DataTableColumn<EventRow>[] = [
    { key: "at", label: "Time", kind: "timestamp", sortable: true },
    { key: "rule", label: "Rule", sortable: true },
    { key: "group", label: "Group", grow: true, cellClassName: "break-all" },
    { key: "incident", label: "Incident", render: (_, row) => <code className="block max-w-40 truncate" title={row.incident}>{row.incident}</code> },
    { key: "kind", label: "Event" },
    { key: "outcome", label: "Outcome", render: (_, row) => <StateBadge state={row.outcome} /> },
    { key: "mode", label: "Mode" },
    { key: "step", label: "Step", align: "right" },
    { key: "inspect", label: "Details", render: (_, row) => <Button variant="outline" size="sm" onClick={() => { setPayloadOpen(false); setFilters({ selectedEvent: row.id }); }}>Inspect event</Button> },
  ];
  const names = config.data?.config.rules.map(rule => rule.name) ?? [];
  return <div className="space-y-4">
    <RulesStatus query={status} />
    <div className="flex flex-wrap items-end gap-3 text-sm">
      <label className="flex min-w-40 flex-col gap-1">Rule<select className={input} value={filters.rule} onChange={event => setFilters({ rule: event.target.value })}><option value="">All rules</option>{filters.rule && !names.includes(filters.rule) && <option value={filters.rule}>{filters.rule}</option>}{names.map(name => <option key={name} value={name}>{name}</option>)}</select></label>
      <label className="flex min-w-40 flex-1 flex-col gap-1">Group<input className={input} value={filters.group} onChange={event => setFilters({ group: event.target.value })} /></label>
      <label className="flex min-w-40 flex-1 flex-col gap-1">Incident<input className={input} value={filters.incident} onChange={event => setFilters({ incident: event.target.value })} /></label>
      <label className="flex min-w-48 flex-col gap-1">Since (RFC3339)<input className={input} placeholder="2026-10-27T12:00:00Z" value={filters.since} onChange={event => setFilters({ since: event.target.value })} /></label>
      <label className="flex flex-col gap-1">Event limit<select className={input} value={filters.limit} onChange={event => setFilters({ limit: event.target.value })}>{[...new Set(["25", "100", "500", "1000", filters.limit])].map(value => <option key={value} value={value}>{value}</option>)}</select></label>
      <Button variant="outline" size="sm" onClick={() => setFilters({ rule: "", group: "", incident: "", since: "", limit: "100", selectedEvent: "" })}>Clear filters</Button>
    </div>
    {config.isError && <QueryError error={config.error} retry={() => void config.refetch()} />}
    {query.isError && <QueryError error={query.error} retry={() => void query.refetch()} />}
    {status.data?.enabled && <><p className="text-xs text-muted-foreground">Latest {filters.limit} matching events · newest first · {rows.length} shown</p><DataTable data={rows} columns={columns} getRowId={row => row.id} loading={query.isPending} emptyMessage={query.isError ? "Event history unavailable. Retry above." : "No events match these filters."} scrollContainerClassName="max-h-[32rem]" showFullscreenControl={false} /></>}
    {query.isSuccess && filters.selectedEvent && !selected && <p role="alert" className="text-sm">The selected event is outside the loaded history. Increase the event limit or clear the filters.</p>}
    {selected && <section aria-label="Event details" className="space-y-3 rounded-lg border border-border p-4">
      <div className="flex items-center justify-between gap-3"><h3 className="font-semibold">{selected.incident.rule} · {selected.kind}</h3><Button variant="outline" size="sm" onClick={() => setFilters({ selectedEvent: "" })}>Close event</Button></div>
      {selected.decision && <div className="space-y-2 text-sm"><HookLabel hook={selected.decision.hook} /><TargetsView targets={selected.decision.targets} /></div>}
      {selected.result?.coalesced && <p className="text-sm">Coalesced with an identical action from the same sample.</p>}
      {selected.result?.error && <p role="alert" className="break-words text-sm text-destructive">{selected.result.error}</p>}
      {selected.result?.targets?.some(target => target.error) && <ul className="text-sm text-destructive">{selected.result.targets.filter(target => target.error).map(target => <li key={target.key} className="break-all">{target.key}: {target.error}</li>)}</ul>}
      {selected.result?.exitCode !== undefined && <p className="text-sm">Exit code: {selected.result.exitCode}</p>}
      {selected.result?.stdout && <div><h4 className="mb-1 text-sm font-medium">Standard output</h4><CodeBlock source={selected.result.stdout} /></div>}
      {selected.result?.stderr && <div><h4 className="mb-1 text-sm font-medium">Standard error</h4><CodeBlock source={selected.result.stderr} /></div>}
      <details open={payloadOpen} onToggle={event => setPayloadOpen(event.currentTarget.open)}><summary className="cursor-pointer text-sm text-muted-foreground">Full event</summary>{payloadOpen && <CodeBlock language="json" source={JSON.stringify(selected, null, 2)} />}</details>
    </section>}
  </div>;
}
