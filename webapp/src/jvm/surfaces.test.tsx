import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { JvmMBeans } from "./JvmMBeans";
import { JvmLogging, JvmThreads } from "./JvmInspectors";
import { JvmClasses } from "./JvmClasses";
import { RouterProvider, useMemoryRouter } from "@flanksource/clicky-ui/rpc";
import { buildMBeanBrowserTree, parseMBeanArgument } from "./mbeans";
import classSource from "./class-source.fixture.json";
import { JvmRuntime } from "./runtime";

vi.mock("./SourceEditor", () => ({ SourceEditor: ({ value, onChange }: { value: string; onChange: (value: string) => void }) => <label>Replacement source<textarea value={value} onChange={event => onChange(event.target.value)} /></label> }));

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); window.history.replaceState(null, "", "/"); });

function view(component: React.ReactNode) {
  function Harness() { return <RouterProvider adapter={useMemoryRouter(window.location.pathname)}><JvmRuntime.Provider value={true}>{component}</JvmRuntime.Provider></RouterProvider>; }
  return render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}><Harness /></QueryClientProvider>);
}

it("groups quoted MBean names without splitting commas inside a quoted identity", () => {
  expect(buildMBeanBrowserTree(['example:type=Pool,name="read,write",scope=tenant', "java.lang:type=Runtime"])).toEqual([
    { domain: "example", count: 1, types: [{ type: "Pool", mbeans: [{ objectName: 'example:type=Pool,name="read,write",scope=tenant', label: "read,write", metadata: "scope=tenant" }] }] },
    { domain: "java.lang", count: 1, types: [{ type: "Runtime", mbeans: [{ objectName: "java.lang:type=Runtime", label: "Runtime", metadata: "" }] }] },
  ]);
});

it.each([["false", "boolean", false], ["9223372036854775807", "long", "9223372036854775807"], ["", "java.lang.String", ""]])("parses MBean argument %s as %s without losing precision", (raw, type, expected) => {
  expect(parseMBeanArgument(String(raw), String(type))).toEqual(expected);
});

it.each([["yes", "boolean"], ["9223372036854775808", "long"], ["128", "byte"], ["Infinity", "double"], ["xx", "char"]])("rejects invalid MBean argument %s for %s", (raw, type) => {
  expect(() => parseMBeanArgument(raw, type)).toThrow();
});

it("selects an MBean, reads live values and invokes the exact overload after mutation consent", async () => {
  const objectName = "example:type=Counter";
  const requests: Array<{ operation: string; params: Record<string, unknown>; allowMutation: boolean }> = [];
  vi.stubGlobal("fetch", vi.fn(async (_url: string, options: RequestInit) => {
    const body = JSON.parse(options.body as string) as typeof requests[number];
    requests.push(body);
    const values: Record<string, unknown> = {
      "jvm.jmx.search": [objectName],
      "jvm.jmx.info": { objectName, className: "example.Counter", attributes: [{ name: "Count", type: "long", readable: true, writable: true }], operations: [{ name: "increment", returnType: "long", signature: [{ name: "amount", type: "long" }] }] },
      "jvm.jmx.read": { attributes: [{ name: "Count", value: 7 }] },
      "jvm.jmx.invoke": 12,
    };
    return new Response(JSON.stringify(values[body.operation]));
  }));
  view(<JvmMBeans agent="fixture" />);
  fireEvent.click(await screen.findByRole("button", { name: objectName }));
  expect(await screen.findByText("7")).toBeInTheDocument();
  expect(new URLSearchParams(window.location.search).get("objectName")).toBe(objectName);
  fireEvent.click(screen.getByRole("tab", { name: /Operations/ }));
  fireEvent.change(screen.getByLabelText("amount (long)"), { target: { value: "9007199254740993" } });
  const invoke = screen.getByRole("button", { name: "Invoke increment" });
  expect(invoke).toBeDisabled();
  fireEvent.click(screen.getByLabelText("Allow MBean changes"));
  fireEvent.click(invoke);
  await waitFor(() => expect(requests).toContainEqual({ operation: "jvm.jmx.invoke", params: { objectName, operation: "increment", args: ["9007199254740993"], signature: ["long"] }, allowMutation: true }));
});

it("sends root logger inheritance as an empty name and null level", async () => {
  const requests: Array<{ operation: string; params: Record<string, unknown>; allowMutation: boolean }> = [];
  vi.stubGlobal("fetch", vi.fn(async (_url: string, options: RequestInit) => {
    const body = JSON.parse(options.body as string) as typeof requests[number];
    requests.push(body);
    return new Response(JSON.stringify(body.operation === "jvm.logging.list" ? [{ name: "", level: "INFO", parent: null }] : { applied: true }));
  }));
  view(<JvmLogging agent="fixture" />);
  fireEvent.click(await screen.findByRole("button", { name: "Edit root logger" }));
  fireEvent.change(screen.getByLabelText("New level"), { target: { value: "inherit" } });
  expect(screen.getByRole("button", { name: "Apply level" })).toBeDisabled();
  fireEvent.click(screen.getByLabelText("Allow logging changes"));
  fireEvent.click(screen.getByRole("button", { name: "Apply level" }));
  await waitFor(() => expect(requests).toContainEqual({ operation: "jvm.logging.set", params: { name: "", level: null }, allowMutation: true }));
});

