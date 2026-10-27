import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ProcessActions } from "./ProcessActions";

afterEach(() => vi.unstubAllGlobals());
const target = { key: "123@100", pid: 123, name: "worker", command: "worker --task acme" };
function actions() {
  render(<QueryClientProvider client={new QueryClient()}><ProcessActions process={target} /></QueryClientProvider>);
}

describe("process actions", () => {
  it("does not act until confirmation and sends the displayed process identity", async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetch);
    actions();
    fireEvent.click(screen.getByRole("button", { name: "Terminate" }));
    expect(screen.getByRole("form")).toHaveTextContent("worker (PID 123)");
    expect(fetch).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(fetch).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Terminate" }));
    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
    await screen.findByRole("status");
    expect(fetch).toHaveBeenCalledWith("/api/top/action", expect.objectContaining({ method: "POST", body: JSON.stringify({ key: target.key, action: "terminate" }) }));
  });
  it("validates nice values and displays permission failures without claiming success", async () => {
    const fetch = vi.fn().mockResolvedValue(new Response("permission denied", { status: 403 }));
    vi.stubGlobal("fetch", fetch);
    actions();
    fireEvent.click(screen.getByRole("button", { name: "Renice" }));
    fireEvent.change(screen.getByRole("spinbutton"), { target: { value: "20" } });
    expect(screen.getByRole("button", { name: "Confirm" })).toBeDisabled();
    fireEvent.change(screen.getByRole("spinbutton"), { target: { value: "15" } });
    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("permission denied");
    await waitFor(() => expect(fetch).toHaveBeenCalledWith("/api/top/action", expect.objectContaining({ body: JSON.stringify({ key: target.key, action: "renice", nice: 15 }) })));
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });
});
