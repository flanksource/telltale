import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { once } from "node:events";
import { mkdir, mkdtemp, rm } from "node:fs/promises";
import { createServer } from "node:net";
import { join, resolve } from "node:path";
import { gzipSync } from "node:zlib";
import { afterAll, beforeAll, describe, expect, it } from "vitest";

const traceId = "1234567890abcdef1234567890abcdef";
const receiverRun = "source=records&run=receiver";
let server: ChildProcessWithoutNullStreams;
let testDir: string;
let apiURL: string;

async function start(enabled: boolean, host = "127.0.0.1") {
  server = spawn(
    resolve(".bin/telltale"),
    [
      "serve",
      "--host",
      host,
      "--port",
      "0",
      ...(enabled ? ["--otlp", "--otlp-grpc-listen", "127.0.0.1:0"] : []),
    ],
    {
      cwd: testDir,
      env: { ...process.env, XDG_CONFIG_HOME: join(testDir, "config"), XDG_CACHE_HOME: join(testDir, "cache") },
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
    const stopped = once(server, "exit");
    server.kill("SIGTERM");
    await stopped;
  }
}

beforeAll(async () => {
  await mkdir(".tmp", { recursive: true });
  testDir = await mkdtemp(resolve(".tmp/receiver-api-"));
  await start(true);
});
afterAll(async () => {
  await stop();
  if (testDir) await rm(testDir, { recursive: true, force: true });
});

function traces() {
  const at = BigInt(Date.now() - 1000) * 1000000n;
  return {
    resourceSpans: [
      {
        resource: {
          attributes: [
            { key: "service.name", value: { stringValue: "checkout" } },
            { key: "replicas", value: { intValue: "3" } },
            {
              key: "password",
              value: { stringValue: "private-resource-value" },
            },
          ],
        },
        scopeSpans: [
          {
            scope: { name: "example-api", version: "1.0" },
            spans: [
              {
                traceId,
                spanId: "1234567890abcdef",
                name: "POST /orders",
                kind: 2,
                startTimeUnixNano: String(at),
                endTimeUnixNano: String(at + 120000000n),
                attributes: [
                  { key: "http.status_code", value: { intValue: "500" } },
                ],
                events: [
                  { name: "charged", timeUnixNano: String(at + 1000000n) },
                ],
              },
              {
                traceId,
                spanId: "abcdef1234567890",
                parentSpanId: "1234567890abcdef",
                name: "charge",
                kind: 3,
                startTimeUnixNano: String(at + 20000000n),
                endTimeUnixNano: String(at + 80000000n),
                status: { code: 2, message: "payment declined" },
              },
            ],
          },
        ],
      },
    ],
  };
}

describe.sequential("OTLP receiver API", () => {
  let body: string;
  it("fails startup before readiness when the gRPC address is occupied", async () => {
    const occupied = createServer();
    await new Promise<void>((resolveListen) =>
      occupied.listen(0, "127.0.0.1", resolveListen),
    );
    try {
      const address = occupied.address();
      if (!address || typeof address === "string")
        throw new Error("TCP listener address required");
      const failed = spawn(
        resolve(".bin/telltale"),
        [
          "serve",
          "--host",
          "127.0.0.1",
          "--port",
          "0",
          "--otlp",
          "--otlp-grpc-listen",
          `127.0.0.1:${address.port}`,
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
      expect(output).toContain("listen OTLP gRPC");
      expect(output).not.toContain("Telltale API:");
    } finally {
      await new Promise<void>((resolveClose, reject) =>
        occupied.close((error) => (error ? reject(error) : resolveClose())),
      );
    }
  });
  it("receives standard OTLP JSON and exposes complete traces with native metadata", async () => {
    body = JSON.stringify(traces());
    const response = await fetch(`${apiURL}/v1/traces`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body,
    });
    expect(response.status).toBe(200);
    expect(await response.json()).toEqual({ partialSuccess: {} });
    expect(
      await (await fetch(`${apiURL}/api/otel/sources`)).json(),
    ).not.toContainEqual(expect.objectContaining({ id: "receiver" }));
    expect(
      (await (await fetch(`${apiURL}/api/traces?kind=spans`)).json()).runs,
    ).toContainEqual(
      expect.objectContaining({
        runId: "receiver",
        status: "running",
        standing: true,
      }),
    );
    const records = await (
      await fetch(
        `${apiURL}/api/traces/receiver/traces/spans/records?filter=operation%3Dcharge`,
      )
    ).json();
    expect(JSON.stringify(records)).toContain(`"traceId":"${traceId}"`);
    const found = await (
      await fetch(
        `${apiURL}/api/otel?source=records&run=receiver&operations=${encodeURIComponent(JSON.stringify({ "POST /orders": "include" }))}&statuses=${encodeURIComponent(JSON.stringify({ error: "include" }))}&minDuration=100ms`,
      )
    ).json();
    expect(found).toMatchObject({
      total: 1,
      traces: [{ traceId, spanCount: 2, durationMs: 120, errors: 1 }],
    });
    const detail = await (
      await fetch(`${apiURL}/api/otel/trace/${traceId}?${receiverRun}`)
    ).json();
    expect(detail.spans).toHaveLength(2);
    expect(detail.spans[0]).toMatchObject({
      resource: { replicas: 3 },
      otlp: {
        resourceSpans: [
          {
            scopeSpans: [
              {
                scope: { name: "example-api" },
                spans: [{ events: [{ name: "charged" }] }],
              },
            ],
          },
        ],
      },
    });
    expect(JSON.stringify(detail)).not.toContain("private-resource-value");
  });
  it("deduplicates compressed retries and reports conflicting duplicates", async () => {
    const retried = await fetch(`${apiURL}/v1/traces`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "Content-Encoding": "gzip",
      },
      body: gzipSync(body),
    });
    expect(retried.status).toBe(200);
    const changed = JSON.parse(body) as ReturnType<typeof traces>;
    changed.resourceSpans[0]!.scopeSpans[0]!.spans[0]!.name =
      "conflicting operation";
    const conflict = await fetch(`${apiURL}/v1/traces`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(changed),
    });
    expect(await conflict.json()).toMatchObject({
      partialSuccess: { rejectedSpans: "1" },
    });
    expect(
      await (
        await fetch(`${apiURL}/api/otel/trace/${traceId}?${receiverRun}`)
      ).json(),
    ).toMatchObject({
      spans: [{ operation: "POST /orders" }, { operation: "charge" }],
    });
  });
  it.each([
    ["application/json", "malformed", 400],
    ["text/plain", "not OTLP", 415],
    ["application/json", " ".repeat((4 << 20) + 1), 413],
  ])("rejects invalid input (%s)", async (contentType, invalid, code) => {
    const response = await fetch(`${apiURL}/v1/traces`, {
      method: "POST",
      headers: { "Content-Type": contentType as string },
      body: invalid as string,
    });
    expect(response.status).toBe(code);
    expect(response.headers.get("content-type")).toBe("application/json");
    expect(await response.json()).toHaveProperty("message");
  });
  it("keeps received traces readable after restarting with ingestion disabled", async () => {
    await stop();
    await start(false);
    const response = await fetch(
      `${apiURL}/api/otel/trace/${traceId}?${receiverRun}`,
    );
    expect(response.status).toBe(200);
    expect((await response.json()).spans).toHaveLength(2);
    expect(
      (await (await fetch(`${apiURL}/api/traces?kind=spans`)).json()).runs,
    ).toContainEqual(
      expect.objectContaining({ runId: "receiver", status: "standing" }),
    );
    expect(
      (
        await fetch(`${apiURL}/v1/traces`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body,
        })
      ).status,
    ).toBe(404);
  });
  it("protects the OTLP route outside /api on a non-loopback HTTP listener", async () => {
    await stop();
    await start(true, "0.0.0.0");
    const response = await fetch(`${apiURL}/v1/traces`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body,
    });
    expect(response.status).toBe(401);
    expect(response.headers.get("content-type")).toBe("application/json");
    expect(await response.json()).toMatchObject({
      code: 16,
      message: "one authorization header is required",
    });
  });
});
