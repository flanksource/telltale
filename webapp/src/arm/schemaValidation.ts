import Ajv2020, { type ValidateFunction } from "ajv/dist/2020";
import type { DraftError, JsonSchema } from "./model";

const ajv = new Ajv2020({ allErrors: true, strict: false, validateFormats: false });
const validators = new WeakMap<JsonSchema, ValidateFunction>();

function schemaValidator(schema: JsonSchema): ValidateFunction {
  let validate = validators.get(schema);
  if (!validate) {
    validate = ajv.compile(schema);
    validators.set(schema, validate);
  }
  return validate;
}

export function argumentErrors(schema: JsonSchema, value: Record<string, unknown>): DraftError[] {
  const validate = schemaValidator(schema);
  if (validate(value)) return [];
  return (validate.errors ?? []).flatMap(error => {
    if (error.keyword === "if") return [];
    const path = error.instancePath;
    if (error.keyword === "required") {
      const field = String(error.params.missingProperty);
      const parent = path.split("/").slice(1).reduce<JsonSchema | undefined>(
        (node, name) => node?.properties?.[name], schema,
      );
      return [{ instancePath: `${path}/${field}`, message: `${parent?.properties?.[field]?.title ?? field} is required.` }];
    }
    return [{ instancePath: path, message: `${path.split("/").pop() || "Capture options"} ${error.message}` }];
  });
}

export function argumentProperties(schema: JsonSchema, value: Record<string, unknown>): Record<string, JsonSchema> {
  const fields: Record<string, JsonSchema> = {};
  function collect(node: JsonSchema) {
    Object.assign(fields, node.properties);
    if (node.if) {
      const branch = schemaValidator(node.if)(value) ? node.then : node.else;
      if (branch) collect(branch);
    }
    node.allOf?.forEach(collect);
  }
  collect(schema);
  return fields;
}

export function visibleArguments(schema: JsonSchema, value: Record<string, unknown>): Record<string, unknown> {
  const fields = argumentProperties(schema, value);
  return Object.fromEntries(Object.entries(value).filter(([key]) => key !== "target" && fields[key]?.["x-hidden"] !== true));
}

export function connectionTypes(field: JsonSchema | undefined): string[] {
  const lookup = field?.["x-clicky-lookup"] as { url?: string } | undefined;
  return lookup?.url ? new URL(lookup.url, "http://lookup").searchParams.get("types")?.split(",").filter(Boolean) ?? [] : [];
}
