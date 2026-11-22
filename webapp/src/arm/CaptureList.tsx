import { AccordionList, Checkbox, DropdownMenu, JsonSchemaForm, type JsonSchemaObject } from "@flanksource/clicky-ui/components";
import { UiCheck } from "@flanksource/clicky-ui/icons";
import { esQueryBuilderFormExtensions } from "@flanksource/clicky-ui/profiles";
import { createOperationsApiClient, useOperationLookupFetcher } from "@flanksource/clicky-ui/rpc";
import { SERVER_KUBECONFIG, backendConnectionTypes, backendForm, captureBackend } from "./backends";
import type { StepProps } from "./catalog";
import { CaptureIcon } from "./CaptureIcon";
import { SEAMS, cloneCapture, hiddenParamKeys, kindByName, type Capture, type CapturePatch, type JsonSchema, type Seam } from "./model";
import { AgentSelect, ConnectionSelect } from "./pickers";
import { visibleArguments } from "./schemaValidation";

const SEAM_NOTE: Partial<Record<Seam, string>> = {
  proxy: "the run's command, set in Process",
  process: "the run's pid, set in Process",
  host: "this host; set the source in its options",
};

const lookupClient = createOperationsApiClient();

const WIDE_OPTIONS = new Set(["address", "asyncProfiler", "dsn", "endpoint", "hosts", "logGroup", "match", "noProxy", "paths", "query", "search", "tables", "text", "upstreamCA", "url"]);

function captureFormSchema(schema: JsonSchema): JsonSchema {
  if (!schema.properties) return schema;
  return {
    "x-columns": "auto",
    "x-column-min-width": "min(100%, 17rem)",
    "x-columns-max-width": "64rem",
    "x-classes": "gap-y-3",
    ...schema,
    properties: Object.fromEntries(Object.entries(schema.properties).map(([key, field]) => [
      key,
      {
        ...(WIDE_OPTIONS.has(key) || field.format === "textarea" ? { "x-col-span": "full" } : {}),
        ...captureFormSchema(field),
      },
    ])),
  };
}

/** The row header's target: the seam, then the connection or agents that seam needs. */
function TargetControls({ catalog, capture, invalid, onChange }: { catalog: StepProps["catalog"]; capture: Capture; invalid: boolean; onChange: (patch: CapturePatch) => void }) {
  const kind = kindByName(catalog.kinds, capture.kind);
  const backend = captureBackend(capture);
  const types = backend ? backendConnectionTypes(kind, backend) : kind.connectionTypes;
  return (
    <div className="flex min-w-0 flex-[2] flex-wrap items-center justify-end gap-2">
      {!backend && kind.seams.length > 1 ? (
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
            onSelect: () => onChange({ seam, params: visibleArguments(kind.params, { ...capture.params, target: { seam } }) }),
          }))}
        />
      ) : (
        capture.seam !== "connection" && <span className="text-xs text-muted-foreground">{SEAMS[capture.seam].label}</span>
      )}
      {(capture.seam === "connection" || backend) && (
        <ConnectionSelect
          id={`arm-${capture.id}-connection`}
          types={types}
          connections={catalog.connections}
          defaultLabel={backend?.provider === "k8s" ? SERVER_KUBECONFIG : undefined}
          value={capture.connection}
          onChange={connection => onChange({ connection })}
          invalid={invalid}
        />
      )}
      {capture.seam === "agent" && (
        <AgentSelect id={`arm-${capture.id}-agent`} probe={kind.name} agents={catalog.agents} deployed={catalog.deployed} value={capture.agent} onChange={agent => onChange({ agent })} invalid={invalid} />
      )}
      {!backend && SEAM_NOTE[capture.seam] && <span className="text-xs text-muted-foreground">{SEAM_NOTE[capture.seam]}</span>}
    </div>
  );
}

/** The armed captures, one accordion row per kind and target: the target is picked in the row, the options when it is open. */
export function CaptureList({ catalog, draft, onChange, errors }: StepProps) {
  const lookupFetcher = useOperationLookupFetcher(lookupClient);
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
        const backend = captureBackend(item);
        const error = errorFor(item.id);
        return (
          <span className="flex min-w-0 items-center gap-2">
            <CaptureIcon kind={kind.name} provider={backend?.provider} className="size-5 shrink-0" />
            <span className="flex min-w-0 flex-col">
              <span className="flex items-center gap-2">
                <span className="truncate text-sm font-medium">{backend?.title ?? kind.title}</span>
                {repeated(item.kind) && <span className="font-mono text-[11px] text-muted-foreground">{item.id}</span>}
              </span>
              {error ? <span role="alert" className="text-xs text-destructive">{error}</span> : <span className="truncate text-xs text-muted-foreground">{backend?.summary ?? kind.summary}</span>}
            </span>
          </span>
        );
      }}
      renderMeta={({ item }) => <TargetControls catalog={catalog} capture={item} invalid={!!errorFor(item.id)} onChange={change => patch(item.id, change)} />}
      renderBody={({ item }) => {
        const kind = kindByName(catalog.kinds, item.kind);
        const backend = captureBackend(item);
        const schema = backend ? backendForm(kind, backend) : kind.params;
        if (!schema) throw new Error(`${backend?.title} is unavailable in the server's start form`);
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
              schema={captureFormSchema(schema) as JsonSchemaObject}
              value={backend || !kind.params.properties?.target ? item.params : { ...item.params, target: { seam: item.seam } }}
              onChange={params => patch(item.id, { params: backend ? params : visibleArguments(kind.params, params) })}
              hiddenKeys={backend ? undefined : hiddenParamKeys(kind)}
              {...(lookupFetcher ? { lookupFetcher } : {})}
              post={esQueryBuilderFormExtensions.post}
              size="sm"
              layout={{ mode: "stacked" }}
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
