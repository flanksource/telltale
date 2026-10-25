import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { once } from "node:events";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterAll, beforeAll, describe, expect, it } from "vitest";

let workDir: string;
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
  workDir = await mkdtemp(join(tmpdir(), "telltale-top-"));
  apiURL = await serve();
});

afterAll(async () => {
  if (server?.exitCode === null) {
    const stopped = once(server, "exit");
    server.kill("SIGTERM");
    expect(await stopped).toEqual([0, null]);
  }
  await rm(workDir, { recursive: true, force: true });
});

interface Usage {
  processes: number;
  rssBytes: number;
}

interface Frame {
  sampledAt: string;
  tick: number;
  groups: Array<{ key: string; name: string; kind: string; pid: number; self: Usage; total: Usage }>;
}

describe("live process groups", () => {
  it("serves the latest frame of the host's groups, including the processes no tool owns", async () => {
    const response = await fetch(`${apiURL}/api/top?all=true`);
    expect(response.status).toBe(200);
    expect(response.headers.get("content-type")).toBe("application/json");
    const frame = await response.json() as Frame;
    expect(frame.tick).toBeGreaterThanOrEqual(1);
    expect(frame.groups.length).toBeGreaterThan(0);
    // The test runner itself is a process of the host: some group counts it, with memory.
    expect(frame.groups.some(group => group.total.processes > 0 && group.total.rssBytes > 0)).toBe(true);
  });

  it("refuses a query it cannot read as true or false", async () => {
    const response = await fetch(`${apiURL}/api/top?all=maybe`);
    expect(response.status).toBe(400);
    expect(await response.text()).toContain('all="maybe" is not true or false');
  });

  it("refuses any method but GET", async () => {
    const response = await fetch(`${apiURL}/api/top`, { method: "POST" });
    expect(response.status).toBe(405);
    expect(response.headers.get("allow")).toBe("GET");
  });
});

describe("processes of a program or a process", () => {
  it("serves a process and the ones under it by pid", async () => {
    const response = await fetch(`${apiURL}/api/top/processes?pid=1&all=true`);
    expect(response.status).toBe(200);
    expect(response.headers.get("content-type")).toBe("application/json");
    const reading = await response.json() as { tick: number; processes: Array<{ pid: number; ppid: number; name: string; origin: object; rssBytes: number }> };
    expect(reading.tick).toBeGreaterThanOrEqual(1);
    expect(reading.processes.map(process => process.pid)).toContain(1);
  });

  it("refuses a request that names both a program and a process", async () => {
    const response = await fetch(`${apiURL}/api/top/processes?name=x&pid=1`);
    expect(response.status).toBe(400);
  });

  it("refuses a request that names neither", async () => {
    const response = await fetch(`${apiURL}/api/top/processes`);
    expect(response.status).toBe(400);
  });
});

describe("facets of a process", () => {
  interface Facets {
    pid: number;
    name: string;
    context: { cwd?: string };
    environment?: Record<string, string>;
    descriptors: Record<"openFiles" | "sockets" | "ports" | "connections", Array<{ fd: string; type: string; port?: number }>>;
  }

  it("serves the server's own facets: the port it listens on, its open files and its environment", async () => {
    const response = await fetch(`${apiURL}/api/top/facets?pid=${server.pid}&all=true`);
    expect(response.status).toBe(200);
    expect(response.headers.get("content-type")).toBe("application/json");
    const facets = await response.json() as Facets;
    expect(facets.pid).toBe(server.pid);
    expect(facets.name).toBe("telltale");
    expect(facets.descriptors.ports.map(port => port.port)).toContain(Number(new URL(apiURL).port));
    expect(facets.descriptors.openFiles.length).toBeGreaterThan(0);
    expect(Object.keys(facets.environment ?? {})).toContain("PATH");
  });

  it("answers 404 for a process the latest frame did not list", async () => {
    const response = await fetch(`${apiURL}/api/top/facets?pid=4194304`);
    expect(response.status).toBe(404);
    expect(await response.text()).toContain("not listed at the latest tick");
  });

  it("refuses a request without a pid", async () => {
    const response = await fetch(`${apiURL}/api/top/facets`);
    expect(response.status).toBe(400);
  });
});

describe("client routes", () => {
  it("serves the app page at a program's /top route, its name dotted", async () => {
    const response = await fetch(`${apiURL}/top/com.apple.WebKit.Networking`);
    expect(response.status).toBe(200);
    expect(response.headers.get("content-type")).toContain("text/html");
    expect(await response.text()).toContain('<div id="root">');
  });
});
