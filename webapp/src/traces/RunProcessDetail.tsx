import { useMemo } from "react";
import { GoProfileView, ProcessTraceDetail, armedFacets } from "@flanksource/clicky-ui/data";
import { Button, Section } from "@flanksource/clicky-ui/components";
import { UiLoader, UiRefresh } from "@flanksource/clicky-ui/icons";
import type { ProcessTraceState } from "./useProcessTrace";

export function RunProcessDetail({ trace, selectedKey }: { trace: ProcessTraceState; selectedKey: string | null }) {
  const selected = useMemo(() => trace.nodes.find(node => node.key === selectedKey) ?? null, [trace.nodes, selectedKey]);
  const canRefresh = trace.canCollect && armedFacets(trace.samples).length > 0;
  const hasProfiles = !!selected && trace.goprof.some(record => record.key === selected.key);
  return <div className="flex h-full min-h-0 flex-col">
    {selected && canRefresh && <div className="flex shrink-0 items-center justify-end gap-2 border-b border-border px-density-4 py-density-1">
      {trace.refreshError && <span className="min-w-0 truncate text-xs text-destructive" role="alert" title={trace.refreshError}>{trace.refreshError}</span>}
      <Button type="button" size="sm" variant="outline" disabled={trace.refreshing}
        title="Read the open files, sockets, ports and connections of this process in full on the next sample"
        onClick={() => void trace.refreshFacets(selected.key)}>
        {trace.refreshing ? <UiLoader className="mr-1 animate-spin" /> : <UiRefresh className="mr-1" />}
        Refresh facets
      </Button>
    </div>}
    <ProcessTraceDetail className="min-h-0 flex-1" node={selected} samples={trace.samples} stacks={trace.stacks}
      {...(trace.canCollect ? { onCollectStack: trace.collectStack } : {})} collecting={trace.collecting}
      {...(trace.collectError ? { collectError: trace.collectError } : {})} />
    {selected && hasProfiles && <div className="max-h-[45%] shrink-0 overflow-y-auto border-t border-border px-density-4 py-density-2" data-go-profiles="">
      <Section title="Go profiles" defaultOpen>
        <GoProfileView records={trace.goprof} processKey={selected.key} />
        {trace.goprofExportUrl && <a className="mt-2 inline-block text-xs text-primary hover:underline" href={trace.goprofExportUrl} download>Download pprof</a>}
      </Section>
    </div>}
  </div>;
}
