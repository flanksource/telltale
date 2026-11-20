import { createRequire } from "node:module";
import { dirname } from "node:path";
import { defineConfig } from "vitest/config";

const webappRequire = createRequire(new URL("./webapp/package.json", import.meta.url));
const sharedRuntimes = ["react", "react-dom", "@tanstack/react-query"];

export default defineConfig({
  test: {
    projects: [
      {
        resolve: {
          alias: Object.fromEntries(sharedRuntimes.map(name => [name, dirname(webappRequire.resolve(`${name}/package.json`))])),
          dedupe: sharedRuntimes,
        },
        test: {
          name: "unit",
          environment: "jsdom",
          server: { deps: { inline: true } },
          include: ["webapp/src/**/*.test.tsx"],
          setupFiles: ["./webapp/src/test/setup.ts"],
        },
      },
      {
        test: {
          name: "config",
          environment: "node",
          include: ["webapp/*.test.ts"],
        },
      },
      {
        test: {
          name: "api",
          environment: "node",
          include: ["packages/api/test/*.spec.ts"],
          // Each spec drives real telltale servers and child processes.
          testTimeout: 30000,
          hookTimeout: 30000,
        },
      },
    ],
  },
});
