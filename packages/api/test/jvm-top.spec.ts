import { execFile, spawn, type ChildProcess } from "node:child_process";
import { once } from "node:events";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { promisify } from "node:util";
import { expect, it } from "vitest";

const run = promisify(execFile);

it("reads live JVM Top through RPC and remote CLI and inspects a sampled thread", async () => {
  const dir = await mkdtemp(join(process.cwd(), ".tmp", "jvm-top-"));
  const env = { ...process.env, XDG_CONFIG_HOME: join(dir, "config") };
  const children: ChildProcess[] = [];
  try {
    const token = (await run(".bin/telltale", ["agents", "token", "create", "top-test"], { env })).stdout.trim();
    const apiToken = (await run(".bin/telltale", ["agents", "token", "create", "top-api-test", "--scope", "api"], { env })).stdout.trim();
    const headers = { Authorization: `Bearer ${apiToken}`, "Content-Type": "application/json" };
    const server = spawn(".bin/telltale", ["serve", "--host", "0.0.0.0", "--port", "0", "--data-dir", dir], { env });
    children.push(server);
    const address = await new Promise<string>((resolve, reject) => {
      let output = "";
      server.stdout.on("data", (data: Buffer) => { output += data.toString(); const match = /Telltale API: (http:\/\/\S+)/.exec(output); if (match) resolve(match[1]!); });
      server.once("error", reject);
      server.once("exit", code => reject(new Error(`Server exited ${code}: ${output}`)));
    });
    await run("javac", ["-d", dir, "pkg/agentinstall/testdata/Workload.java"]);
    const config = join(dir, "agent.json");
    await writeFile(config, JSON.stringify({ url: address, token, service: "jvm-top-test", allowMutation: false }));
    const java = spawn("java", [`-javaagent:${join(process.cwd(), "pkg/agentinstall/java/assets/telltale-agent.jar")}=${config}`, "-cp", dir, "Workload"]);
    children.push(java);
    let id: string | undefined;
    for (let attempt = 0; attempt < 100; attempt++) {
      const agents = await (await fetch(`${address}/api/agents`, { headers })).json() as Array<{ agentId: string; service: string }>;
      id = agents.find(agent => agent.service === "jvm-top-test")?.agentId;
      if (id) break;
      await new Promise(resolve => setTimeout(resolve, 100));
    }
    expect(id).toBeDefined();
    expect((await fetch(`${address}/api/v1/jvm/top`, { method: "POST", body: "{}" })).status).toBe(401);
    expect((await fetch(`${address}/api/v1/jvm/top`, { method: "POST", headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" }, body: "{}" })).status).toBe(403);
    const response = await fetch(`${address}/api/v1/jvm/top`, { method: "POST", headers, body: JSON.stringify({ agent: id, format: "json" }) });
    expect(response.status).toBe(200);
    const envelope = await response.json() as { success: boolean; output: string };
    expect(envelope.success).toBe(true);
    const frame = JSON.parse(envelope.output) as { agentId: string; metrics: { memory: unknown }; threads: { threads: Array<{ id: number }> } };
    expect(frame.agentId).toBe(id);
    expect(frame.metrics.memory).toBeDefined();
    expect(frame.threads.threads.length).toBeGreaterThan(0);
    const streamURL = `${address}/api/v1/jvm/top?agent=${encodeURIComponent(id!)}`;
    expect((await fetch(streamURL)).status).toBe(401);
    const controller = new AbortController();
    const stream = await fetch(streamURL, { headers, signal: controller.signal });
    expect(stream.headers.get("content-type")).toBe("text/event-stream");
    const reader = stream.body!.pipeThrough(new TextDecoderStream()).getReader();
    try {
      let payload = "";
      for (;;) {
        const { value, done } = await reader.read();
        if (done) throw new Error("JVM Top stream ended before delivering a sample");
        payload += value;
        const data = /^data: (.*)\n\n/m.exec(payload);
        if (!data) continue;
        expect(JSON.parse(data[1])).toMatchObject({ agentId: id, threads: { threads: expect.any(Array) } });
        break;
      }
    } finally {
      await reader.cancel();
      controller.abort();
    }
    const cli = await run(".bin/telltale", ["jvm", "top", "--server", address, "--agent", id!, "--once", "--format", "json"], { env: { ...env, TELLTALE_API_TOKEN: apiToken } });
    expect(JSON.parse(cli.stdout)).toMatchObject({ agentId: id, threads: { cpuTimeEnabled: expect.any(Boolean) } });
    const stack = await fetch(`${address}/api/agents/${encodeURIComponent(id!)}/call`, { method: "POST", headers, body: JSON.stringify({ operation: "jvm.threads.stack", params: { id: frame.threads.threads[0]!.id } }) });
    expect(stack.status).toBe(200);
    expect(await stack.json()).toMatchObject({ text: expect.any(String), frames: expect.any(Array) });
    const spec = await (await fetch(`${address}/api/openapi.json`, { headers })).json() as { components: { schemas: Record<string, unknown> } };
    expect(spec.components.schemas.JVMTopFrame).toBeDefined();
  } finally {
    for (const child of children.reverse()) { if (child.exitCode === null) { const stopped = once(child, "exit"); child.kill("SIGTERM"); await stopped; } }
    await rm(dir, { recursive: true, force: true });
  }
}, 60000);
