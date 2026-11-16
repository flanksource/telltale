import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { mkdir, mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { connect } from "node:net";
import { join, resolve } from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

interface Exit { code: number | null; signal: NodeJS.Signals | null }
interface Command {
  child: ChildProcessWithoutNullStreams;
  exited: Promise<Exit>;
  output: () => string;
}
interface Run { runId: string; status: string }

let directory: string;
let commands: Command[];

function command(executable: string, args: string[]): Command {
  const child = spawn(executable, args, {
    env: { ...process.env, XDG_CONFIG_HOME: join(directory, "config"), XDG_CACHE_HOME: join(directory, "cache"), PATH: `${join(directory, "bin")}:${process.env.PATH}` },
  });
  let output = "";
  child.stdout.on("data", (chunk: Buffer) => { output += chunk.toString(); });
  child.stderr.on("data", (chunk: Buffer) => { output += chunk.toString(); });
  const exited = new Promise<Exit>((resolveExit, reject) => {
    child.once("error", reject);
    child.once("exit", (code, signal) => resolveExit({ code, signal }));
  });
  const running = { child, exited, output: () => output };
  commands.push(running);
  return running;
}

async function ready(running: Command): Promise<string> {
  await expect.poll(() => {
    if (running.child.exitCode !== null) throw new Error(running.output());
    return /http:\/\/(?:127\.0\.0\.1|localhost|\[::1\]):\d+/.exec(running.output())?.[0];
  }, { timeout: 10000 }).toBeDefined();
  return /http:\/\/(?:127\.0\.0\.1|localhost|\[::1\]):\d+/.exec(running.output())![0];
}

async function runs(url: string): Promise<Run[]> {
  const response = await fetch(`${url}/api/traces`, { signal: AbortSignal.timeout(5000) });
  expect(response.status).toBe(200);
  return ((await response.json()) as { runs: Run[] }).runs;
}

beforeEach(async () => {
  await mkdir(".tmp", { recursive: true });
  directory = await mkdtemp(resolve(".tmp/shutdown-api-"));
  commands = [];
  await mkdir(join(directory, "bin"));
  for (const opener of ["open", "xdg-open"]) {
    await writeFile(join(directory, "bin", opener), "#!/bin/sh\nexit 0\n", { mode: 0o755 });
  }
});

afterEach(async () => {
  for (const running of commands) {
    if (running.child.exitCode === null && running.child.signalCode === null) running.child.kill("SIGKILL");
    await running.exited;
  }
  await rm(directory, { recursive: true, force: true });
});

describe.sequential("command shutdown", () => {
  it("exits on one Ctrl+C after repeated live viewer polling and leaves its attached target alive", async () => {
    const target = command("sleep", ["60"]);
    expect(target.child.pid).toBeDefined();
    const running = command(".bin/telltale", ["run", "process", "--pid", String(target.child.pid), "--ui", "--data-dir", directory]);
    const url = await ready(running);
    const run = (await runs(url))[0]!;
    for (let batch = 0; batch < 10; batch++) {
      await Promise.all(Array.from({ length: 4 }, async () => {
        const response = await fetch(`${url}/api/traces/${run.runId}/processes`, { signal: AbortSignal.timeout(5000) });
        expect(response.status).toBe(200);
        await response.arrayBuffer();
      }));
    }
    running.child.kill("SIGINT");
    expect(await running.exited).toEqual({ code: 0, signal: null });
    expect(target.child.exitCode).toBeNull();
    const manifest = JSON.parse(await readFile(join(directory, "traces", run.runId, "run.json"), "utf8")) as {
      runId: string; endedAt: string; exitCode: number; error?: string; captures: { kind: string; ref: { toSeq: number } }[];
    };
    expect(manifest).toMatchObject({ runId: run.runId, exitCode: 0, endedAt: expect.any(String) });
    expect(Date.parse(manifest.endedAt)).toBeGreaterThan(0);
    expect(manifest.error).toBeUndefined();
    expect(manifest.captures[0]?.kind).toBe("process");
    expect(manifest.captures[0]?.ref.toSeq).toBeGreaterThan(0);
  });

  it("keeps the viewer open after natural completion and exits on its next Ctrl+C", async () => {
    const running = command(".bin/telltale", ["trace", "run", "--ui", "--data-dir", directory, "--", "sleep", "0.2"]);
    const url = await ready(running);
    await expect.poll(async () => (await runs(url))[0]?.status, { timeout: 10000 }).toBe("succeeded");
    expect(running.child.exitCode).toBeNull();
    running.child.kill("SIGINT");
    expect(await running.exited).toEqual({ code: 0, signal: null });
  });

  it("closes a replay viewer on its first Ctrl+C", async () => {
    const recorded = command(".bin/telltale", ["trace", "run", "--data-dir", directory, "--", "true"]);
    expect(await recorded.exited).toEqual({ code: 0, signal: null });
    const ids = await readdir(join(directory, "traces"));
    expect(ids).toHaveLength(1);
    const running = command(".bin/telltale", ["trace", "replay", ids[0]!, "--ui", "--data-dir", directory]);
    const url = await ready(running);
    expect(await runs(url)).toHaveLength(1);
    running.child.kill("SIGINT");
    expect(await running.exited).toEqual({ code: 0, signal: null });
  });

  it("stops the HTTP server gracefully on SIGTERM", async () => {
    const running = command(".bin/telltale", ["serve", "--port", "0", "--data-dir", directory]);
    await ready(running);
    running.child.kill("SIGTERM");
    expect(await running.exited).toEqual({ code: 0, signal: null });
  });

  it("forces exit on a second Ctrl+C while HTTP shutdown is waiting for a connection", async () => {
    const running = command(".bin/telltale", ["serve", "--port", "0", "--data-dir", directory]);
    const url = new URL(await ready(running));
    const socket = connect(Number(url.port), url.hostname.replace(/^\[|\]$/g, ""));
    await new Promise<void>((resolveConnect, reject) => { socket.once("connect", resolveConnect); socket.once("error", reject); });
    socket.write("GET /health HTTP/1.1\r\nHost: localhost\r\n");
    running.child.kill("SIGINT");
    await expect.poll(running.output).toContain("Shutting down");
    const before = performance.now();
    running.child.kill("SIGINT");
    expect(await running.exited).toEqual({ code: 130, signal: null });
    expect(performance.now() - before).toBeLessThan(1000);
    socket.destroy();
  });
});
