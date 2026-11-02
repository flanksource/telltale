import { FilterBar, SegmentedControl, type FilterBarFilter, type FilterBarMultiFilter } from "@flanksource/clicky-ui/components";
import type { ModeFilter, Modes, SearchResult, SearchValues, Source } from "./api";
import { durationOf, durationRequest, MULTI_SPAN, rangeOf, shortOperation, windowOf } from "./query";

export const RECEIVER_RUN = "receiver";

/** The source switch's choice: received spans (the receiver run), recorded spans, or a connection. */
export function sourceChoice(value: SearchValues): string {
  const source = value.source ?? "records";
  return source !== "records" ? source : value.run === RECEIVER_RUN ? "received" : "records";
}

function chooseSource(choice: string): SearchValues {
  if (choice === "received") return { source: "records", run: RECEIVER_RUN, index: undefined, format: undefined };
  return { source: choice, run: undefined, index: undefined, format: undefined };
}

/** Options for a filter, keeping its selected values listed while the result that suggests them loads. */
function withSelected(options: { value: string; label: string; description?: string }[], selected: Modes | undefined) {
  const listed = new Set(options.map(option => option.value));
  return [...options, ...Object.keys(selected ?? {}).filter(value => !listed.has(value)).map(value => ({ value, label: value }))];
}

/** One line of trace filters: source, services, root operations, attributes, status, duration, span count and the time window. */
export function QueryBar({ value, onChange, onError, sources, runs, result }: {
  value: SearchValues;
  onChange: (next: Partial<SearchValues>) => void;
  onError: (message: string) => void;
  sources: Source[];
  runs: string[];
  result?: SearchResult;
}) {
  const choice = sourceChoice(value);
  const connection = sources.find(source => source.id === value.source && source.id !== "records");
  const multi = (key: ModeFilter, label: string, options: { value: string; label: string; description?: string }[], extra: Partial<FilterBarMultiFilter> = {}): FilterBarMultiFilter => ({
    ...extra, key, kind: "multi", label, value: value[key] ?? {}, onChange: next => onChange({ [key]: next }), options: withSelected(options, value[key]),
  });
  const filters: FilterBarFilter[] = [
    multi("services", "Service", (result?.services ?? []).map(service => ({ value: service, label: service }))),
    multi("operations", "Root operation", (result?.rootOperations ?? []).map(({ operation }) => ({
      value: operation, label: shortOperation(operation), ...(operation !== shortOperation(operation) ? { description: operation } : {}),
    }))),
    multi("attributes", "Attributes", (result?.attributes ?? []).map(({ value: expr, count }) => ({ value: expr, label: expr, description: `${count} traces` })), {
      allowCustomValue: true, placeholder: "key=value", description: "Any span's attribute: key=value, key=prefix*, or a bare key to match its presence.",
    }),
    multi("statuses", "Status", [{ value: "ok", label: "OK" }, { value: "error", label: "Error" }]),
    { key: "duration", kind: "duration", label: "Duration", value: durationOf(value), onChange: next => onChange(durationRequest(next)), unit: "s" },
    { key: "hide-single", kind: "boolean", label: "Hide single-span traces", value: (value.minSpans ?? 0) >= MULTI_SPAN, onChange: hide => onChange({ minSpans: hide ? MULTI_SPAN : undefined }) },
  ];
  if (choice === "records") filters.push({
    key: "run", kind: "enum", label: "Run", value: value.run ?? "", onChange: run => onChange({ run: run || undefined }),
    options: [{ value: "", label: "Every run" }, ...runs.filter(run => run !== RECEIVER_RUN).map(run => ({ value: run }))],
  });
  if (connection) filters.push(
    { key: "index", kind: "text", label: "Index", value: value.index ?? "", onChange: index => onChange({ index: index || undefined }), placement: "overflow" },
    { key: "format", kind: "enum", label: "Document format", value: value.format ?? "", onChange: format => onChange({ format: format || undefined }), options: [{ value: "", label: "Connection default" }, { value: "flat" }, { value: "jaeger" }], placement: "overflow" },
  );
  const range = rangeOf(value);
  return (
    <FilterBar
      leading={
        <SegmentedControl
          aria-label="Source"
          size="sm"
          value={choice}
          onChange={next => onChange(chooseSource(next))}
          options={[
            ...(runs.includes(RECEIVER_RUN) ? [{ id: "received", label: "Received" }] : []),
            { id: "records", label: "Recorded" },
            ...sources.filter(source => source.id !== "records").map(source => ({ id: source.id, label: source.name })),
          ]}
        />
      }
      filters={filters}
      timeRange={{
        from: range.from, to: range.to, timeEnabled: true, emptyLabel: "Last hour",
        onApply: (from, to) => {
          try {
            onChange(windowOf(from, to, Date.now()));
          } catch (error) {
            onError(error instanceof Error ? error.message : String(error));
          }
        },
      }}
    />
  );
}
