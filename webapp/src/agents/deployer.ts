/**
 * The Deployer (pkg/deploy, kind: Deployer) the Kubernetes flow builds from
 * its form: the agent attached to the JVM of each running pod of the picked
 * workload, its HTTP listener reached through a port-forward, and its
 * executions signed by the issuer connection's key.
 */
import { parseWorkloadKey } from "@flanksource/clicky-ui/components";

/** The workload kinds a Deployer selects, by WorkloadPicker kind. */
export const DEPLOYER_KINDS = { deployment: "Deployment", statefulset: "StatefulSet", pod: "Pod" } as const;

export type PickerKind = keyof typeof DEPLOYER_KINDS;

export interface AttachDeployerForm {
  /** Deployer name; defaults to the workload's. */
  name: string;
  /** The WorkloadPicker key of the workload: namespace/kind/name. */
  workload: string;
  container: string;
  /** The JVM's pid inside the container. */
  pid: number;
  port: number;
  directory: string;
  service: string;
  allowMutation: boolean;
  /** connection:// reference holding the `signingKey` secret. */
  issuer: string;
  /** Where the agents' JWKS comes from: the issuer itself, or a Secret/ConfigMap key read or published on deploy. */
  jwks: JWKSChoice;
}

export type JWKSMode = "issuer" | "read" | "publish";

export interface JWKSChoice {
  mode: JWKSMode;
  /** The Secret or ConfigMap key, for the read and publish modes. */
  ref?: { kind: "secret" | "configmap"; name: string; key: string };
}

export const DEFAULT_ATTACH_FORM: AttachDeployerForm = {
  name: "",
  workload: "",
  container: "",
  pid: 1,
  port: 7070,
  directory: "/tmp/telltale",
  service: "",
  allowMutation: false,
  issuer: "",
  jwks: { mode: "issuer" },
};

const JWKS_KIND = { secret: "Secret", configmap: "ConfigMap" } as const;

const NAME = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;

/** The picked workload's namespace, Deployer kind and name; undefined until one is picked. */
export function pickedWorkload(key: string): { namespace: string; kind: (typeof DEPLOYER_KINDS)[PickerKind]; name: string } | undefined {
  const parsed = parseWorkloadKey(key);
  if (!parsed.namespace || !parsed.name || !parsed.kind || !(parsed.kind in DEPLOYER_KINDS)) return undefined;
  return { namespace: parsed.namespace, kind: DEPLOYER_KINDS[parsed.kind as PickerKind], name: parsed.name };
}

/** The deployer's name: the one given, else the workload's. */
export const deployerName = (form: AttachDeployerForm) => form.name.trim() || pickedWorkload(form.workload)?.name || "";

/** What the form still lacks, by field. */
export function attachFormErrors(form: AttachDeployerForm): Partial<Record<keyof AttachDeployerForm, string>> {
  const errors: Partial<Record<keyof AttachDeployerForm, string>> = {};
  if (!pickedWorkload(form.workload)) errors.workload = "Pick the Deployment, StatefulSet or pod that runs the JVM";
  else if (!NAME.test(deployerName(form))) errors.name = "A lowercase DNS name, such as orders";
  if (!form.container.trim()) errors.container = "The container that runs the JVM";
  if (!Number.isInteger(form.pid) || form.pid <= 0) errors.pid = "A positive pid";
  if (!Number.isInteger(form.port) || form.port <= 0 || form.port > 65535) errors.port = "A port from 1 to 65535";
  if (!form.directory.startsWith("/")) errors.directory = "An absolute directory in the container";
  if (!form.issuer.startsWith("connection://")) errors.issuer = "A saved connection holding the signingKey secret";
  if (form.jwks.mode !== "issuer" && !form.jwks.ref?.name.trim()) {
    errors.jwks = form.jwks.mode === "read" ? "The Secret or ConfigMap that holds the JWKS" : "The Secret or ConfigMap to publish the JWKS to";
  }
  return errors;
}

const quote = (value: string) => JSON.stringify(value);

/** The Deployer document for form, as `telltale jvm deploy` takes it. */
export function attachDeployerYaml(form: AttachDeployerForm): string {
  const workload = pickedWorkload(form.workload);
  if (!workload) throw new Error("no workload is picked");
  const name = deployerName(form);
  return [
    "apiVersion: telltale.flanksource.com/v1alpha1",
    "kind: Deployer",
    "metadata:",
    `  name: ${quote(name)}`,
    "spec:",
    "  selector:",
    `    kind: ${workload.kind}`,
    `    namespace: ${quote(workload.namespace)}`,
    `    name: ${quote(workload.name)}`,
    `    container: ${quote(form.container.trim())}`,
    "  javaContainer:",
    "    kind: attach",
    `    url: ${quote(`portforward://{{.Pod.Name}}.{{.Pod.Namespace}}:${form.port}?kind=pod`)}`,
    `    pid: ${form.pid}`,
    `    directory: ${quote(form.directory)}`,
    "    bind: 0.0.0.0",
    `    port: ${form.port}`,
    `    service: ${quote(form.service.trim() || name)}`,
    ...(form.allowMutation ? ["    allowMutation: true"] : []),
    "  auth:",
    `    issuer: ${quote(form.issuer)}`,
    ...jwksYaml(form.jwks),
    "",
  ].join("\n");
}

function jwksYaml({ mode, ref }: JWKSChoice): string[] {
  if (mode === "issuer") return [];
  if (!ref) throw new Error(`a ${mode} JWKS needs a Secret or ConfigMap`);
  return [
    "    jwks:",
    `      mode: ${mode}`,
    `      kind: ${JWKS_KIND[ref.kind]}`,
    `      name: ${quote(ref.name.trim())}`,
    ...(ref.key.trim() ? [`      key: ${quote(ref.key.trim())}`] : []),
  ];
}
