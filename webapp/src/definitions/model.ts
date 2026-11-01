import type { JsonSchemaObject, JsonSchemaProperty } from "@flanksource/clicky-ui/components";
import type { StartFieldError } from "../traces/api";
import { DEFINITION_API_VERSION, DEFINITION_KIND, type DefinitionEntry, type DefinitionList, type DefinitionSpec, type TraceDefinition } from "./api";

/** The keys of spec the capture sections hold, beside the plain fields and the connections. */
export const SECTION_KEYS = ["proxy", "instrument", "collect"];

export const isPlainObject = (value: unknown): value is Record<string, unknown> => !!value && typeof value === "object" && !Array.isArray(value);

/** A new definition: a name and nothing to trace yet. */
export function emptyDefinition(name: string): TraceDefinition {
  return { apiVersion: DEFINITION_API_VERSION, kind: DEFINITION_KIND, metadata: { name }, spec: {} };
}

export function getIn(value: unknown, segments: string[]): unknown {
  return segments.reduce<unknown>((current, segment) => (isPlainObject(current) ? current[segment] : undefined), value);
}

/**
 * object with the value at segments replaced by next. undefined or [] removes
 * the key (an explicit {} is kept: it configures a single-object capture), and
 * a parent the removal leaves empty goes with it, so a removed last capture
 * leaves no empty list or group in the saved file.
 */
export function setIn(object: Record<string, unknown>, segments: string[], next: unknown): Record<string, unknown> {
  const [head, ...rest] = segments;
  if (head === undefined) throw new Error("setIn: no path to set");
  const child = rest.length === 0 ? next : setIn(isPlainObject(object[head]) ? object[head] : {}, rest, next);
  const removed = rest.length === 0
    ? child === undefined || (Array.isArray(child) && child.length === 0)
    : Object.keys(child as Record<string, unknown>).length === 0;
  const others = Object.fromEntries(Object.entries(object).filter(([key]) => key !== head));
  return removed ? others : { ...others, [head]: child };
}

/** The JSON pointer of the value at a dotted path under spec, such as /spec/collect/prometheus/scrape. */
export function specPointer(path: string): string {
  return `/spec/${path.split(".").join("/")}`;
}

/** The errors at pointer or inside it. */
export function errorsUnder(errors: StartFieldError[], pointer: string): StartFieldError[] {
  return errors.filter(error => error.instancePath === pointer || error.instancePath.startsWith(`${pointer}/`));
}

/** The errors no rendered form owns: none of owners is them or holds them. */
export function unplacedErrors(errors: StartFieldError[], owners: string[]): StartFieldError[] {
  return errors.filter(error => !owners.some(owner => error.instancePath === owner || error.instancePath.startsWith(`${owner}/`)));
}

/**
 * item with the entry's resolved default name, and the connection it takes,
 * as the placeholders of its name and connection fields, the name first.
 */
export function withPlaceholders(item: JsonSchemaObject, entry: DefinitionEntry | undefined): JsonSchemaObject {
  const properties = { ...item.properties };
  if (entry && properties.name) properties.name = { ...properties.name, default: entry.defaultName };
  if (entry?.connection && properties.connection) properties.connection = { ...properties.connection, default: entry.connection };
  return { ...item, properties, ...(properties.name ? { "x-order": ["name", ...Object.keys(properties).filter(key => key !== "name")] } : {}) };
}

/** One typed capture list of a group, with the schema of its entries (null when the schema has none). */
export interface ListView {
  kind: "list";
  list: DefinitionList;
  itemSchema: JsonSchemaObject | null;
}

/** One single-object capture of a group, such as collect.process.tree. */
export interface ObjectView {
  kind: "object";
  path: string;
  title: string;
  description?: string;
  schema: JsonSchemaObject;
}

export interface GroupView {
  /** Empty for the lists that sit directly in a section, as proxy's do. */
  name: string;
  title: string;
  description?: string;
  captures: (ListView | ObjectView)[];
}

export interface SectionView {
  name: string;
  title: string;
  description?: string;
  groups: GroupView[];
}

function propertyAt(schema: JsonSchemaProperty | undefined, segments: string[]): JsonSchemaProperty | undefined {
  return segments.reduce<JsonSchemaProperty | undefined>((current, segment) => current?.properties?.[segment], schema);
}

const objectSchema = (schema: JsonSchemaProperty | undefined): JsonSchemaObject | null => (isPlainObject(schema) && isPlainObject(schema.properties) ? schema as JsonSchemaObject : null);

/**
 * The sections, groups and capture types the editor offers: the lists in the
 * server's order, and in each named group its single-object captures, in the
 * schema's order.
 */
export function sectionViews(spec: JsonSchemaProperty, lists: DefinitionList[]): SectionView[] {
  const sections: SectionView[] = [];
  for (const list of lists) {
    let section = sections.find(candidate => candidate.name === list.section);
    if (!section) {
      const schema = propertyAt(spec, [list.section]);
      section = { name: list.section, title: schema?.title ?? list.section, ...(schema?.description ? { description: schema.description } : {}), groups: [] };
      sections.push(section);
    }
    let group = section.groups.find(candidate => candidate.name === list.group);
    if (!group) {
      const schema = list.group ? propertyAt(spec, [list.section, list.group]) : undefined;
      group = { name: list.group, title: schema?.title ?? list.group, ...(schema?.description ? { description: schema.description } : {}), captures: [] };
      section.groups.push(group);
    }
    group.captures.push({ kind: "list", list, itemSchema: objectSchema(propertyAt(spec, list.path.split("."))?.items) });
  }
  for (const section of sections) {
    for (const group of section.groups) {
      if (!group.name) continue;
      const properties = propertyAt(spec, [section.name, group.name])?.properties ?? {};
      const order = Object.keys(properties);
      for (const [key, property] of Object.entries(properties)) {
        const schema = objectSchema(property);
        if (property.type === "object" && schema) {
          group.captures.push({ kind: "object", path: `${section.name}.${group.name}.${key}`, title: property.title ?? key, ...(property.description ? { description: property.description } : {}), schema });
        }
      }
      const position = (capture: ListView | ObjectView) => order.indexOf(capture.kind === "list" ? capture.list.type : capture.path.split(".").at(-1)!);
      group.captures.sort((a, b) => position(a) - position(b));
    }
  }
  return sections;
}

/** How many captures spec holds under section: list entries plus configured single-object captures. */
export function captureCount(spec: DefinitionSpec, section: SectionView): number {
  return section.groups.flatMap(group => group.captures).reduce((count, capture) => {
    const value = getIn(spec, capture.kind === "list" ? capture.list.path.split(".") : capture.path.split("."));
    if (capture.kind === "list") return count + (Array.isArray(value) ? value.length : 0);
    return count + (value === undefined ? 0 : 1);
  }, 0);
}

/** A parsed YAML document as a definition, or why it cannot be one. */
export function asDefinition(value: Record<string, unknown>): TraceDefinition | string {
  if (!isPlainObject(value.metadata)) return "metadata must be a mapping with the definition's name";
  if (!isPlainObject(value.spec)) return "spec must be a mapping";
  return value as unknown as TraceDefinition;
}
