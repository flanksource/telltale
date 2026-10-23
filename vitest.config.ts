import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    projects: [
      {
        test: {
          name: "unit",
          environment: "jsdom",
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
          hookTimeout: 30000,
        },
      },
    ],
  },
});
