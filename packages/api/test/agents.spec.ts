import { execFile, spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { randomBytes } from "node:crypto";
import { once } from "node:events";
import { mkdtemp, rm } from "node:fs/promises";
import { connect } from "node:net";
import { join } from "node:path";
import { promisify } from "node:util";
import { afterAll, beforeAll, describe, expect, it } from "vitest";

const run = promisify(execFile);
let workDir: string;
let server: ChildProcessWithoutNullStreams;
let apiURL: string;
let token: string;

beforeAll(async () => {
  workDir = await mkdtemp(join(process.cwd(), ".tmp", "agents-"));
  token = (await run(".bin/telltale", ["agents", "token", "create", "api-test", "--work-dir", workDir])).stdout.trim();
  server = spawn(".bin/telltale", ["serve", "--port", "0", "--work-dir", workDir]);
  apiURL = await new Promise<string>((resolve, reject) => {
    let output = "";
    server.stdout.on("data", (chunk: Buffer) => {
      output += chunk.toString();
      const match = /Telltale API: (http:\/\/\S+)/.exec(output);
      if (match) resolve(match[1]);
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
  if (workDir) await rm(workDir, { recursive: true, force: true });
});

describe("agents API", () => {
  it("requires API scope on non-loopback server routes", async () => {
    const apiToken = (await run(".bin/telltale", ["agents", "token", "create", "api-test", "--scope", "api", "--work-dir", workDir])).stdout.trim();
    const publicServer = spawn(".bin/telltale", ["serve", "--host", "0.0.0.0", "--port", "0", "--work-dir", workDir]);
    try {
      const address = await new Promise<string>((resolve, reject) => {
        let output = "";
        publicServer.stdout.on("data", (chunk: Buffer) => {
          output += chunk.toString();
          const match = /Telltale API: http:\/\/\S+:(\d+)/.exec(output);
          if (match) resolve(`http://127.0.0.1:${match[1]}`);
        });
        publicServer.stderr.on("data", (chunk: Buffer) => { output += chunk.toString(); });
        publicServer.once("error", reject);
        publicServer.once("exit", code => reject(new Error(`Server exited (${code}): ${output}`)));
      });
      const unauthenticated = await fetch(`${address}/api/traces`);
      expect(unauthenticated.status).toBe(401);
      expect(await unauthenticated.json()).toEqual({ error: "unauthorized" });
      expect((await fetch(`${address}/api/traces`, { headers: { Authorization: `Bearer ${token}` } })).status).toBe(403);
      expect((await fetch(`${address}/api/traces`, { headers: { Authorization: `Bearer ${apiToken}` } })).status).toBe(200);
    } finally {
      if (publicServer.exitCode === null) {
        const stopped = once(publicServer, "exit");
        publicServer.kill("SIGTERM");
        await stopped;
      }
    }
  }, 20_000);

  it("lists agents and rejects an invalid hub credential", async () => {
    const list = await fetch(`${apiURL}/api/agents`);
    expect(list.status).toBe(200);
    expect(Array.isArray(await list.json())).toBe(true);
    const denied = await fetch(`${apiURL}/api/agents/connect`, { headers: { Authorization: "Bearer invalid" } });
    expect(denied.status).toBe(401);
    expect(await denied.json()).toEqual({ error: "unauthorized" });
  });

  it("upgrades a connection authenticated by a minted token", async () => {
    const url = new URL(apiURL);
    const socket = connect(Number(url.port), url.hostname);
    const response = new Promise<string>((resolve, reject) => {
      socket.once("data", data => { resolve(data.toString()); socket.destroy(); });
      socket.once("error", reject);
    });
    await once(socket, "connect");
    socket.write(`GET /api/agents/connect HTTP/1.1\r\nHost: ${url.host}\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: ${randomBytes(16).toString("base64")}\r\nAuthorization: Bearer ${token}\r\n\r\n`);
    expect(await response).toMatch(/^HTTP\/1\.1 101 /);
  });

  it("arms a connected agent selected by its pod resource", async () => {
    const child = spawn("go", ["run", "./pkg/tracing/seam/testdata/hubchild"], {
      env: { ...process.env, TELLTALE_URL: apiURL, TELLTALE_TOKEN: token, POD_NAME: "api-pod", AGENT_CONFIG_DIR: workDir, AGENT_LISTEN: "tcp" },
    });
    let childError = "";
    child.stderr.on("data", (chunk: Buffer) => { childError += chunk.toString(); });
    try {
      let connected = false;
      for (let attempt = 0; attempt < 300; attempt++) {
        const agents = await (await fetch(`${apiURL}/api/agents`)).json() as Array<{ resource: Record<string, string> }>;
        if (agents.some(agent => agent.resource["k8s.pod.name"] === "api-pod")) { connected = true; break; }
        if (child.exitCode !== null) break;
        await new Promise(resolve => setTimeout(resolve, 100));
      }
      expect(connected, childError).toBe(true);
      const response = await fetch(`${apiURL}/api/traces`, {
        method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ command: "sleep 1", traces: { http: { enabled: true, features: ["http"], target: { seam: "agent", selector: { "k8s.pod.name": "api-pod" } } } } }),
      });
      const result = await response.json() as unknown;
      expect(response.status, JSON.stringify(result)).toBe(201);
      expect(result).toMatchObject({ captures: [expect.objectContaining({ kind: "http" })] });
    } finally {
      child.stdin.end();
      if (child.exitCode === null) await once(child, "exit");
    }
  }, 45_000);
});
