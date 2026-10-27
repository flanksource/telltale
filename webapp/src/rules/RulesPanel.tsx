import { useQuery } from "@tanstack/react-query";
import { DataTable, CodeBlock, type DataTableColumn } from "@flanksource/clicky-ui/data";
import { useRefreshInterval } from "../monitoring/RefreshControl";
import { fetchRules, type RulePreview } from "./api";
import { useRuleConfig, useRuleFilters, useRulesStatus } from "./useRules";
import { QueryError, RulesStatus, StateBadge } from "./RuleStatus";
import { RuleDetail } from "./RuleDetail";

type RuleRow = { name: string; scope: string; trigger: string; hold: string; state: string; matches: number; incidents: number };
export function RulesPanel() {
  const status = useRulesStatus();
  const config = useRuleConfig(status.data);
  const interval = useRefreshInterval();
  const [filters, setFilters] = useRuleFilters();
  const preview = useQuery({ queryKey: ["rules-preview"], queryFn: ({ signal }) => fetchRules<RulePreview[]>("/preview", signal), enabled: status.data?.enabled === true, retry: false, refetchInterval: interval });
  const rows: RuleRow[] = (config.data?.config.rules ?? []).map(rule => {
    const incidents = status.data?.incidents.filter(incident => incident.rule === rule.name && incident.state !== "closed") ?? [];
    const readings = preview.data?.filter(item => item.rule === rule.name);
    const matches = readings?.filter(item => item.matched).length ?? 0;
    const state = status.data?.error || preview.isError ? "error" : incidents.find(incident => incident.state === "breached")?.state || incidents[0]?.state || (matches > 0 ? "matching" : !readings?.some(item => item.known) ? "unknown" : "clear");
    return { name: rule.name, scope: rule.groupBy, trigger: rule.match, hold: rule.for, state, matches, incidents: incidents.length };
  });
  const columns: DataTableColumn<RuleRow>[] = [
    { key: "name", label: "Rule", render: (_, row) => <button type="button" className="font-medium text-primary underline-offset-4 hover:underline" onClick={() => setFilters({ rule: row.name })}>{row.name}</button> },
    { key: "scope", label: "Scope" },
    { key: "trigger", label: "Trigger", grow: true, minWidth: 200, render: (_, row) => <CodeBlock language="cel" source={row.trigger} inline /> },
    { key: "hold", label: "Hold" },
    { key: "state", label: "State", render: (_, row) => <StateBadge state={row.state} /> },
    { key: "matches", label: "Matches", align: "right" },
    { key: "incidents", label: "Incidents", align: "right" },
  ];
  const selected = filters.rule ? config.data?.config.rules.find(rule => rule.name === filters.rule) : config.data?.config.rules[0];
  return <div className="space-y-4">
    <RulesStatus query={status} />
    {config.isError && <QueryError error={config.error} retry={() => void config.refetch()} />}
    {preview.isError && <QueryError error={preview.error} retry={() => void preview.refetch()} />}
    {status.data?.enabled && <DataTable data={rows} columns={columns} getRowId={row => row.name} loading={config.isPending} emptyMessage="No rules configured." scrollContainerClassName="max-h-72" showFullscreenControl={false} />}
    {config.data && filters.rule && !selected && <p role="alert">Rule {filters.rule} is not configured. Select an available rule above.</p>}
    {selected && <RuleDetail rule={selected} previews={preview.data?.filter(item => item.rule === selected.name) ?? []} incidents={status.data?.incidents.filter(incident => incident.rule === selected.name) ?? []} previewLoading={preview.isPending} previewError={preview.error?.message} onEvents={() => {
      const params = new URLSearchParams(window.location.search);
      params.set("rulesRule", selected.name);
      params.delete("rulesEvent");
      params.set("topTab", "events");
      window.history.pushState(null, "", `${window.location.pathname}?${params}`);
      window.dispatchEvent(new PopStateEvent("popstate"));
    }} />}
  </div>;
}
