import { useState } from "react";
import { Button, Combobox, type ComboboxOption } from "@flanksource/clicky-ui/components";
import { ConnectionTypeIcon } from "@flanksource/clicky-ui/connections";
import { UiAdd } from "@flanksource/clicky-ui/icons";
import type { Agent, DeployedTarget } from "../agents/api";
import { DeployAgentDialog } from "../agents/DeployAgentDialog";
import type { ConnectionInfo } from "../definitions/api";
import type { AgentChoice } from "./model";

const TYPE_LABEL: Record<string, string> = { sql_server: "SQL Server", postgres: "Postgres", kubernetes: "Kubernetes" };
const typeLabel = (type: string) => TYPE_LABEL[type] ?? type;

/** One saved connection of the types a capture accepts, sized for a row header; with none saved, Add connection. */
export function ConnectionSelect({ id, types, connections, value, onChange, invalid }: {
  id: string;
  types: string[];
  connections: ConnectionInfo[];
  value: string;
  onChange: (reference: string) => void;
  invalid: boolean;
}) {
  const wanted = types.map(typeLabel).join(" or ") || "saved";
  // A new tab keeps the draft; the connections list refetches when this tab regains focus.
  const add = () => window.open("/connections", "_blank", "noopener");
  if (connections.length === 0) {
    return (
      <Button size="sm" variant="outline" className={invalid ? "border-destructive" : undefined} onClick={add} title={`No ${wanted} connection is saved yet`}>
        <UiAdd className="mr-1" /> Add {wanted} connection
      </Button>
    );
  }
  const options: ComboboxOption[] = connections.map(connection => ({
    value: connection.reference,
    label: connection.name,
    selectedLabel: connection.name,
    ...(connection.source ? { description: connection.source.replace(/^file:\/\//, "") } : {}),
    icon: <ConnectionTypeIcon type={connection.type} className="size-4" />,
    trailing: <span className="text-[11px] text-muted-foreground">{typeLabel(connection.type)}</span>,
  }));
  return (
    <Combobox
      id={id}
      ariaLabel={`${wanted} connection`}
      size="sm"
      className="min-w-32 flex-1"
      menuWidth={360}
      options={options}
      value={value}
      onChange={onChange}
      allowCustomValue={false}
      invalid={invalid}
      ariaRequired
      placeholder={`${wanted} connection…`}
      footer={
        <button type="button" className="flex w-full items-center gap-1 px-3 py-1.5 text-left text-xs text-primary hover:bg-muted" onClick={add}>
          <UiAdd /> Add a {wanted} connection
        </button>
      }
    />
  );
}

const isWildcard = (item: string) => item.includes("=");

const choiceItems = (choice: AgentChoice): string[] =>
  choice.mode === "id" ? choice.agents.filter(Boolean) : choice.selector.split(",").map(pair => pair.trim()).filter(Boolean);

/**
 * The newest pick decides the mode: picking an agent keeps only agents (the
 * capture runs on each), picking a wildcard keeps only wildcards (any agent
 * whose resource attributes match all of them).
 */
export function nextChoice(previous: AgentChoice, next: string[]): AgentChoice {
  const before = choiceItems(previous);
  const added = next.find(item => !before.includes(item));
  const wildcard = added ? isWildcard(added) : previous.mode === "selector";
  if (!wildcard) return { mode: "id", agents: next.filter(item => !isWildcard(item)) };
  return { mode: "selector", selector: next.filter(isWildcard).join(", "), wait: previous.mode === "selector" ? previous.wait : true };
}

function wildcardOptions(agents: Agent[]): ComboboxOption[] {
  const counts = new Map<string, number>();
  for (const agent of agents) for (const [key, value] of Object.entries(agent.resource)) counts.set(`${key}=${value}`, (counts.get(`${key}=${value}`) ?? 0) + 1);
  return [...counts].map(([pair, count]) => ({
    value: pair,
    label: pair,
    group: "Wildcards",
    description: `Any agent with ${pair}`,
    trailing: <span className="text-[11px] text-muted-foreground">{count} now</span>,
  }));
}

/**
 * Connected agents that offer the probe, deployed Kubernetes targets, or
 * wildcards on resource attributes, in one dropdown sized for a row header.
 * Its footer deploys a new agent and picks it.
 */
export function AgentSelect({ id, probe, agents, deployed, value, onChange, invalid }: {
  id: string;
  probe: string;
  agents: Agent[];
  deployed: DeployedTarget[];
  value: AgentChoice;
  onChange: (next: AgentChoice) => void;
  invalid: boolean;
}) {
  const [deploying, setDeploying] = useState(false);
  const offering = agents.filter(agent => agent.probes.some(p => p.name === probe));
  const connected = new Set(agents.map(agent => agent.agentId));
  const targets = deployed.filter(target => target.state === "deployed" && !connected.has(target.target));
  const selected = choiceItems(value);
  const wildcards = wildcardOptions(offering);
  const known = new Set([...offering.map(agent => agent.agentId), ...targets.map(target => target.target)]);
  const unknown = selected.filter(item => !known.has(item) && !wildcards.some(option => option.value === item));
  const options: ComboboxOption[] = [
    ...offering.map(agent => ({
      value: agent.agentId,
      label: agent.service || agent.agentId,
      selectedLabel: agent.service || agent.agentId,
      group: "Agents",
      description: `${agent.runtime.lang} ${agent.runtime.version} · probes: ${agent.probes.map(p => p.name).join(", ")}`,
      trailing: <span className="font-mono text-[11px] text-muted-foreground">{agent.agentId}</span>,
    })),
    ...targets.map(target => ({
      value: target.target,
      label: target.pod,
      selectedLabel: target.pod,
      group: "Deployed targets",
      description: `${target.namespace}/${target.pod} · ${target.container}; connects when the run starts`,
      trailing: <span className="font-mono text-[11px] text-muted-foreground">{target.target.split("/")[1]}</span>,
    })),
    ...wildcards,
    ...unknown.map(item => ({ value: item, label: item, group: isWildcard(item) ? "Wildcards" : "Agents" })),
  ];
  return (
    <>
      <Combobox
        id={id}
        ariaLabel="Agents or wildcards"
        size="sm"
        className="min-w-32 flex-1"
        menuWidth={380}
        multiple
        variant="tags"
        options={options}
        value={selected}
        onChange={next => onChange(nextChoice(value, next))}
        allowCustomValue
        invalid={invalid}
        ariaRequired
        placeholder={offering.length || targets.length ? "Agents or key=value…" : `No agent offers ${probe}; deploy one`}
        footer={
          <button type="button" className="flex w-full items-center gap-1 px-3 py-1.5 text-left text-xs text-primary hover:bg-muted" onClick={() => setDeploying(true)}>
            <UiAdd /> Deploy an agent
          </button>
        }
      />
      {deploying && (
        <DeployAgentDialog
          open
          onClose={() => setDeploying(false)}
          onDeployed={ids => onChange({ mode: "id", agents: [...(value.mode === "id" ? value.agents.filter(Boolean) : []), ...ids.filter(agent => !selected.includes(agent))] })}
        />
      )}
    </>
  );
}
