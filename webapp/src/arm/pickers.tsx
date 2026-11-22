import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Button, Combobox, Modal, type ComboboxOption } from "@flanksource/clicky-ui/components";
import { ConnectionTypeIcon, CreateConnectionDialog } from "@flanksource/clicky-ui/connections";
import { UiAdd } from "@flanksource/clicky-ui/icons";
import type { Agent, DeployedTarget } from "../agents/api";
import { DeployAgentDialog } from "../agents/DeployAgentDialog";
import { definitionKeys, type ConnectionInfo } from "../definitions/api";
import { connectionsFor } from "./catalog";
import type { AgentChoice } from "./model";

const TYPE_LABEL: Record<string, string> = { sql_server: "SQL Server", postgres: "Postgres", kubernetes: "Kubernetes" };
const typeLabel = (type: string) => TYPE_LABEL[type] ?? type;

function CaptureConnectionDialog({ types, existing, onClose, onCreated }: {
  types: string[];
  existing: string[];
  onClose: () => void;
  onCreated: (reference: string) => void;
}) {
  const [type, setType] = useState(types.length === 1 ? types[0] : undefined);
  if (types.length > 1 && !type) {
    return (
      <Modal open onClose={onClose} title="Choose a connection type" size="sm">
        <div className="flex flex-wrap gap-2">
          {types.map(option => (
            <Button key={option} variant="outline" onClick={() => setType(option)}>
              <ConnectionTypeIcon type={option} className="mr-1 size-4" />
              {typeLabel(option)}
            </Button>
          ))}
        </div>
      </Modal>
    );
  }
  return <CreateConnectionDialog apiBase="/api/v1" type={type} existing={existing} onClose={onClose} onCreated={onCreated} />;
}

/** A capture's saved connection picker and its add action. */
export function ConnectionSelect({ id, types, connections, value, onChange, invalid, defaultLabel }: {
  id: string;
  types: string[];
  connections: ConnectionInfo[];
  value: string;
  onChange: (reference: string) => void;
  invalid: boolean;
  defaultLabel?: string;
}) {
  const [adding, setAdding] = useState(false);
  const queryClient = useQueryClient();
  const wanted = types.map(typeLabel).join(" or ") || "saved";
  const add = () => setAdding(true);
  const options: ComboboxOption[] = connectionsFor(types, connections).map(connection => ({
    value: connection.reference,
    label: connection.name,
    selectedLabel: connection.name,
    ...(connection.source ? { description: connection.source.replace(/^file:\/\//, "") } : {}),
    icon: <ConnectionTypeIcon type={connection.type} className="size-4" />,
    trailing: <span className="text-[11px] text-muted-foreground">{typeLabel(connection.type)}</span>,
  }));
  if (value && !options.some(option => option.value === value)) {
    options.push({ value, label: value.replace(/^connection:\/\//, "") });
  }
  const defaultValue = "server-kubeconfig";
  if (defaultLabel) options.unshift({ value: defaultValue, label: defaultLabel });
  return (
    <>
      {options.length ? (
        <Combobox
          id={id}
          ariaLabel={`${wanted} connection`}
          size="sm"
          className="min-w-32 flex-1"
          menuWidth={360}
          options={options}
          value={value || (defaultLabel ? defaultValue : "")}
          onChange={selected => onChange(selected === defaultValue ? "" : selected)}
          allowCustomValue={false}
          invalid={invalid}
          ariaRequired={!defaultLabel}
          placeholder={`${wanted} connection…`}
          footer={
            <button type="button" className="flex w-full items-center gap-1 px-3 py-1.5 text-left text-xs text-primary hover:bg-muted" onMouseDown={event => event.preventDefault()} onClick={add}>
              <UiAdd /> Add a {wanted} connection
            </button>
          }
        />
      ) : (
        <Button size="sm" variant="outline" className={invalid ? "border-destructive" : undefined} onClick={add}>
          <UiAdd className="mr-1" /> Add {wanted} connection
        </Button>
      )}
      {adding && (
        <CaptureConnectionDialog
          types={types}
          existing={connections.map(connection => connection.reference)}
          onClose={() => setAdding(false)}
          onCreated={reference => {
            onChange(reference);
            setAdding(false);
            void queryClient.invalidateQueries({ queryKey: definitionKeys.connections });
          }}
        />
      )}
    </>
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
