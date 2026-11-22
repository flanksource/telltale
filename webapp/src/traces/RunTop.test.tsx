import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { RunTop } from "./RunTop";
import { TopRowDetail } from "./TopRowDetail";
import type { RunTraces } from "./api";
import type { ClickyDocument } from "@flanksource/clicky-ui/clicky";

const frame = { id: "sample-one", source: "database", startedAt: "2026-11-11T10:00:00Z", completedAt: "2026-11-11T10:00:01Z", observed: 150, retained: 150, limit: 200 };
const run: RunTraces = {runId:"sample-run", mount:"/api/traces/sample-run", dir:"sample-run", status:"succeeded", captures:[{kind:"sql_top",ref:{kind:"sql_top",stream:"sql-stream"}}]};
const table: ClickyDocument = {version:1,node:{kind:"table",columns:[{name:"seq",defaultHidden:true},{name:"sessionId",label:"Session",filterKey:"filter.sessionId",sortKey:"sessionId"}],rows:[{cells:{seq:{kind:"text",text:"1",filterValue:1},sessionId:{kind:"text",text:"session 12",filterValue:12}},detail:{kind:"text",text:"SELECT name FROM example_table"}}]}};
let requests: URL[];

beforeEach(()=>{
  requests=[];
  window.history.replaceState(null,"","/?viewer=1&feedSort=timestamp#capture");
  vi.stubGlobal("fetch",vi.fn(async(input:string, init?:RequestInit)=>{
    const url=new URL(input,window.location.origin); requests.push(url);
    if(url.pathname.endsWith("/kinds")) return Response.json([{name:"sql_top",title:"SQL",exportFormats:[],top:{identity:["sessionId"],metrics:[]}}]);
    if(url.searchParams.get("view")==="frames") return Response.json([frame]);
    if((init?.headers as Record<string,string>)?.Accept==="application/schema+json") return Response.json({columns:[{name:"sessionId",label:"Session",type:"number",filter:{kind:"terms",key:"filter.sessionId"}}]});
    const empty=!!url.searchParams.get("q");
    return Response.json({selection:frame.id,frame,metrics:[],rows:empty?[]:[{seq:1,sessionId:12}],count:empty?0:150,nextCursor:empty||url.searchParams.has("cursor")?"":"next-page",table:empty?{...table,node:{...table.node,rows:[]}}:table});
  }));
});
afterEach(()=> { vi.useRealTimers(); vi.unstubAllGlobals(); });
function renderTop(currentRun = run){return render(<QueryClientProvider client={new QueryClient({defaultOptions:{queries:{retry:false}}})}><RunTop run={currentRun}/></QueryClientProvider>);}

