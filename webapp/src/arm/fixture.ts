import type { StartForm } from "../traces/api";

// The schema's if/then conditional, parsed as the server serves it: a `then` key in a literal reads as a thenable.
const enabled = (params: Record<string, unknown>) => JSON.parse(`[{"if":{"properties":{"enabled":{"const":true}}},"then":${JSON.stringify(params)}}]`) as unknown[];
const target = (seams: string[]) => ({ type: "object", properties: { seam: { type: "string", enum: seams }, agent: { type: "string" } }, "x-telltale-seams": seams });

/** A start form in the shape GET /api/traces/start serves, trimmed to four kinds. */
export const START_FORM: StartForm = {
  schema: {
    type: "object",
    properties: {
      traces: {
        type: "object",
        properties: {
          http: {
            type: "object", title: "HTTP", description: "Record HTTP exchanges from an agent or a local proxy",
            "x-telltale-seams": ["self", "agent", "proxy"], "x-telltale-default-seam": "self",
            properties: { enabled: { type: "boolean" } },
            allOf: enabled({ properties: { target: target(["self", "agent", "proxy"]), mode: { type: "string", enum: ["connect", "mitm"], default: "connect" } } }),
          },
          sql_top: {
            type: "object", title: "SQL top", description: "Sample the queries a Postgres or SQL Server is running",
            "x-telltale-seams": ["connection", "agent"], "x-telltale-default-seam": "connection",
            properties: { enabled: { type: "boolean" } },
            allOf: enabled({ properties: { target: target(["connection", "agent"]), connection: { type: "string", "x-clicky-lookup": { url: "/api/v1/connection?types=postgres,sql_server" } }, interval: { type: "string", default: "1s" } } }),
          },
          jvm_trace: {
            type: "object", title: "JVM methods", description: "Record method calls in a JVM",
            "x-telltale-seams": ["agent"], "x-telltale-default-seam": "agent",
            properties: { enabled: { type: "boolean" } },
            allOf: enabled({ required: ["targets"], properties: { target: target(["agent"]), targets: { type: "array", items: { type: "object" } } } }),
          },
          own: {
            type: "object", title: "Own process", description: "Only telltale's own process",
            "x-telltale-seams": ["self"], "x-telltale-default-seam": "self",
            properties: { enabled: { type: "boolean" } },
            allOf: enabled({ properties: {} }),
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
