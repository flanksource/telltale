import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { defineConfig, type UserConfig } from "vite";

export type ViteOptions = { localUI: boolean; uiDirectory: string; apiURL: string };
const appRoot = fileURLToPath(new URL(".", import.meta.url));

function clickyAliases(uiDirectory: string) {
  const packageRoot = resolve(uiDirectory, "packages/ui");
  if (!existsSync(resolve(packageRoot, "src/index.ts"))) {
    throw new Error(`Local Clicky UI source missing at ${packageRoot}; set TELLTALE_CLICKY_UI_DIR to the clicky-ui checkout`);
  }
  const manifest = JSON.parse(readFileSync(resolve(packageRoot, "package.json"), "utf8")) as { exports: Record<string, string | { import: string }> };
  return Object.entries(manifest.exports).flatMap(([key, value]) => {
    const target = typeof value === "string" ? value : value.import;
    if (!target?.startsWith("./dist/")) return [];
    const entry = key === "." ? "@flanksource/clicky-ui" : `@flanksource/clicky-ui${key.slice(1)}`;
    const cssSource: Record<string, string> = { "./dist/styles.css": "styles/full.css", "./dist/mdx-editor.css": "styles/mdx-editor.css" };
    const relative = cssSource[target] ?? target.slice("./dist/".length).replace(/\.js$/, ".ts");
    const source = resolve(packageRoot, "src", relative);
    if (!existsSync(source)) throw new Error(`Missing local Clicky UI entrypoint ${source}`);
    return [{ find: new RegExp(`^${entry.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`), replacement: source }];
  });
}

export function createViteConfig(options: ViteOptions): UserConfig {
  return {
    plugins: [react(), tailwindcss(), options.localUI && {
      name: "watch-clicky-source",
      configureServer(server) { server.watcher.add(resolve(options.uiDirectory, "packages/ui/src")); },
    }, {
      name: "preserve-embed-sentinel",
      generateBundle() { this.emitFile({ type: "asset", fileName: ".gitkeep", source: "\n" }); },
    }],
    resolve: {
      alias: options.localUI ? clickyAliases(options.uiDirectory) : [],
      dedupe: ["react", "react-dom", "@tanstack/react-query"],
    },
    optimizeDeps: options.localUI ? { exclude: ["@flanksource/clicky-ui"] } : {},
    server: {
      fs: { allow: options.localUI ? [appRoot, resolve(options.uiDirectory, "packages/ui")] : [appRoot] },
      proxy: { "/api": options.apiURL, "/health": options.apiURL },
    },
    build: {
      rolldownOptions: {
        output: { codeSplitting: { groups: [{ name: "react-runtime", test: /node_modules\/(?:\.pnpm\/[^/]+\/node_modules\/)?(?:react|react-dom|scheduler)\// }] } },
      },
    },
  };
}

export default defineConfig(({ command, mode }) => createViteConfig({
  localUI: command === "serve" && mode !== "test" && process.env.TELLTALE_DEV === "1",
  uiDirectory: process.env.TELLTALE_CLICKY_UI_DIR ? resolve(process.env.TELLTALE_CLICKY_UI_DIR) : resolve(appRoot, "../../clicky-ui"),
  apiURL: process.env.TELLTALE_API_URL ?? "http://localhost:8080",
}));
