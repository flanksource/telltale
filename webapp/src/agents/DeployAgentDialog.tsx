import { useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Button, Checkbox, Combobox, Field, InputField, Modal, SegmentedControl, SecretKeySelector, WorkloadPicker, type SecretKind, type SecretResource, type WorkloadKind, type WorkloadResource } from "@flanksource/clicky-ui/components";
import { Badge, Callout } from "@flanksource/clicky-ui/data";
import { UiLoader } from "@flanksource/clicky-ui/icons";
import { useConnections } from "../definitions/api";
import { ProcessPicker } from "../arm/ProcessPicker";
import { agentKeys, deployAgents, installLocalJava, type DeployedTarget } from "./api";
import { failure } from "../traces/api";
import { DEFAULT_ATTACH_FORM, DEPLOYER_KINDS, attachDeployerYaml, attachFormErrors, pickedWorkload, type AttachDeployerForm, type JWKSChoice, type JWKSMode, type PickerKind } from "./deployer";

type Destination = "local" | "kubernetes";

const DIRECTORY_KEY = "telltale.deploy.localDirectory";

function rememberedDirectory(): string {
  try {
    return window.localStorage.getItem(DIRECTORY_KEY) ?? "";
  } catch {
    return "";
  }
}

function rememberDirectory(directory: string) {
  try {
    window.localStorage.setItem(DIRECTORY_KEY, directory);
  } catch {
    // A browser without storage asks again next time.
  }
}

/** Attach the Java agent to a JVM on the server's host; the new agent is picked once it connects. */
function LocalJava({ onDeployed }: { onDeployed: (agents: string[]) => void }) {
  const queryClient = useQueryClient();
  const [pid, setPid] = useState<number | null>(null);
  const [directory, setDirectory] = useState(rememberedDirectory);
  const [service, setService] = useState("");
  const [allowMutation, setAllowMutation] = useState(false);
  const [tried, setTried] = useState(false);
  const install = useMutation({
    mutationFn: async (request: Parameters<typeof installLocalJava>[0]) => {
      const result = await installLocalJava(request);
      if (!result.agent) throw new Error(`the agent attached to pid ${result.pid} did not report its id`);
      return result.agent;
    },
    onSuccess: async agent => {
      rememberDirectory(directory);
      await queryClient.invalidateQueries({ queryKey: agentKeys.connected });
      onDeployed([agent]);
    },
  });
  const errors = {
    pid: pid === null ? "Choose the JVM to attach to" : undefined,
    directory: directory.startsWith("/") ? undefined : "An absolute directory on this host, such as /Users/you/.local/share/telltale",
    service: service.trim() ? undefined : "The service name the agent reports",
  };
  const shown = (error: string | undefined) => (tried ? error : undefined);
  const submit = () => {
    setTried(true);
    if (pid === null || errors.directory || errors.service) return;
    install.mutate({ pid, directory, service: service.trim(), allowMutation });
  };
  return (
    <div className="space-y-3">
      <p className="text-sm text-muted-foreground">
        Attaches telltale's Java agent to a running JVM on the server's host with the JDK Attach API, as <code>telltale agents install java</code> does. It stays until the JVM exits.
      </p>
      <ProcessPicker id="deploy-local-pid" value={pid} onChange={setPid} error={shown(errors.pid)} initialSearch="java" helper="Running processes on the server's host" />
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Service" htmlFor="deploy-local-service" required error={shown(errors.service)} helper="How the agent names itself, e.g. orders">
          <InputField id="deploy-local-service" value={service} onChange={setService} invalid={!!shown(errors.service)} />
        </Field>
        <Field label="Agent directory" htmlFor="deploy-local-directory" required error={shown(errors.directory)} helper="Keeps the versioned agent jar">
          <InputField id="deploy-local-directory" value={directory} onChange={setDirectory} placeholder="/Users/you/.local/share/telltale" invalid={!!shown(errors.directory)} />
        </Field>
      </div>
      <Checkbox checked={allowMutation} onChange={setAllowMutation} label="Allow mutations (patching classes, redefining, setting MBean attributes); each call still opts in" />
      {install.error && <p role="alert" className="text-sm text-destructive">{install.error.message}</p>}
      <Button onClick={submit} disabled={install.isPending}>
        {install.isPending && <UiLoader className="mr-1 animate-spin" />}
        Attach agent
      </Button>
    </div>
  );
}

