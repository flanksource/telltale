import { vi } from "vitest";
import type { RunTraces, StartForm } from "../traces/api";
import type { DefinitionEntry, DefinitionSummary, StoredDefinition, TraceDefinition } from "./api";
import { fixtureLists, fixtureSchema } from "./fixtures";

// A fetch that answers the trace definition routes as the server does, for specs.

export function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
}

export const startForm: StartForm = {
  schema: {
    type: "object",
    properties: {
      command: { type: "string", title: "Command" },
      pid: { type: "integer", title: "PID" },
      duration: { type: "string", title: "Duration" },
      every: { type: "string", title: "Every" },
      traces: { type: "object", properties: {} },
    },
  },
  defaults: { command: "" },
};

export const startedRun: RunTraces = { runId: "run-1", mount: "/api/traces/run-1", dir: "/cache/telltale/traces/run-1", status: "running", command: ["./bin/checkout"] };

/** The entries of definition as the server resolves them: <name>-<section>[-<group>]-<type>[-<n>]. */
export function resolvedEntries(definition: TraceDefinition): DefinitionEntry[] {
  return fixtureLists.flatMap(list => {
    const value = list.path.split(".").reduce<unknown>((current, key) => (current && typeof current === "object" ? (current as Record<string, unknown>)[key] : undefined), definition.spec);
    const items = Array.isArray(value) ? value : [];
    return items.map((item: { name?: string }, index) => {
      const defaultName = [definition.metadata.name, list.section, list.group, list.type, items.length > 1 ? String(index + 1) : ""].filter(Boolean).join("-");
      return { pointer: `/spec/${list.path.split(".").join("/")}/${index}`, path: list.path, kind: list.kind, name: item.name || defaultName, defaultName };
    });
  });
}

export interface ServerOptions {
  definitions?: DefinitionSummary[];
  stored?: Record<string, StoredDefinition>;
  /** Answers a PUT; by default it saves the body as the next version. */
  save?: (name: string, body: { layer: string; resourceVersion: string; definition: TraceDefinition }) => Response;
  start?: Response;
}

export function serveDefinitions(options: ServerOptions = {}) {
  const stored = { ...options.stored };
  const fetchMock = vi.fn((input: string, init?: RequestInit) => {
    const [path] = input.split("?") as [string];
    const method = init?.method ?? "GET";
    const body = typeof init?.body === "string" ? JSON.parse(init.body) as Record<string, unknown> : {};
    if (path === "/api/traces/definitions") return Promise.resolve(json(options.definitions ?? []));
    if (path === "/api/traces/definitions/schema") return Promise.resolve(json(fixtureSchema));
    if (path === "/api/traces/definitions/validate" && method === "POST") {
      return Promise.resolve(json({ errors: [], entries: resolvedEntries(body.definition as TraceDefinition) }));
    }
    if (path === "/api/traces/connections") {
      return Promise.resolve(json({ connections: [{ reference: "connection://telltale-prometheus", name: "telltale-prometheus", type: "prometheus", source: "/home/dev/.config/telltale/connections/prometheus.yaml" }] }));
    }
    if (path === "/api/traces/start") return Promise.resolve(json(startForm));
    if (path === "/api/traces" && method === "POST") return Promise.resolve(options.start ?? json(startedRun, 201));
    const one = /^\/api\/traces\/definitions\/([^/]+)$/.exec(path);
    if (one) {
      const name = decodeURIComponent(one[1]!);
      if (method === "GET") return Promise.resolve(stored[name] ? json(stored[name]) : new Response(`no trace definition ${name}`, { status: 404 }));
      if (method === "DELETE") return Promise.resolve(new Response(null, { status: 204 }));
      if (method === "PUT") {
        const put = body as { layer: string; resourceVersion: string; definition: TraceDefinition };
        if (options.save) return Promise.resolve(options.save(name, put));
        const saved: StoredDefinition = {
          name, layer: put.layer as StoredDefinition["layer"], path: `/work/checkout/.telltale/traces/${name}.yaml`, resourceVersion: `${put.resourceVersion}+1`,
          definition: put.definition, yaml: "", entries: resolvedEntries(put.definition),
        };
        stored[name] = saved;
        return Promise.resolve(json(saved));
      }
    }
    return Promise.resolve(new Response(`unknown route ${method} ${input}`, { status: 404 }));
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

/** The parsed JSON bodies of the calls fetchMock had to url with method. */
export function bodiesOf(fetchMock: ReturnType<typeof serveDefinitions>, url: string, method: string): unknown[] {
  return fetchMock.mock.calls
    .filter(([input, init]) => input.split("?")[0] === url && (init?.method ?? "GET") === method)
    .map(([, init]) => JSON.parse(String(init?.body)));
}
