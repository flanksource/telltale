import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, fireEvent } from "@testing-library/react";
import { RouterProvider, useMemoryRouter } from "@flanksource/clicky-ui/rpc";
import { afterEach, expect, it, vi } from "vitest";
import JvmPage from "./JvmPage";

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
  const execute = await screen.findByRole("button", { name: "Execute" });
  expect(execute).toBeDisabled();
  fireEvent.change(screen.getByLabelText(/Source/), { target: { value: "return 14" } });
  fireEvent.click(screen.getByLabelText("Allow this operation to change the JVM"));
  fireEvent.click(execute);
  expect(await screen.findByText("hello")).toBeInTheDocument();
  expect(fetchMock).toHaveBeenCalledWith("/api/agents/java%2Ftest/call", expect.objectContaining({ body: JSON.stringify({ operation: "jvm.groovy.eval", params: { source: "return 14" }, allowMutation: true }) }));
});
