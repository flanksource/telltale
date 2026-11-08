import { execFile, spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { once } from "node:events";
import { mkdir, mkdtemp, rm } from "node:fs/promises";
import { createServer, type Server } from "node:http";
import type { AddressInfo } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { afterAll, beforeAll, describe, expect, it } from "vitest";

const run = promisify(execFile);

let testDir: string;
let upstream: Server;
let upstreamURL: string;
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
  testDir = await mkdtemp(join(tmpdir(), "telltale-traces-"));
  upstream = createServer((_, response) => response.end("traced"));
  upstream.listen(0, "127.0.0.1");
  await once(upstream, "listening");
  upstreamURL = `http://127.0.0.1:${(upstream.address() as AddressInfo).port}/hello`;
  await run(".bin/telltale", ["trace", "run", "--data-dir", testDir, "--", "curl", "-sS", upstreamURL], { env: { ...process.env, XDG_CONFIG_HOME: join(testDir, "config") } });
  apiURL = await serve();
});

afterAll(async () => {
  if (server?.exitCode === null) {
    const stopped = once(server, "exit");
    server.kill("SIGTERM");
    expect(await stopped).toEqual([0, null]);
  }
  upstream?.close();
  await rm(testDir, { recursive: true, force: true });
});

interface Captured {
  kind: string;
  ref: { kind: string; stream: string };
  summary?: { count: number };
}

interface ListedRun {
  runId: string;
  mount: string;
  status: string;
  command: string[];
  exitCode?: number;
  captures: Captured[];
  process?: { tree: { kind: string; stream: string }; samples: { kind: string; stream: string }; facets: { kind: string; stream: string } };
}

interface Listed {
  runs: ListedRun[];
}

