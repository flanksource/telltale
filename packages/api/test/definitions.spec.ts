import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { once } from "node:events";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterAll, beforeAll, describe, expect, it } from "vitest";

let testDir: string;
let server: ChildProcessWithoutNullStreams;
let apiURL: string;

function serve(): Promise<string> {
  server = spawn(".bin/telltale", ["serve", "--port", "0", "--data-dir", testDir], { env: { ...process.env, XDG_CONFIG_HOME: join(testDir, "config") } });
  return new Promise<string>((resolve, reject) => {
    let output = "";
    server.stdout.on("data", (chunk: Buffer) => {
      output += chunk.toString();
      const match = /Telltale API: (http:\/\/\S+)/.exec(output);
      if (match) resolve(match[1]);
    });
    server.once("error", reject);
    server.stderr.on("data", (chunk: Buffer) => { output += chunk.toString(); });
    server.once("exit", (code) => reject(new Error(`Server exited (${code}): ${output}`)));
  });
}

beforeAll(async () => {
  testDir = await mkdtemp(join(tmpdir(), "telltale-definitions-"));
  apiURL = await serve();
});

afterAll(async () => {
  if (server?.exitCode === null) {
    const stopped = once(server, "exit");
    server.kill("SIGTERM");
    expect(await stopped).toEqual([0, null]);
  }
  await rm(testDir, { recursive: true, force: true });
});

// probe reads SELECT 1 from an in-memory sqlite database: a host run that
// reaches no network.
const probe = {
  apiVersion: "telltale.flanksource.com/v1alpha1",
  kind: "TraceDefinition",
  metadata: { name: "api-probe" },
  spec: { description: "Reads one row", collect: { query: { sql: [{ type: "sqlite", url: ":memory:", query: "SELECT 1 AS one" }] } } },
};

interface Entry { pointer: string; path: string; kind: string; name: string; defaultName: string }
interface Detail { name: string; layer: string; resourceVersion: string; yaml: string; entries: Entry[] }
interface Run { runId: string; mount: string; status: string; definition?: { name: string; resourceVersion: string } }

const json = { "Content-Type": "application/json" };

function put(resourceVersion: string, definition: object): Promise<Response> {
  return fetch(`${apiURL}/api/traces/definitions/api-probe`, { method: "PUT", headers: json, body: JSON.stringify({ layer: "global", resourceVersion, definition }) });
}

describe("trace definitions", () => {
  it("validates mixed Prometheus endpoint schemes without connecting to Kubernetes", async () => {
    const response = await fetch(`${apiURL}/api/traces/definitions/validate`, {
      method: "POST", headers: json,
      body: JSON.stringify({ definition: { ...probe, spec: { collect: { prometheus: { scrape: [{ endpoints: [
        "port-forward://metrics-exporter.monitoring:9100/metrics",
        "proxy://metrics-exporter.monitoring:9100/metrics",
        "https://metrics.example.com/metrics",
      ] }] } } } } }),
    });
    expect(response.status).toBe(200);
    expect(await response.json()).toMatchObject({ errors: [], entries: [expect.objectContaining({ kind: "prometheus" })] });
  });

  it("publishes JVM protocol entries and validates agent SQL top without a database connection", async () => {
    const protocols = {
      http: [{ agent: { id: "java/checkout" }, features: ["http"] }],
      httpTop: [{ agent: { id: "java/checkout" }, direction: "both" }],
      sql: [{ agent: { id: "java/checkout" } }],
      sqlTop: [{ agent: { id: "java/checkout" }, poll: "5s" }],
      jms: [{ agent: { id: "java/checkout" }, direction: "incoming" }],
      spans: [{ agent: { id: "java/checkout" } }],
    };
    const response = await fetch(`${apiURL}/api/traces/definitions/validate`, {
      method: "POST", headers: json,
      body: JSON.stringify({ definition: { ...probe, spec: { instrument: { jvm: protocols } } } }),
    });
    expect(response.status).toBe(200);
    const validation = await response.json() as { errors: unknown[]; entries: Entry[] };
    expect(validation.errors).toEqual([]);
    expect(validation.entries.map(entry => ({ path: entry.path, kind: entry.kind }))).toEqual([
      { path: "instrument.jvm.http", kind: "http" },
      { path: "instrument.jvm.httpTop", kind: "http_top" },
      { path: "instrument.jvm.sql", kind: "sql" },
      { path: "instrument.jvm.sqlTop", kind: "sql_top" },
      { path: "instrument.jvm.jms", kind: "jms" },
      { path: "instrument.jvm.spans", kind: "spans" },
    ]);
    const form = await (await fetch(`${apiURL}/api/traces/start`)).json() as { schema: { properties: { traces: { properties: Record<string, unknown> } } } };
    expect(form.schema.properties.traces.properties).toHaveProperty("http_top");
    expect(form.schema.properties.traces.properties).toHaveProperty("jms");
  });

  it("rejects the Go-only SQL self selector at the JVM definition entry", async () => {
    const response = await fetch(`${apiURL}/api/traces/definitions/validate`, {
      method: "POST", headers: json,
      body: JSON.stringify({ definition: { ...probe, spec: { instrument: { jvm: { sql: [{ agent: { id: "java/checkout" }, connections: ["self"] }] } } } } }),
    });
    expect(response.status).toBe(200);
    const validation = await response.json() as { errors: { instancePath: string }[] };
    expect(validation.errors).toEqual([expect.objectContaining({ instancePath: "/spec/instrument/jvm/sql/0" })]);
  });

  it("creates, lists and starts a definition, refusing a stale write, and records it on the run", async () => {
    const created = await put("", probe);
    expect(created.status).toBe(200);
    const saved = await created.json() as Detail;
    expect(saved.entries).toEqual([expect.objectContaining({ path: "collect.query.sql", kind: "query", defaultName: "api-probe-collect-query-sql" })]);

    const listed = await (await fetch(`${apiURL}/api/traces/definitions`)).json() as { name: string; layer: string }[];
    expect(listed).toEqual(expect.arrayContaining([expect.objectContaining({ name: "api-probe", layer: "global" })]));
    expect((await put("", probe)).status).toBe(409);

    const started = await fetch(`${apiURL}/api/traces`, { method: "POST", headers: json, body: JSON.stringify({ definition: "api-probe", duration: "500ms" }) });
    expect(started.status).toBe(201);
    const run = await started.json() as Run;
    expect(run.definition).toEqual(expect.objectContaining({ name: "api-probe", resourceVersion: saved.resourceVersion }));

    const both = await fetch(`${apiURL}/api/traces`, { method: "POST", headers: json, body: JSON.stringify({ definition: "api-probe", traces: { http: { enabled: true } } }) });
    expect(both.status).toBe(400);
  });

  it("validates a definition, answering each entry's default name and the errors at their pointers", async () => {
    const response = await fetch(`${apiURL}/api/traces/definitions/validate`, {
      method: "POST", headers: json, body: JSON.stringify({ definition: { ...probe, spec: { ...probe.spec, every: "soon" } } }),
    });
    expect(response.status).toBe(200);
    const validation = await response.json() as { errors: { instancePath: string }[]; entries: Entry[] };
    expect(validation.errors).toEqual([expect.objectContaining({ instancePath: "/spec/every" })]);
    expect(validation.entries.map(entry => entry.defaultName)).toEqual(["api-probe-collect-query-sql"]);
  });
});
