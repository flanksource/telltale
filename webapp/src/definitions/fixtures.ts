import type { JsonSchemaObject } from "@flanksource/clicky-ui/components";
import type { DefinitionList, DefinitionSchema, DefinitionSummary, StoredDefinition, TraceDefinition } from "./api";

// A trimmed TraceDefinition CRD schema (crds/telltale.flanksource.com_tracedefinitions.yaml)
// and definitions over it, for specs and stories.

const name = { type: "string", title: "Name", description: "Names the capture's session; empty takes <definition>-<section>-<group>-<type>-<n>" } as const;
const connection = { type: "string", title: "Connection", description: "A connection:// reference; empty takes the definition's default of its type" } as const;

const specSchema: JsonSchemaObject = {
  type: "object",
  properties: {
    collect: {
      type: "object", title: "Collect", description: "Reads only: never changes the target, its runtime or the servers it reaches",
      properties: {
        process: {
          type: "object", title: "Process",
          properties: {
            tree: {
              type: "object", title: "Process tree", description: "What the run-wide process tree reads of each process",
              properties: { facets: { type: "boolean", title: "Process facets" } },
            },
          },
        },
        prometheus: {
          type: "object", title: "Prometheus",
          properties: {
            scrape: {
              type: "array", title: "Scrape",
              items: {
                type: "object",
                properties: {
                  endpoints: { type: "array", title: "Endpoints", items: { type: "string" } },
                  interval: { type: "string", title: "Interval", description: "How often each endpoint is scraped such as 15s" },
                  name,
                },
              },
            },
          },
        },
        query: {
          type: "object", title: "Query",
          properties: {
            prometheus: {
              type: "array", title: "Prometheus",
              items: { type: "object", properties: { connection, name, query: { type: "string", title: "Query" }, step: { type: "string", title: "Step" } } },
            },
          },
        },
      },
    },
    connections: { type: "array", title: "Connections", items: { "x-kubernetes-preserve-unknown-fields": true } },
    description: { type: "string", title: "Description" },
    every: { type: "string", title: "Every", description: "How often checkpointed captures are read such as 30s" },
    proxy: {
      type: "object", title: "Proxy", description: "The command is routed through these",
      properties: {
        http: {
          type: "array", title: "HTTP proxies",
          items: { type: "object", properties: { listen: { type: "string", title: "Listen" }, mode: { type: "string", title: "Mode", enum: ["observe", "mitm"] }, name } },
        },
      },
    },
  },
};

export const fixtureLists: DefinitionList[] = [
  { path: "proxy.http", section: "proxy", group: "", type: "http", kind: "http", title: "HTTP proxies" },
  { path: "collect.process.nativeStacks", section: "collect", group: "process", type: "nativeStacks", kind: "stacks", title: "Native stacks" },
  { path: "collect.query.prometheus", section: "collect", group: "query", type: "prometheus", kind: "query", title: "Prometheus" },
  { path: "collect.prometheus.scrape", section: "collect", group: "prometheus", type: "scrape", kind: "prometheus", title: "Scrape" },
];

export const fixtureSchema: DefinitionSchema = {
  schema: {
    type: "object",
    properties: {
      apiVersion: { type: "string" },
      kind: { type: "string" },
      metadata: { type: "object" },
      spec: specSchema,
    },
  },
  lists: fixtureLists,
  layers: [
    { name: "cwd", writable: true, dir: "/work/checkout/.telltale/traces" },
    { name: "global", writable: true, dir: "/home/dev/.config/telltale/traces" },
    { name: "builtin", writable: false, dir: "" },
  ],
};

export const checkoutDefinition: TraceDefinition = {
  apiVersion: "telltale.flanksource.com/v1alpha1",
  kind: "TraceDefinition",
  metadata: { name: "checkout" },
  spec: {
    description: "Scrape the checkout service",
    connections: ["connection://telltale-prometheus"],
    collect: { prometheus: { scrape: [{ endpoints: ["http://localhost:8080/metrics"], interval: "15s" }] } },
  },
};

export const checkoutStored: StoredDefinition = {
  name: "checkout",
  layer: "cwd",
  path: "/work/checkout/.telltale/traces/checkout.yaml",
  resourceVersion: "v1",
  definition: checkoutDefinition,
  yaml: "apiVersion: telltale.flanksource.com/v1alpha1\nkind: TraceDefinition\n",
  entries: [{ pointer: "/spec/collect/prometheus/scrape/0", path: "collect.prometheus.scrape", kind: "prometheus", name: "checkout-collect-prometheus-scrape", defaultName: "checkout-collect-prometheus-scrape" }],
};

export const psStored: StoredDefinition = {
  name: "ps",
  layer: "builtin",
  path: "builtin:ps.yaml",
  resourceVersion: "b1",
  definition: {
    apiVersion: "telltale.flanksource.com/v1alpha1",
    kind: "TraceDefinition",
    metadata: { name: "ps" },
    spec: { description: "The process tree with facets", collect: { process: { tree: { facets: true } } } },
  },
  yaml: "",
  entries: [],
};

export const fixtureSummaries: DefinitionSummary[] = [
  { name: "checkout", layer: "cwd", path: checkoutStored.path, resourceVersion: "v1", description: "Scrape the checkout service", counts: { proxy: 0, instrument: 0, collect: 1 } },
  { name: "broken", layer: "global", path: "/home/dev/.config/telltale/traces/broken.yaml", resourceVersion: "v9", counts: { proxy: 0, instrument: 0, collect: 0 }, problem: "yaml: line 3: mapping values are not allowed in this context" },
  { name: "ps", layer: "builtin", path: psStored.path, resourceVersion: "b1", description: "The process tree with facets", counts: { proxy: 0, instrument: 0, collect: 1 } },
];
