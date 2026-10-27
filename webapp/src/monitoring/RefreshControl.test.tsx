import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { RefreshControl, useRefreshInterval } from "./RefreshControl";

beforeEach(() => { localStorage.clear(); vi.useFakeTimers(); });
afterEach(() => { vi.useRealTimers(); localStorage.clear(); });
describe("monitoring refresh", () => {
  it("reschedules polling, pauses it, supports manual refresh and restores the saved interval", async () => {
    const read = vi.fn(() => Promise.resolve("ready"));
    function Harness() {
      const interval = useRefreshInterval();
      useQuery({ queryKey: ["top-live"], queryFn: read, refetchInterval: interval });
      return <RefreshControl />;
    }
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const mount = () => render(<QueryClientProvider client={client}><Harness /></QueryClientProvider>);
    const page = mount();
    await act(async () => { await vi.advanceTimersByTimeAsync(10); });
    expect(read).toHaveBeenCalledTimes(1);
    fireEvent.change(screen.getByRole("combobox", { name: "Refresh interval" }), { target: { value: "5000" } });
    await act(async () => { await vi.advanceTimersByTimeAsync(4999); });
    expect(read).toHaveBeenCalledTimes(1);
    await act(async () => { await vi.advanceTimersByTimeAsync(10); });
    expect(read).toHaveBeenCalledTimes(2);
    fireEvent.change(screen.getByRole("combobox", { name: "Refresh interval" }), { target: { value: "0" } });
    await act(async () => { await vi.advanceTimersByTimeAsync(30000); });
    expect(read).toHaveBeenCalledTimes(2);
    fireEvent.click(screen.getByRole("button", { name: "Refresh now" }));
    await act(async () => { await vi.advanceTimersByTimeAsync(10); });
    expect(read).toHaveBeenCalledTimes(3);
    page.unmount();
    mount();
    expect(screen.getByRole("combobox", { name: "Refresh interval" })).toHaveValue("0");
  });
});
