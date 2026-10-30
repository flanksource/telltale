import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { once } from "node:events";
import { mkdir, mkdtemp, rm } from "node:fs/promises";
import { join, resolve } from "node:path";
import { afterAll, beforeAll, describe, expect, it } from "vitest";

let server: ChildProcessWithoutNullStreams;
let testDir: string;
let apiURL: string;

beforeAll(async () => {
  await mkdir(".tmp", { recursive: true });
  testDir = await mkdtemp(resolve(".tmp/connections-"));
  server = spawn(resolve(".bin/telltale"), ["serve", "--port", "0"], {
    cwd: testDir, env: { ...process.env, XDG_CONFIG_HOME: join(testDir, "config"), XDG_CACHE_HOME: join(testDir, "cache") },
  });
  apiURL = await new Promise<string>((resolveURL, reject) => {
    let output = "";
    server.stdout.on("data", (chunk: Buffer) => {
      output += chunk.toString();
      const match = /Telltale API: (http:\/\/\S+)/.exec(output);
      if (match) resolveURL(match[1]);
    });
    server.stderr.on("data", (chunk: Buffer) => { output += chunk.toString(); });
    server.once("error", reject);
    server.once("exit", code => reject(new Error(`Server exited (${code}): ${output}`)));
  });
});

afterAll(async () => {
  if (server?.exitCode === null) {
    const stopped = once(server, "exit");
    server.kill("SIGTERM");
    await stopped;
  }
  if (testDir) await rm(testDir, { recursive: true, force: true });
});

describe("connections workspace", () => {
  it("serves the form schema and supports persistent CRUD and queries", async () => {
    const schema = await fetch(`${apiURL}/api/v1/connection`, { headers: { Accept: "application/schema+json" } });
    expect(schema.status).toBe(200);
    expect(await schema.json()).toHaveProperty("properties");
    const body = { name: "workspace", type: "sqlite", url: join(testDir, "workspace.db") };
    const created = await fetch(`${apiURL}/api/v1/connection`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
    expect(created.status).toBe(201);
    const connection = await created.json() as { id: string };
    const path = `${apiURL}/api/v1/connection/${connection.id}`;
    expect(await (await fetch(path)).json()).toMatchObject(body);
    expect(await (await fetch(`${apiURL}/api/v1/connections/dashboard`)).json()).toMatchObject({ connections: [expect.objectContaining({ name: body.name })] });
    const queried = await fetch(`${path}/browser/query`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ query: "SELECT 7 AS value", options: {} }) });
    expect(queried.status).toBe(200);
    expect(await queried.json()).toMatchObject({ rows: [{ value: 7 }] });
    const updated = await fetch(path, { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ ...body, name: "renamed" }) });
    expect(updated.status).toBe(200);
    const renamed = await updated.json() as { id: string; name: string };
    expect(renamed.name).toBe("renamed");
    expect((await fetch(`${apiURL}/api/v1/connection/${renamed.id}`, { method: "DELETE" })).status).toBe(204);
    expect((await fetch(`${apiURL}/api/v1/connection/${renamed.id}`)).status).toBe(404);
  });

  it.each(["/connections", "/agents", "/arm", "/arm/worker.example/http", "/query"])("serves the UI deep link %s", async path => {
    const response = await fetch(apiURL + path);
    expect(response.status).toBe(200);
    expect(response.headers.get("content-type")).toContain("text/html");
  });
});