describe("recorded trace runs", () => {
	 it("serves pinned process measurements separately from the styled event stream", async () => {
    const listed = await (await fetch(`${apiURL}/api/traces`)).json() as Listed;
    const recorded = listed.runs[0];
    const snapshot = await fetch(`${apiURL}${recorded.mount}/processes`);
    expect(snapshot.status).toBe(200);
    expect(await snapshot.json()).toMatchObject({ armed: true, rows: expect.arrayContaining([expect.objectContaining({ runId: recorded.runId, key: expect.any(String), pid: expect.any(Number), rssBytes: expect.any(Number), memoryPercent: expect.any(Number) })]) });
    const events = await fetch(`${apiURL}${recorded.mount}/records?excludeKind=process&presentation=ansi&sort=timestamp&limit=1`);
    expect(events.status).toBe(200);
    const page = await events.json() as { rows: { kind: string; ansi: string }[] };
    expect(page.rows).toHaveLength(1);
    expect(page.rows[0].kind).toBe("http");
    expect(page.rows[0].ansi).toContain("GET");
    expect(page.rows[0].ansi).toContain("\u001b[");
    const raw = await (await fetch(`${apiURL}${recorded.mount}/records?kind=http&limit=1`)).json() as { rows: Record<string, unknown>[] };
    expect(raw.rows[0]).not.toHaveProperty("ansi");
  });
	 it("exposes one process kind with identity, numeric metrics, facet events and derived groups", async () => {
    const listed = await (await fetch(`${apiURL}/api/traces`)).json() as Listed;
    const recorded = listed.runs[0];
    expect(recorded.process).toMatchObject({ tree: { kind: "process" }, samples: { kind: "process_metrics" }, facets: { kind: "process_facets" } });
    const catalog = await (await fetch(`${apiURL}${recorded.mount}/api/v1/traces/kinds`)).json() as { name: string; results?: { name: string }[] }[];
    expect(catalog.filter(kind => kind.name.startsWith("process"))).toEqual([expect.objectContaining({ name: "process", results: [expect.objectContaining({ name: "facets" }), expect.objectContaining({ name: "metrics" })] })]);
    const metrics = await fetch(`${apiURL}${recorded.mount}/traces/process/records?result=metrics&limit=1`);
    expect(metrics.status).toBe(200);
    expect(await metrics.json()).toMatchObject({ rows: [expect.objectContaining({ entity: expect.any(String), rssBytes: expect.any(Number) })] });
    const groups = await fetch(`${apiURL}${recorded.mount}/process/groups?stream=${recorded.process!.tree.stream}`);
    expect(groups.status).toBe(200);
    expect(await groups.json()).toMatchObject({ highSeq: expect.any(Number), frames: expect.arrayContaining([expect.objectContaining({ groups: expect.arrayContaining([expect.objectContaining({ total: expect.objectContaining({ processes: expect.any(Number) }) })]) })]) });
  });
  it("replays the API's compact run ID through the same stored capture feed", async () => {
    const listed = await (await fetch(`${apiURL}/api/traces`)).json() as Listed;
    const recorded = listed.runs[0];
    expect(recorded.runId).toMatch(/^[A-Za-z0-9]{1,9}\d{8}T\d{6}Z(?:_\d+)?$/);
    const replayed = await run(".bin/telltale", ["trace", "replay", "--data-dir", testDir, "--format", "ndjson", recorded.runId]);
    const rows = replayed.stdout.trim().split("\n").map(line => JSON.parse(line) as { runId: string; kind: string; record: { url: string } });
    expect(rows).toContainEqual(expect.objectContaining({ runId: recorded.runId, kind: "http", record: expect.objectContaining({ url: upstreamURL }) }));
    expect(rows).toContainEqual(expect.objectContaining({ runId: recorded.runId, kind: "process", result: "metrics" }));
  });
  it("pages the unified armed-capture feed and serves a completed NDJSON stream", async () => {
    const listed = await (await fetch(`${apiURL}/api/traces`)).json() as Listed;
    const recorded = listed.runs[0];
    expect(recorded).toBeDefined();
    const response = await fetch(`${apiURL}${recorded.mount}/records?kind=http&sort=seq&limit=1`);
    expect(response.status).toBe(200);
    const page = await response.json() as { rows: { runId: string; kind: string; stream: string; seq: number; record: Record<string, unknown> }[] };
    expect(page.rows).toHaveLength(1);
    expect(page.rows[0]).toMatchObject({ runId: recorded.runId, kind: "http", seq: 1, record: { request: expect.anything() } });
    const streamed = await fetch(`${apiURL}${recorded.mount}/records?kind=http&follow=1&format=ndjson`);
    expect(streamed.status).toBe(200);
    expect(JSON.parse((await streamed.text()).trim())).toMatchObject(page.rows[0]);
  });

  it("serves typed result-profile columns and query rows for a recorded capture", async () => {
    const listed = await (await fetch(`${apiURL}/api/traces`)).json() as Listed;
    const recorded = listed.runs[0];
    const capture = recorded.captures.find(item => item.kind === "http")!;
    const path = `${apiURL}${recorded.mount}/traces/http/records?stream=${encodeURIComponent(capture.ref.stream)}`;
    const schema = await fetch(path, { headers: { Accept: "application/schema+json" } });
    expect(schema.status).toBe(200);
    expect(await schema.json()).toMatchObject({ optionsSchema: { properties: { where: { type: "string" } } }, columns: expect.arrayContaining([expect.objectContaining({ name: "url" })]) });
    const rows = await fetch(path + "&limit=1");
    expect(rows.status).toBe(200);
    expect(await rows.json()).toMatchObject({ rows: [expect.objectContaining({ url: upstreamURL })] });
  });
  it("lists the run with the proxy capture it armed", async () => {
    const response = await fetch(`${apiURL}/api/traces`);
    expect(response.status).toBe(200);
    const listed = await response.json() as Listed;
    expect(listed.runs).toHaveLength(1);
    expect(listed.runs[0]).toMatchObject({
      mount: `/api/traces/${listed.runs[0].runId}`,
      status: "succeeded", exitCode: 0, captures: [{ kind: "http", summary: { count: 1 } }],
    });
  });

  it("exports the exchange the proxy relayed from the run's store", async () => {
    const listed = await (await fetch(`${apiURL}/api/traces`)).json() as Listed;
    const { mount, captures } = listed.runs[0];
    const ref = captures[0].ref;
    const response = await fetch(`${apiURL}${mount}/api/v1/traces/${ref.kind}/export?format=ndjson&stream=${ref.stream}`);
    expect(response.status).toBe(200);
    expect(response.headers.get("content-type")).toBe("application/x-ndjson");
    const records = (await response.text()).trim().split("\n").map(line => JSON.parse(line) as { url: string });
    expect(records.map(record => record.url)).toEqual([upstreamURL]);
  });

  it("returns 404 for a run it holds no store for", async () => {
    const response = await fetch(`${apiURL}/api/traces/run-missing/api/v1/traces/kinds`);
    expect(response.status).toBe(404);
  });
});

