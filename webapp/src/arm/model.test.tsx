import { describe, expect, it } from "vitest";
import { START_FORM } from "./fixture";
import { EMPTY_DRAFT, addCapture, agentProbeDraft, buildStartRequest, captureAt, cliEquivalent, kindByName, readKinds, stepForError, validate, withOnlyAgent, type ArmDraft } from "./model";

const kinds = readKinds(START_FORM);
const kind = (name: string) => kindByName(kinds, name);
const captures: ArmDraft = { ...EMPTY_DRAFT, source: { kind: "captures" } };

describe("readKinds", () => {
  it("reads each kind's summary, seams without telltale's own, default seam and connection types from the start form", () => {
    expect(kinds.map(({ name, summary, seams, defaultSeam, connectionTypes, group }) => ({ name, summary, seams, defaultSeam, connectionTypes, group }))).toEqual([
      { name: "http", summary: "Record HTTP exchanges from an agent or a local proxy", seams: ["agent", "proxy"], defaultSeam: "proxy", connectionTypes: [], group: "traffic" },
      { name: "sql_top", summary: "Sample the queries a Postgres or SQL Server is running", seams: ["connection", "agent"], defaultSeam: "connection", connectionTypes: ["postgres", "sql_server"], group: "sql" },
      { name: "jvm_trace", summary: "Record method calls in a JVM", seams: ["agent"], defaultSeam: "agent", connectionTypes: [], group: "jvm" },
    ]);
  });

  it("refuses a kind that publishes no seams", () => {
    const traces = (START_FORM.schema as { properties: { traces: { properties: Record<string, Record<string, unknown>> } } }).properties.traces.properties;
    const { "x-telltale-seams": _seams, ...http } = traces.http!;
    expect(() => readKinds({ ...START_FORM, schema: { properties: { traces: { properties: { http } } } } })).toThrow("trace kind http publishes no x-telltale-seams");
  });
});

describe("buildStartRequest", () => {
  it("sends a kind armed twice as a list, one entry per capture, and one entry per chosen agent", () => {
    let draft = addCapture(captures, kind("sql_top"), { connection: "connection://mssql" });
    draft = addCapture(draft, kind("sql_top"), { connection: "connection://prod-backup" });
    draft = addCapture(draft, kind("jvm_trace"), { seam: "agent", agent: { mode: "id", agents: ["java/orders", "deployer/orders/shop/orders-0"] }, allowMutation: true, params: { targets: [{ class: "example.Worker", method: "compute" }] } });
    draft = { ...draft, run: { mode: "none" }, window: { ...draft.window, stop: "duration", duration: "5m" } };
    expect(buildStartRequest(draft, kinds)).toEqual({
      duration: "5m",
      traces: {
        sql_top: [
          { enabled: true, interval: "1s", connection: "connection://mssql" },
          { enabled: true, interval: "1s", connection: "connection://prod-backup" },
        ],
        jvm_trace: [
          { enabled: true, targets: [{ class: "example.Worker", method: "compute" }], target: { seam: "agent", agent: "java/orders", allowMutation: true } },
          { enabled: true, targets: [{ class: "example.Worker", method: "compute" }], target: { seam: "agent", agent: "deployer/orders/shop/orders-0", allowMutation: true } },
        ],
      },
    });
  });

  it("aims a proxy capture with its seam and leaves the command to the run", () => {
    const draft = { ...addCapture(captures, kind("http")), run: { mode: "command" as const, command: " make test " } };
    expect(buildStartRequest(draft, kinds)).toEqual({ command: "make test", traces: { http: { enabled: true, mode: "connect", target: { seam: "proxy" } } } });
    expect(cliEquivalent(draft, kinds)).toEqual({ command: `telltale trace run --trace 'http:{"mode":"connect","target":{"seam":"proxy"}}' -- make test` });
  });
});

describe("validate", () => {
  it("names each capture's missing target at its request pointer, and the command a proxy needs", () => {
    let draft = addCapture(captures, kind("sql_top"));
    draft = addCapture(draft, kind("sql_top"), { connection: "connection://mssql" });
    draft = addCapture(draft, kind("http"));
    expect(validate(draft, kinds)).toEqual([
      { instancePath: "/traces/sql_top/0/connection", message: "SQL top needs a postgres or sql_server connection.", captureId: "sql_top" },
      { instancePath: "/command", message: "A proxy capture needs the command whose traffic it routes." },
    ]);
  });

  it("opens an agent probe deep link armed on that agent, needing nothing else", () => {
    const draft = agentProbeDraft(kinds, "java/orders", "jvm_trace", { targets: [{ class: "A", method: "b" }] });
    expect(validate(draft, kinds)).toEqual([]);
    expect(() => agentProbeDraft(kinds, "java/orders", "sql_top")).not.toThrow();
  });
});

describe("withOnlyAgent", () => {
  const jvm = kind("jvm_trace");
  it("picks the only agent offering the probe, else the only deployed target when none is connected", () => {
    expect(withOnlyAgent(jvm, {}, ["52234"], ["deployer/checkout/shop/checkout-0"])).toEqual({ seam: "agent", agent: { mode: "id", agents: ["52234"] } });
    expect(withOnlyAgent(jvm, {}, [], ["deployer/checkout/shop/checkout-0"])).toEqual({ seam: "agent", agent: { mode: "id", agents: ["deployer/checkout/shop/checkout-0"] } });
  });
  it("leaves the choice open when several could run it, one is chosen already, or the capture is not on an agent", () => {
    expect(withOnlyAgent(jvm, {}, ["52234", "java/orders"], [])).toEqual({});
    expect(withOnlyAgent(jvm, {}, [], ["deployer/a/ns/p-0", "deployer/a/ns/p-1"])).toEqual({});
    const chosen = { seam: "agent" as const, agent: { mode: "id" as const, agents: ["java/orders"] } };
    expect(withOnlyAgent(jvm, chosen, ["52234"], [])).toEqual(chosen);
    expect(withOnlyAgent(kind("http"), { seam: "proxy" }, ["52234"], [])).toEqual({ seam: "proxy" });
  });
});

describe("refusals", () => {
  it("maps a server refusal of a listed entry back to its capture and step", () => {
    let draft = addCapture(captures, kind("sql_top"), { connection: "connection://a" });
    draft = addCapture(draft, kind("sql_top"), { connection: "connection://b" });
    expect(captureAt(draft, kinds, "/traces/sql_top/1/interval")).toBe("sql_top-2");
    expect(captureAt(draft, kinds, "/traces/http")).toBeUndefined();
    expect([ "/traces/sql_top/1", "/command", "/every", "/definition", "/somewhere"].map(instancePath => stepForError({ instancePath, message: "" }))).toEqual(["captures", "process", "window", "source", undefined]);
  });
});
