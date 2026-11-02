import { Button, IconButton, JsonSchemaForm, Section, type JsonSchemaObject } from "@flanksource/clicky-ui/components";
import { Badge } from "@flanksource/clicky-ui/data";
import { UiAdd, UiTrash } from "@flanksource/clicky-ui/icons";
import type { StartFieldError } from "../traces/api";
import type { DefinitionEntry, DefinitionSpec } from "./api";
import { captureCount, errorsUnder, getIn, isPlainObject, setIn, specPointer, withPlaceholders, type GroupView, type ListView, type ObjectView, type SectionView } from "./model";

export interface CaptureSectionsProps {
  sections: SectionView[];
  spec: DefinitionSpec;
  onSpecChange: (next: DefinitionSpec) => void;
  /** The resolved entries, by JSON pointer. */
  entries: Map<string, DefinitionEntry>;
  errors: StartFieldError[];
  readOnly: boolean;
}

type CaptureProps = Omit<CaptureSectionsProps, "sections">;

const formProps = { size: "sm", showPreferencesMenu: false, persistPreferences: false, applyDefaults: false } as const;

function EntryForm({ pointer, schema, value, onChange, entry, errors, readOnly, onRemove }: {
  pointer: string;
  schema: JsonSchemaObject;
  value: unknown;
  onChange: (next: Record<string, unknown>) => void;
  entry: DefinitionEntry | undefined;
  errors: StartFieldError[];
  readOnly: boolean;
  onRemove: () => void;
}) {
  const title = entry?.name ?? "Resolving its name…";
  return (
    <li className="space-y-2 rounded-md border border-border p-3">
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <span className="font-medium">{title}</span>
        {entry && <Badge size="xs" variant="outline">{entry.kind}</Badge>}
        {entry?.connection && <span className="text-xs text-muted-foreground">takes {entry.connection}</span>}
        {!readOnly && <IconButton icon={UiTrash} label={`Remove ${title}`} className="ml-auto" onClick={onRemove} />}
      </div>
      {isPlainObject(value)
        ? <JsonSchemaForm {...formProps} schema={withPlaceholders(schema, entry)} value={value} onChange={onChange} readOnly={readOnly} instancePath={pointer} errors={errorsUnder(errors, pointer)} idPrefix={pointer.slice(1).replaceAll("/", "-")} />
        : <p role="alert" className="text-sm text-destructive">{pointer} is {JSON.stringify(value)}, not a mapping: fix it in the YAML tab.</p>}
    </li>
  );
}

function ListCapture({ view, spec, onSpecChange, entries, errors, readOnly }: CaptureProps & { view: ListView }) {
  const { list, itemSchema } = view;
  const segments = list.path.split(".");
  const value = getIn(spec, segments);
  const items: unknown[] = Array.isArray(value) ? value : [];
  const setItems = (next: unknown[]) => onSpecChange(setIn(spec, segments, next) as DefinitionSpec);
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-baseline gap-2">
        <h4 className="text-sm font-medium">{list.title}</h4>
        <span className="text-xs text-muted-foreground">{list.kind}</span>
        {list.description && <p className="w-full text-xs text-muted-foreground">{list.description}</p>}
      </div>
      {value !== undefined && !Array.isArray(value) && <p role="alert" className="text-sm text-destructive">{specPointer(list.path)} must be a list, not {JSON.stringify(value)}: fix it in the YAML tab.</p>}
      {!itemSchema && <p role="alert" className="text-sm text-destructive">The definition schema has no entry schema at spec.{list.path}.</p>}
      {itemSchema && items.length > 0 && (
        <ul className="space-y-2">
          {items.map((item, index) => {
            const pointer = `${specPointer(list.path)}/${index}`;
            return (
              <EntryForm
                key={pointer}
                pointer={pointer}
                schema={itemSchema}
                value={item}
                entry={entries.get(pointer)}
                errors={errors}
                readOnly={readOnly}
                onChange={next => setItems(items.map((current, at) => (at === index ? next : current)))}
                onRemove={() => setItems(items.filter((_, at) => at !== index))}
              />
            );
          })}
        </ul>
      )}
      {!readOnly && itemSchema && Array.isArray(value ?? []) && (
        <Button type="button" size="sm" variant="outline" onClick={() => setItems([...items, {}])}>
          <UiAdd className="mr-1" />Add {list.title}
        </Button>
      )}
    </div>
  );
}

function ObjectCapture({ view, spec, onSpecChange, errors, readOnly }: CaptureProps & { view: ObjectView }) {
  const segments = view.path.split(".");
  const value = getIn(spec, segments);
  const pointer = specPointer(view.path);
  const set = (next: unknown) => onSpecChange(setIn(spec, segments, next) as DefinitionSpec);
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-baseline gap-2">
        <h4 className="text-sm font-medium">{view.title}</h4>
        {view.description && <p className="w-full text-xs text-muted-foreground">{view.description}</p>}
      </div>
      {value === undefined
        ? !readOnly && <Button type="button" size="sm" variant="outline" onClick={() => set({})}><UiAdd className="mr-1" />Configure {view.title}</Button>
        : (
          <div className="space-y-2 rounded-md border border-border p-3">
            {!readOnly && <div className="flex"><IconButton icon={UiTrash} label={`Remove ${view.title}`} className="ml-auto" onClick={() => set(undefined)} /></div>}
            {isPlainObject(value)
              ? <JsonSchemaForm {...formProps} schema={view.schema} value={value} onChange={set} readOnly={readOnly} instancePath={pointer} errors={errorsUnder(errors, pointer)} idPrefix={pointer.slice(1).replaceAll("/", "-")} />
              : <p role="alert" className="text-sm text-destructive">{pointer} is {JSON.stringify(value)}, not a mapping: fix it in the YAML tab.</p>}
          </div>
        )}
    </div>
  );
}

function Group({ group, ...props }: CaptureProps & { group: GroupView }) {
  const captures = group.captures.map(capture => (capture.kind === "list"
    ? <ListCapture key={capture.list.path} view={capture} {...props} />
    : <ObjectCapture key={capture.path} view={capture} {...props} />));
  if (!group.name) return <div className="space-y-4">{captures}</div>;
  const count = captureCount(props.spec, { name: "", title: "", groups: [group] });
  return (
    <Section title={group.title} summary={count ? `${count} configured` : undefined} defaultOpen={count > 0}>
      <div className="space-y-4">
        {group.description && <p className="text-xs text-muted-foreground">{group.description}</p>}
        {captures}
      </div>
    </Section>
  );
}

/** The Proxy / Instrument / Collect sections of a definition, each by platform group, with an add per capture type. */
export function CaptureSections({ sections, ...props }: CaptureSectionsProps) {
  return (
    <div className="space-y-4">
      {sections.map(section => {
        const count = captureCount(props.spec, section);
        return (
          <Section key={section.name} title={section.title} summary={`${count} ${count === 1 ? "capture" : "captures"}`} defaultOpen>
            <div className="space-y-3">
              {section.description && <p className="text-sm text-muted-foreground">{section.description}</p>}
              {section.groups.map(group => <Group key={group.name || section.name} group={group} {...props} />)}
            </div>
          </Section>
        );
      })}
    </div>
  );
}
