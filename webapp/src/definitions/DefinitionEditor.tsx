import { useEffect, useMemo, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { Button, ConfirmDialog, FormErrorSummary, JsonSchemaForm, Tabs, type JsonSchemaObject } from "@flanksource/clicky-ui/components";
import { Badge, Callout, parseFixtureYaml, stringifyFixtureYaml } from "@flanksource/clicky-ui/data";
import { UiCopy, UiLock, UiPlay, UiRefresh, UiSave, UiTrash } from "@flanksource/clicky-ui/icons";
import type { StartFieldError } from "../traces/api";
import {
  DefinitionConflict, DefinitionInvalid, deleteDefinition, saveDefinition, useDefinitionValidation,
  type DefinitionLayer, type DefinitionSchema, type DefinitionSpec, type DefinitionEntry, type StoredDefinition, type TraceDefinition,
} from "./api";
import { CaptureSections } from "./CaptureSections";
import { ConnectionsPanel } from "./ConnectionsPanel";
import { asDefinition, errorsUnder, SECTION_KEYS, sectionViews, specPointer, unplacedErrors } from "./model";
import { StartDefinitionDialog } from "./StartDefinitionForm";

/** What the editor edits: a stored definition, or a new one not yet written to its layer. */
export interface EditorSource {
  name: string;
  layer: DefinitionLayer;
  /** Empty for a new definition. */
  resourceVersion: string;
  definition: TraceDefinition;
  entries: DefinitionEntry[];
  /** The file it was read from; a new one has none yet. */
  path?: string;
}

export interface DefinitionEditorProps {
  schema: DefinitionSchema;
  source: EditorSource;
  onSaved: (saved: StoredDefinition) => void;
  /** Read the definition from disk again, discarding the edit. */
  onReload?: () => void;
  onDeleted?: () => void;
  /** How long the editor waits after an edit before validating it. */
  validateDelayMs?: number;
}

function useDebounced<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(value), delayMs);
    return () => clearTimeout(timer);
  }, [value, delayMs]);
  return debounced;
}

const LAYER_TONE = { cwd: "info", global: "neutral", builtin: "warning" } as const;

export function LayerBadge({ layer }: { layer: DefinitionLayer }) {
  return <Badge size="sm" tone={LAYER_TONE[layer]} {...(layer === "builtin" ? { icon: UiLock } : {})}>{layer}</Badge>;
}

const formProps = { size: "sm", showPreferencesMenu: false, persistPreferences: false, applyDefaults: false } as const;

function ConflictCallout({ conflict, onReload, onOverwrite, onDismiss, overwriting }: { conflict: DefinitionConflict; onReload?: () => void; onOverwrite?: () => void; onDismiss: () => void; overwriting: boolean }) {
  return (
    <Callout variant="warning" title="Changed on disk">
      <p>{conflict.message}</p>
      <p className="mt-1">Reload the file and lose your edits, or overwrite it with your version.</p>
      <div className="mt-2 flex flex-wrap gap-2">
        {onReload && <Button type="button" size="sm" variant="outline" onClick={onReload}><UiRefresh className="mr-1" />Reload from disk</Button>}
        {onOverwrite && <Button type="button" size="sm" variant="destructive" disabled={overwriting} onClick={onOverwrite}>Overwrite with my version</Button>}
        <Button type="button" size="sm" variant="ghost" onClick={onDismiss}>Keep editing</Button>
      </div>
    </Callout>
  );
}

