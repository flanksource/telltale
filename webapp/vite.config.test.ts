import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { afterAll, afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import viteConfig, { createViteConfig } from "./vite.config";
import { createServer } from "vite";

mkdirSync(".tmp", { recursive: true });
const uiDirectory = mkdtempSync(resolve(".tmp/clicky-ui-"));
const options = { localUI: false, uiDirectory, apiURL: "http://localhost:8080" };

beforeAll(() => {
  const source = resolve(uiDirectory, "packages/ui/src");
  mkdirSync(`${source}/styles`, { recursive: true });
  for (const file of ["index.ts", "components.ts", "tailwind-preset.ts", "styles/full.css", "styles/mdx-editor.css"]) {
    writeFileSync(resolve(source, file), "");
  }
  writeFileSync(resolve(source, "../package.json"), JSON.stringify({ exports: {
    ".": { import: "./dist/index.js" },
    "./components": { import: "./dist/components.js" },
    "./tailwind-preset": { import: "./dist/tailwind-preset.js" },
    "./styles.css": "./dist/styles.css",
    "./mdx-editor.css": "./dist/mdx-editor.css",
  } }));
});
afterAll(() => rmSync(uiDirectory, { recursive: true }));
afterEach(() => vi.unstubAllEnvs());

describe("Clicky UI resolution", () => {
  it("watches the external source tree for live component changes", async () => {
    const config = createViteConfig({ ...options, localUI: true });
    const server = await createServer({ ...config, root: resolve("webapp"), configFile: false, server: { ...config.server, middlewareMode: true, ws: false } });
    try {
      const source = resolve(uiDirectory, "packages/ui/src");
      await expect.poll(() => Object.keys(server.watcher.getWatched()), { timeout: 3000 }).toContain(source);
      const changed: string[] = [];
      server.watcher.on("change", (path) => changed.push(path));
      writeFileSync(`${source}/components.ts`, "export const revision = 'updated';\n");
      await expect.poll(() => changed, { timeout: 3000 }).toContain(`${source}/components.ts`);
    } finally {
      await server.close();
    }
  });

  it("ignores local development environment variables during production builds", async () => {
    vi.stubEnv("TELLTALE_DEV", "1");
    vi.stubEnv("TELLTALE_CLICKY_UI_DIR", `${uiDirectory}/missing`);
    const config = await viteConfig({ command: "build", mode: "production" });
    expect(config.resolve?.alias).toEqual([]);
  });

  it("keeps production builds on installed dependencies", () => {
    const config = createViteConfig(options);
    expect(config.resolve?.alias).toEqual([]);
  });

  it("uses exact aliases for local source, CSS, and theme entries", () => {
    const config = createViteConfig({ ...options, localUI: true });
    const aliases = config.resolve?.alias;
    expect(Array.isArray(aliases)).toBe(true);
    if (!Array.isArray(aliases)) throw new Error("Expected alias array");
    const components = aliases.find((alias) => new RegExp(alias.find).test("@flanksource/clicky-ui/components"));
    expect(components?.replacement).toBe(`${uiDirectory}/packages/ui/src/components.ts`);
    expect(aliases.find((alias) => new RegExp(alias.find).test("@flanksource/clicky-ui/mdx-editor.css"))?.replacement).toBe(`${uiDirectory}/packages/ui/src/styles/mdx-editor.css`);
    expect(aliases.some((alias) => new RegExp(alias.find).test("@flanksource/clicky-ui-extra"))).toBe(false);
    expect(config.resolve?.dedupe).toEqual(expect.arrayContaining(["react", "react-dom", "@tanstack/react-query"]));
    expect(config.server?.fs?.allow).toContain(`${uiDirectory}/packages/ui`);
  });

  it("fails when local source is explicitly requested but missing", () => {
    expect(() => createViteConfig({ ...options, localUI: true, uiDirectory: `${uiDirectory}/missing` })).toThrow("TELLTALE_CLICKY_UI_DIR");
  });
});