const WORKLOAD_KINDS = Object.keys(DEPLOYER_KINDS) as PickerKind[];

const JWKS_MODES: { id: JWKSMode; label: string }[] = [
  { id: "issuer", label: "From the issuer" },
  { id: "read", label: "Read from the cluster" },
  { id: "publish", label: "Publish to the cluster" },
];

const JWKS_HELP: Record<JWKSMode, string> = {
  issuer: "Agents get the issuer connection's public keys inline.",
  read: "Agents get the JWKS a Secret or ConfigMap already holds, such as a shared issuer's; it must include this issuer's signing key.",
  publish: "Each deploy writes the issuer's JWKS to this Secret or ConfigMap, creating it if needed, then provisions it.",
};

/** The Secrets or ConfigMaps of a namespace and their keys (commons-db connections API). */
function secretLoader(namespace: string | undefined) {
  return async (kind: SecretKind): Promise<SecretResource[]> => {
    if (!namespace) throw new Error("Pick the workload first: the JWKS object lives in its namespace");
    const query = new URLSearchParams({ kind, namespace });
    const response = await fetch(`/api/v1/connections/secrets?${query}`, { headers: { Accept: "application/json" } });
    if (!response.ok) throw await failure(response, `List ${kind}s in ${namespace}`);
    return response.json() as Promise<SecretResource[]>;
  };
}

/** Where the agents' JWKS comes from, and the Secret or ConfigMap key when it is the cluster. */
function JWKSField({ value, namespace, error, onChange }: { value: JWKSChoice; namespace: string | undefined; error: string | undefined; onChange: (next: JWKSChoice) => void }) {
  const loadResources = useMemo(() => secretLoader(namespace), [namespace]);
  const ref = value.ref ?? { kind: "configmap" as const, name: "", key: "jwks.json" };
  return (
    <Field label="Token keys (JWKS)" error={error} helper={JWKS_HELP[value.mode]}>
      <div className="space-y-2">
        <SegmentedControl<JWKSMode> aria-label="Where the JWKS comes from" size="sm" value={value.mode} onChange={mode => onChange(mode === "issuer" ? { mode } : { mode, ref })} options={JWKS_MODES} />
        {value.mode !== "issuer" && (
          <SecretKeySelector
            value={ref}
            onChange={next => onChange({ mode: value.mode, ...(next && "key" in next && (next.kind === "secret" || next.kind === "configmap") ? { ref: { kind: next.kind, name: next.name, key: next.key } } : {}) })}
            loadResources={loadResources}
            sources={["configmap", "secret"]}
            strict={value.mode === "read"}
          />
        )}
      </div>
    </Field>
  );
}

/** Namespaces and workloads of the server's current kube context (commons-db connections API). */
const loadNamespaces = async (): Promise<string[]> => {
  const response = await fetch("/api/v1/connections/namespaces", { headers: { Accept: "application/json" } });
  if (!response.ok) throw await failure(response, "List namespaces");
  return response.json() as Promise<string[]>;
};

const loadWorkloads = async (kinds: WorkloadKind[], namespace?: string): Promise<Partial<Record<WorkloadKind, WorkloadResource[]>>> => {
  if (!namespace) throw new Error("Pick a namespace to list its workloads");
  const query = new URLSearchParams({ namespace, kinds: kinds.join(",") });
  const response = await fetch(`/api/v1/connections/workloads?${query}`, { headers: { Accept: "application/json" } });
  if (!response.ok) throw await failure(response, `List workloads in ${namespace}`);
  return response.json() as Promise<Partial<Record<WorkloadKind, WorkloadResource[]>>>;
};

