import { describe, expect, it } from "vitest";
import { DEFAULT_ATTACH_FORM, attachDeployerYaml, attachFormErrors, type AttachDeployerForm } from "./deployer";

const orders: AttachDeployerForm = {
  ...DEFAULT_ATTACH_FORM,
  workload: "shop/deployment/orders",
  container: "app",
  issuer: "connection://shop/telltale-signing",
};

describe("attach deployer", () => {
  it("builds the Deployer jvm deploy takes for the picked workload, reaching each pod's listener through a port-forward", () => {
    expect(attachDeployerYaml(orders)).toBe([
      "apiVersion: telltale.flanksource.com/v1alpha1",
      "kind: Deployer",
      "metadata:",
      '  name: "orders"',
      "spec:",
      "  selector:",
      "    kind: Deployment",
      '    namespace: "shop"',
      '    name: "orders"',
      '    container: "app"',
      "  javaContainer:",
      "    kind: attach",
      '    url: "portforward://{{.Pod.Name}}.{{.Pod.Namespace}}:7070?kind=pod"',
      "    pid: 1",
      '    directory: "/tmp/telltale"',
      "    bind: 0.0.0.0",
      "    port: 7070",
      '    service: "orders"',
      "  auth:",
      '    issuer: "connection://shop/telltale-signing"',
      "",
    ].join("\n"));
  });

  it("names the Secret or ConfigMap key the JWKS is read from or published to, and leaves it out for the issuer's own", () => {
    expect(attachDeployerYaml({ ...orders, jwks: { mode: "publish", ref: { kind: "configmap", name: "telltale-jwks", key: "jwks.json" } } })).toContain(
      '  auth:\n    issuer: "connection://shop/telltale-signing"\n    jwks:\n      mode: publish\n      kind: ConfigMap\n      name: "telltale-jwks"\n      key: "jwks.json"\n',
    );
    expect(attachDeployerYaml({ ...orders, jwks: { mode: "read", ref: { kind: "secret", name: "shared-jwks", key: "" } } })).toContain("    jwks:\n      mode: read\n      kind: Secret\n      name: \"shared-jwks\"\n");
    expect(attachDeployerYaml(orders)).not.toContain("jwks:");
    expect(attachFormErrors({ ...orders, jwks: { mode: "read", ref: { kind: "secret", name: " ", key: "jwks.json" } } })).toEqual({ jwks: "The Secret or ConfigMap that holds the JWKS" });
  });

  it("names every field an empty form lacks, a namespace with no workload picked, and an invalid deployer name", () => {
    expect(Object.keys(attachFormErrors(DEFAULT_ATTACH_FORM)).sort()).toEqual(["container", "issuer", "workload"]);
    expect(Object.keys(attachFormErrors({ ...orders, workload: "shop/" }))).toEqual(["workload"]);
    expect(attachFormErrors({ ...orders, name: "Orders API" }).name).toBe("A lowercase DNS name, such as orders");
    expect(attachDeployerYaml({ ...orders, workload: "shop/statefulset/orders-db", name: "orders-db-jvm" })).toContain('  name: "orders-db-jvm"\nspec:\n  selector:\n    kind: StatefulSet\n    namespace: "shop"\n    name: "orders-db"');
    expect(attachFormErrors({ ...orders, port: 70000, directory: "tmp" })).toEqual({ port: "A port from 1 to 65535", directory: "An absolute directory in the container" });
    expect(attachFormErrors(orders)).toEqual({});
  });
});
