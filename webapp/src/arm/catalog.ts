import type { Agent, DeployedTarget } from "../agents/api";
import type { ConnectionInfo, DefinitionSummary } from "../definitions/api";
import type { ArmDraft, DraftError, Kind } from "./model";

/** What the wizard offers, as the server lists it. */
export interface ArmCatalog {
  kinds: Kind[];
  connections: ConnectionInfo[];
  definitions: DefinitionSummary[];
  agents: Agent[];
  deployed: DeployedTarget[];
}

export interface StepProps {
  catalog: ArmCatalog;
  draft: ArmDraft;
  onChange: (next: ArmDraft) => void;
  /** Errors shown only once the step was left or a refusal sent the user back. */
  errors: DraftError[];
}

export const errorAt = (errors: DraftError[], path: string) => errors.find(error => error.instancePath === path)?.message;

export function connectionsFor(types: string[], connections: ConnectionInfo[]): ConnectionInfo[] {
  return types.length ? connections.filter(connection => types.includes(connection.type)) : connections;
}
