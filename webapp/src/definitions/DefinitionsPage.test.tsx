import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { RouterProvider, useMemoryRouter } from "@flanksource/clicky-ui/rpc";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { AppRoutes, matchRoute } from "../routes";
import { checkoutStored, fixtureSummaries, psStored } from "./fixtures";
import { bodiesOf, json, serveDefinitions, startedRun } from "./testServer";

afterEach(() => vi.unstubAllGlobals());

// The page loads lazily; load it once up front so no test waits on the first import.
beforeAll(async () => { await import("./DefinitionsPage"); }, 30000);

function renderAt(path: string) {
  function Harness() {
    const router = useMemoryRouter(path);
    return <RouterProvider adapter={router}><output data-testid="path">{router.pathname}</output><AppRoutes /></RouterProvider>;
  }
  return render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><Harness /></QueryClientProvider>);
}

const scrapePointer = "/spec/collect/prometheus/scrape/0";

describe("definition routes", () => {
  it.each([
    ["/definitions", { page: "definitions" }],
    ["/definitions/checkout", { page: "definitions", name: "checkout" }],
    ["/definitions/a%2Fb", { page: "definitions", name: "a/b" }],
    ["/definitions/%E0%A4%A", { page: "notFound", pathname: "/definitions/%E0%A4%A" }],
  ])("reads %s", (pathname, route) => {
    expect(matchRoute(pathname)).toEqual(route);
  });
});

describe("the definitions list", () => {
  it("lists each definition's layer, description, capture counts and problem", async () => {
    serveDefinitions({ definitions: fixtureSummaries });
    renderAt("/definitions");
    const checkout = await screen.findByRole("row", { name: /checkout/ });
    expect(within(checkout).getByRole("link", { name: "checkout" })).toHaveAttribute("href", "/definitions/checkout");
    expect(checkout).toHaveTextContent("cwd");
    expect(checkout).toHaveTextContent("Scrape the checkout service");
    expect(within(checkout).getByLabelText("collect captures")).toHaveTextContent("1");
    expect(screen.getByRole("row", { name: /broken/ })).toHaveTextContent("yaml: line 3: mapping values are not allowed in this context");
    expect(screen.getByRole("row", { name: /ps/ })).toHaveTextContent("builtin");
  });

  it("keeps the New action when there is no definition yet", async () => {
    serveDefinitions({ definitions: [] });
    renderAt("/definitions");
    expect(await screen.findByText(/No trace definitions yet/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "New definition" })).toBeEnabled();
  });

  it("shows a failed listing verbatim and keeps the New action", async () => {
    vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(new Response("config dir unreadable", { status: 500 }))));
    renderAt("/definitions");
    expect(await screen.findByText("list trace definitions failed (500): config dir unreadable")).toHaveAttribute("role", "alert");
    expect(screen.getByRole("button", { name: "New definition" })).toBeInTheDocument();
  });

  it("creates a new definition in the chosen layer and opens it", async () => {
    const fetchMock = serveDefinitions({ definitions: [] });
    renderAt("/definitions");
    fireEvent.click(await screen.findByRole("button", { name: "New definition" }));
    fireEvent.change(await screen.findByRole("textbox", { name: "Name" }), { target: { value: "orders" } });
    fireEvent.change(screen.getByRole("combobox", { name: "Save to" }), { target: { value: "global" } });
    fireEvent.click(screen.getByRole("button", { name: "Create" }));
    fireEvent.click(await screen.findByRole("button", { name: "Save" }));
    await waitFor(() => expect(bodiesOf(fetchMock, "/api/traces/definitions/orders", "PUT")).toEqual([{
      layer: "global", resourceVersion: "",
      definition: { apiVersion: "telltale.flanksource.com/v1alpha1", kind: "TraceDefinition", metadata: { name: "orders" }, spec: {} },
    }]));
    await waitFor(() => expect(screen.getByTestId("path")).toHaveTextContent("/definitions/orders"));
  });
});

