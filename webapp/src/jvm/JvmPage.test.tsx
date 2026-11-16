import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { RouterProvider, useMemoryRouter } from "@flanksource/clicky-ui/rpc";
import { afterEach, expect, it, vi } from "vitest";
import JvmPage from "./JvmPage";

vi.mock("./SourceEditor", () => ({ SourceEditor: ({ value, onChange }: { value: string; onChange: (value: string) => void }) => <label>Source<textarea value={value} onChange={event => onChange(event.target.value)} /></label> }));

afterEach(() => vi.unstubAllGlobals());

it("requires the mutation choice and sends it with script execution", async () => {
  const fetchMock = vi.fn(async (_url: string, options?: RequestInit) => {
    if (!options) return new Response(JSON.stringify([{ name: "jvm.groovy.eval", title: "Groovy", description: "Execute once", mutates: true, paramsSchema: { type: "object", required: ["source"], properties: { source: { type: "string", title: "Source" } } } }]));
    if (JSON.parse(options.body as string).operation === "jvm.status") return new Response(JSON.stringify({ allowMutation: true, instrumentation: true, patches: [] }));
    return new Response(JSON.stringify({ value: 14, stdout: "hello" }));
  });
  vi.stubGlobal("fetch", fetchMock);
  function Harness() { return <RouterProvider adapter={useMemoryRouter("/jvm/java%2Ftest/jvm.groovy.eval")}><JvmPage agent="java/test" operation="jvm.groovy.eval" /></RouterProvider>; }
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><Harness /></QueryClientProvider>);
  const execute = await screen.findByRole("button", { name: "Run script" });
  expect(execute).toBeDisabled();
  fireEvent.change(screen.getByLabelText(/Source/), { target: { value: "return 14" } });
  fireEvent.click(screen.getByLabelText("Allow this operation to change the JVM"));
  fireEvent.click(execute);
  expect(await screen.findByText("hello")).toBeInTheDocument();
  expect(fetchMock).toHaveBeenCalledWith("/api/agents/java%2Ftest/call", expect.objectContaining({ body: JSON.stringify({ operation: "jvm.groovy.eval", params: { source: "return 14" }, allowMutation: true }) }));
});

it("keeps mutation actions disabled when the runtime forbids changes", async () => {
  const fetchMock = vi.fn(async (_url: string, options?: RequestInit) => new Response(JSON.stringify(options ? { allowMutation: false, patches: [] } : [{ name: "jvm.groovy.eval", title: "Groovy", description: "", mutates: true, paramsSchema: { type: "object", properties: { source: { type: "string" } } } }])));
  vi.stubGlobal("fetch", fetchMock);
  render(<QueryClientProvider client={new QueryClient()}><JvmPage agent="locked" operation="jvm.groovy.eval" /></QueryClientProvider>);
  await waitFor(() => expect(screen.getByLabelText("Allow this operation to change the JVM")).toBeDisabled());
  fireEvent.change(screen.getByLabelText("Source"), { target: { value: "return 1" } });
  expect(screen.getByRole("button", { name: "Run script" })).toBeDisabled();
  expect(fetchMock.mock.calls.filter(([, options]) => options && JSON.parse(options.body as string).operation !== "jvm.status")).toHaveLength(0);
});

it("compiles source without lease options and keeps its preview valid when only the restore duration changes", async () => {
  window.history.replaceState(null, "", "/jvm/patch/jvm.classes.patch?class=example.Target");
  const fetchMock = vi.fn(async (_url: string, options?: RequestInit) => {
    if (!options) return new Response(JSON.stringify([{ name: "jvm.classes.patch", title: "Patch", description: "", mutates: true, paramsSchema: { type: "object", properties: { class: { type: "string" }, source: { type: "string" }, leaseSeconds: { type: "integer" } } } }]));
    return new Response(JSON.stringify(JSON.parse(options.body as string).operation === "jvm.status" ? { allowMutation: true, patches: [] } : { success: true, classes: ["example.Target"], diagnostics: [] }));
  });
  vi.stubGlobal("fetch", fetchMock);
  render(<QueryClientProvider client={new QueryClient()}><JvmPage agent="patch" operation="jvm.classes.patch" /></QueryClientProvider>);
  fireEvent.change(await screen.findByLabelText("Source"), { target: { value: "class Target {}" } });
  fireEvent.click(screen.getByLabelText("Allow this operation to change the JVM"));
  fireEvent.click(screen.getByRole("button", { name: "Compile preview" }));
  const apply = screen.getByRole("button", { name: "Apply patch" });
  await waitFor(() => expect(apply).toBeEnabled());
  fireEvent.change(screen.getByLabelText("Restore original bytecode after"), { target: { value: "300" } });
  expect(apply).toBeEnabled();
  expect(fetchMock).toHaveBeenCalledWith("/api/agents/patch/call", expect.objectContaining({ body: JSON.stringify({ operation: "jvm.classes.compile", params: { class: "example.Target", source: "class Target {}" }, allowMutation: false }) }));
  window.history.replaceState(null, "", "/");
});
