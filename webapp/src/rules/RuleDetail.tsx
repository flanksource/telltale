import { Button } from "@flanksource/clicky-ui/components";
import { CodeBlock, DataTable, type DataTableColumn } from "@flanksource/clicky-ui/data";
import { formatBytes } from "../top/groups";
import { HookLabel, StateBadge } from "./RuleStatus";
import type { Incident, Rule, RulePreview } from "./api";
import { TargetsView } from "./TargetsView";

type PreviewRow = { key: string; group: string; match: string; cores: number | null; cpu: number | null; rss: number; targets: string; error: string };
const previewColumns: DataTableColumn<PreviewRow>[] = [
  { key: "group", label: "Group", sortable: true, grow: true },
  { key: "match", label: "Match", render: (_, row) => <StateBadge state={row.match} /> },
  { key: "cores", label: "Cores", align: "right", render: (_, row) => row.cores === null ? "Unknown" : row.cores.toFixed(2) },
  { key: "cpu", label: "Host CPU", align: "right", render: (_, row) => row.cpu === null ? "Unknown" : `${row.cpu.toFixed(1)}%` },
  { key: "rss", label: "RSS", align: "right", render: (_, row) => formatBytes(row.rss) },
  { key: "targets", label: "Targets", grow: true, cellClassName: "break-words" },
  { key: "error", label: "Error", grow: true, cellClassName: "text-destructive whitespace-normal" },
];
export function RuleDetail({ rule, previews, incidents, onEvents, previewLoading, previewError }: { rule: Rule; previews: RulePreview[]; incidents: Incident[]; onEvents: () => void; previewLoading: boolean; previewError: string | undefined }) {
  const unattributed = previews.filter(item => item.error && item.group.members.length === 0);
  const rows: PreviewRow[] = previews.filter(item => !unattributed.includes(item)).map(item => ({
    key: item.group.key, group: item.group.name || item.group.key, match: item.error ? "error" : !item.known ? "unknown" : item.matched ? "matching" : "clear",
    cores: item.group.cores, cpu: item.group.cpu, rss: item.group.rss,
    targets: item.steps.map((step, index) => `${index + 1}: ${step.targets.processes?.length ?? 0} processes, ${step.targets.sessions?.length ?? 0} sessions`).join("; "),
    error: [item.error, ...item.steps.map(step => step.error)].filter(Boolean).join("; "),
  }));
  return (
    <section aria-label={`Rule ${rule.name}`} className="space-y-4 rounded-lg border border-border p-4">
      <div className="flex flex-wrap items-center justify-between gap-3"><h3 className="text-lg font-semibold">{rule.name}</h3><Button size="sm" variant="outline" onClick={onEvents}>View events for {rule.name}</Button></div>
      <div className="grid gap-4 md:grid-cols-2">
        <div className="min-w-0"><h4 className="mb-1 text-sm font-medium">Trigger · held for {rule.for}</h4><CodeBlock language="cel" source={rule.match} /></div>
        <div className="min-w-0"><h4 className="mb-1 text-sm font-medium">Recovery · held for {rule.recoverFor}</h4><CodeBlock language="cel" source={rule.recover || `!(${rule.match})`} /></div>
      </div>
      <h4 className="font-medium">Escalation steps</h4>
      <ol className="grid gap-3 md:grid-cols-2">
        {rule.steps.map((step, index) => <li key={index} className="min-w-0 space-y-2 rounded border border-border bg-muted/20 p-3">
          <div className="flex flex-wrap items-center gap-2 text-sm"><span className="font-semibold">{index + 1}. <HookLabel hook={step.hook} /></span><span className="text-muted-foreground">wait {step.wait}</span></div>
          <CodeBlock language="cel" source={step.hook.targets} />
          <p className="text-xs text-muted-foreground">Timeout {step.hook.timeout || "10s"}</p>
        </li>)}
      </ol>
      {incidents.length > 0 && <div className="space-y-2"><h4 className="font-medium">Incidents</h4>{incidents.map(incident => <div key={incident.id} className="flex flex-wrap items-center gap-2 text-sm"><StateBadge state={incident.state} /><code className="break-all">{incident.group}</code><span>{incident.step >= rule.steps.length ? "Steps complete" : `Step ${incident.step + 1} of ${rule.steps.length}`}</span>{incident.error && <span className="text-destructive">{incident.error}</span>}</div>)}</div>}
      <div className="space-y-2"><h4 className="font-medium">Live target preview</h4><p className="text-xs text-muted-foreground">CPU is percent of total host capacity. Open a group row to inspect exact targets. Previewing never executes hooks.</p><DataTable data={rows} columns={previewColumns} getRowId={row => row.key} loading={previewLoading} emptyMessage={previewError ? "Preview unavailable. Retry above." : "No attributed groups for this rule."} scrollContainerClassName="max-h-80" showFullscreenControl={false} renderExpandedRow={row => <div className="space-y-4 p-3">{previews.find(item => item.group.key === row.key)?.steps.map((step, index) => <div key={index} className="space-y-2"><h5 className="font-medium">Step {index + 1} · <HookLabel hook={step.hook} /></h5><TargetsView targets={step.targets} /></div>)}</div>} /></div>
      {unattributed.length > 0 && <details className="text-sm"><summary className="cursor-pointer text-muted-foreground">{unattributed.length} groups missing attribution</summary><ul className="mt-2 max-h-40 space-y-1 overflow-auto">{unattributed.map(item => <li key={item.group.key} className="break-all">{item.group.name || item.group.key}: {item.error}</li>)}</ul></details>}
      <details className="text-sm"><summary className="cursor-pointer text-muted-foreground">Rule configuration</summary><CodeBlock language="json" source={JSON.stringify(rule, null, 2)} /></details>
    </section>
  );
}
