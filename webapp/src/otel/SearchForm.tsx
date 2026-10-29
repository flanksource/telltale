import {
  Button,
  JsonSchemaForm,
  Section,
} from "@flanksource/clicky-ui/components";
import { type SearchForm as Form, type SearchValues, type Source } from "./api";

export function SearchForm({
  form,
  sources,
  value,
  onChange,
  onSearch,
  pending,
  services,
  operations,
}: {
  form: Form;
  sources: Source[];
  value: SearchValues;
  onChange: (value: SearchValues) => void;
  onSearch: () => void;
  pending: boolean;
  services: string[];
  operations: string[];
}) {
  const properties = { ...form.schema.properties };
  delete properties.source;
  if (value.source === "records" || value.source === "receiver") {
    delete properties.index;
    delete properties.format;
  } else delete properties.run;
  if (value.source === "receiver") delete properties.run;
  for (const [key, options] of [
    ["service", services],
    ["operation", operations],
  ] as const) {
    if (
      properties[key] &&
      typeof properties[key] === "object" &&
      options.length > 0
    )
      properties[key] = { ...properties[key], examples: options };
  }
  const common = ["service", "operation", "traceId", "lookback", "status"];
  const more = Object.keys(properties).filter((key) => !common.includes(key));
  const fields = (keys: string[], prefix: string) => (
    <JsonSchemaForm
      schema={{
        ...form.schema,
        properties: Object.fromEntries(
          Object.entries(properties).filter(([key]) => keys.includes(key)),
        ),
        "x-order": keys,
        required: (form.schema.required ?? []).filter((key) =>
          keys.includes(key),
        ),
      }}
      value={value}
      onChange={(next) => onChange({ ...value, ...next })}
      size="sm"
      idPrefix={prefix}
      showPreferencesMenu={false}
      persistPreferences={false}
    />
  );
  return (
    <form
      className="space-y-4"
      onSubmit={(event) => {
        event.preventDefault();
        onSearch();
      }}
    >
      <label className="flex flex-col gap-1 text-sm font-medium">
        Trace source
        <select
          className="max-w-full rounded border border-border bg-background px-3 py-2"
          value={value.source ?? "records"}
          onChange={(event) =>
            onChange({
              ...value,
              source: event.target.value,
              run: "",
              index: "",
              format: "",
            })
          }
        >
          {sources.map((source) => (
            <option key={source.id} value={source.id}>
              {source.name} ({source.type})
            </option>
          ))}
        </select>
      </label>
      {fields(common, "otel-search")}
      <Button type="submit" disabled={pending}>
        {pending ? "Searching…" : "Find traces"}
      </Button>
      <Section
        title="More filters"
        defaultOpen={more.some(
          (key) =>
            key !== "limit" && value[key] !== undefined && value[key] !== "",
        )}
      >
        {fields(more, "otel-more")}
      </Section>
    </form>
  );
}
