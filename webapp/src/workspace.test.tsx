import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { RouterProvider, useMemoryRouter } from "@flanksource/clicky-ui/rpc";
import { START_FORM } from "./arm/fixture";
import { AppRoutes, matchRoute } from "./routes";

afterEach(() => vi.unstubAllGlobals());

// The pages load lazily; load them once up front so no test waits on the first import.
beforeAll(async () => {
  await Promise.all([import("./workspace/AgentsPage"), import("./workspace/ArmPage")]);
}, 30000);

function json(value: unknown) {
  return new Response(JSON.stringify(value), { headers: { "Content-Type": "application/json" } });
}

function renderAt(path: string, navigate?: (to: string) => void) {
  function Harness() {
    const router = useMemoryRouter(path);
    return <RouterProvider adapter={navigate ? { ...router, navigate } : router}><AppRoutes /></RouterProvider>;
  }
  return render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><Harness /></QueryClientProvider>);
}

describe("workspace routes", () => {
	 it("lists saved deployed agents in their namespace with diagnostics and no invented probes", async () => {
		const target = "deployer/orders/shop/orders-0";
		vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(json([{
			agentId: target, connected: false, group: "namespace/shop",
			resource: { "k8s.namespace.name": "shop", "k8s.pod.name": "orders-0", "k8s.container.name": "app" },
			known: { agentId: target, runtime: "java", names: ["orders"], state: "deployed", endpoint: "http://orders-0:7070" },
		}]))));
		const navigate = vi.fn();
		renderAt("/agents", navigate);
		expect(await screen.findByRole("heading", { name: "namespace/shop" })).toBeInTheDocument();
		expect(screen.getByText(target)).toBeInTheDocument();
		expect(screen.getByText("Not connected")).toBeInTheDocument();
		expect(screen.getByText("deployed")).toBeInTheDocument();
		expect(screen.getByText("http://orders-0:7070")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Arm capture" })).not.toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "JVM diagnostics" }));
		expect(navigate).toHaveBeenCalledWith(`/jvm/${encodeURIComponent(target)}`);
	});
  it.each(["connections", "agents", "arm", "query"])("routes /%s", page => {
    expect(matchRoute(`/${page}`)).toEqual({ page });
  });
  it("decodes agent and record identities in deep links", () => {
    expect(matchRoute("/arm/hub%2Fworker-1/http")).toEqual({ page: "arm", agent: "hub/worker-1", probe: "http" });
    expect(matchRoute("/records/run-1/http/stream-1")).toEqual({ page: "records", run: "run-1", kind: "http", stream: "stream-1" });
    expect(matchRoute("/records/run-1/http/%E0%A4%A")).toEqual({ page: "notFound", pathname: "/records/run-1/http/%E0%A4%A" });
  });

  it("lists advertised agent probes and seeds a capture for the selected agent", async () => {
    const fetchMock = vi.fn((url: string, init?: RequestInit) => {
      if (url === "/api/agents") return Promise.resolve(json([{
        agentId: "worker-1", service: "worker", connected: true, group: "other", runtime: { lang: "go", version: "1.26" },
        resource: { "service.name": "worker" }, labels: {},
        probes: [{ name: "http", title: "HTTP traffic" }], warnings: [],
      }]));
      if (url === "/api/traces/start") return Promise.resolve(json(START_FORM));
      if (url === "/api/traces/connections") return Promise.resolve(json({ connections: [] }));
      if (url === "/api/traces/definitions") return Promise.resolve(json([]));
      if (url.startsWith("/api/v1/jvm/deployers")) return Promise.resolve(json({ success: true, output: "[]" }));
      if (url === "/api/traces" && init?.method === "POST") return Promise.resolve(json({ runId: "armed-run", status: "running" }));
      return Promise.resolve(new Response("unknown route", { status: 404 }));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderAt("/agents");
    expect(await screen.findByText("worker-1")).toBeInTheDocument();
    expect(screen.getByText("HTTP traffic")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Arm capture" }));
    // The deep link opens on Captures with the probe armed on that agent; the rest needs nothing.
    for (let step = 0; step < 3; step++) fireEvent.click(await screen.findByRole("button", { name: /Next/ }));
    fireEvent.click(await screen.findByRole("button", { name: /Start run/ }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith("/api/traces", expect.objectContaining({
      method: "POST", body: JSON.stringify({ traces: { http: { enabled: true, mode: "connect", target: { seam: "agent", agent: "worker-1" } } } }),
    })));
  });

  it("surfaces agent listing failures", async () => {
    vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(new Response("hub unavailable", { status: 503 }))));
    renderAt("/agents");
    expect(await screen.findByRole("alert")).toHaveTextContent("hub unavailable");
  });
});
