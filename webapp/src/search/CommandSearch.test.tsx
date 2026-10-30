import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { RouterProvider, useMemoryRouter } from "@flanksource/clicky-ui/rpc";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CommandSearch } from "./CommandSearch";

afterEach(() => vi.unstubAllGlobals());

function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
}

const processes = {
  sampledAt: "2026-10-30T10:00:00Z", tick: 3,
  processes: [{ key: "4242:1", pid: 4242, ppid: 1, name: "node", command: "node server.js", groupKey: "other:user:node", origin: { owner: "acme", scope: "user" }, processes: 1, rssBytes: 1024 }],
};

const runs = { runs: [{ runId: "run-curl", mount: "/api/traces/run-curl", dir: "/data/run-curl", status: "succeeded", command: ["curl", "https://example.com"] }] };

function serve(processesResponse: () => Response = () => json(processes)) {
  const fetchMock = vi.fn((url: string) => {
    if (url.startsWith("/api/top/processes")) return Promise.resolve(processesResponse());
    if (url === "/api/traces") return Promise.resolve(json(runs));
    return Promise.resolve(new Response("", { status: 404 }));
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

function renderSearch() {
  function Harness() {
    const router = useMemoryRouter("/");
    return <RouterProvider adapter={router}><output data-testid="path">{router.pathname}</output><CommandSearch /></RouterProvider>;
  }
  return render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><Harness /></QueryClientProvider>);
}

function openWithHotkey() {
  fireEvent.keyDown(document, { key: "k", ctrlKey: true });
  return screen.findByRole("combobox");
}

describe("the ⌘K search", () => {
  it("opens on the hotkey and lists the trace runs", async () => {
    serve();
    renderSearch();
    await openWithHotkey();
    expect(await screen.findByRole("option", { name: /run-curl/ })).toHaveTextContent("curl https://example.com");
  });

  it("searches live processes on the server and opens the chosen one's page", async () => {
    const fetchMock = serve();
    renderSearch();
    fireEvent.change(await openWithHotkey(), { target: { value: "server.js" } });

    const option = await screen.findByRole("option", { name: /pid 4242/ });
    expect(fetchMock).toHaveBeenCalledWith("/api/top/processes?q=server.js&all=true", expect.anything());
    fireEvent.click(option);

    await waitFor(() => expect(screen.getByTestId("path")).toHaveTextContent("/top/4242"));
  });

  it("surfaces a failed process search", async () => {
    serve(() => new Response("top unavailable", { status: 503 }));
    renderSearch();
    fireEvent.change(await openWithHotkey(), { target: { value: "node" } });
    expect(await screen.findByRole("alert")).toHaveTextContent("read processes failed (503): top unavailable");
  });
});
