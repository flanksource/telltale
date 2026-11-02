import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { once } from "node:events";
import { mkdirSync, mkdtempSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { stripVTControlCharacters } from "node:util";
import { afterAll, beforeAll, describe, expect, it } from "vitest";

let webDirectory: string;
beforeAll(() => {
  mkdirSync(".tmp", { recursive: true });
  webDirectory = mkdtempSync(resolve(".tmp/dev-ports-"));
  writeFileSync(`${webDirectory}/package.json`, JSON.stringify({ name: "dev-port-fixture", private: true, type: "module" }));
  writeFileSync(`${webDirectory}/pnpm-workspace.yaml`, "packages: []\n");
  symlinkSync(resolve("node_modules"), `${webDirectory}/node_modules`, "dir");
  writeFileSync(`${webDirectory}/index.html`, "<html><head><title>Development fixture</title></head><body></body></html>");
  writeFileSync(`${webDirectory}/vite.config.ts`, `export default {
    cacheDir: ".vite",
    server: { proxy: { "/api": process.env.TELLTALE_API_URL, "/health": process.env.TELLTALE_API_URL } }
  };`);
});
afterAll(() => rmSync(webDirectory, { recursive: true, force: true }));

describe("development ports", () => {
  it.each([{ args: [], color: "1" }, { args: ["--ui-port", "0"], color: "0" }])("serves Vite and the Go proxy on an available port with $args", async ({ args, color }) => {
    const server: ChildProcessWithoutNullStreams = spawn(".bin/telltale", ["serve", "--dev", "--port", "0", "--web-dir", webDirectory, "--data-dir", `${webDirectory}/data`, ...args], {
      env: { ...process.env, FORCE_COLOR: color, XDG_CONFIG_HOME: `${webDirectory}/config` },
    });
    try {
      const { apiURL, uiURL } = await new Promise<{ apiURL: string; uiURL: string }>((resolve, reject) => {
        let output = "";
        const collect = (chunk: Buffer) => {
          output += stripVTControlCharacters(chunk.toString());
          const api = /Telltale API: (http:\/\/\S+)/.exec(output);
          const ui = /Local:\s+(http:\/\/\S+)/.exec(output);
          if (api && ui) resolve({ apiURL: api[1], uiURL: ui[1] });
        };
        server.stdout.on("data", collect);
        server.stderr.on("data", collect);
        server.once("error", reject);
        server.once("exit", (code) => reject(new Error(`Dev server exited (${code}): ${output}`)));
      });
      expect(Number(new URL(uiURL).port)).toBeGreaterThan(0);
      expect(new URL(uiURL).port).not.toBe("5173");
      for (const url of [apiURL, uiURL]) {
        const frontend = await fetch(new URL("/settings", url));
        expect(frontend.status).toBe(200);
        expect(await frontend.text()).toContain("/@vite/client");
        const health = await fetch(new URL("/health", url));
        expect(health.status).toBe(200);
        expect(await health.json()).toMatchObject({ status: "healthy" });
      }
    } finally {
      if (server.exitCode === null) {
        const stopped = once(server, "exit");
        server.kill("SIGTERM");
        expect(await stopped).toEqual([0, null]);
      }
    }
  }, 30000);
});