it("opens the lock owner's stack without selecting the waiting thread's row", async () => {
  const requests: Array<{ operation: string; params: Record<string, unknown> }> = [];
  const threads = [
    { id: 1, name: "worker", state: "BLOCKED", cpuPercent: 0, lockName: "example.Lock", lockOwnerId: 2, lockOwnerName: "owner" },
    { id: 2, name: "owner", state: "RUNNABLE", cpuPercent: 5, lockName: null, lockOwnerId: null, lockOwnerName: null },
  ];
  vi.stubGlobal("fetch", vi.fn(async (_url: string, options: RequestInit) => {
    const body = JSON.parse(options.body as string) as typeof requests[number];
    requests.push(body);
    return new Response(JSON.stringify(body.operation === "jvm.threads" ? { threads, sampledMs: 200, cpuTimeEnabled: true, deadlocked: [] } : { thread: threads[1], text: "owner stack", frames: [] }));
  }));
  view(<JvmThreads agent="fixture" />);
  fireEvent.click(await screen.findByRole("button", { name: "owner" }));
  expect(await screen.findByText("owner stack")).toBeInTheDocument();
  expect(requests.filter(request => request.operation === "jvm.threads.stack")).toEqual([{ operation: "jvm.threads.stack", params: { id: 2 }, allowMutation: false }]);
});

it("compiles the selected loader's source before starting its leased redefine capture", async () => {
  window.history.replaceState(null, "", "/jvm/fixture/jvm.classes.source?class=Workload&loader=14dad5dc");
  const requests: Array<{ operation?: string; params?: Record<string, unknown>; duration?: string; traces?: unknown }> = [];
  let downloaded: Blob | undefined;
  vi.stubGlobal("URL", class extends URL {
    static createObjectURL(blob: Blob) { downloaded = blob; return "blob:class-source"; }
    static revokeObjectURL() {}
  });
  const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
  vi.stubGlobal("fetch", vi.fn(async (url: string, options: RequestInit) => {
    const body = JSON.parse(options.body as string) as typeof requests[number];
    requests.push(body);
    const values: Record<string, unknown> = {
      "jvm.loaders": [{ hash: "14dad5dc", label: "Application", classCount: 1, kind: "application" }],
      "jvm.classes": { total: 1, offset: 0, limit: 100, classes: [{ name: "Workload", loader: "14dad5dc" }] },
      "jvm.classes.source": classSource,
      "jvm.classes.compile": { success: true, diagnostics: [] },
    };
    return new Response(JSON.stringify(url === "/api/traces" ? { runId: "patch-run", mount: "/api/traces/patch-run", status: "running", dir: "fixture" } : values[body.operation! ]));
  }));
  function Harness() { return <RouterProvider adapter={useMemoryRouter("/jvm/fixture/jvm.classes.source")}><JvmClasses agent="fixture" /></RouterProvider>; }
  const { container } = view(<Harness />);
  await screen.findByRole("button", { name: "Edit source" });
  fireEvent.change(screen.getByLabelText("Jump to method"), { target: { value: "compute(J)D" } });
  await waitFor(() => expect(container.querySelector("pre")).toHaveTextContent("return Math.sqrt(l + 1L);"));
  expect(container.querySelector("pre")).not.toHaveTextContent("System.out.println");
  fireEvent.click(screen.getByRole("button", { name: "Download source" }));
  await waitFor(() => expect(downloaded).toBeInstanceOf(Blob));
  expect(await new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.onerror = () => reject(reader.error);
    reader.readAsText(downloaded!);
  })).toBe(classSource.source);
  expect(click).toHaveBeenCalledOnce();
  click.mockRestore();
  fireEvent.click(await screen.findByRole("button", { name: "Edit source" }));
  expect(screen.getByLabelText("Replacement source")).toHaveValue(classSource.source);
  fireEvent.change(screen.getByLabelText("Replacement source"), { target: { value: classSource.source.replace("return Math.sqrt(l + 1L);", "return 7.0;") } });
  const apply = screen.getByRole("button", { name: "Start redefinition" });
  fireEvent.click(screen.getByLabelText("Allow replacing class bytecode"));
  expect(apply).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "Compile preview" }));
  await waitFor(() => expect(apply).toBeEnabled());
  const replacement = classSource.source.replace("return Math.sqrt(l + 1L);", "return 8.0;");
  fireEvent.change(screen.getByLabelText("Replacement source"), { target: { value: replacement } });
  expect(apply).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "Compile preview" }));
  await waitFor(() => expect(apply).toBeEnabled());
  fireEvent.click(apply);
  await waitFor(() => expect(requests).toContainEqual({ duration: "15m", traces: { jvm_redefine: { enabled: true, target: { seam: "agent", agent: "fixture", allowMutation: true }, class: "Workload", loader: "14dad5dc", source: replacement } } }));
  expect(new URLSearchParams(window.location.search).has("source")).toBe(false);
});
