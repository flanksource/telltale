import type { StartForm } from "../traces/api";

// The schema's if/then conditional, parsed as the server serves it: a `then` key in a literal reads as a thenable.
const variants = (params: Record<string, unknown>) => {
  const item = {
    type: "object",
    properties: { enabled: { type: "boolean" } },
    allOf: JSON.parse(`[{"if":{"required":["enabled"],"properties":{"enabled":{"const":true}}},"then":${JSON.stringify(params)}}]`) as unknown[],
  };
  return [{ ...item }, { type: "array", items: item, minItems: 1 }];
};
const target = (seams: string[]) => ({ type: "object", properties: { seam: { type: "string", enum: seams }, agent: { type: "string" } }, "x-telltale-seams": seams });
const logRules: unknown[] = JSON.parse(`[
  {"if":{"required":["target"],"properties":{"target":{"required":["seam"],"properties":{"seam":{"const":"host"}}}}},"then":{"required":["source"]}},
  {"if":{"required":["source"],"properties":{"source":{"const":"cloud"}}},"then":{"required":["backend","connection"]}},
  {"if":{"required":["source"],"properties":{"source":{"const":"kubernetes"}}},"then":{"required":["namespace","podSelector"]}},
  {"if":{"required":["backend"],"properties":{"backend":{"const":"loki"}}},"then":{"properties":{"connection":{"type":"string","x-clicky-lookup":{"url":"/api/v1/connection?types=loki"}}}}}
]`);

/** A start form in the shape GET /api/traces/start serves, trimmed to the kinds exercised by the wizard specs. */
export const START_FORM: StartForm = {
  schema: {
    type: "object",
    properties: {
      traces: {
        type: "object",
        properties: {
          http: {
            title: "HTTP", description: "Record HTTP exchanges from an agent or a local proxy",
            "x-telltale-seams": ["self", "agent", "proxy"], "x-telltale-default-seam": "self",
            oneOf: variants({ properties: { target: target(["self", "agent", "proxy"]), mode: { type: "string", enum: ["connect", "mitm"], default: "connect" } } }),
          },
          sql_top: {
            title: "SQL top", description: "Sample the queries a Postgres or SQL Server is running",
            "x-telltale-seams": ["connection", "agent"], "x-telltale-default-seam": "connection",
            oneOf: variants({ properties: { target: target(["connection", "agent"]), connection: { type: "string", "x-clicky-lookup": { url: "/api/v1/connection?types=postgres,sql_server" } }, interval: { type: "string", default: "1s" } } }),
          },
          jvm_trace: {
            title: "JVM methods", description: "Record method calls in a JVM",
            "x-telltale-seams": ["agent"], "x-telltale-default-seam": "agent",
            oneOf: variants({ required: ["targets"], properties: {
              target: target(["agent"]),
              targets: { type: "array", title: "Methods", items: { type: "object", required: ["class", "method"], properties: {
                class: { type: "string", title: "Class" }, method: { type: "string", title: "Method" },
              } } },
            } }),
          },
          logs: {
            title: "Logs", description: "Record application logs",
            "x-telltale-seams": ["self", "agent", "host"], "x-telltale-default-seam": "self",
            oneOf: variants({ allOf: logRules, properties: {
              target: target(["self", "agent", "host"]),
              source: { type: "string" }, backend: { type: "string" }, connection: { type: "string" },
              namespace: { type: "string" }, podSelector: { type: "string" }, container: { type: "string" },
              query: { type: "string" }, timeField: { type: "string" }, minLevel: { type: "string" }, match: { type: "string" },
              options: { type: "object", "x-telltale-provider-options": {
                loki: { type: "object", properties: { direction: { type: "string", enum: ["forward", "backward"] } } },
              } },
            } }),
          },
          spans: {
            title: "Spans", description: "Record application spans",
            "x-telltale-seams": ["self", "agent", "host"], "x-telltale-default-seam": "self",
            oneOf: variants({ properties: {
              target: target(["self", "agent", "host"]),
              opensearch: { type: "object", required: ["connection"], properties: {
                connection: { type: "string", "x-clicky-lookup": { url: "/api/v1/connection?types=opentelemetry" } },
                index: { type: "string", title: "Index" },
              } },
            } }),
          },
          own: {
            title: "Own process", description: "Only telltale's own process",
            "x-telltale-seams": ["self"], "x-telltale-default-seam": "self",
            oneOf: variants({ properties: {} }),
          },
        },
      },
    },
  },
  defaults: {
    command: "",
    traces: {
      http: { enabled: true, mode: "connect", target: { seam: "proxy" } },
      sql_top: { enabled: false, interval: "1s" },
      jvm_trace: { enabled: false },
    },
  },
};
