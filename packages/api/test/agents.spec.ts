import { execFile, spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { randomBytes } from "node:crypto";
import { once } from "node:events";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { connect } from "node:net";
import { join } from "node:path";
import { promisify } from "node:util";
import { afterAll, beforeAll, describe, expect, it } from "vitest";

const run = promisify(execFile);
let testDir: string;
let server: ChildProcessWithoutNullStreams;
let apiURL: string;
let token: string;
// env keeps the tokens the tests mint out of the user's config dir.
const env = () => ({ ...process.env, XDG_CONFIG_HOME: join(testDir, "config") });

beforeAll(async () => {
  testDir = await mkdtemp(join(process.cwd(), ".tmp", "agents-"));
  token = (await run(".bin/telltale", ["agents", "token", "create", "api-test"], { env: env() })).stdout.trim();
  server = spawn(".bin/telltale", ["serve", "--port", "0", "--data-dir", testDir], { env: env() });
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
  if (testDir) await rm(testDir, { recursive: true, force: true });
});

describe("agents API", () => {
  it("mirrors saved deployed inventory between the API and CLI without connecting targets", async () => {
    const directory = join(testDir, "config", "telltale", "deployers");
    const file = join(directory, "fixture-orders.yaml");
    const target = "deployer/fixture-orders/fixture-ns/orders-0";
    const endpoint = "http://127.0.0.1:1";
    await mkdir(directory, { recursive: true });
    await writeFile(file, JSON.stringify({
      apiVersion: "telltale.flanksource.com/v1alpha1", kind: "Deployer", metadata: { name: "fixture-orders" },
      spec: {
        selector: { kind: "Pod", namespace: "fixture-ns", name: "orders-0" },
        javaContainer: { kind: "attach", url: endpoint, directory: "/tmp/telltale", bind: "127.0.0.1", port: 7070, service: "orders" },
        auth: { issuer: "connection://example/issuer" },
      },
      status: { targets: [{ target, namespace: "fixture-ns", pod: "orders-0", container: "app", state: "deployed", endpoint }] },
    }));
    try {
      const response = await fetch(`${apiURL}/api/agents`);
      expect(response.status).toBe(200);
      const inventory = await response.json() as Array<Record<string, unknown>>;
      const agent = inventory.find(entry => entry.agentId === target);
      expect(agent).toMatchObject({
        agentId: target, connected: false, group: "namespace/fixture-ns",
        resource: { "k8s.namespace.name": "fixture-ns", "k8s.pod.name": "orders-0", "k8s.container.name": "app" },
        known: { agentId: target, runtime: "java", state: "deployed", endpoint },
      });
      expect(agent).not.toHaveProperty("runtime");
      expect(agent).not.toHaveProperty("started");
      expect(agent).not.toHaveProperty("probes");
      const cli = await run(".bin/telltale", ["agents", "list", "--server", apiURL, "--format", "json"], { env: env() });
      const groups = JSON.parse(cli.stdout) as Record<string, unknown[]>;
      expect(groups["namespace/fixture-ns"]).toContainEqual(agent);
    } finally {
      await rm(file);
    }
  });

  it("serves the live JVM catalog, preserves script integers and hot-loads an extension", async () => {
    await run("javac", ["-d", testDir, "pkg/agentinstall/testdata/Workload.java"]);
    const config = join(testDir, "java-agent.json");
    await writeFile(config, JSON.stringify({ url: apiURL, token, service: "api-jvm", allowMutation: true, extensionsDir: join(testDir, "extensions") }));
    const child = spawn("java", [`-javaagent:${join(process.cwd(), "pkg/agentinstall/java/assets/telltale-agent.jar")}=${config}`, "-cp", testDir, "Workload"]);
    let output = "";
    child.stderr.on("data", (chunk: Buffer) => { output += chunk.toString(); });
    try {
      let id: string | undefined;
      for (let attempt = 0; attempt < 100; attempt++) {
        const agents = await (await fetch(`${apiURL}/api/agents`)).json() as Array<{ agentId: string; service: string }>;
        id = agents.find(agent => agent.service === "api-jvm")?.agentId;
        if (id || child.exitCode !== null) break;
        await new Promise(resolve => setTimeout(resolve, 100));
      }
      expect(id, output).toBeDefined();
      const snapshot = await fetch(`${apiURL}/api/v1/jvm/snapshot`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ agent: id, jfr: "1s", heapHistogram: true }) });
      expect(snapshot.status).toBe(200);
      expect(snapshot.headers.get("content-type")).toBe("application/zip");
      expect(snapshot.headers.get("x-telltale-snapshot-status")).toBe("complete");
      const bundle = Buffer.from(await snapshot.arrayBuffer());
      expect(bundle.subarray(0, 2).toString()).toBe("PK");
      expect(bundle.includes(Buffer.from("manifest.json"))).toBe(true);
      expect(bundle.includes(Buffer.from("recording.jfr"))).toBe(true);
      const base = `${apiURL}/api/agents/${encodeURIComponent(id!)}`;
      const catalog = await (await fetch(`${base}/catalog`)).json() as Array<{ name: string; paramsSchema: unknown }>;
      expect(catalog).toContainEqual(expect.objectContaining({ name: "jvm.groovy.eval", paramsSchema: expect.objectContaining({ required: ["source"] }) }));
      const execute = (body: string) => fetch(`${base}/call`, { method: "POST", headers: { "Content-Type": "application/json" }, body });
      const denied = await execute('{"operation":"jvm.groovy.eval","params":{"source":"return 7"}}');
      expect(denied.status).toBe(400);
      const integer = await execute('{"operation":"jvm.groovy.eval","allowMutation":true,"params":{"source":"return args.id","args":{"id":9007199254740993}}}');
      expect(integer.status).toBe(200);
      expect(await integer.text()).toContain('"value":9007199254740993');
      const invoked = await execute('{"operation":"jvm.jmx.invoke","allowMutation":true,"params":{"objectName":"java.lang:type=Memory","operation":"gc","args":[],"signature":[]}}');
      expect(invoked.status).toBe(200);
      expect(await invoked.text()).toBe("null");
      const jar = (await readFile("sdk/java/build/libs/telltale-example-extension.jar")).toString("base64");
      expect((await execute(JSON.stringify({ operation: "jvm.extensions.install", allowMutation: true, params: { jar } }))).status).toBe(200);
      expect(await (await fetch(`${base}/catalog`)).json()).toContainEqual(expect.objectContaining({ name: "extension.example.greet" }));
      const greet = await execute(JSON.stringify({ operation: "extension.example.greet", params: { name: "operator" } }));
      expect(await greet.json()).toEqual({ message: "Hello operator" });
      expect((await execute(JSON.stringify({ operation: "jvm.extensions.replace", allowMutation: true, params: { jar } }))).status).toBe(200);
      expect((await execute('{"operation":"jvm.extensions.unload","allowMutation":true,"params":{"id":"example"}}')).status).toBe(200);
      expect(await (await fetch(`${base}/catalog`)).json()).not.toContainEqual(expect.objectContaining({ name: "extension.example.greet" }));
    } finally {
      if (child.exitCode === null) { const stopped = once(child, "exit"); child.kill("SIGTERM"); await stopped; }
    }
  }, 30_000);
  it("rejects malformed diagnostic calls before selecting a JVM", async () => {
    for (const body of ['{}', 'null', '{"agent":"missing","output":"/some/path"}', '{"agent":"missing","jfr":"-1s"}']) {
      const response = await fetch(`${apiURL}/api/v1/jvm/snapshot`, { method: "POST", headers: { "Content-Type": "application/json" }, body });
      expect(response.status).toBe(400);
      expect(response.headers.get("content-type")).not.toBe("application/zip");
    }
    for (const body of ['{}', 'null', '{"params":{}}', '{"operation":"jvm.catalog"} {}', '{"operation":"jvm.catalog","unexpected":true}']) {
      const response = await fetch(`${apiURL}/api/agents/missing/call`, {
        method: "POST", headers: { "Content-Type": "application/json" }, body,
      });
      expect(response.status).toBe(400);
      expect(await response.text()).not.toContain("is not connected");
    }
  });

  it("requires API scope on non-loopback server routes", async () => {
    const apiToken = (await run(".bin/telltale", ["agents", "token", "create", "api-test", "--scope", "api"], { env: env() })).stdout.trim();
    const publicServer = spawn(".bin/telltale", ["serve", "--host", "0.0.0.0", "--port", "0", "--data-dir", testDir], { env: env() });
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
      expect((await fetch(`${address}/api/v1/jvm/snapshot`, { method: "POST", headers: { "Content-Type": "application/json" }, body: '{"agent":"missing"}' })).status).toBe(401);
      expect((await fetch(`${address}/api/v1/jvm/snapshot`, { method: "POST", headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` }, body: '{"agent":"missing"}' })).status).toBe(403);
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
      env: { ...process.env, TELLTALE_URL: apiURL, TELLTALE_TOKEN: token, POD_NAME: "api-pod", AGENT_CONFIG_DIR: testDir, AGENT_LISTEN: "tcp" },
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
      expect(result).toMatchObject({ captures: [expect.objectContaining({ kind: "http", target: { seam: "agent", selector: { "k8s.pod.name": "api-pod" } } })] });
    } finally {
      child.stdin.end();
      if (child.exitCode === null) await once(child, "exit");
    }
  }, 45_000);
});
