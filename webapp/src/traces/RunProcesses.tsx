import { useMemo } from "react";
import { GoProfileView, ProcessTraceDetail, ProcessTraceTree, armedFacets } from "@flanksource/clicky-ui/data";
import { Button, Section, SplitPane } from "@flanksource/clicky-ui/components";
import { UiLoader, UiRefresh } from "@flanksource/clicky-ui/icons";
import type { ProcessTraceState } from "./useProcessTrace";

export interface RunProcessesProps {
  /** The run's process capture, as useProcessTrace reads it. */
  trace: ProcessTraceState;
  /** The key of the selected process, or null. */
  selectedKey: string | null;
  onSelect: (key: string) => void;
  hideExited: boolean;
  onHideExitedChange: (hide: boolean) => void;
  expandAll: boolean | null;
  onExpandAllChange: (next: boolean | null) => void;
  className?: string;
}

/**
 * The Processes view: the run's process tree beside the selected process's
 * detail (command, lifetime, usage, stack dumps, environment and the facet
 * tabs the samples carry) and, when the run ingested Go profiles for it, its
 * profiles. Collect and refresh need the run's live capture
 * (`trace.canCollect`): a stored run shows what was recorded and offers
 * neither.
 */
export function RunProcesses({
  trace,
  selectedKey,
  onSelect,
  hideExited,
  onHideExitedChange,
  expandAll,
  onExpandAllChange,
  className,
}: RunProcessesProps) {
  const selected = useMemo(() => trace.nodes.find(node => node.key === selectedKey) ?? null, [trace.nodes, selectedKey]);
  const canRefresh = trace.canCollect && armedFacets(trace.samples).length > 0;
  const hasProfiles = !!selected && trace.goprof.some(record => record.key === selected.key);

  return (
    <SplitPane
      className={className ?? "h-full min-h-0"}
      defaultSplit={45}
      minLeft={25}
      minRight={30}
      leftClass="border-r-2 border-border bg-muted/30"
      left={(
        <ProcessTraceTree
          className="h-full"
          nodes={trace.nodes}
          samples={trace.samples}
          selectedKey={selectedKey}
          onSelect={onSelect}
          expandAll={expandAll}
          onExpandAllChange={onExpandAllChange}
          hideExited={hideExited}
          onHideExitedChange={onHideExitedChange}
          loading={trace.loading}
          {...(trace.error ? { error: trace.error } : {})}
        />
      )}
      right={(
        <div className="flex h-full min-h-0 flex-col">
          {selected && canRefresh && (
            <div className="flex shrink-0 items-center justify-end gap-2 border-b border-border px-density-4 py-density-1">
              {trace.refreshError && <span className="min-w-0 truncate text-xs text-red-700 dark:text-red-400" role="alert" title={trace.refreshError}>{trace.refreshError}</span>}
              <Button
                type="button"
                size="sm"
                variant="outline"
                disabled={trace.refreshing}
                title="Read the open files, sockets, ports and connections of this process in full on the next sample"
                onClick={() => void trace.refreshFacets(selected.key)}
              >
                {trace.refreshing ? <UiLoader className="mr-1 animate-spin" /> : <UiRefresh className="mr-1" />}
                Refresh facets
              </Button>
            </div>
          )}
          <ProcessTraceDetail
            className="min-h-0 flex-1"
            node={selected}
            samples={trace.samples}
            stacks={trace.stacks}
            {...(trace.canCollect ? { onCollectStack: trace.collectStack } : {})}
            collecting={trace.collecting}
            {...(trace.collectError ? { collectError: trace.collectError } : {})}
          />
          {selected && hasProfiles && (
            <div className="max-h-[45%] shrink-0 overflow-y-auto border-t border-border px-density-4 py-density-2" data-go-profiles="">
              <Section title="Go profiles" defaultOpen>
                <GoProfileView records={trace.goprof} processKey={selected.key} />
                {trace.goprofExportUrl && (
                  <a className="mt-2 inline-block text-xs text-primary hover:underline" href={trace.goprofExportUrl} download>
                    Download pprof
                  </a>
                )}
              </Section>
            </div>
          )}
        </div>
      )}
    />
  );
}
