import { useState } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { RouterProvider, useMemoryRouter } from "@flanksource/clicky-ui/rpc";
import { afterEach, expect, it, vi } from "vitest";
import ArmPage from "../workspace/ArmPage";
import { START_FORM } from "./fixture";

afterEach(() => vi.unstubAllGlobals());

const JAVA_PID = 4242;
const NEW_AGENT = "java/orders-4242";

type Handler = (url: URL, init?: RequestInit) => Response | undefined;

/** A server with no agent connected until the install attaches one. */
function stubServer(extra: Handler = () => undefined) {
  let attached = false;
  const fetcher = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const url = new URL(String(input), window.location.origin);
    const own = extra(url, init);
    if (own) return own;
    switch (url.pathname) {
      case "/api/traces/start": return Response.json(START_FORM);
      case "/api/traces/connections": return Response.json({ connections: [{ reference: "connection://mssql", name: "mssql", type: "sql_server", source: "file:///connections/mssql.yaml" }] });
      case "/api/traces/definitions": return Response.json([]);
      case "/api/agents": return Response.json(attached ? [{ agentId: NEW_AGENT, service: "orders", connected: true, group: "other", runtime: { lang: "java", version: "21" }, resource: {}, labels: {}, probes: [{ name: "jvm_trace", title: "JVM methods" }] }] : []);
      case "/api/v1/jvm/deployers": return Response.json({ success: true, output: "[]\n" });
      case "/api/top/processes": return Response.json({ sampledAt: "", tick: 1, processes: [{ key: "4242", pid: JAVA_PID, ppid: 1, name: "java", command: "java -jar orders.jar", groupKey: "", origin: {} }] });
      case "/api/v1/agents/install/java":
        attached = true;
        return Response.json({ success: true, output: JSON.stringify({ pid: JAVA_PID, sha256: "abc", jar: "/opt/telltale/agent-abc/telltale-agent.jar", agent: NEW_AGENT, dryRun: false }) });
      case "/api/traces": return Response.json({ runId: "started-run", mount: "/api/traces/started-run", status: "running", dir: "" });
    }
    return new Response(`unexpected ${init?.method ?? "GET"} ${url.pathname}`, { status: 404 });
  });
  vi.stubGlobal("fetch", fetcher);
  return fetcher;
}

function renderArm(path = "/arm") {
  function Harness() {
    const router = useMemoryRouter(path);
    const [last, setLast] = useState(path);
    const adapter = { ...router, navigate: (to: string, opts?: { replace?: boolean }) => { setLast(to); router.navigate(to, opts); } };
    return <RouterProvider adapter={adapter}><output data-testid="path">{last}</output><ArmPage /></RouterProvider>;
  }
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><Harness /></QueryClientProvider>);
}

const posted = (fetcher: ReturnType<typeof stubServer>, path: string) =>
  fetcher.mock.calls.filter(([input, init]) => init?.method === "POST" && new URL(String(input), window.location.origin).pathname === path).map(([, init]) => JSON.parse(String(init?.body)) as unknown);

async function next() {
  fireEvent.click(screen.getByRole("button", { name: /Next/ }));
}

