import { describe, expect, it, vi } from "vitest";
import type { RouterAdapter } from "@flanksource/clicky-ui/rpc";
import { basePath, basenameRouter, withBase } from "./base";
import { runMount, tracesEndpoint } from "./traces/api";

const prefix = "/gavel/runs/42/viewer";

function adapter(pathname: string): RouterAdapter {
  return { pathname, navigate: vi.fn(), renderLink: vi.fn(() => <a />) };
}

describe("the base path a host mounts the viewer under", () => {
  it.each([
    [null, ""],
    ["/", ""],
    [`${prefix}/`, prefix],
    [`http://example.test${prefix}/`, prefix],
  ])("reads <base href=%j> as %j", (href, want) => {
    expect(basePath(href)).toBe(want);
  });

  it("prefixes an absolute app path with the base, and leaves it alone at the root", () => {
    expect(withBase("/api/traces", prefix)).toBe(`${prefix}/api/traces`);
    expect(withBase("/api/traces", "")).toBe("/api/traces");
  });

  it("derives the traces API and a run's mount from the base", () => {
    expect(tracesEndpoint(prefix)).toBe(`${prefix}/api/traces`);
    expect(runMount("run 1", tracesEndpoint(prefix))).toBe(`${prefix}/api/traces/run%201`);
    expect(runMount("run 1", tracesEndpoint(""))).toBe("/api/traces/run%201");
  });
});

describe("a router under a base", () => {
  it("routes on the path below the base and navigates and links above it", () => {
    const inner = adapter(`${prefix}/records/run-1/jvm_trace/stream-1`);
    const router = basenameRouter(inner, prefix);

    expect(router.pathname).toBe("/records/run-1/jvm_trace/stream-1");
    router.navigate("/top", { replace: true });
    expect(inner.navigate).toHaveBeenCalledWith(`${prefix}/top`, { replace: true });
    router.renderLink({ to: "/?run=run-1", children: "Back" });
    expect(inner.renderLink).toHaveBeenCalledWith({ to: `${prefix}/?run=run-1`, children: "Back" });
  });

  it("is the overview at the base itself", () => {
    expect(basenameRouter(adapter(prefix), prefix).pathname).toBe("/");
  });

  it("is the browser router unchanged at the root", () => {
    const inner = adapter("/top");
    expect(basenameRouter(inner, "")).toBe(inner);
  });
});