/** Edits a trace definition as a form over its CRD schema or as YAML, and saves, duplicates, deletes or starts it. */
export function DefinitionEditor({ schema, source, onSaved, onReload, onDeleted, validateDelayMs = 400 }: DefinitionEditorProps) {
  const isNew = source.resourceVersion === "";
  const readOnly = source.layer === "builtin";
  const [draft, setDraftState] = useState(source.definition);
  const [tab, setTab] = useState<"form" | "yaml">("form");
  const [yamlText, setYamlText] = useState<string | null>(null);
  const [yamlError, setYamlError] = useState<string | null>(null);
  const [saveErrors, setSaveErrors] = useState<StartFieldError[] | null>(null);
  const [conflict, setConflict] = useState<DefinitionConflict | null>(null);
  const [starting, setStarting] = useState(false);
  const [confirmingDelete, setConfirmingDelete] = useState(false);

  const sourceJson = useMemo(() => JSON.stringify(source.definition), [source.definition]);
  const draftJson = useMemo(() => JSON.stringify(draft), [draft]);
  const dirty = draftJson !== sourceJson;
  const validation = useDefinitionValidation(useDebounced(dirty || isNew ? draftJson : null, validateDelayMs));
  const sections = useMemo(() => sectionViews(schema.schema.properties?.spec ?? {}, schema.lists), [schema]);
  const specSchema = schema.schema.properties?.spec as JsonSchemaObject | undefined;

  const setDraft = (next: TraceDefinition) => {
    setDraftState(next);
    setSaveErrors(null);
  };
  const setSpec = (spec: DefinitionSpec) => setDraft({ ...draft, spec });

  const onRefused = (error: Error) => {
    if (error instanceof DefinitionInvalid) setSaveErrors(error.errors);
    if (error instanceof DefinitionConflict) setConflict(error);
  };
  const save = useMutation({
    mutationFn: (resourceVersion: string) => saveDefinition({ name: source.name, layer: source.layer, resourceVersion, definition: draft }),
    onSuccess: saved => { setConflict(null); onSaved(saved); },
    onError: onRefused,
  });
  const duplicate = useMutation({
    mutationFn: (layer: DefinitionLayer) => saveDefinition({ name: source.name, layer, resourceVersion: "", definition: draft }),
    onSuccess: onSaved,
    onError: onRefused,
  });
  const remove = useMutation({
    mutationFn: () => deleteDefinition({ name: source.name, layer: source.layer, resourceVersion: source.resourceVersion }),
    onSuccess: () => onDeleted?.(),
    onError: onRefused,
  });

  const entries = useMemo(() => new Map((validation.data?.entries ?? source.entries).map(entry => [entry.pointer, entry])), [validation.data, source.entries]);
  const errors = saveErrors ?? validation.data?.errors ?? [];
  const topKeys = Object.keys(specSchema?.properties ?? {}).filter(key => key !== "connections" && !SECTION_KEYS.includes(key));
  const owners = [...topKeys.map(key => `/spec/${key}`), "/spec/connections", ...sections.flatMap(section => section.groups.flatMap(group => group.captures.map(capture => specPointer(capture.kind === "list" ? capture.list.path : capture.path))))];
  const otherError = [save.error, duplicate.error, remove.error].find(error => error && !(error instanceof DefinitionInvalid) && !(error instanceof DefinitionConflict));
  const writable = schema.layers.filter(layer => layer.writable);
  const savePath = source.path ?? schema.layers.find(layer => layer.name === source.layer)?.dir;

  const editYaml = (text: string) => {
    setYamlText(text);
    if (text.trim() === "") return setYamlError("The YAML is empty: a definition needs its metadata and spec.");
    const parsed = parseFixtureYaml(text);
    if (!parsed.ok) return setYamlError(parsed.error);
    const definition = asDefinition(parsed.value);
    if (typeof definition === "string") return setYamlError(definition);
    setYamlError(null);
    setDraft(definition);
  };
  const discardYaml = () => { setYamlText(null); setYamlError(null); };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-2xl font-semibold">{source.name}</h1>
        <LayerBadge layer={source.layer} />
        {dirty && !readOnly && <Badge size="sm" tone="warning">unsaved</Badge>}
        {savePath && <code className="text-xs text-muted-foreground">{savePath}</code>}
        <div className="ml-auto flex flex-wrap gap-2">
          {!isNew && <Button type="button" variant="outline" onClick={() => setStarting(true)}><UiPlay className="mr-1" />Start run</Button>}
          {readOnly && writable.map(layer => (
            <Button key={layer.name} type="button" variant="outline" disabled={duplicate.isPending} onClick={() => duplicate.mutate(layer.name)}><UiCopy className="mr-1" />Duplicate to {layer.name}</Button>
          ))}
          {!readOnly && !isNew && <Button type="button" variant="outline" disabled={remove.isPending} onClick={() => setConfirmingDelete(true)}><UiTrash className="mr-1" />Delete</Button>}
          {!readOnly && (
            <Button type="button" disabled={save.isPending || yamlError !== null || (!dirty && !isNew)} onClick={() => save.mutate(source.resourceVersion)}>
              <UiSave className="mr-1" />Save
            </Button>
          )}
        </div>
      </div>
      {readOnly && <Callout variant="note">Built-in definitions are read-only. Duplicate it to a config dir to change it; the copy then shadows the built-in.</Callout>}
      {conflict && (
        <ConflictCallout
          conflict={conflict}
          overwriting={save.isPending}
          {...(onReload ? { onReload: () => { setConflict(null); onReload(); } } : {})}
          {...(!readOnly && !remove.isError ? { onOverwrite: () => save.mutate(conflict.resourceVersion) } : {})}
          onDismiss={() => setConflict(null)}
        />
      )}
      {otherError && <p role="alert" className="text-sm text-destructive">{otherError.message}</p>}
      {validation.isError && <p role="alert" className="text-sm text-destructive">{validation.error.message}</p>}
      <FormErrorSummary errors={unplacedErrors(errors, owners)} />

      <Tabs
        tabs={[{ id: "form", label: "Form", disabled: yamlError !== null }, { id: "yaml", label: "YAML" }]}
        value={tab}
        onChange={id => {
          if (id === "form") discardYaml();
          setTab(id as "form" | "yaml");
        }}
      />
      {tab === "form" && (
        <div className="space-y-4">
          {specSchema && <JsonSchemaForm {...formProps} schema={specSchema} value={draft.spec} onChange={next => setSpec({ ...draft.spec, ...next })} hiddenKeys={["connections", ...SECTION_KEYS]} readOnly={readOnly} instancePath="/spec" errors={topKeys.flatMap(key => errorsUnder(errors, `/spec/${key}`))} idPrefix="definition-spec" />}
          {!specSchema && <p role="alert" className="text-sm text-destructive">The definition schema has no spec.</p>}
          <ConnectionsPanel connections={draft.spec.connections} onChange={connections => setSpec({ ...draft.spec, connections })} errors={errors} readOnly={readOnly} />
          <CaptureSections sections={sections} spec={draft.spec} onSpecChange={setSpec} entries={entries} errors={errors} readOnly={readOnly} />
        </div>
      )}
      {tab === "yaml" && (
        <div className="space-y-2">
          <p className="text-sm text-muted-foreground">{readOnly ? "The built-in definition." : `What Save writes${savePath ? ` to ${savePath}` : ""}. Edits here update the form.`}</p>
          {yamlError && (
            <Callout variant="caution" title="This YAML is not a definition yet">
              <p role="alert">{yamlError}</p>
              <p className="mt-1">The form keeps the last version that parsed. Fix the YAML, or discard these edits.</p>
              <Button type="button" size="sm" variant="outline" className="mt-2" onClick={discardYaml}>Discard YAML edits</Button>
            </Callout>
          )}
          <textarea
            aria-label="Definition YAML"
            className="h-[32rem] w-full rounded-md border border-border bg-background p-3 font-mono text-xs"
            spellCheck={false}
            readOnly={readOnly}
            value={yamlText ?? stringifyFixtureYaml(draft)}
            onChange={event => editYaml(event.target.value)}
          />
        </div>
      )}
      <ConfirmDialog
        open={confirmingDelete}
        title={`Delete ${source.name}?`}
        message={`This removes ${savePath ?? "the file"}.`}
        confirmLabel="Delete"
        variant="destructive"
        onConfirm={() => { setConfirmingDelete(false); remove.mutate(); }}
        onCancel={() => setConfirmingDelete(false)}
      />
      {!isNew && <StartDefinitionDialog definition={source.name} open={starting} onClose={() => setStarting(false)} unsaved={dirty} />}
    </div>
  );
}
