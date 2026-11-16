import { AccordionList, Checkbox, DropdownMenu, JsonSchemaForm, type JsonSchemaObject } from "@flanksource/clicky-ui/components";
import { UiCheck } from "@flanksource/clicky-ui/icons";
import { connectionsFor, type StepProps } from "./catalog";
import { SEAMS, cloneCapture, hiddenParamKeys, kindByName, type Capture, type CapturePatch, type Seam } from "./model";
import { AgentSelect, ConnectionSelect } from "./pickers";

const SEAM_NOTE: Partial<Record<Seam, string>> = {
  proxy: "the run's command, set in Process",
  process: "the run's pid, set in Process",
  host: "this host; set the source in its options",
};

/** The row header's target: the seam, then the connection or agents that seam needs. */
function TargetControls({ catalog, capture, invalid, onChange }: { catalog: StepProps["catalog"]; capture: Capture; invalid: boolean; onChange: (patch: CapturePatch) => void }) {
  const kind = kindByName(catalog.kinds, capture.kind);
  return (
    <div className="flex min-w-0 flex-[2] flex-wrap items-center justify-end gap-2">
      {kind.seams.length > 1 ? (
        <DropdownMenu
          label={SEAMS[capture.seam].label}
          title={`Where ${capture.id} runs: ${SEAMS[capture.seam].label}`}
          size="sm"
          variant="outline"
          align="right"
          menuLabel={`Where ${capture.id} runs`}
          items={kind.seams.map(seam => ({
            label: (
              <span className="flex items-center gap-2">
                <UiCheck className={seam === capture.seam ? "text-primary" : "invisible"} aria-hidden />
                {SEAMS[seam].label}
              </span>
            ),
            title: SEAMS[seam].description,
            onSelect: () => onChange({ seam }),
          }))}
        />
      ) : (
        capture.seam !== "connection" && <span className="text-xs text-muted-foreground">{SEAMS[capture.seam].label}</span>
      )}
      {capture.seam === "connection" && (
        <ConnectionSelect
          id={`arm-${capture.id}-connection`}
          types={kind.connectionTypes}
          connections={connectionsFor(kind, catalog.connections)}
          value={capture.connection}
          onChange={connection => onChange({ connection })}
          invalid={invalid}
        />
      )}
      {capture.seam === "agent" && (
        <AgentSelect id={`arm-${capture.id}-agent`} probe={kind.name} agents={catalog.agents} deployed={catalog.deployed} value={capture.agent} onChange={agent => onChange({ agent })} invalid={invalid} />
      )}
      {SEAM_NOTE[capture.seam] && <span className="text-xs text-muted-foreground">{SEAM_NOTE[capture.seam]}</span>}
    </div>
  );
}

/** The armed captures, one accordion row per kind and target: the target is picked in the row, the options when it is open. */
export function CaptureList({ catalog, draft, onChange, errors }: StepProps) {
  const setCaptures = (captures: Capture[]) => onChange({ ...draft, captures });
  const patch = (id: string, change: CapturePatch) => setCaptures(draft.captures.map(capture => (capture.id === id ? { ...capture, ...change } : capture)));
  const errorFor = (id: string) => errors.filter(error => error.captureId === id).map(error => error.message).join(" ") || undefined;
  const repeated = (kind: string) => draft.captures.filter(capture => capture.kind === kind).length > 1;
  return (
    <AccordionList<Capture>
      items={draft.captures}
      onChange={setCaptures}
      idPrefix="arm-capture"
      itemLabel={({ item }) => item.id}
      allowDuplicate
      allowRemove
      revealActions={false}
      cloneItem={capture => cloneCapture(draft, capture)}
      rowClassName="flex-wrap"
      renderHeader={({ item }) => {
        const kind = kindByName(catalog.kinds, item.kind);
        const error = errorFor(item.id);
        return (
          <span className="flex min-w-0 flex-col">
            <span className="flex items-center gap-2">
              <span className="truncate text-sm font-medium">{kind.title}</span>
              {repeated(item.kind) && <span className="font-mono text-[11px] text-muted-foreground">{item.id}</span>}
            </span>
            {error ? <span role="alert" className="text-xs text-destructive">{error}</span> : <span className="truncate text-xs text-muted-foreground">{kind.summary}</span>}
          </span>
        );
      }}
      renderMeta={({ item }) => <TargetControls catalog={catalog} capture={item} invalid={!!errorFor(item.id)} onChange={change => patch(item.id, change)} />}
      renderBody={({ item }) => {
        const kind = kindByName(catalog.kinds, item.kind);
        return (
          <div className="space-y-3">
            {item.seam === "agent" && item.agent.mode === "selector" && (
              <Checkbox checked={item.agent.wait} onChange={wait => patch(item.id, { agent: { ...item.agent, wait } as Capture["agent"] })} label="Wait for a matching agent to connect" />
            )}
            {item.seam === "agent" && (
              <Checkbox
                checked={item.allowMutation}
                onChange={allowMutation => patch(item.id, { allowMutation })}
                label="Allow this capture to change the runtime (instrumenting methods, redefining classes); the agent must be installed with mutations allowed"
              />
            )}
            <JsonSchemaForm
              schema={kind.params as JsonSchemaObject}
              value={item.params}
              onChange={params => patch(item.id, { params })}
              hiddenKeys={hiddenParamKeys(kind)}
              size="sm"
              idPrefix={`arm-${item.id}`}
              showPreferencesMenu={false}
              persistPreferences={false}
              applyDefaults={false}
            />
          </div>
        );
      }}
    />
  );
}
