import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
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
afterEach(()=>vi.unstubAllGlobals());
function renderTop(){return render(<QueryClientProvider client={new QueryClient({defaultOptions:{queries:{retry:false}}})}><RunTop run={run}/></QueryClientProvider>);}

describe("run Top integration",()=>{
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
    const originalFetch = globalThis.fetch;
    vi.stubGlobal("fetch", vi.fn(async(input:string, init?:RequestInit)=>{
      if(new URL(input,window.location.origin).searchParams.get("view")==="frames") return Response.json([
        {...frame,tick:1},
        {...frame,id:"incomplete",tick:2,summary:{profileId:"profile-2",complete:false,dropped:3}},
      ]);
      return originalFetch(input,init);
    }));
    render(<QueryClientProvider client={new QueryClient({defaultOptions:{queries:{retry:false}}})}><RunTop run={{...run,status:"running"}}/></QueryClientProvider>);
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
