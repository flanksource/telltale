import { execFile, spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { once } from "node:events";
import { mkdtemp, rm } from "node:fs/promises";
import { createServer, type Server } from "node:http";
import type { AddressInfo } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { afterAll, beforeAll, describe, expect, it } from "vitest";

const run = promisify(execFile);

let workDir: string;
let upstream: Server;
let upstreamURL: string;
let server: ChildProcessWithoutNullStreams;
let apiURL: string;

function serve(): Promise<string> {
  server = spawn(".bin/telltale", ["serve", "--port", "0", "--work-dir", workDir]);
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
  workDir = await mkdtemp(join(tmpdir(), "telltale-traces-"));
  upstream = createServer((_, response) => response.end("traced"));
  upstream.listen(0, "127.0.0.1");
  await once(upstream, "listening");
  upstreamURL = `http://127.0.0.1:${(upstream.address() as AddressInfo).port}/hello`;
  await run(".bin/telltale", ["trace", "run", "--work-dir", workDir, "--", "curl", "-sS", upstreamURL]);
  apiURL = await serve();
});

afterAll(async () => {
  if (server?.exitCode === null) {
    const stopped = once(server, "exit");
    server.kill("SIGTERM");
    expect(await stopped).toEqual([0, null]);
  }
  upstream?.close();
  await rm(workDir, { recursive: true, force: true });
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
}

interface Listed {
  runs: ListedRun[];
}

describe("recorded trace runs", () => {
  it("lists the run with the proxy capture it armed", async () => {
    const response = await fetch(`${apiURL}/api/traces`);
    expect(response.status).toBe(200);
    const listed = await response.json() as Listed;
    expect(listed.runs).toHaveLength(1);
    expect(listed.runs[0]).toMatchObject({
      mount: `/api/traces/${listed.runs[0].runId}`,
      status: "succeeded", exitCode: 0, captures: [{ kind: "http_proxy", summary: { count: 1 } }],
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
  it("serves the start form", async () => {
    const response = await fetch(`${apiURL}/api/traces/start`);
    expect(response.status).toBe(200);
    const form = await response.json() as { schema: { required: string[] }; defaults: { traces: Record<string, { enabled: boolean }> } };
    expect(form.schema.required).toEqual(["command"]);
    expect(form.defaults.traces.http_proxy.enabled).toBe(true);
  });

  it("runs a shell command under the proxy it arms and records its exchange", async () => {
    const response = await start({ command: `curl -sS ${upstreamURL}`, traces: { http_proxy: { enabled: true } } });
    expect(response.status).toBe(201);
    const started = await response.json() as ListedRun;
    expect(started).toMatchObject({ status: "running", command: ["sh", "-c", `curl -sS ${upstreamURL}`] });
    expect(await settled(started.runId)).toMatchObject({ status: "succeeded", exitCode: 0, captures: [{ kind: "http_proxy", summary: { count: 1 } }] });
  });

  it("stops a running run, which still records how it ended", async () => {
    const started = await (await start({ command: "sleep 60" })).json() as ListedRun;
    expect((await fetch(`${apiURL}${started.mount}`, { method: "DELETE" })).status).toBe(202);
    expect((await settled(started.runId)).status).toBe("failed");
    expect((await fetch(`${apiURL}${started.mount}`, { method: "DELETE" })).status).toBe(409);
  });

  it("refuses a start request field by field", async () => {
    const response = await start({ command: "", traces: { http_proxy: { enabled: true, mode: "sideways" } } });
    expect(response.status).toBe(400);
    const refused = await response.json() as { errors: Array<{ instancePath: string }> };
    expect(refused.errors.map(error => error.instancePath).sort()).toEqual(["/command", "/traces/http_proxy"]);
  });

  it("filters the runs it lists", async () => {
    const listed = await (await fetch(`${apiURL}/api/traces?status=failed&q=sleep`)).json() as Listed;
    expect(listed.runs.map(listedRun => listedRun.command)).toEqual([["sh", "-c", "sleep 60"]]);
  });
});
