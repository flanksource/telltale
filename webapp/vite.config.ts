import { existsSync, readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
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
    const pattern = entry.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
    if (key.includes("*")) {
      const assetDirectory = resolve(packageRoot, dirname(target.replace(/\*.*$/, "*")));
      if (!existsSync(assetDirectory)) throw new Error(`Missing local Clicky UI assets ${assetDirectory}; build the local package first`);
      const source = resolve(packageRoot, target.replaceAll("*", "$1"));
      return [{ find: new RegExp(`^${pattern.replace("\\*", "(.+)")}$`), replacement: source }];
    }
    const cssSource: Record<string, string> = { "./dist/styles.css": "styles/full.css", "./dist/mdx-editor.css": "styles/mdx-editor.css" };
    const relative = cssSource[target] ?? target.slice("./dist/".length).replace(/\.js$/, ".ts");
    const source = resolve(packageRoot, "src", relative);
    if (!existsSync(source)) throw new Error(`Missing local Clicky UI entrypoint ${source}`);
    return [{ find: new RegExp(`^${pattern}$`), replacement: source }];
  });
}

export function createViteConfig(options: ViteOptions): UserConfig {
  return {
    // Assets resolve against index.html's <base href>, which a host mounting the
    // viewer under a prefix rewrites (webapp.Handler).
    base: "./",
    plugins: [react(), tailwindcss(), options.localUI && {
      name: "watch-clicky-source",
      configureServer(server) { server.watcher.add(resolve(options.uiDirectory, "packages/ui/src")); },
    }, {
      name: "preserve-embed-sentinel",
      generateBundle() { this.emitFile({ type: "asset", fileName: ".gitkeep", source: "\n" }); },
    }],
    resolve: {
      alias: options.localUI ? clickyAliases(options.uiDirectory) : [],
      dedupe: ["react", "react-dom", "@tanstack/react-query", "monaco-editor", "@monaco-editor/react"],
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
  localUI: mode === "local-ui" || (command === "serve" && mode !== "test" && process.env.TELLTALE_DEV === "1"),
  uiDirectory: process.env.TELLTALE_CLICKY_UI_DIR ? resolve(process.env.TELLTALE_CLICKY_UI_DIR) : resolve(appRoot, "../../clicky-ui"),
  apiURL: process.env.TELLTALE_API_URL ?? "http://localhost:8080",
}));
