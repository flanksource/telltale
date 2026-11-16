import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { StartRunForm } from "./StartRunForm";

afterEach(() => vi.unstubAllGlobals());

it("loads saved connections through the shared lookup client and submits the chosen reference", async () => {
  const fetcher = vi.fn(async (input: string | URL | Request, options?: RequestInit) => {
    const url = new URL(String(input), window.location.origin);
    if (url.pathname === "/api/traces/definitions") return Response.json([]);
    if (url.pathname === "/api/traces/start") return Response.json({
      schema: { type: "object", properties: { connection: { type: "string", title: "Connection", "x-clicky-lookup": { url: "/api/v1/connection?types=postgres,sql_server", filter: "connection" } } } }, defaults: {},
    });
    if (url.pathname === "/api/v1/connection") return Response.json({ filters: { connection: { options: { "connection://tenant/warehouse": { kind: "text", plain: "warehouse (postgres)" } } } } });
    if (url.pathname === "/api/traces" && options?.method === "POST") return Response.json({ runId: "chosen-run", mount: "/api/traces/chosen-run", captures: [] });
    return new Response("Unexpected request", { status: 404 });
  });
  vi.stubGlobal("fetch", fetcher);
  const onStarted = vi.fn();
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
    <StartRunForm onStarted={onStarted} />
  </QueryClientProvider>);
  fireEvent.focus(await screen.findByRole("combobox", { name: "Connection" }));
  fireEvent.mouseDown(await screen.findByRole("option", { name: "warehouse (postgres)" }));
  fireEvent.click(screen.getByRole("button", { name: "Start run" }));
  await waitFor(() => expect(onStarted).toHaveBeenCalled());
  const lookup = fetcher.mock.calls.find(([url]) => String(url).includes("__lookup=filters"));
  expect(new URL(String(lookup?.[0]), window.location.origin).searchParams.get("types")).toBe("postgres,sql_server");
  const submitted = fetcher.mock.calls.find(([, options]) => options?.method === "POST");
  expect(JSON.parse(String(submitted?.[1]?.body))).toEqual({ connection: "connection://tenant/warehouse" });
});