describe("run Top integration",()=>{
  it("updates from samples over SSE and does not poll JSON while live", async () => {
    const opened: EventTarget[] = [];
    const close = vi.fn();
    vi.stubGlobal("EventSource", class extends EventTarget {
      constructor(readonly url: string) { super(); opened.push(this); }
      close() { close(); }
    });
    renderTop({ ...run, status: "running" });
    await waitFor(() => expect(opened).toHaveLength(1));
    expect(opened[0]).toHaveProperty("url", expect.stringContaining("&follow=1"));
    act(() => opened[0]!.dispatchEvent(new MessageEvent("message", { data: JSON.stringify({ reset: true, frames: [frame], result: { selection: frame.id, frame, rows: [{ seq: 1, sessionId: 12 }], count: 1, table } }) })));
    expect(await screen.findByText("session 12")).toBeInTheDocument();
    await waitFor(() => expect(requests.some(url => url.searchParams.get("view") === "top")).toBe(true));
    const count = requests.length;
    vi.useFakeTimers();
    await act(async () => vi.advanceTimersByTimeAsync(4500));
    expect(requests).toHaveLength(count);
    vi.useRealTimers();
    fireEvent.click(screen.getByText("session 12"));
    expect(await screen.findByRole("dialog")).toHaveTextContent("SELECT name");
    expect(close).toHaveBeenCalledOnce();
  });
  it("puts SQL Server rollback progress on the table and keeps its filters available with zero results", async () => {
    const rollbackFrame = { ...frame, summary: { engine: "sqlserver" } };
    const rollbackSQL = "UPDATE orders\nSET paid = 1";
    const samples: Record<string, string | number>[] = [
      { seq: 1, sessionId: 12, engine: "sqlserver", sql: rollbackSQL, command: "KILLED/ROLLBACK", elapsedMs: 360_000, percentComplete: 0, estimatedRemainingMs: 120_000 },
      { seq: 2, sessionId: 13, engine: "sqlserver", sql: "SELECT id FROM orders", command: "SELECT", elapsedMs: 90_000 },
    ];
    const columns = [
      ...table.node.columns!, { name: "engine", label: "Engine" }, { name: "traceId", label: "Trace ID" }, { name: "command", label: "Command" },
      { name: "elapsedMs", label: "Elapsed Ms" }, { name: "estimatedRemainingMs" }, { name: "percentComplete", label: "Progress" },
      { name: "transactionLogBytesUsed", label: "Transaction log used" }, { name: "sql", label: "SQL" },
    ];
    const sqlTable: ClickyDocument = { ...table, node: { ...table.node, columns, rows: samples.map(sample => ({
      cells: Object.fromEntries(columns.map(({ name }) => {
        const value = sample[name];
        return [name, { kind: "text" as const, text: value === undefined ? "" : String(value), ...(value === undefined ? {} : { filterValue: value }) }];
      })),
      detail: { kind: "text", children: [
        { kind: "code", language: "sql", source: String(sample.sql) },
        { kind: "text", text: `Command ${sample.command}` },
      ] },
    })) } };
    const baseFetch = globalThis.fetch;
    vi.stubGlobal("fetch", vi.fn(async (input: string, init?: RequestInit) => {
      const url = new URL(input, window.location.origin);
      if (url.pathname.endsWith("/kinds")) return baseFetch(input, init);
      if (url.searchParams.get("view") === "frames") return Response.json([rollbackFrame]);
      if ((init?.headers as Record<string, string>)?.Accept === "application/schema+json") return Response.json({ columns: columns.map(column => ({
        ...column, filter: column.name === "command" ? { key: "filter.command", kind: "terms", lookup: true } :
          { key: `filter.${column.name}`, kind: column.name === "percentComplete" ? "range" : "exact" },
      })) });
      const empty = !!url.searchParams.get("q");
      return Response.json({ selection: frame.id, frame: rollbackFrame, rows: empty ? [] : samples, count: empty ? 0 : samples.length,
        table: empty ? { ...sqlTable, node: { ...sqlTable.node, rows: [] } } : sqlTable });
    }));
    renderTop();
    expect(await screen.findByRole("progressbar", { name: "Rollback progress" })).toHaveAttribute("value", "0");
    expect(screen.queryAllByRole("checkbox", { name: /^Select row / })).toHaveLength(0);
    expect(screen.getAllByRole("progressbar", { name: /progress/i })).toHaveLength(1);
    expect(screen.getByText("6 min", { exact: true })).toBeInTheDocument();
    expect(screen.getByText("2 min", { exact: true })).toBeInTheDocument();
    const statement = screen.getByRole("columnheader", { name: /SQL statement/ });
    const command = screen.getByRole("columnheader", { name: /Command/ });
    expect(statement.compareDocumentPosition(command) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(screen.queryByRole("columnheader", { name: /Trace ID/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "Transaction log used" })).not.toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Command" })).toBeInTheDocument();
    fireEvent.change(screen.getByRole("searchbox", { name: "Search sample" }), { target: { value: "missing" } });
    await waitFor(() => expect(screen.queryByRole("progressbar", { name: "Rollback progress" })).not.toBeInTheDocument());
    expect(screen.getByRole("button", { name: "Progress filter" })).toBeInTheDocument();
    fireEvent.change(screen.getByRole("searchbox", { name: "Search sample" }), { target: { value: "" } });
    fireEvent.click(await screen.findByRole("row", { name: /KILLED\/ROLLBACK/ }));
    const dialog = await screen.findByRole("dialog", { name: "SQL statement" });
    expect(within(dialog).getByRole("button", { name: "Copy code" })).toBeInTheDocument();
    expect(dialog.textContent).toMatch(/UPDATE orders\s+SET paid = 1[\s\S]*Command KILLED\/ROLLBACK/);
  });
  it("pins paging to a completed sample and stores stable row selection in the URL",async()=>{
    renderTop();
    await screen.findByText("session 12");
    fireEvent.click(screen.getByRole("button",{name:"Next page"}));
    await waitFor(()=>expect(requests.some(url=>url.searchParams.get("cursor")==="next-page"&&url.searchParams.get("frame")===frame.id)).toBe(true));
    fireEvent.click(await screen.findByText("session 12"));
    expect(await screen.findByRole("dialog")).toHaveTextContent("SELECT name");
    await waitFor(()=>expect(new URLSearchParams(window.location.search).get("sampleSelected")).toBe("[12]"));
    expect(new URLSearchParams(window.location.search).get("sampleFrame")).toBe(frame.id);
    expect(window.location.hash).toBe("#capture");
  });
  it("restores zero-result search from navigation and keeps the native filter controls available",async()=>{
    renderTop();
    await screen.findByText("session 12");
    act(()=>{
      window.history.pushState(null,"","/?viewer=1&sampleSearch=absent&sampleFilter=filter.sessionId%3D12#capture");
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
    await waitFor(()=>expect(screen.queryByText("session 12")).not.toBeInTheDocument());
    expect(screen.getByRole("searchbox",{name:"Search sample"})).toHaveValue("absent");
    expect(await screen.findByText("150 retained of 150 observed",{exact:false})).toBeInTheDocument();
    expect(requests.some(url=>url.searchParams.get("q")==="absent"&&url.searchParams.get("filter")==="sessionId=12")).toBe(true);
  });
  it("reports an omitted blocker without leaving the selected completed sample",async()=>{
    vi.stubGlobal("fetch",vi.fn(async(input:string)=>{requests.push(new URL(input,window.location.origin));return Response.json({count:0,table});}));
    render(<TopRowDetail raw={{seq:1,blockedBy:99}} table={table} mount={run.mount} capture={run.captures![0]!} selection={frame.id} relation={{column:"blockedBy",target:"sessionId",label:"Blocker"}}/>);
    fireEvent.click(screen.getByRole("button",{name:"Open blocker 99"}));
    expect(await screen.findByRole("status")).toHaveTextContent("Blocker 99 was omitted from this completed sample.");
    expect(requests[0]?.searchParams.get("frame")).toBe(frame.id);
    expect(requests[0]?.searchParams.get("filter")).toBe("sessionId=99");
  });
  it("shows a newer incomplete observation while retaining the last complete table",async()=>{
    const initial = { ...frame, tick: 1 };
    const incomplete = { ...frame, id: "incomplete", tick: 2, summary: { profileId: "profile-2", complete: false, dropped: 3 } };
    vi.stubGlobal("EventSource", class extends EventTarget {
      constructor() {
        super();
        queueMicrotask(() => {
          const result = { selection: initial.id, frame: initial, rows: [{ seq: 1, sessionId: 12 }], count: 1, table };
          this.dispatchEvent(new MessageEvent("message", { data: JSON.stringify({ reset: true, frames: [initial], result }) }));
          this.dispatchEvent(new MessageEvent("message", { data: JSON.stringify({ frames: [incomplete] }) }));
        });
      }
      close() {}
    });
    renderTop({ ...run, status: "running" });
    expect(await screen.findByText(/Incomplete snapshot/)).toHaveTextContent("3 observations dropped");
    expect(await screen.findByText("session 12")).toBeInTheDocument();
  });
  it("selects string operation identities from raw values without requiring rendered filter metadata",async()=>{
    const originalFetch = globalThis.fetch;
    vi.stubGlobal("fetch",vi.fn(async(input:string,init?:RequestInit)=>{
      const response=await originalFetch(input,init);
      const body=await response.json();
      if(body.table){
        body.rows=[{seq:1,sessionId:"request-a"}];
        body.table.node.rows[0].cells.sessionId={kind:"text",text:"request-a"};
        delete body.table.node.rows[0].detail;
      }
      return Response.json(body);
    }));
    renderTop();
    fireEvent.click(await screen.findByText("request-a"));
    await waitFor(()=>expect(new URLSearchParams(window.location.search).get("sampleSelected")).toBe('["request-a"]'));
    expect(screen.getByRole("dialog")).toHaveTextContent(/sessionId:\s*"request-a"/);
  });
});
