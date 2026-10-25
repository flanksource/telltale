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

interface Listed {
  runs: Array<{ runId: string; mount: string; run: { exitCode: number; captures: Array<{ kind: string; ref: { kind: string; stream: string }; summary: { count: number } }> } }>;
}

describe("recorded trace runs", () => {
  it("lists the run with the proxy capture it armed", async () => {
    const response = await fetch(`${apiURL}/api/traces`);
    expect(response.status).toBe(200);
    const listed = await response.json() as Listed;
    expect(listed.runs).toHaveLength(1);
    expect(listed.runs[0]).toMatchObject({
      mount: `/api/traces/${listed.runs[0].runId}`,
      run: { exitCode: 0, captures: [{ kind: "http_proxy", summary: { count: 1 } }] },
    });
  });

  it("exports the exchange the proxy relayed from the run's store", async () => {
    const listed = await (await fetch(`${apiURL}/api/traces`)).json() as Listed;
    const { mount, run: recorded } = listed.runs[0];
    const ref = recorded.captures[0].ref;
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
