import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { once } from "node:events";
import { afterAll, beforeAll, describe, expect, it } from "vitest";

let server: ChildProcessWithoutNullStreams;
let apiURL: string;

beforeAll(async () => {
  server = spawn(".bin/telltale", ["serve", "--port", "0"]);
  apiURL = await new Promise<string>((resolve, reject) => {
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
});

afterAll(async () => {
  if (server?.exitCode === null) {
    const stopped = once(server, "exit");
    server.kill("SIGTERM");
    expect(await stopped).toEqual([0, null]);
  }
});

describe("standalone API and embedded UI", () => {
  it("reports backend health", async () => {
    const response = await fetch(`${apiURL}/health`);
    expect(response.status).toBe(200);
    expect(await response.json()).toMatchObject({ status: "healthy", version: expect.any(String) });
  });

  it("exposes the generated OpenAPI contract", async () => {
    const response = await fetch(`${apiURL}/api/openapi.json`);
    expect(response.status).toBe(200);
    expect(await response.json()).toMatchObject({ info: { title: "Telltale" }, openapi: expect.stringMatching(/^3\./) });
  });

  it("serves deep links with revalidated HTML", async () => {
    const response = await fetch(`${apiURL}/settings`);
    expect(response.status).toBe(200);
    expect(response.headers.get("cache-control")).toBe("no-cache");
    expect(await response.text()).toContain("<title>Telltale</title>");
  });

  it.each(["/api/missing", "/assets/missing.js"])("returns 404 for %s", async (path) => {
    const response = await fetch(`${apiURL}${path}`);
    expect(response.status).toBe(404);
    expect(response.headers.get("content-type")).not.toContain("text/html");
  });
});
