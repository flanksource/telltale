import {
  execFile,
  spawn,
  type ChildProcessWithoutNullStreams,
} from "node:child_process";
import { once } from "node:events";
import { mkdir, mkdtemp, rm } from "node:fs/promises";
import { join, resolve } from "node:path";
import { promisify } from "node:util";
import { afterAll, beforeAll, describe, expect, it } from "vitest";

const run = promisify(execFile);
const traceID = "11111111111111111111111111111111";
let server: ChildProcessWithoutNullStreams;
let testDir: string;
let apiURL: string;

beforeAll(async () => {
  await mkdir(".tmp", { recursive: true });
  testDir = await mkdtemp(resolve(".tmp/otel-api-"));
  await run("go", [
    "run",
    "./internal/tracequery/testdata/seed",
    "--data-dir",
    join(testDir, "cache", "telltale"),
  ]);
  server = spawn(resolve(".bin/telltale"), ["serve", "--port", "0"], {
    cwd: testDir,
    env: { ...process.env, XDG_CONFIG_HOME: join(testDir, "config"), XDG_CACHE_HOME: join(testDir, "cache") },
  });
  apiURL = await new Promise<string>((resolveURL, reject) => {
    let output = "";
    server.stdout.on("data", (chunk: Buffer) => {
      output += chunk.toString();
      const match = /Telltale API: (http:\/\/\S+)/.exec(output);
      if (match) resolveURL(match[1]);
    });
    server.stderr.on("data", (chunk: Buffer) => {
      output += chunk.toString();
    });
    server.once("error", reject);
    server.once("exit", (code) =>
      reject(new Error(`Server exited (${code}): ${output}`)),
    );
  });
}, 60000);
afterAll(async () => {
  if (server?.exitCode === null) {
    const stopped = once(server, "exit");
    server.kill("SIGTERM");
    await stopped;
  }
  if (testDir) await rm(testDir, { recursive: true, force: true });
});

describe("OpenTelemetry search API", () => {
  it("searches recorded SDK and imported spans as whole traces, counting root operations", async () => {
    const filter = (value: Record<string, string>) => encodeURIComponent(JSON.stringify(value));
    const response = await fetch(
      `${apiURL}/api/otel?source=records&services=${filter({ payments: "include" })}&operations=${filter({ "POST /orders": "include" })}&statuses=${filter({ error: "include" })}&minDuration=100ms&minSpans=2&attributes=${filter({ "http.status_code=5*": "include" })}`,
    );
    expect(response.status).toBe(200);
    expect(await response.json()).toMatchObject({
      total: 1,
      traces: [
        {
          traceId: traceID,
          service: "checkout",
          durationMs: 120,
          spanCount: 3,
          errors: 1,
        },
      ],
      rootOperations: [{ operation: "POST /orders", count: 1, p50Ms: 120, p95Ms: 120 }],
    });
    const all = (await (
      await fetch(`${apiURL}/api/otel?source=records`)
    ).json()) as { total: number };
    expect(all.total).toBe(3);
  });
  it("retrieves a complete trace with parents and attributes", async () => {
    const response = await fetch(
      `${apiURL}/api/otel/trace/${traceID}?source=records`,
    );
    expect(response.status).toBe(200);
    expect(await response.json()).toMatchObject({
      missingParents: [],
      spans: expect.arrayContaining([
        expect.objectContaining({
          operation: "charge",
          parentId: "1111111111111111",
          attributes: {
            "http.status_code": 500,
            "error.message": "payment declined",
          },
        }),
      ]),
    });
  });
  it("serves the schema, source catalog and OpenAPI", async () => {
    expect(await (await fetch(`${apiURL}/api/otel/form`)).json()).toMatchObject(
      {
        schema: {
          properties: {
            attributes: { type: "object" },
            source: { type: "string" },
            minSpans: { type: "integer" },
            format: { enum: ["flat", "jaeger"] },
          },
        },
        defaults: { source: "records", lookback: "1h", minSpans: 2 },
      },
    );
    expect(await (await fetch(`${apiURL}/api/otel/sources`)).json()).toEqual([
      { id: "records", name: "Recorded traces", type: "records" },
    ]);
    expect(
      await (await fetch(`${apiURL}/api/openapi.json`)).json(),
    ).toHaveProperty("paths./api/otel");
  });
  it.each([
    "limit=1001",
    "minDuration=-1ms",
    `statuses=${encodeURIComponent(JSON.stringify({ maybe: "include" }))}`,
    `services=${encodeURIComponent(JSON.stringify({ checkout: "only" }))}`,
    "from=not-a-date",
    "attributes=oops",
    "unknown=1",
    "source=records&source=records",
  ])("refuses invalid search %s", async (query) => {
    const response = await fetch(
      `${apiURL}/api/otel?${query.startsWith("source=") ? query : `source=records&${query}`}`,
    );
    expect(response.status).toBe(400);
  });
  it("returns 404 for a trace absent from the selected source", async () => {
    expect(
      (await fetch(`${apiURL}/api/otel/trace/missing?source=records`)).status,
    ).toBe(404);
  });
  it.each(["/otel", `/otel/${traceID}`])(
    "serves the trace UI deep link %s",
    async (path) => {
      expect((await fetch(apiURL + path)).status).toBe(200);
    },
  );
});
