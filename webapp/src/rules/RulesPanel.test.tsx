import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { RulesPanel } from "./RulesPanel";
import { EventsPanel } from "./EventsPanel";
import { serveRules } from "./testData";
import TopPage from "../top/TopPage";

beforeEach(() => { window.history.replaceState(null, "", "/top?topTab=rules"); localStorage.clear(); });
afterEach(() => vi.unstubAllGlobals());
function mount(children: React.ReactNode) { return render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>{children}</QueryClientProvider>); }

describe("resource rule views", () => {
  it("visualizes a configured rule's trigger, escalation steps and live incident state", async () => {
    serveRules();
    mount(<RulesPanel />);
    expect(await screen.findByRole("button", { name: "browser-limit" })).toBeInTheDocument();
    expect(screen.getByText("Observation only")).toBeInTheDocument();
    expect(screen.getAllByText("group.members.size() > 4").length).toBeGreaterThan(0);
    expect(screen.getByRole("heading", { name: "Escalation steps" })).toBeInTheDocument();
    expect(screen.getByText("SIGTERM")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "View events for browser-limit" })).toBeInTheDocument();
    expect(screen.getAllByText("breached").length).toBeGreaterThan(0);
  });
  it("explains disabled monitoring without requesting unavailable config or preview", async () => {
    const fetchMock = vi.fn(() => Promise.resolve(new Response(JSON.stringify({ enabled: false, enforcing: false, incidents: [] }), { status: 200 })));
    vi.stubGlobal("fetch", fetchMock);
    mount(<RulesPanel />);
    expect(await screen.findByText(/Start Telltale with/)).toBeInTheDocument();
    expect(fetchMock.mock.calls).toHaveLength(1);
  });
  it("shows persisted event filters, outcomes and inspectable hook output", async () => {
    serveRules();
    window.history.replaceState(null, "", "/top?topTab=events&rulesRule=browser-limit");
    mount(<EventsPanel />);
    expect(await screen.findByText("observed")).toBeInTheDocument();
    expect(screen.getByRole("cell", { name: "incident-1" })).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Rule" })).toHaveValue("browser-limit");
    fireEvent.click(screen.getByRole("button", { name: "Inspect event" }));
    expect(await screen.findByText("hook output")).toBeInTheDocument();
    expect(window.location.search).toContain("rulesEvent=");
    fireEvent.change(screen.getByRole("textbox", { name: "Group" }), { target: { value: "host" } });
    expect(window.location.search).toContain("rulesGroup=host");
  });
  it("opens a rule's events and restores tab selection on browser navigation", async () => {
    serveRules();
    mount(<TopPage />);
    fireEvent.click(await screen.findByRole("button", { name: "View events for browser-limit" }));
    expect(await screen.findByRole("tab", { name: "Events" })).toHaveAttribute("aria-selected", "true");
    expect(await screen.findByText("observed")).toBeInTheDocument();
    expect(window.location.search).toContain("rulesRule=browser-limit");
    act(() => {
      window.history.replaceState(null, "", "/top?topTab=rules");
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
    expect(screen.getByRole("tab", { name: "Rules" })).toHaveAttribute("aria-selected", "true");
  });
  it("debounces event filters and requests only the final value", async () => {
    serveRules();
    mount(<EventsPanel />);
    await screen.findByText("observed");
    vi.useFakeTimers();
    try {
      const requests = () => vi.mocked(fetch).mock.calls.filter(([url]) => String(url).startsWith("/api/rules/events?"));
      fireEvent.change(screen.getByRole("textbox", { name: "Group" }), { target: { value: "ho" } });
      await act(async () => { await vi.advanceTimersByTimeAsync(100); });
      fireEvent.change(screen.getByRole("textbox", { name: "Group" }), { target: { value: "host" } });
      await act(async () => { await vi.advanceTimersByTimeAsync(249); });
      expect(requests()).toHaveLength(1);
      await act(async () => { await vi.advanceTimersByTimeAsync(10); });
      expect(requests()).toHaveLength(2);
      expect(String(requests()[1]![0])).toContain("group=host");
    } finally {
      vi.useRealTimers();
    }
  });
  it("keeps event filters available and shows a failed request", async () => {
    serveRules();
    const original = fetch;
    vi.stubGlobal("fetch", vi.fn((url: string, init?: RequestInit) => url.startsWith("/api/rules/events?") ? Promise.resolve(new Response("history unavailable", { status: 500 })) : original(url, init)));
    mount(<EventsPanel />);
    expect(await screen.findByRole("alert")).toHaveTextContent("history unavailable");
    expect(screen.getByRole("textbox", { name: "Group" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Clear filters" })).toBeInTheDocument();
  });
});