it("deploys a Java agent into a local JVM from a capture's Add menu and starts the run on it", { timeout: 30000 }, async () => {
  const fetcher = stubServer();
  renderArm();
  fireEvent.click(await screen.findByRole("button", { name: /Choose captures/ }));
  fireEvent.click(await screen.findByRole("button", { name: "Add JVM methods" }));
  fireEvent.click(await screen.findByText("Deploy an agent…"));

  const dialog = await screen.findByRole("dialog", { name: "Deploy an agent" });
  const process = within(dialog).getByRole("combobox", { name: /Process/ });
  fireEvent.focus(process);
  fireEvent.change(process, { target: { value: "java" } });
  // The picker searches after its debounce, so the option takes longer than the default wait.
  fireEvent.mouseDown(await screen.findByRole("option", { name: new RegExp(`java \\(${JAVA_PID}\\)`) }, { timeout: 3000 }));
  fireEvent.change(within(dialog).getByLabelText(/Service/), { target: { value: "orders" } });
  fireEvent.change(within(dialog).getByLabelText(/Agent directory/), { target: { value: "/opt/telltale" } });
  fireEvent.click(within(dialog).getByRole("button", { name: "Attach agent" }));

  await waitFor(() => expect(screen.queryByRole("dialog", { name: "Deploy an agent" })).not.toBeInTheDocument());
  expect(posted(fetcher, "/api/v1/agents/install/java")).toEqual([{ destination: "local", pid: JAVA_PID, directory: "/opt/telltale", service: "orders", "allow-mutation": false, format: "json" }]);

  await next();
  expect(await screen.findByRole("alert")).toHaveTextContent("Methods is required.");
  const target = { class: "example.OrderService", method: "submit" };
  fireEvent.click(screen.getByRole("button", { name: /^JVM methods/ }));
  fireEvent.click(screen.getByRole("button", { name: "View options for Methods" }));
  fireEvent.click(await screen.findByRole("menuitem", { name: "Stack form" }));
  fireEvent.click(await screen.findByRole("button", { name: "Add item" }));
  fireEvent.change(await screen.findByRole("textbox", { name: /Class/ }), { target: { value: target.class } });
  fireEvent.change(screen.getByRole("textbox", { name: /Method/ }), { target: { value: target.method } });
  await next();
  await next();
  await next();
  fireEvent.click(await screen.findByRole("button", { name: /Start run/ }));
  await waitFor(() => expect(screen.getByTestId("path")).toHaveTextContent("/?run=started-run"));
  expect(posted(fetcher, "/api/traces")).toEqual([{ traces: { jvm_trace: { enabled: true, target: { seam: "agent", agent: NEW_AGENT }, targets: [target] } } }]);
});

it("shows the server's refusal of one listed capture on that capture once Fix sends the user back", { timeout: 30000 }, async () => {
  stubServer((url, init) => url.pathname === "/api/traces" && init?.method === "POST"
    ? Response.json({ errors: [{ instancePath: "/traces/sql_top/1/interval", message: "must be a duration" }] }, { status: 400 })
    : undefined);
  renderArm();
  fireEvent.click(await screen.findByRole("button", { name: /Choose captures/ }));
  for (let armed = 0; armed < 2; armed++) {
    fireEvent.click(await screen.findByRole("button", { name: "Add SQL top" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /Saved connection/ }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /mssql/ }));
  }
  await next();
  await next();
  await next();
  fireEvent.click(await screen.findByRole("button", { name: /Start run/ }));
  fireEvent.click(await screen.findByRole("button", { name: "Fix in Captures" }));
  expect(await screen.findByText("must be a duration")).toBeInTheDocument();
  expect(screen.getAllByRole("alert").map(alert => alert.textContent)).toEqual(["must be a duration"]);
});

it("arms a backend from the quick picker, edits its options, and starts through a saved connection", async () => {
  const fetcher = stubServer(url => url.pathname === "/api/traces/connections" ? Response.json({
    connections: [
      { reference: "connection://orders-logs", name: "orders-logs", type: "loki" },
      { reference: "connection://inventory", name: "inventory", type: "postgres" },
    ],
  }) : undefined);
  renderArm();
  fireEvent.click(await screen.findByRole("button", { name: /Choose captures/ }));
  fireEvent.click(screen.getByRole("button", { name: "Add trace" }));
  const menu = await screen.findByRole("dialog", { name: "Add a trace" });
  expect(within(menu).getByRole("button", { name: /BigQuery/ })).toBeDisabled();
  fireEvent.click(within(menu).getByRole("button", { name: /Loki/ }));
  await next();
  expect(await screen.findByRole("alert")).toHaveTextContent("Loki needs a saved connection");
  fireEvent.focus(screen.getByRole("combobox", { name: "loki connection" }));
  expect(screen.queryByRole("option", { name: /inventory/ })).not.toBeInTheDocument();
  fireEvent.mouseDown(await screen.findByRole("option", { name: /orders-logs/ }));
  fireEvent.click(screen.getByRole("button", { name: /Loki A LogQL stream selector/ }));
  const query = '{app="orders"}';
  fireEvent.change(screen.getByRole("textbox", { name: /LogQL query/ }), { target: { value: query } });
  await next();
  await next();
  await next();
  expect(await screen.findByText("orders-logs")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: /Start run/ }));
  await waitFor(() => expect(screen.getByTestId("path")).toHaveTextContent("/?run=started-run"));
  expect(posted(fetcher, "/api/traces")).toEqual([{
    traces: { logs: { enabled: true, target: { seam: "host" }, source: "cloud", backend: "loki", connection: "connection://orders-logs", query } },
  }]);
});

