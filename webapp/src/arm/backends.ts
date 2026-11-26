import type { Capture, JsonSchema, Kind } from "./model";
import { argumentProperties, connectionTypes } from "./schemaValidation";

export interface CaptureBackend {
  id: string;
  kind: "logs" | "spans" | "query";
  provider: string;
  title: string;
  summary: string;
  queryLabel?: string;
}

export const CAPTURE_BACKENDS: CaptureBackend[] = [
  { id: "kubernetes", kind: "logs", provider: "k8s", title: "Kubernetes pod logs", summary: "Pod logs by namespace and selector" },
  { id: "loki", kind: "logs", provider: "loki", title: "Loki", summary: "A LogQL stream selector", queryLabel: "LogQL query" },
  { id: "opensearch-logs", kind: "logs", provider: "opensearch", title: "OpenSearch logs", summary: "An index searched by query and time field", queryLabel: "OpenSearch query string" },
  { id: "cloudwatch", kind: "logs", provider: "cloudwatch", title: "CloudWatch Logs", summary: "A log group read with Logs Insights", queryLabel: "Logs Insights query" },
  { id: "gcpcloudlogging", kind: "logs", provider: "gcpcloudlogging", title: "GCP Cloud Logging", summary: "A Cloud Logging filter", queryLabel: "Cloud Logging filter" },
  { id: "azureloganalytics", kind: "logs", provider: "azureloganalytics", title: "Azure Log Analytics", summary: "A KQL query over a workspace", queryLabel: "KQL query" },
  { id: "bigquery", kind: "query", provider: "bigquery", title: "BigQuery", summary: "Log rows from a BigQuery SQL query", queryLabel: "BigQuery SQL query" },
  { id: "opensearch-spans", kind: "spans", provider: "opensearch", title: "OpenSearch traces", summary: "Spans from an OpenSearch-backed OpenTelemetry connection" },
  { id: "jaeger", kind: "query", provider: "jaeger", title: "Jaeger", summary: "Traces by service and operation" },
  { id: "opentelemetry", kind: "query", provider: "opentelemetry", title: "OpenTelemetry", summary: "Traces from an OpenTelemetry query endpoint" },
];

export const SERVER_KUBECONFIG = "Server kubeconfig";

export function captureBackend(capture: Capture): CaptureBackend | undefined {
  if (!capture.backend) return undefined;
  const backend = CAPTURE_BACKENDS.find(item => item.id === capture.backend);
  if (!backend) throw new Error(`Unknown capture backend ${capture.backend}`);
  return backend;
}

function selectProperties(properties: Record<string, JsonSchema>, keys: string[]): Record<string, JsonSchema> {
  return Object.fromEntries(keys.flatMap(key => properties[key] ? [[key, properties[key]]] : []));
}

export function backendForm(kind: Kind, backend: CaptureBackend): JsonSchema | undefined {
  const fields = kind.params.properties ?? {};
  if (backend.kind === "spans") {
    const source = fields.opensearch;
    if (!source?.properties) return undefined;
    const { connection: _connection, ...properties } = source.properties;
    return { ...source, properties, required: source.required?.filter(key => key !== "connection") };
  }
  if (backend.kind === "query") {
    const provider = fields.provider?.properties;
    if (!provider?.[backend.provider]) return undefined;
    return {
      type: "object",
      properties: {
        ...selectProperties(provider, ["query", backend.provider]),
        ...selectProperties(fields, ["from", "to", "every", "untilStopped", "limit"]),
      },
    };
  }
  if (backend.provider === "k8s") {
    if (!fields.namespace || !fields.podSelector) return undefined;
    return {
      type: "object",
      required: kind.params.allOf?.find(rule => rule.if?.properties?.source?.const === "kubernetes")?.then?.required,
      properties: {
        namespace: { ...fields.namespace, title: "Namespace" },
        podSelector: { ...fields.podSelector, title: "Pod selector", description: "Label selector for the pods to follow" },
        ...selectProperties(fields, ["container", "match"]),
      },
    };
  }
  const options = fields.options?.["x-telltale-provider-options"] as Record<string, JsonSchema> | undefined;
  if (!fields.query || !options?.[backend.provider]) return undefined;
  return {
    type: "object",
    properties: {
      query: { ...fields.query, title: backend.queryLabel, format: "textarea" },
      ...selectProperties(fields, ["timeField", "minLevel", "match"]),
      options: { ...options[backend.provider], title: `${backend.title} options` },
    },
  };
}

function logSource(backend: CaptureBackend) {
  return backend.provider === "k8s" ? { source: "kubernetes" } : { source: "cloud", backend: backend.provider };
}

export function backendConnectionTypes(kind: Kind, backend: CaptureBackend): string[] {
  switch (backend.kind) {
    case "spans":
      return connectionTypes(kind.params.properties?.opensearch?.properties?.connection);
    case "query":
      return connectionTypes(kind.params.properties?.provider?.properties?.connection);
    case "logs":
      return connectionTypes(argumentProperties(kind.params, { target: { seam: "host" }, ...logSource(backend) }).connection);
  }
}

export function backendParams(capture: Capture, backend: CaptureBackend): Record<string, unknown> {
  const connection = capture.connection ? { connection: capture.connection } : {};
  if (backend.kind === "spans") return { opensearch: { ...capture.params, ...connection } };
  if (backend.kind === "query") {
    const { query, [backend.provider]: options, ...window } = capture.params;
    return { ...window, provider: { type: backend.provider, ...connection, query, [backend.provider]: options } };
  }
  return { ...capture.params, ...logSource(backend), ...connection };
}
