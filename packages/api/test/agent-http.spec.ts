import { execFile, spawn } from "node:child_process";
import { generateKeyPairSync, randomBytes, sign } from "node:crypto";
import { once } from "node:events";
import { copyFile, mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { promisify } from "node:util";
import { expect, it } from "vitest";

const run = promisify(execFile);

it("keeps the JVM identity while upgrading the runtime behind its HTTP API", async () => {
  const dir = await mkdtemp(join(process.cwd(), ".tmp", "agent-http-"));
  const { privateKey, publicKey } = generateKeyPairSync("rsa", { modulusLength: 2048 });
  const issuer = "http-fixture";
  const audience = "http-fixture-jvm";
  const kid = "fixture-key";
  const encode = (value: unknown) => Buffer.from(JSON.stringify(value)).toString("base64url");
  const unsigned = `${encode({ alg: "RS256", typ: "JWT", kid })}.${encode({ iss: issuer, aud: audience, sub: "fixture", scope: "jvm:read", exp: Math.floor(Date.now() / 1000) + 60 })}`;
  const token = `${unsigned}.${sign("RSA-SHA256", Buffer.from(unsigned), privateKey).toString("base64url")}`;
  await run("javac", ["-d", dir, "pkg/agentinstall/testdata/Workload.java"]);
  const config = join(dir, "agent.json");
  await writeFile(config, JSON.stringify({
    token: randomBytes(32).toString("hex"), service: "http-fixture", allowMutation: false,
    resource: { "deployment.environment.name": "integration" },
    http: { bind: "127.0.0.1", port: 0, portFile: join(dir, "port"), issuer, audience, jwks: { keys: [{ ...publicKey.export({ format: "jwk" }), kid, alg: "RS256", use: "sig" }] } },
  }));
  const child = spawn("java", [`-javaagent:${join(process.cwd(), "pkg/agentinstall/java/assets/telltale-agent.jar")}=${config}`, "-cp", dir, "Workload"]);
  let output = "";
  child.stderr.on("data", (chunk: Buffer) => { output += chunk.toString(); });
  try {
    await expect.poll(async () => (await readdir(dir)).includes("port"), { timeout: 10000, message: output }).toBe(true);
    const base = `http://127.0.0.1:${await readFile(join(dir, "port"), "utf8")}`;
    const headers = { "X-Telltale-Authorization": `Bearer ${token}` };
    const response = await fetch(`${base}/hello`, { headers });
    expect(response.status, await response.clone().text()).toBe(200);
    const hello = await response.json() as { resource: Record<string, string> };
    expect(hello.resource).toEqual({
      "service.name": "http-fixture", "process.pid": String(child.pid),
      "deployment.environment.name": "integration", "telltale.source.instance": expect.any(String),
    });
    expect(hello.resource["telltale.source.instance"]).toMatch(/^[0-9a-f-]{36}$/);
    const again = await fetch(`${base}/hello`, { headers });
    expect(again.status).toBe(200);
    expect(await again.json()).toMatchObject({ resource: hello.resource });
    const upgraded = join(dir, "upgraded.jar");
    const manifest = join(dir, "upgrade.mf");
    await copyFile("pkg/agentinstall/java/assets/telltale-agent.jar", upgraded);
    await writeFile(manifest, "Implementation-Version: api-upgrade-fixture\n\n");
    await run("jar", ["--update", "--file", upgraded, "--manifest", manifest]);
    await run("java", ["-cp", upgraded, "com.flanksource.telltale.Attach", String(child.pid), upgraded, config, "true"]);
    const replacedBase = `http://127.0.0.1:${await readFile(join(dir, "port"), "utf8")}`;
    const health = await fetch(`${replacedBase}/health`);
    expect(health.status).toBe(200);
    expect(await health.json()).toMatchObject({ status: "ok", runtimeVersion: "api-upgrade-fixture", upgradeApiVersion: 1 });
    const replaced = await fetch(`${replacedBase}/hello`, { headers });
    expect(replaced.status).toBe(200);
    expect(await replaced.json()).toMatchObject({ sdkVersion: "api-upgrade-fixture", resource: hello.resource });
  } finally {
    if (child.exitCode === null) { const stopped = once(child, "exit"); child.kill("SIGTERM"); await stopped; }
    await rm(dir, { recursive: true, force: true });
  }
}, 30000);
