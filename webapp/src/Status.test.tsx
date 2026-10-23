import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import Status from "./Status";

afterEach(() => vi.unstubAllGlobals());

function renderStatus() {
  return render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <Status />
    </QueryClientProvider>,
  );
}

describe("backend connectivity", () => {
  it("shows loading while the health request is pending", () => {
    vi.stubGlobal("fetch", vi.fn(() => new Promise(() => {})));
    renderStatus();
    expect(screen.getByRole("status")).toHaveTextContent("Connecting");
  });

  it("shows the server version after connecting", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ status: "healthy", version: "test-version" }))));
    renderStatus();
    expect(await screen.findByText("Connected")).toBeInTheDocument();
    expect(screen.getByText("test-version")).toBeInTheDocument();
  });

  it.each([503, 404])("surfaces HTTP %s failures", async (status) => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("Unavailable", { status })));
    renderStatus();
    expect(await screen.findByRole("alert")).toHaveTextContent(`HTTP ${status}`);
  });

  it("rejects malformed health responses", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ status: "healthy" }))));
    renderStatus();
    expect(await screen.findByRole("alert")).toHaveTextContent("Invalid health response");
  });
});
