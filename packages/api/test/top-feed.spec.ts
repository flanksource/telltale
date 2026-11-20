import { execFile, spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { once } from "node:events";
import { mkdir, mkdtemp, readFile, rm } from "node:fs/promises";
import { join, resolve } from "node:path";
import { promisify } from "node:util";
import { afterAll, beforeAll, expect, it } from "vitest";

let directory: string;
let server: ChildProcessWithoutNullStreams;
let apiURL: string;

beforeAll(async () => {
  await mkdir(".tmp", { recursive: true });
  directory = await mkdtemp(resolve(".tmp/top-feed-"));
  const fixture = join(directory, "handlers.pb");
  await promisify(execFile)("go", ["run", "./hack/jms-profile-fixture", fixture]);
  server = spawn(".bin/telltale", ["serve", "--port", "0", "--data-dir", directory, "--otlp", "--otlp-grpc-listen", "localhost:0"], { env: { ...process.env, XDG_CONFIG_HOME: join(directory, "config") } });
  apiURL = await new Promise<string>((resolveURL, reject) => {
    let output = "";
    server.stdout.on("data", (chunk: Buffer) => {
      output += chunk.toString();
      const match = /Telltale API: (http:\/\/\S+)/.exec(output);
      if (match) resolveURL(match[1]!);
    });
    server.stderr.on("data", (chunk: Buffer) => { output += chunk.toString(); });
    server.once("error", reject);
    server.once("exit", code => reject(new Error(`Server exited (${code}): ${output}`)));
  });
  const received = await fetch(`${apiURL}/v1/profiles`, { method: "POST", headers: { "Content-Type": "application/x-protobuf", "OTLP-Profiles-Development-Version": "1" }, body: new Uint8Array(await readFile(fixture)) });
  expect(received.status).toBe(200);
}, 60000);

afterAll(async () => {
  if (server?.exitCode === null) {
    const stopped = once(server, "exit");
    server.kill("SIGTERM");
    await stopped;
  }
  if (directory) await rm(directory, { recursive: true, force: true });
});

it("excludes Top snapshots from paged and live All events while retaining their Top view", async () => {
  const mount = `${apiURL}/api/traces/receiver`;
  expect(await (await fetch(`${mount}/records`)).json()).toEqual({ rows: [], nextCursor: "" });
  expect(await (await fetch(`${mount}/records?follow=1&format=ndjson`)).text()).toBe("");
  expect(await (await fetch(`${mount}/records?follow=1`)).text()).toBe("event: end\ndata: {}\n\n");
  const top = await fetch(`${mount}/traces/jms_top/records?view=top`);
  expect(top.status).toBe(200);
  expect(await top.json()).toMatchObject({ count: 2, rows: expect.arrayContaining([expect.objectContaining({ destination: "orders", state: "busy" }), expect.objectContaining({ destination: "replies", state: "idle" })]) });
});
