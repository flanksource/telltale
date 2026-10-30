import {
  execFileSync,
  spawn,
  type ChildProcessWithoutNullStreams,
} from "node:child_process";
import { once } from "node:events";
import { mkdir, mkdtemp, rm } from "node:fs/promises";
import { resolve, join } from "node:path";
import { afterAll, beforeAll, describe, expect, it } from "vitest";

const traceId = "1234567890abcdef1234567890abcdef";
const finalTraceId = "2234567890abcdef1234567890abcdef";
const ingestTraceId = "3234567890abcdef1234567890abcdef";
const version = execFileSync(resolve(".bin/telltale"), ["--version"], {
  encoding: "utf8",
})
  .trim()
  .replace(/^telltale version /, "");
let testDir: string;
let server: ChildProcessWithoutNullStreams;
let apiURL: string;

async function start(flags: string[], host = "127.0.0.1") {
  server = spawn(
    resolve(".bin/telltale"),
    ["serve", "--host", host, "--port", "0", ...flags],
    {
      cwd: testDir,
      env: {
        ...process.env,
        XDG_CONFIG_HOME: join(testDir, "config"), XDG_CACHE_HOME: join(testDir, "cache"),
        OTEL_EXPORTER_OTLP_TRACES_ENDPOINT: "https://example.invalid:1",
      },
    },
  );
  apiURL = await new Promise<string>((resolveURL, reject) => {
    let output = "";
    server.stdout.on("data", (chunk: Buffer) => {
      output += chunk.toString();
      const match = /Telltale API: (http:\/\/\S+)/.exec(output);
      if (match) resolveURL(match[1]!.replace("0.0.0.0", "127.0.0.1"));
    });
    server.stderr.on("data", (chunk: Buffer) => {
      output += chunk.toString();
    });
    server.once("error", reject);
    server.once("exit", (code) =>
      reject(new Error(`Server exited (${code}): ${output}`)),
    );
  });
}

async function stop() {
  if (server?.exitCode === null) {
    const exited = once(server, "exit");
    server.kill("SIGTERM");
    const [code, signal] = await exited;
    expect({ code, signal }).toEqual({ code: 0, signal: null });
  }
}

beforeAll(async () => {
  await mkdir(".tmp", { recursive: true });
  testDir = await mkdtemp(resolve(".tmp/selftrace-api-"));
});
afterAll(async () => {
  await stop();
  if (testDir) await rm(testDir, { recursive: true, force: true });
});

describe.sequential("HTTP self-tracing", () => {
  it("keeps ordinary serve and receiver-only requests uninstrumented", async () => {
    await start([]);
    expect(
      await (await fetch(`${apiURL}/api/otel/sources`)).json(),
    ).not.toContainEqual(expect.objectContaining({ id: "receiver" }));
    await stop();
    await start(["--otlp", "--otlp-grpc-listen", "127.0.0.1:0"]);
    await fetch(`${apiURL}/health`);
    const search = await (
      await fetch(`${apiURL}/api/otel?source=records&run=receiver&service=telltale`)
    ).json();
    expect(search).toMatchObject({ total: 0 });
    await stop();
  });

  it("automatically enables its receiver, exports locally and exposes request metadata", async () => {
    await start(["--self-trace", "--otlp-grpc-listen", "127.0.0.1:0"]);
    const response = await fetch(
      `${apiURL}/api/otel/trace/example-missing?source=records&run=receiver&tags=${encodeURIComponent(JSON.stringify({ password: "private-query-value" }))}`,
      {
        headers: {
          traceparent: `00-${traceId}-1234567890abcdef-01`,
          Authorization: "Bearer private-request-value",
        },
      },
    );
    expect(response.status).toBe(404);
    await expect
      .poll(
        async () => {
          const result = await fetch(
            `${apiURL}/api/otel/trace/${traceId}?source=records&run=receiver`,
          );
          return result.status;
        },
        { timeout: 10000 },
      )
      .toBe(200);
    const detail = await (
      await fetch(`${apiURL}/api/otel/trace/${traceId}?source=records&run=receiver`)
    ).json();
    expect(detail.spans).toHaveLength(1);
    expect(detail.spans[0]).toMatchObject({
      service: "telltale",
      operation: "GET /api/otel/trace/{id}",
      parentId: "1234567890abcdef",
      resource: { "service.name": "telltale", "service.version": version },
      attributes: { "http.response.status_code": 404 },
    });
    expect(JSON.stringify(detail)).not.toContain("private-");
  }, 15000);

  it("excludes receiver ingestion and persists the final request before shutdown", async () => {
    const ingest = await fetch(`${apiURL}/v1/traces`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        traceparent: `00-${ingestTraceId}-1234567890abcdef-01`,
      },
      body: "{}",
    });
    expect(ingest.status).toBe(200);
    await fetch(`${apiURL}/health`, {
      headers: { traceparent: `00-${finalTraceId}-1234567890abcdef-01` },
    });
    await stop();
    await start([]);
    expect(
      (await fetch(`${apiURL}/api/otel/trace/${finalTraceId}?source=records&run=receiver`))
        .status,
    ).toBe(200);
    expect(
      (await fetch(`${apiURL}/api/otel/trace/${ingestTraceId}?source=records&run=receiver`))
        .status,
    ).toBe(404);
    await stop();
  });

  it("preserves HTTP authentication and traces denied requests when HTTP binds publicly", async () => {
    const deniedTraceId = "4234567890abcdef1234567890abcdef";
    await start(
      ["--self-trace", "--otlp-grpc-listen", "127.0.0.1:0"],
      "0.0.0.0",
    );
    const denied = await fetch(`${apiURL}/api/agents`, {
      headers: { traceparent: `00-${deniedTraceId}-1234567890abcdef-01` },
    });
    expect(denied.status).toBe(401);
    await stop();
    await start([]);
    const detail = await (
      await fetch(`${apiURL}/api/otel/trace/${deniedTraceId}?source=records&run=receiver`)
    ).json();
    expect(detail.spans).toHaveLength(1);
    expect(detail.spans[0].attributes).toMatchObject({
      "http.response.status_code": 401,
    });
    await stop();
  });

  it("rejects a non-loopback gRPC binding before readiness", async () => {
    const failed = spawn(
      resolve(".bin/telltale"),
      [
        "serve",
        "--port",
        "0",
        "--self-trace",
        "--otlp-grpc-listen",
        "0.0.0.0:0",
      ],
      {
        cwd: testDir,
        env: { ...process.env, XDG_CONFIG_HOME: join(testDir, "config"), XDG_CACHE_HOME: join(testDir, "cache") },
      },
    );
    let output = "";
    failed.stdout.on("data", (chunk: Buffer) => {
      output += chunk.toString();
    });
    failed.stderr.on("data", (chunk: Buffer) => {
      output += chunk.toString();
    });
    const [code] = await once(failed, "exit");
    expect(code).toBe(1);
    expect(output).toContain(
      "self-trace requires a loopback OTLP gRPC listener",
    );
    expect(output).not.toContain("Telltale API:");
  });
});
