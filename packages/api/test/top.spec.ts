import { execFileSync, spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { once } from "node:events";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { afterAll, beforeAll, describe, expect, it } from "vitest";

let testDir: string;
let server: ChildProcessWithoutNullStreams;
let apiURL: string;

function serve(): Promise<string> {
  server = spawn(".bin/telltale", ["serve", "--port", "0", "--data-dir", testDir], { env: { ...process.env, XDG_CONFIG_HOME: join(testDir, "config") } });
  return new Promise<string>((resolve, reject) => {
    let output = "";
    server.stdout.on("data", (chunk: Buffer) => {
      output += chunk.toString();
      const match = /Telltale API: (http:\/\/\S+)/.exec(output);
      if (match) resolve(match[1]);
    });
    server.once("error", reject);
    server.stderr.on("data", (chunk: Buffer) => { output += chunk.toString(); });
    server.once("exit", (code) => reject(new Error(`Server exited (${code}): ${output}`)));
  });
}

beforeAll(async () => {
  await mkdir(".tmp", { recursive: true });
  testDir = await mkdtemp(resolve(".tmp", "telltale-top-"));
  const connections = join(testDir, "config", "flanksource", "connections");
  await mkdir(connections, { recursive: true });
  await writeFile(join(connections, "warehouse.yaml"), "apiVersion: mission-control.flanksource.com/v1\nkind: Connection\nmetadata: {name: warehouse, namespace: tenant}\nspec: {postgres: {url: {value: 'postgres://localhost/example'}}}\n");
  await writeFile(join(connections, "metrics.yaml"), "apiVersion: mission-control.flanksource.com/v1\nkind: Connection\nmetadata: {name: metrics}\nspec: {type: prometheus, url: {value: 'http://localhost:9090'}}\n");
  apiURL = await serve();
});

afterAll(async () => {
  if (server?.exitCode === null) {
    const stopped = once(server, "exit");
    server.kill("SIGTERM");
    expect(await stopped).toEqual([0, null]);
  }
  await rm(testDir, { recursive: true, force: true });
});

interface Usage {
  processes: number;
  rssBytes: number;
  swapBytes?: number;
  compressedBytes?: number;
  hogs?: Record<string, string>;
}

describe("completed sample records",()=>{
  it("publishes the same SQL connection chooser for CLI operation and web start forms",async()=>{
    const response=await fetch(`${apiURL}/api/openapi.json`);
    expect(response.status).toBe(200);
    const api=await response.json() as {paths:Record<string,{post:{requestBody:{content:{"application/json":{schema:{properties:Record<string,unknown>}}}}};get:{parameters:{name:string}[]}}>};
    expect(api.paths["/api/v1/run/sql_top"]?.post.requestBody.content["application/json"].schema.properties.connection).toMatchObject({title:"Connection","x-clicky-lookup":{url:"/api/v1/connection?types=postgres,sql_server",filter:"connection"}});
    expect(api.paths["/api/v1/connection"]?.get.parameters.map(parameter=>parameter.name)).toEqual(expect.arrayContaining(["types","__lookup","__lookup_filter","__lookup_q"]));
    const form=await (await fetch(`${apiURL}/api/traces/start`)).json() as {schema:{properties:{traces:{properties:Record<string,{allOf:{then:{properties:Record<string,unknown>}}[]}>}}}};
    expect(form.schema.properties.traces.properties.sql_top?.allOf[0]?.then.properties.connection).toMatchObject({title:"Connection","x-clicky-lookup":{url:"/api/v1/connection?types=postgres,sql_server",filter:"connection"}});
    const choices=await fetch(`${apiURL}/api/v1/connection?types=postgres,sql_server&__lookup=filters`);
    expect(choices.status).toBe(200);
    expect(await choices.json()).toMatchObject({filters:{connection:{options:{"connection://tenant/warehouse":{plain:"warehouse (postgres)"}},total:1}}});
  });
  it("uses captured query columns and applies native filters and order before paging",async()=>{
    const saved=await fetch(`${apiURL}/api/traces/definitions/sample-top-test`,{method:"PUT",headers:{"Content-Type":"application/json"},body:JSON.stringify({layer:"global",resourceVersion:"",definition:{apiVersion:"telltale.flanksource.com/v1alpha1",kind:"TraceDefinition",metadata:{name:"sample-top-test"},spec:{collect:{query:{sql:[{type:"sqlite",url:":memory:",query:"WITH RECURSIVE valueset(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM valueset WHERE n<150) SELECT n FROM valueset"}]}}}}})});
    expect(saved.status).toBe(200);
    const started=await fetch(`${apiURL}/api/traces`,{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({duration:"500ms",definition:"sample-top-test"})});
    expect(started.status).toBe(201);
    const run=await started.json() as {mount:string;runId:string};
    await expect.poll(async()=>((await (await fetch(`${apiURL}/api/traces`)).json()) as {runs:{runId:string;status:string}[]}).runs.find(item=>item.runId===run.runId)?.status,{timeout:10000}).toBe("succeeded");
    const endpoint=`${apiURL}${run.mount}/traces/query/records`;
    const frames=await (await fetch(`${endpoint}?view=frames`)).json() as {id:string;retained:number}[];
    expect(frames).toHaveLength(1);expect(frames[0]?.retained).toBe(150);
    const params=new URLSearchParams({view:"top",filter:"n=>148",sort:"n",desc:"true",limit:"1"});
    const response=await fetch(`${endpoint}?${params}`);const payload=await response.text();expect(response.status,payload).toBe(200);
    const page=JSON.parse(payload) as {selection:string;rows:{n:number}[];count:number;nextCursor:string};
    expect(page.count).toBe(2);expect(page.rows[0]?.n).toBe(150);
    params.set("cursor",page.nextCursor);params.set("frame",page.selection);
    const next=await (await fetch(`${endpoint}?${params}`)).json() as {rows:{n:number}[]};
    expect(next.rows.map(row=>row.n)).toEqual([149]);
    const empty=await (await fetch(`${endpoint}?view=top&filter=n%3D%3C0`)).json() as {rows:unknown[];frame:{retained:number}};
    expect(empty.rows).toEqual([]);expect(empty.frame.retained).toBe(150);
    const schema=await fetch(`${endpoint}?view=top`,{headers:{Accept:"application/schema+json"}});
    expect(schema.status).toBe(200);expect(await schema.json()).toMatchObject({columns:expect.arrayContaining([expect.objectContaining({name:"n",type:"number"})])});
  });
});

interface Frame {
  sampledAt: string;
  tick: number;
  groups: Array<{ key: string; name: string; kind: string; pid: number; context: { repo?: string; gitCommonDir?: string }; self: Usage; total: Usage }>;
}

describe("live process groups", () => {
  it("reports platform memory columns without substituting compressed memory for disk swap", async () => {
    const response = await fetch(`${apiURL}/api/top/processes?pid=${server.pid}&all=true`);
    expect(response.status).toBe(200);
    const reading = await response.json() as { processes: Array<Usage & { pid: number }> };
    const own = reading.processes.find(process => process.pid === server.pid);
    expect(own?.rssBytes).toBeGreaterThan(0);
    if (process.platform === "darwin") {
      expect(own?.compressedBytes).toBeGreaterThanOrEqual(0);
      expect(own?.swapBytes).toBeUndefined();
    } else if (process.platform === "linux") {
      expect(own?.swapBytes).toBeGreaterThanOrEqual(0);
      expect(own?.compressedBytes).toBeUndefined();
    }
  });

  it("carries Git identity in raw frames without adding presentation buckets", async () => {
    const response = await fetch(`${apiURL}/api/top`);
    expect(response.status).toBe(200);
    const frame = await response.json() as Frame;
    const ownGroup = frame.groups.find(group => group.pid === server.pid);
    const gitCommonDir = resolve(execFileSync("git", ["rev-parse", "--git-common-dir"], { encoding: "utf8" }).trim());
    expect(ownGroup?.context).toMatchObject({ repo: resolve("."), gitCommonDir });
    expect(frame.groups.some(group => group.kind === "bucket")).toBe(false);
  });

  it("serves the latest frame of the host's groups, including the processes no tool owns", async () => {
    const response = await fetch(`${apiURL}/api/top?all=true`);
    expect(response.status).toBe(200);
    expect(response.headers.get("content-type")).toBe("application/json");
    const frame = await response.json() as Frame;
    expect(frame.tick).toBeGreaterThanOrEqual(1);
    expect(frame.groups.length).toBeGreaterThan(0);
    // The test runner itself is a process of the host: some group counts it, with memory.
    expect(frame.groups.some(group => group.total.processes > 0 && group.total.rssBytes > 0)).toBe(true);
  });

  it("refuses a query it cannot read as true or false", async () => {
    const response = await fetch(`${apiURL}/api/top?all=maybe`);
    expect(response.status).toBe(400);
    expect(await response.text()).toContain('all="maybe" is not true or false');
  });

  it("refuses any method but GET", async () => {
    const response = await fetch(`${apiURL}/api/top`, { method: "POST" });
    expect(response.status).toBe(405);
    expect(response.headers.get("allow")).toBe("GET");
  });
});

describe("processes of a program or a process", () => {
  it("serves a process and the ones under it by pid", async () => {
    const response = await fetch(`${apiURL}/api/top/processes?pid=1&all=true`);
    expect(response.status).toBe(200);
    expect(response.headers.get("content-type")).toBe("application/json");
    const reading = await response.json() as { tick: number; processes: Array<{ pid: number; ppid: number; name: string; origin: object; rssBytes: number }> };
    expect(reading.tick).toBeGreaterThanOrEqual(1);
    expect(reading.processes.map(process => process.pid)).toContain(1);
  });

  it("serves the processes whose pid holds a search text", async () => {
    const response = await fetch(`${apiURL}/api/top/processes?q=1&all=true`);
    expect(response.status).toBe(200);
    const reading = await response.json() as { processes: Array<{ pid: number }> };
    expect(reading.processes.map(process => process.pid)).toContain(1);
  });

  it("refuses a request that names both a program and a process", async () => {
    const response = await fetch(`${apiURL}/api/top/processes?name=x&pid=1`);
    expect(response.status).toBe(400);
  });

  it("refuses a request that names neither", async () => {
    const response = await fetch(`${apiURL}/api/top/processes`);
    expect(response.status).toBe(400);
  });
});

describe("facets of a process", () => {
  interface Facets {
    pid: number;
    name: string;
    context: { cwd?: string };
    environment?: Record<string, string>;
    descriptors: Record<"openFiles" | "sockets" | "ports" | "connections", Array<{ fd: string; type: string; port?: number }>>;
  }

  it("serves the server's own facets: the port it listens on, its open files and its environment", async () => {
    const response = await fetch(`${apiURL}/api/top/facets?pid=${server.pid}&all=true`);
    expect(response.status).toBe(200);
    expect(response.headers.get("content-type")).toBe("application/json");
    const facets = await response.json() as Facets;
    expect(facets.pid).toBe(server.pid);
    expect(facets.name).toBe("telltale");
    expect(facets.descriptors.ports.map(port => port.port)).toContain(Number(new URL(apiURL).port));
    expect(facets.descriptors.openFiles.length).toBeGreaterThan(0);
    expect(Object.keys(facets.environment ?? {})).toContain("PATH");
  });

  it("answers 404 for a process the latest frame did not list", async () => {
    const response = await fetch(`${apiURL}/api/top/facets?pid=4194304`);
    expect(response.status).toBe(404);
    expect(await response.text()).toContain("not listed at the latest tick");
  });

  it("refuses a request without a pid", async () => {
    const response = await fetch(`${apiURL}/api/top/facets`);
    expect(response.status).toBe(400);
  });
});

describe("client routes", () => {
  it("serves the app page at a program's /top route, its name dotted", async () => {
    const response = await fetch(`${apiURL}/top/com.apple.WebKit.Networking`);
    expect(response.status).toBe(200);
    expect(response.headers.get("content-type")).toContain("text/html");
    expect(await response.text()).toContain('<div id="root">');
  });
});

describe("process actions", () => {
  it("renices and terminates a disposable child by its start-qualified identity", async () => {
    const child = spawn("sleep", ["60"]);
    const exited = once(child, "exit");
    try {
      let key = "";
      await expect.poll(async () => {
        const response = await fetch(`${apiURL}/api/top/processes?pid=${child.pid}&all=true`);
        const reading = await response.json() as { processes: Array<{ pid: number; key: string }> };
        key = reading.processes.find(p => p.pid === child.pid)?.key ?? "";
        return key;
      }, { timeout: 10000 }).toContain("@");
      const act = (body: object) => fetch(`${apiURL}/api/top/action`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
      expect((await act({ key: `${key}0`, action: "terminate" })).status).toBe(409);
      expect((await act({ key, action: "renice", nice: 20 })).status).toBe(400);
      expect((await act({ key, action: "renice", nice: 10 })).status).toBe(204);
      expect((await act({ key, action: "terminate" })).status).toBe(204);
      expect(await exited).toEqual([null, "SIGTERM"]);
    } finally {
      if (child.exitCode === null && child.signalCode === null) child.kill("SIGKILL");
      await exited;
    }
  });

  it("rejects cross-origin actions, unsafe payloads and non-POST methods", async () => {
    const actionURL = `${apiURL}/api/top/action`;
    expect((await fetch(actionURL)).status).toBe(405);
    expect((await fetch(actionURL, { method: "POST", body: "{}" })).status).toBe(415);
    expect((await fetch(actionURL, { method: "POST", headers: { "Content-Type": "application/json", Origin: "https://untrusted.example" }, body: "{}" })).status).toBe(403);
    for (const body of ['{"key":"1@1","action":"terminate"}', '{"key":"123@1","action":"kill"}', '{} {}', '{"extra":1}']) {
      expect((await fetch(actionURL, { method: "POST", headers: { "Content-Type": "application/json" }, body })).status).toBe(400);
    }
  });
});