it("creates and selects a connection in the wizard while preserving the capture options", async () => {
  const open = vi.fn();
  vi.stubGlobal("open", open);
  const connection = { name: "orders-logs", type: "loki", url: "https://logs.example.test" };
  let saved = false;
  const fetcher = stubServer((url, init) => {
    if (url.pathname === "/api/traces/connections") {
      return Response.json({ connections: saved ? [{ ...connection, reference: "connection://orders-logs" }] : [] });
    }
    if (url.pathname === "/api/v1/connection") {
      if (init?.method === "POST") {
        saved = true;
        return Response.json(connection, { status: 201 });
      }
      return Response.json({
        type: "object", required: ["name", "type", "url"], properties: {
          name: { type: "string", title: "Name" },
          type: { type: "string", title: "Type", enum: ["loki"] },
          url: { type: "string", title: "URL" },
        },
      }, { headers: { "Content-Type": "application/schema+json" } });
    }
    if (url.pathname === "/api/v1/connections/contexts") return Response.json({ contexts: [] });
  });
  renderArm();
  fireEvent.click(await screen.findByRole("button", { name: /Choose captures/ }));
  fireEvent.click(screen.getByRole("button", { name: "Add Loki" }));
  fireEvent.click(await screen.findByRole("menuitem", { name: "Choose later" }));
  fireEvent.click(screen.getByRole("button", { name: /Loki A LogQL stream selector/ }));
  const query = screen.getByRole("textbox", { name: /LogQL query/ });
  fireEvent.change(query, { target: { value: '{app="orders"}' } });
  fireEvent.click(screen.getByRole("button", { name: "Add loki connection" }));

  const dialog = await screen.findByRole("dialog", { name: /New Loki connection/i });
  fireEvent.change(within(dialog).getByRole("textbox", { name: /Name/ }), { target: { value: connection.name } });
  fireEvent.change(within(dialog).getByRole("textbox", { name: /URL/ }), { target: { value: connection.url } });
  fireEvent.click(within(dialog).getByRole("button", { name: "Create and select" }));

  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  expect(await screen.findByRole("combobox", { name: "loki connection" })).toHaveValue(connection.name);
  expect(query).toHaveValue('{app="orders"}');
  expect(screen.getByTestId("path")).toHaveTextContent("/arm");
  expect(posted(fetcher, "/api/v1/connection")).toEqual([connection]);
  expect(open).not.toHaveBeenCalled();
});

it("opens a type chooser from the saved connection picker and keeps the selection on cancel", async () => {
  stubServer();
  renderArm();
  fireEvent.click(await screen.findByRole("button", { name: /Choose captures/ }));
  fireEvent.click(screen.getByRole("button", { name: "Add SQL top" }));
  fireEvent.click(await screen.findByRole("menuitem", { name: /Saved connection/ }));
  fireEvent.click(await screen.findByRole("menuitem", { name: /mssql/ }));
  const picker = screen.getByRole("combobox", { name: "Postgres or SQL Server connection" });
  fireEvent.focus(picker);
  fireEvent.click(await screen.findByRole("button", { name: "Add a Postgres or SQL Server connection" }));

  const dialog = await screen.findByRole("dialog", { name: "Choose a connection type" });
  expect(within(dialog).getByRole("button", { name: "Postgres" })).toBeInTheDocument();
  expect(within(dialog).getByRole("button", { name: "SQL Server" })).toBeInTheDocument();
  fireEvent.click(within(dialog).getByRole("button", { name: "Close" }));

  expect(picker).toHaveValue("mssql");
  expect(screen.getByTestId("path")).toHaveTextContent("/arm");
});
