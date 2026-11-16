import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { JvmSnapshot } from "./JvmSnapshot";

afterEach(() => { vi.unstubAllGlobals(); window.history.replaceState(null, "", "/"); });

it("keeps expensive collectors opt-in and exposes partial downloads", async () => {
  const fetch = vi.fn().mockResolvedValue({ ok: true, headers: new Headers({ "X-Telltale-Snapshot-Status": "partial" }), blob: async () => new Blob(["zip"]) });
  vi.stubGlobal("fetch", fetch);
  URL.createObjectURL = vi.fn().mockReturnValue("blob:snapshot");
  URL.revokeObjectURL = vi.fn();
  render(<JvmSnapshot agent="fixture-agent" />);
  expect(screen.getByLabelText("Include JFR profiling")).not.toBeChecked();
  expect(screen.getByLabelText("Include heap histogram")).not.toBeChecked();
  fireEvent.click(screen.getByRole("button", { name: "Create support bundle" }));
  await screen.findByText(/Partial bundle/);
  expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({ agent: "fixture-agent", heapHistogram: false });
  expect(screen.getByRole("link", { name: "Download support ZIP" })).toHaveAttribute("href", "blob:snapshot");
});

it("preserves selections and cancels the collection request", async () => {
  vi.stubGlobal("fetch", vi.fn((_url, options) => new Promise((_resolve, reject) => options.signal.addEventListener("abort", () => reject(new DOMException("Aborted", "AbortError"))))));
  render(<JvmSnapshot agent="fixture-agent" />);
  fireEvent.click(screen.getByLabelText("Include JFR profiling"));
  expect(screen.getByLabelText("Recording duration")).toHaveValue("30s");
  expect(window.location.search).toContain("snapshotJfr=true");
  fireEvent.click(screen.getByRole("button", { name: "Create support bundle" }));
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("Snapshot cancelled."));
});
