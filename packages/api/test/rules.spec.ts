import { execFileSync, spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { once } from "node:events";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { afterAll, beforeAll, describe, expect, it } from "vitest";

let directory: string;
let server: ChildProcessWithoutNullStreams;
let child: ChildProcessWithoutNullStreams;
let apiURL: string;

function startServer(options: { directory: string; file: string; enforce?: boolean; signal?: AbortSignal }): { process: ChildProcessWithoutNullStreams; ready: Promise<string> } {
  const process = spawn(".bin/telltale", ["serve", "--port", "0", "--work-dir", options.directory, "--rules", options.file, ...(options.enforce ? ["--enforce-rules"] : [])], options.signal ? { signal: options.signal } : {});
  const ready = new Promise<string>((resolveServer, reject) => {
    let output = "";
    process.stdout.on("data", (chunk: Buffer) => {
      output += chunk.toString();
      const match = /Telltale API: (http:\/\/\S+)/.exec(output);
      if (match) resolveServer(match[1]);
    });
    process.stderr.on("data", (chunk: Buffer) => { output += chunk.toString(); });
    process.once("error", reject);
    process.once("exit", code => reject(new Error(`Server exited (${code}): ${output}`)));
  });
  return { process, ready };
}

async function stopProcess(process: ChildProcessWithoutNullStreams | undefined): Promise<void> {
  if (process?.exitCode === null && process.signalCode === null) {
    const stopped = once(process, "exit");
    process.kill("SIGTERM");
    await stopped;
  }
}

beforeAll(async () => {
  await mkdir(".tmp", { recursive: true });
  directory = await mkdtemp(resolve(".tmp", "rules-api-"));
  child = spawn("sleep", ["60"]);
  await once(child, "spawn");
  const config = `version: 1
rules:
  - name: disposable-child
    groupBy: tree
    match: group.members.exists(p, p.pid == ${child.pid}) && rss > 0
    for: 0s
    recoverFor: 0s
    steps:
      - wait: 0s
        hook:
          type: signal
          targets: group.members.filter(p, p.pid == ${child.pid})
          signal: SIGTERM
`;
  const file = join(directory, "rules.yaml");
  await writeFile(file, config);
  const started = startServer({ directory, file });
  server = started.process;
  apiURL = await started.ready;
});

afterAll(async () => {
  if (server?.exitCode === null) {
    const stopped = once(server, "exit");
    server.kill("SIGTERM");
    expect(await stopped).toEqual([0, null]);
  }
  if (child?.exitCode === null && child.signalCode === null) {
    const stopped = once(child, "exit");
    child.kill("SIGTERM");
    await stopped;
  }
  if (directory) await rm(directory, { recursive: true, force: true });
});

describe("resource rule HTTP contracts", () => {
  it("observes continuously without executing configured process hooks", async () => {
    await new Promise(resolveWait => setTimeout(resolveWait, 4500));
    const response = await fetch(`${apiURL}/api/rules/status`);
    expect(response.status).toBe(200);
    const status = await response.json() as { enabled: boolean; enforcing: boolean; sampledAt: string; error?: string };
    expect(status.enabled).toBe(true);
    expect(status.enforcing).toBe(false);
    expect(status.error).toBeUndefined();
    expect(Date.now() - Date.parse(status.sampledAt)).toBeLessThan(5000);
    expect(child.exitCode).toBeNull();
    expect(child.signalCode).toBeNull();
  });

  it("publishes config, CEL bindings, schema, preview and durable observation events", async () => {
    const configResponse = await fetch(`${apiURL}/api/rules`);
    expect(configResponse.status).toBe(200);
    const config = await configResponse.json() as { bindings: Record<string, string>; schema: { properties: object } };
    expect(config.bindings.cpu).toContain("host.cores");
    expect(config.schema.properties).toHaveProperty("rules");
    expect(config.schema.properties).toHaveProperty("rules.items.properties.groupBy.enum", expect.arrayContaining(["host"]));
    const previewResponse = await fetch(`${apiURL}/api/rules/preview`);
    expect(previewResponse.status).toBe(200);
    const preview = await previewResponse.json() as Array<{ matched: boolean; steps: Array<{ targets: { processes: Array<{ pid: number; groupName: string; groupRoot: boolean; groupPath: string[] }> } }> }>;
    const matched = preview.filter(item => item.matched);
    expect(matched.length).toBeGreaterThan(0);
    expect(matched.every(item => item.steps[0].targets.processes.every(process => process.pid === child.pid))).toBe(true);
    expect(matched[0].steps[0].targets.processes[0]).toEqual(expect.objectContaining({ groupName: expect.any(String), groupRoot: expect.any(Boolean), groupPath: expect.arrayContaining([expect.any(String)]) }));
    const eventsResponse = await fetch(`${apiURL}/api/rules/events?rule=disposable-child`);
    expect(eventsResponse.status).toBe(200);
    const events = await eventsResponse.json() as Array<{ kind: string; result?: { status: string } }>;
    expect(events.some(event => event.kind === "intent")).toBe(true);
    expect(events.some(event => event.result?.status === "observed")).toBe(true);
    const specResponse = await fetch(`${apiURL}/api/openapi.json`);
    const spec = await specResponse.json() as { paths: Record<string, object> };
    for (const path of ["/api/rules", "/api/rules/preview", "/api/rules/status", "/api/rules/events"]) expect(spec.paths).toHaveProperty(path);
  });

  it("rejects mutations and invalid history filters", async () => {
    expect((await fetch(`${apiURL}/api/rules`, { method: "POST" })).status).toBe(405);
    expect((await fetch(`${apiURL}/api/rules/events?limit=0`)).status).toBe(400);
  });

  it("releases a disposable server when startup is aborted", async ({ onTestFinished }) => {
    const controller = new AbortController();
    const started = startServer({ directory, file: join(directory, "rules.yaml"), signal: controller.signal });
    onTestFinished(() => stopProcess(started.process));
    const refused = expect(started.ready).rejects.toThrow(/abort/i);
    controller.abort();
    await refused;
    await stopProcess(started.process);
    expect(started.process.signalCode).toBe("SIGTERM");
  });

  it("renices and then terminates the exact disposable target while another process survives", async ({ signal, onTestFinished }) => {
    const work = await mkdtemp(join(directory, "enforce-"));
    const worker = spawn("sleep", ["60"]);
    await once(worker, "spawn");
    const exited = once(worker, "exit");
    let enforcer: ChildProcessWithoutNullStreams | undefined;
    onTestFinished(async () => { await stopProcess(enforcer); await stopProcess(worker); });
    try {
      const file = join(work, "rules.yaml");
      await writeFile(file, `version: 1
rules:
  - name: exact-child
    groupBy: tree
    match: group.members.exists(p, p.pid == ${worker.pid}) && rss > 0
    for: 0s
    recoverFor: 0s
    steps:
      - wait: 0s
        hook: {type: renice, targets: 'group.members.filter(p, p.pid == ${worker.pid})', nice: 10}
      - wait: 2s
        hook: {type: signal, targets: 'group.members.filter(p, p.pid == ${worker.pid})', signal: SIGTERM}
`);
      const started = startServer({ directory: work, file, enforce: true, signal });
      enforcer = started.process;
      const url = await started.ready;
      await expect.poll(() => execFileSync("ps", ["-o", "ni=", "-p", String(worker.pid)], { encoding: "utf8" }).trim(), { timeout: 10000, interval: 50 }).toBe("10");
      await expect.poll(() => worker.signalCode, { timeout: 10000 }).toBe("SIGTERM");
      expect(await exited).toEqual([null, "SIGTERM"]);
      expect(child.signalCode).toBeNull();
      const eventsResponse = await fetch(`${url}/api/rules/events?rule=exact-child`);
      const events = await eventsResponse.json() as Array<{ result?: { status: string } }>;
      expect(events.some(event => event.result?.status === "succeeded")).toBe(true);
    } finally {
      await stopProcess(enforcer);
      await stopProcess(worker);
      await exited;
    }
  }, 30_000);
});