function TargetList({ targets }: { targets: DeployedTarget[] }) {
  if (targets.length === 0) return <p className="text-sm text-muted-foreground">No running pod matches the selector.</p>;
  return (
    <ul className="divide-y divide-border rounded-md border border-border text-sm">
      {targets.map(target => (
        <li key={target.target} className="space-y-0.5 px-3 py-1.5">
          <span className="flex flex-wrap items-center justify-between gap-2">
            <span className="font-mono text-xs">{target.target}</span>
            <Badge size="xs" variant="soft" tone={target.state === "failed" ? "danger" : target.state === "deployed" ? "success" : "info"}>{target.state}</Badge>
          </span>
          {target.error && <span className="block text-xs text-destructive">{target.error}</span>}
        </li>
      ))}
    </ul>
  );
}

/** Build a Deployer, preview the pods it selects, deploy, and pick the deployed targets. */
function KubernetesDeployer({ onDeployed }: { onDeployed: (agents: string[]) => void }) {
  const queryClient = useQueryClient();
  const connections = useConnections();
  const [form, setForm] = useState<AttachDeployerForm>(DEFAULT_ATTACH_FORM);
  const [tried, setTried] = useState(false);
  const errors = attachFormErrors(form);
  const valid = Object.keys(errors).length === 0;
  const yaml = valid ? attachDeployerYaml(form) : "";
  const preview = useMutation({ mutationFn: (deployer: string) => deployAgents({ deployer, dryRun: true }) });
  const deploy = useMutation({
    mutationFn: (deployer: string) => deployAgents({ deployer, dryRun: false }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: agentKeys.deployed }),
  });
  const set = (patch: Partial<AttachDeployerForm>) => {
    setForm({ ...form, ...patch });
    preview.reset();
    deploy.reset();
  };
  const shown = (key: keyof AttachDeployerForm) => (tried ? errors[key] : undefined);
  const text = (key: "name" | "container" | "directory" | "service", label: string, helper: string, placeholder?: string) => (
    <Field label={label} htmlFor={`deploy-k8s-${key}`} required={key !== "service"} error={shown(key)} helper={helper}>
      <InputField id={`deploy-k8s-${key}`} value={form[key]} onChange={value => set({ [key]: value })} placeholder={placeholder} invalid={!!shown(key)} />
    </Field>
  );
  const run = (action: typeof preview) => {
    setTried(true);
    if (valid) action.mutate(yaml);
  };
  const deployed = (deploy.data ?? []).filter(target => target.state === "deployed").map(target => target.target);
  return (
    <div className="space-y-3">
      <p className="text-sm text-muted-foreground">
        Attaches the agent to the JVM in each running pod of the workload you pick, as <code>telltale jvm deploy</code> does, with the server's kubeconfig. Executions reach each pod through a port-forward with tokens the issuer signs; no hub is needed.
      </p>
      <Field label="Workload" required error={shown("workload")} helper="The Deployment, StatefulSet or pod whose running pods get the agent, from the server's kubeconfig">
        <WorkloadPicker
          value={form.workload}
          onChange={workload => set({ workload })}
          loadNamespaces={loadNamespaces}
          loadWorkloads={loadWorkloads}
          kinds={WORKLOAD_KINDS}
          allowNamespaceSelection
          allowCustomValue={false}
          strict
          placeholder="Pick a workload…"
        />
      </Field>
      <div className="grid gap-3 sm:grid-cols-2">
        {text("name", "Deployer name", "Names the targets: deployer/<name>/<namespace>/<pod>; defaults to the workload's", pickedWorkload(form.workload)?.name ?? "orders")}
        {text("container", "Container", "The container running the JVM", "app")}
        {text("service", "Service", "How the agents name themselves; defaults to the deployer name")}
        <Field label="JVM pid" htmlFor="deploy-k8s-pid" required error={shown("pid")} helper="Inside the container; usually 1">
          <InputField id="deploy-k8s-pid" type="number" value={String(form.pid)} onChange={value => set({ pid: Number(value) })} invalid={!!shown("pid")} />
        </Field>
        <Field label="Listener port" htmlFor="deploy-k8s-port" required error={shown("port")} helper="The agent listens here inside the pod">
          <InputField id="deploy-k8s-port" type="number" value={String(form.port)} onChange={value => set({ port: Number(value) })} invalid={!!shown("port")} />
        </Field>
        {text("directory", "Agent directory", "Where the jar is copied in the container")}
        <Field label="Issuer connection" htmlFor="deploy-k8s-issuer" required error={shown("issuer") ?? (connections.isError ? connections.error.message : undefined)} helper="A saved connection whose signingKey secret is an RSA PEM key">
          <Combobox
            id="deploy-k8s-issuer"
            options={(connections.data ?? []).map(connection => ({ value: connection.reference, label: connection.name, description: connection.type }))}
            value={form.issuer}
            onChange={issuer => set({ issuer })}
            allowCustomValue={false}
            loading={connections.isPending}
            invalid={!!shown("issuer")}
            placeholder="connection://…"
          />
        </Field>
      </div>
      <JWKSField value={form.jwks} namespace={pickedWorkload(form.workload)?.namespace} error={shown("jwks")} onChange={jwks => set({ jwks })} />
      <Checkbox checked={form.allowMutation} onChange={allowMutation => set({ allowMutation })} label="Allow mutations; each call still opts in" />
      {yaml && (
        <details className="text-xs">
          <summary className="cursor-pointer text-muted-foreground">Deployer YAML</summary>
          <pre className="mt-1 max-h-60 overflow-auto rounded-md bg-muted p-2 font-mono">{yaml}</pre>
        </details>
      )}
      {preview.error && <p role="alert" className="text-sm text-destructive">{preview.error.message}</p>}
      {preview.data && !deploy.data && (
        <section aria-label="Selected pods" className="space-y-1">
          <h4 className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Pods the selector picks</h4>
          <TargetList targets={preview.data} />
        </section>
      )}
      {deploy.error && <p role="alert" className="text-sm text-destructive">{deploy.error.message}</p>}
      {deploy.data && (
        <section aria-label="Deployed targets" className="space-y-1">
          <h4 className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Deploy result</h4>
          <TargetList targets={deploy.data} />
        </section>
      )}
      <div className="flex flex-wrap gap-2">
        {!deploy.data && (
          <Button variant="outline" onClick={() => run(preview)} disabled={preview.isPending}>
            {preview.isPending && <UiLoader className="mr-1 animate-spin" />}
            Preview pods
          </Button>
        )}
        {preview.data && preview.data.length > 0 && !deploy.data && (
          <Button onClick={() => run(deploy)} disabled={deploy.isPending}>
            {deploy.isPending && <UiLoader className="mr-1 animate-spin" />}
            Deploy to {preview.data.length} {preview.data.length === 1 ? "pod" : "pods"}
          </Button>
        )}
        {deployed.length > 0 && <Button onClick={() => onDeployed(deployed)}>Use {deployed.length === 1 ? "this target" : `these ${deployed.length} targets`}</Button>}
      </div>
    </div>
  );
}

const DESTINATIONS: { id: Destination; label: string }[] = [
  { id: "local", label: "JVM on this host" },
  { id: "kubernetes", label: "Kubernetes pods" },
];

/**
 * Deploys a new Java agent, on the server's host or into Kubernetes pods,
 * and hands back the agent ids to capture from.
 */
export function DeployAgentDialog({ open, onClose, onDeployed }: { open: boolean; onClose: () => void; onDeployed: (agents: string[]) => void }) {
  const [destination, setDestination] = useState<Destination>("local");
  const done = (agents: string[]) => {
    onDeployed(agents);
    onClose();
  };
  return (
    <Modal open={open} onClose={onClose} title="Deploy an agent" size="lg">
      <div className="space-y-4">
        <SegmentedControl<Destination> aria-label="Where the agent runs" value={destination} onChange={setDestination} options={DESTINATIONS} />
        {destination === "local" ? <LocalJava onDeployed={done} /> : <KubernetesDeployer onDeployed={done} />}
        <Callout variant="note" label="Whole JVM">
          The agent serves every application in the JVM it joins, including others sharing its container. Mutations need it installed with mutations allowed and opt-in on each call.
        </Callout>
      </div>
    </Modal>
  );
}