async function start(body: unknown): Promise<Response> {
  return fetch(`${apiURL}/api/traces`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
}

// settled polls the run until it is no longer running.
async function settled(runId: string): Promise<ListedRun> {
  for (let attempt = 0; attempt < 150; attempt++) {
    const listed = await (await fetch(`${apiURL}/api/traces?q=${encodeURIComponent(runId)}`)).json() as Listed;
    if (listed.runs[0] && listed.runs[0].status !== "running") return listed.runs[0];
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  throw new Error(`run ${runId} is still running`);
}

describe("runs started over the API", () => {
  it("previews the embedded Java installer through its generated API", async () => {
    const response = await fetch(`${apiURL}/api/v1/agents/install/java`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ pid: 1, directory: join(testDir, "agents"), "dry-run": true }) });
    expect(response.status).toBe(200);
    expect(await response.text()).toContain("telltale-agent.jar");
  });

  it("rejects cross-origin installer requests and leaves other command execution disabled", async () => {
    const refused = await fetch(`${apiURL}/api/v1/agents/install/java`, { method: "POST", headers: { Origin: "https://untrusted.example.com", "Content-Type": "application/json" }, body: "{}" });
    expect(refused.status).toBe(403);
    expect((await fetch(`${apiURL}/api/v1/agents/token/create/example`, { method: "POST" })).status).toBe(404);
  });

  it("captures and exports a disposable JVM through a PID-only start request", async () => {
    const classes = join(testDir, "java-classes");
    await mkdir(classes);
    await run("javac", ["-d", classes, "pkg/agentinstall/testdata/Workload.java"]);
    const child = spawn("java", ["-cp", classes, "Workload"]);
    try {
      await once(child.stdout, "data");
      const response = await start({ pid: child.pid, duration: "2s", every: "1s", traces: { jvm_jfr: { enabled: true, engine: "jfr" } } });
      expect(response.status).toBe(201);
      const started = await response.json() as ListedRun;
      const finished = await settled(started.runId);
      expect(finished.status).toBe("succeeded");
      expect(finished.captures[0].summary?.count).toBeGreaterThan(0);
      const capture = finished.captures[0];
      const exported = await fetch(`${apiURL}${finished.mount}/api/v1/traces/jvm_jfr/export?format=ndjson&stream=${capture.ref.stream}`);
      expect(exported.status).toBe(200);
      expect((await exported.text()).trim()).not.toBe("");
    } finally {
      const exited = once(child, "exit");
      child.kill("SIGTERM");
      await exited;
    }
  });

  it("offers SQL Server and JVM capture schemas with connection and agent targets", async () => {
    const response = await fetch(`${apiURL}/api/traces/start`);
    const form = await response.json() as { schema: { properties: { traces: { properties: Record<string, { title: string }> }; pid: { type: string }; duration: { type: string } } } };
    expect(response.status).toBe(200);
    expect(form.schema.properties.traces.properties).toMatchObject({ sql_xevent: { title: "SQL Server XEvents" }, sql_deadlock: { title: "SQL Server deadlocks" }, jvm_jfr: { title: "JVM JFR" } });
    expect(form.schema.properties.pid.type).toBe("integer");
    expect(form.schema.properties.duration.type).toBe("string");
  });

  it("validates a commandless historical deadlock request at its capture fields", async () => {
    const response = await start({ traces: { sql_deadlock: { enabled: true, from: "2026-10-28T12:00:00Z", to: "2026-10-28T11:00:00Z" } } });
    expect(response.status).toBe(400);
    const refused = await response.json() as { errors: { instancePath: string }[] };
    expect(refused.errors.map(field => field.instancePath).sort()).toEqual(["/traces/sql_deadlock", "/traces/sql_deadlock/connection"]);
  });

  it("serves the start form", async () => {
    const response = await fetch(`${apiURL}/api/traces/start`);
    expect(response.status).toBe(200);
    const form = await response.json() as { schema: { required: string[] }; defaults: { traces: Record<string, { enabled: boolean }> } };
    expect(form.schema.required).toBeUndefined();
    expect(form.defaults.traces.http.enabled).toBe(true);
  });

  it("runs a shell command under the proxy it arms and records its exchange", async () => {
    const response = await start({ command: `curl -sS ${upstreamURL}`, traces: { http: { enabled: true, target: { seam: "proxy" } } } });
    expect(response.status).toBe(201);
    const started = await response.json() as ListedRun;
    expect(started).toMatchObject({ status: "running", command: ["sh", "-c", `curl -sS ${upstreamURL}`], output: { stdout: "stdout.log", stderr: "stderr.log" } });
    expect(await settled(started.runId)).toMatchObject({ status: "succeeded", exitCode: 0, captures: [{ kind: "http", summary: { count: 1 } }] });
  });

  it("stops a running run, which still records how it ended", async () => {
    const started = await (await start({ command: "sleep 60" })).json() as ListedRun;
    expect((await fetch(`${apiURL}${started.mount}`, { method: "DELETE" })).status).toBe(202);
    expect((await settled(started.runId)).status).toBe("failed");
    expect((await fetch(`${apiURL}${started.mount}`, { method: "DELETE" })).status).toBe(409);
  });

  it("refuses a start request field by field", async () => {
    const response = await start({ command: "", traces: { http: { enabled: true, target: { seam: "proxy" }, mode: "sideways" } } });
    expect(response.status).toBe(400);
    const refused = await response.json() as { errors: Array<{ instancePath: string }> };
    expect(refused.errors.map(error => error.instancePath).sort()).toEqual(["/command", "/traces/http"]);
  });

  it("rejects a seam the kind does not support", async () => {
    const response = await start({ command: "true", traces: { http: { enabled: true, target: { seam: "process" } } } });
    expect(response.status).toBe(400);
    const refused = await response.json() as { errors: Array<{ instancePath: string; message: string }> };
    expect(refused.errors).toEqual([expect.objectContaining({ instancePath: "/traces/http", message: expect.stringContaining("unsupported seam") })]);
  });

  it("filters the runs it lists", async () => {
    const listed = await (await fetch(`${apiURL}/api/traces?status=failed&q=sleep`)).json() as Listed;
    expect(listed.runs.map(listedRun => listedRun.command)).toEqual([["sh", "-c", "sleep 60"]]);
  });
});
