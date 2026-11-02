import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import QueryPage from "./QueryPage";

vi.mock("@flanksource/clicky-ui/profiles", async importOriginal => {
  const original = await importOriginal<typeof import("@flanksource/clicky-ui/profiles")>();
  return { ...original, ConnectionQueryWorkspace: ({ execute, options, onOptionsChange, lookupFilterValues }: import("@flanksource/clicky-ui/profiles").ConnectionQueryWorkspaceProps) => <>
    <output data-testid="options">{JSON.stringify(options)}</output>
    <button onClick={() => onOptionsChange({ limit: 25 })}>Edit options</button>
    <button onClick={() => void execute({ query: "SELECT 7 AS value", options: { limit: 25 }, pagination: { limit: 25, offset: 25 } })}>Run query</button>
    <button onClick={() => void lookupFilterValues?.({ query: "SELECT 7 AS value", options: {}, filters: {}, filterKey: "filter.value", search: "7", limit: 20 })}>Lookup</button>
  </> };
});

afterEach(() => vi.unstubAllGlobals());

describe("query workspace adapter", () => {
  it.each([
    ["sqlite", "snapshot", {}],
    ["postgres", "orders", { database: "orders" }],
  ])("runs %s queries with the actual database scope and server pagination", async (driver, database, expected) => {
    const fetchMock = vi.fn((url: string) => Promise.resolve(new Response(JSON.stringify(
      url.endsWith("/dashboard") ? { connections: [] }
        : url.endsWith("/browser") ? { kind: "query", provider: driver, catalog: true }
          : url.endsWith("/inspect") ? { kind: "sql", driver, database, dialect: "standard", databases: [database], schemas: [] }
            : url.endsWith("/filters/values") ? { options: [{ value: "7" }] }
              : { rows: [{ value: 7 }] },
    ), { headers: { "Content-Type": "application/json" } })));
    vi.stubGlobal("fetch", fetchMock);
    render(<QueryClientProvider client={new QueryClient()}><QueryPage selected="local" /></QueryClientProvider>);
    await waitFor(() => expect(screen.getByTestId("options")).toHaveTextContent(JSON.stringify(expected)));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith("/api/v1/connection/local/browser/inspect", undefined));
    fireEvent.click(screen.getByText("Edit options"));
    fireEvent.click(screen.getByText("Run query"));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith("/api/v1/connection/local/browser/query", expect.objectContaining({
      method: "POST", body: JSON.stringify({ query: "SELECT 7 AS value", options: { limit: 25, ...expected }, pagination: { limit: 25, offset: 25 } }),
    })));
    fireEvent.click(screen.getByText("Lookup"));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith("/api/v1/connection/local/browser/filters/values", expect.objectContaining({ method: "POST" })));
  });
});
