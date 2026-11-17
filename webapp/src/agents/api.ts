import { useQuery } from "@tanstack/react-query";
import { failure } from "../traces/api";

interface AgentIdentity {
  agentId: string;
  group: string;
  resource: Record<string, string>;
}

/** A connected agent with its live handshake. */
export interface Agent extends AgentIdentity {
  connected: true;
  service: string;
  runtime: { lang: string; version: string };
  labels: Record<string, string>;
  probes: { name: string; title: string; mutates?: boolean; checkpoints?: boolean }[];
  warnings?: string[];
}

export interface KnownAgent extends AgentIdentity {
  connected: false;
  known: {
    agentId: string;
    runtime?: string;
    names?: string[];
    state?: string;
    endpoint?: string;
  };
}

export type AgentInfo = Agent | KnownAgent;

export function isConnectedAgent(agent: AgentInfo): agent is Agent {
  return agent.connected;
}

export async function fetchAgents(): Promise<AgentInfo[]> {
  const response = await fetch("/api/agents", { headers: { Accept: "application/json" } });
  if (!response.ok) throw await failure(response, "List agents");
  return response.json() as Promise<AgentInfo[]>;
}

export const agentKeys = { connected: ["agents"] as const, deployed: ["deployed-targets"] as const };

export function useAgents(refetchInterval: number | false = 5000) {
  return useQuery({ queryKey: agentKeys.connected, queryFn: fetchAgents, retry: false, refetchInterval });
}

/** One pod a deployer selected, and how its deploy went (deploy.TargetStatus). */
export interface DeployedTarget {
  /** The agent id captures and calls name: deployer/<deployer>/<namespace>/<pod>. */
  target: string;
  namespace: string;
  pod: string;
  container: string;
  state: string;
  agentAPIVersion?: number;
  endpoint?: string;
  error?: string;
}

/** A command the server runs for the browser (clicky RPC) answers with its stdout. */
interface CommandResult {
  success: boolean;
  output?: string;
  error?: string;
}

/**
 * Runs a CLI command over the server's RPC API and decodes the JSON it prints.
 * The RPC answer omits the output of a command that printed an empty list, so
 * a list command passes `empty` as what that means.
 */
async function runCommand<T>(path: string, body: Record<string, unknown>, what: string, empty?: T): Promise<T> {
  const response = await fetch(`/api/v1/${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify({ ...body, format: "json" }),
  });
  const text = await response.text();
  let result: CommandResult;
  try {
    result = JSON.parse(text) as CommandResult;
  } catch {
    throw new Error(`${what} failed (${response.status}): ${text.trim()}`);
  }
  if (!response.ok || !result.success) throw new Error(`${what} failed: ${result.error?.trim() || result.output?.trim() || `HTTP ${response.status}`}`);
  const output = result.output?.trim();
  if (!output) {
    if (empty !== undefined) return empty;
    throw new Error(`${what} printed nothing`);
  }
  return JSON.parse(output) as T;
}

/** The targets the server's stored deployers reached, which captures name by target id. */
export function fetchDeployedTargets(): Promise<DeployedTarget[]> {
  return runCommand<DeployedTarget[]>("jvm/deployers", {}, "List deployed targets", []);
}

export function useDeployedTargets() {
  return useQuery({ queryKey: agentKeys.deployed, queryFn: fetchDeployedTargets, retry: false });
}

/** Attaching the Java agent to a JVM on the server's own host (`agents install java`). */
export interface LocalJavaInstall {
  pid: number;
  /** Absolute directory on the host that keeps the versioned agent jar. */
  directory: string;
  service: string;
  allowMutation: boolean;
  dryRun?: boolean;
}

/** What an install did (agentinstall.Result). */
export interface InstallResult {
  pid: number;
  sha256: string;
  jar: string;
  agent?: string;
  dryRun: boolean;
}

export function installLocalJava({ pid, directory, service, allowMutation, dryRun }: LocalJavaInstall): Promise<InstallResult> {
  return runCommand<InstallResult>("agents/install/java", {
    destination: "local", pid, directory, service, "allow-mutation": allowMutation, ...(dryRun ? { "dry-run": true } : {}),
  }, "Install the Java agent");
}

/** Deploys a Deployer document (YAML) to the pods it selects; a dry run only lists them. */
export async function deployAgents({ deployer, dryRun }: { deployer: string; dryRun: boolean }): Promise<DeployedTarget[]> {
  return runCommand<DeployedTarget[]>("jvm/deploy", { deployer, ...(dryRun ? { "dry-run": true } : {}) }, dryRun ? "Preview the deployer" : "Deploy the agent", []);
}