describe("the definition editor", () => {
  it("adds a Prometheus scrape and shows the name it will take", async () => {
    const fetchMock = serveDefinitions({ stored: { checkout: { ...checkoutStored, definition: { ...checkoutStored.definition, spec: { description: "empty" } }, entries: [] } } });
    renderAt("/definitions/checkout");
    fireEvent.click(await screen.findByRole("button", { name: "Prometheus" }));
    fireEvent.click(screen.getByRole("button", { name: "Add Scrape" }));
    await waitFor(() => expect(bodiesOf(fetchMock, "/api/traces/definitions/validate", "POST")).toContainEqual({
      definition: { ...checkoutStored.definition, spec: { description: "empty", collect: { prometheus: { scrape: [{}] } } } },
    }));
    expect(await screen.findByPlaceholderText("checkout-collect-prometheus-scrape")).toBeInTheDocument();
  });

  it("shows a refused save's field error inside the entry it names", async () => {
    const message = "interval \"0s\" must be at least 1s";
    serveDefinitions({
      stored: { checkout: checkoutStored },
      save: () => json({ name: "checkout", errors: [{ instancePath: `${scrapePointer}/interval`, message }] }, 400),
    });
    renderAt("/definitions/checkout");
    fireEvent.change(await screen.findByRole("textbox", { name: "Description" }), { target: { value: "Scrape it again" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    const error = await screen.findByText(message);
    const entryForm = error.closest("[data-json-schema-form]");
    expect(entryForm).toContainElement(screen.getByPlaceholderText("checkout-collect-prometheus-scrape"));
    expect(screen.getByRole("textbox", { name: "Description" })).toHaveValue("Scrape it again");
  });

  it("offers to reload or overwrite when the file changed on disk", async () => {
    let attempt = 0;
    const fetchMock = serveDefinitions({
      stored: { checkout: checkoutStored },
      save: (_name, body) => (++attempt === 1
        ? json({ error: "checkout.yaml changed on disk: version v2, not v1", resourceVersion: "v2" }, 409)
        : json({ ...checkoutStored, resourceVersion: "v3", definition: body.definition })),
    });
    renderAt("/definitions/checkout");
    fireEvent.change(await screen.findByRole("textbox", { name: "Description" }), { target: { value: "Mine" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("checkout.yaml changed on disk: version v2, not v1")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Reload from disk" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Overwrite with my version" }));
    await waitFor(() => expect(bodiesOf(fetchMock, "/api/traces/definitions/checkout", "PUT").map(body => (body as { resourceVersion: string }).resourceVersion)).toEqual(["v1", "v2"]));
    await waitFor(() => expect(screen.queryByText(/changed on disk/)).not.toBeInTheDocument());
    expect(screen.getByRole("textbox", { name: "Description" })).toHaveValue("Mine");
  });

  it("opens a built-in definition read-only, offering to duplicate it", async () => {
    const fetchMock = serveDefinitions({ stored: { ps: psStored } });
    renderAt("/definitions/ps");
    expect(await screen.findByText(/Built-in definitions are read-only/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Delete" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Add / })).not.toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Description" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Duplicate to cwd" }));
    await waitFor(() => expect(bodiesOf(fetchMock, "/api/traces/definitions/ps", "PUT")).toEqual([{ layer: "cwd", resourceVersion: "", definition: psStored.definition }]));
    expect(await screen.findByRole("button", { name: "Save" })).toBeInTheDocument();
  });

  it("round-trips an edit made in the YAML tab", async () => {
    serveDefinitions({ stored: { checkout: checkoutStored } });
    renderAt("/definitions/checkout");
    fireEvent.click(await screen.findByRole("tab", { name: "YAML" }));
    const yaml = screen.getByRole("textbox", { name: "Definition YAML" }) as HTMLTextAreaElement;
    expect(yaml.value).toContain("description: Scrape the checkout service");
    fireEvent.change(yaml, { target: { value: yaml.value.replace("Scrape the checkout service", "Edited as YAML") } });
    fireEvent.click(screen.getByRole("tab", { name: "Form" }));
    expect(screen.getByRole("textbox", { name: "Description" })).toHaveValue("Edited as YAML");
  });

  it("holds invalid YAML without losing the form, until fixed or discarded", async () => {
    serveDefinitions({ stored: { checkout: checkoutStored } });
    renderAt("/definitions/checkout");
    fireEvent.click(await screen.findByRole("tab", { name: "YAML" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Definition YAML" }), { target: { value: "spec: [unclosed" } });
    expect(await screen.findByText("This YAML is not a definition yet")).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent(/must be sufficiently indented/);
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    fireEvent.click(screen.getByRole("tab", { name: "Form" }));
    expect(screen.getByRole("textbox", { name: "Definition YAML" })).toHaveValue("spec: [unclosed");
    fireEvent.click(screen.getByRole("button", { name: "Discard YAML edits" }));
    expect((screen.getByRole("textbox", { name: "Definition YAML" }) as HTMLTextAreaElement).value).toContain("Scrape the checkout service");
    fireEvent.click(screen.getByRole("tab", { name: "Form" }));
    expect(screen.getByRole("textbox", { name: "Description" })).toHaveValue("Scrape the checkout service");
  });

  it("starts the saved definition with execution fields only and opens the run", async () => {
    const fetchMock = serveDefinitions({ stored: { checkout: checkoutStored } });
    renderAt("/definitions/checkout");
    fireEvent.click(await screen.findByRole("button", { name: "Start run" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.change(await within(dialog).findByRole("textbox", { name: /Command/ }), { target: { value: "./bin/checkout" } });
    fireEvent.change(within(dialog).getByRole("textbox", { name: /Duration/ }), { target: { value: "20s" } });
    expect(within(dialog).queryByRole("textbox", { name: /Traces/ })).not.toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("button", { name: "Start run" }));
    await waitFor(() => expect(bodiesOf(fetchMock, "/api/traces", "POST")).toEqual([{ definition: "checkout", command: "./bin/checkout", duration: "20s" }]));
    await waitFor(() => expect(screen.getByTestId("path")).toHaveTextContent(`/?run=${startedRun.runId}`));
  });

  it("shows a refused start's errors in the dialog", async () => {
    serveDefinitions({ stored: { checkout: checkoutStored }, start: json({ errors: [{ instancePath: "", message: "start prometheus: no endpoint answered" }] }, 400) });
    renderAt("/definitions/checkout");
    fireEvent.click(await screen.findByRole("button", { name: "Start run" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(await within(dialog).findByRole("button", { name: "Start run" }));
    expect(await within(dialog).findByText("start prometheus: no endpoint answered")).toHaveAttribute("role", "alert");
  });
});
