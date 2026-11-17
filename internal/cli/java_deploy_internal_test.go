package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/flanksource/telltale/pkg/agentinstall"
	"github.com/flanksource/telltale/pkg/definition"
	"github.com/flanksource/telltale/pkg/deploy"
)

// commandAt is the command root reaches by path.
func commandAtPath(path ...string) *cobra.Command {
	cmd, _, err := NewRoot("test").Find(path)
	Expect(err).NotTo(HaveOccurred())
	return cmd
}

func flagNames(cmd *cobra.Command) []string {
	var names []string
	cmd.Flags().VisitAll(func(flag *pflag.Flag) { names = append(names, flag.Name+"="+flag.DefValue) })
	slices.Sort(names)
	return names
}

// parsedFlags are the javaDeployFlags and flag set args parse into.
func parsedFlags(args ...string) (javaDeployFlags, *pflag.FlagSet) {
	cmd := commandAtPath("jvm", "deploy")
	Expect(cmd.ParseFlags(args)).To(Succeed())
	var flags javaDeployFlags
	// The command's flags bind to its own javaDeployFlags; read them back by name.
	get := func(name string) string { return cmd.Flags().Lookup(name).Value.String() }
	flags.file, flags.deployer, flags.issuer, flags.name = get("file"), get("deployer"), get("issuer"), get("name")
	flags.install.Destination.Kind = get("destination")
	return flags, cmd.Flags()
}

var _ = Describe("jvm deploy and agents install java", func() {
	It("are one command with the same flags under both names", func() {
		deployCmd, installCmd := commandAtPath("jvm", "deploy"), commandAtPath("agents", "install", "java")
		Expect(deployCmd.Name()).To(Equal("deploy"))
		Expect(installCmd.Name()).To(Equal("java"))
		Expect(flagNames(installCmd)).To(Equal(flagNames(deployCmd)))
		Expect(flagNames(deployCmd)).To(ContainElements("hub-url=", "token-file=", "host=", "file=", "deployer=", "issuer=", "jwks-name=", "labels=[]", "kubeconfig=", "context="))
	})

	It("serves both names over RPC with the same inputs", func() {
		spec := kindOpenAPI(NewRoot("test"))
		inputs := func(path string) []string {
			Expect(spec.Paths).To(HaveKey(path))
			var names []string
			for name := range spec.Paths[path]["post"].RequestBody.Content["application/json"].Schema.Properties {
				names = append(names, name)
			}
			slices.Sort(names)
			return names
		}
		Expect(inputs("/api/v1/agents/install/java")).To(Equal(inputs("/api/v1/jvm/deploy")))
		Expect(inputs("/api/v1/jvm/deploy")).To(ContainElements("target", "deployer", "issuer", "dry-run"))
	})

	It("builds a Deployer from flags: one pod's attach listener, reached over a port-forward, verifying the issuer's tokens", func() {
		cmd := commandAtPath("agents", "install", "java")
		flags := javaDeployFlags{install: agentinstall.Options{Destination: agentinstall.Destination{Kind: "kubernetes", Namespace: "shop", Pod: "orders-0", Container: "orders"},
			PID: 1, Directory: "/tmp/telltale", Service: "orders"}, issuer: "connection://shop/telltale-signing", bind: "127.0.0.1", port: 7070,
			jwks: deploy.JWKSSource{Mode: deploy.JWKSPublish, Kind: "ConfigMap", Name: "telltale-jwks"}}
		mode, err := flags.mode(cmd.Flags())
		Expect(err).NotTo(HaveOccurred())
		Expect(mode).To(Equal(modeDeployerFlags))
		built, err := flags.builtDeployer()
		Expect(err).NotTo(HaveOccurred())
		container, err := deploy.NewJavaContainerSpec(deploy.AttachSpec{Kind: deploy.KindAttach, URL: "portforward://{{.Pod.Name}}.{{.Pod.Namespace}}:7070?kind=pod",
			PID: 1, Directory: "/tmp/telltale", Bind: "127.0.0.1", Port: 7070, Service: "orders"})
		Expect(err).NotTo(HaveOccurred())
		Expect(*built).To(Equal(deploy.Deployer{TypeMeta: metav1.TypeMeta{APIVersion: definition.APIVersion, Kind: deploy.Kind}, ObjectMeta: metav1.ObjectMeta{Name: "orders"},
			Spec: deploy.Spec{Selector: deploy.Selector{Kind: deploy.WorkloadPod, Namespace: "shop", Name: "orders-0", Container: "orders"}, JavaContainer: container,
				Auth: deploy.AuthSpec{Issuer: "connection://shop/telltale-signing", JWKS: &deploy.JWKSSource{Mode: deploy.JWKSPublish, Kind: "ConfigMap", Name: "telltale-jwks"}}}}))
		// The built document is one a Deployer file could hold.
		encoded, err := json.Marshal(built)
		Expect(err).NotTo(HaveOccurred())
		decoded, err := deploy.Decode(encoded)
		Expect(err).NotTo(HaveOccurred())
		Expect(decoded.Spec.Selector).To(Equal(built.Spec.Selector))
	})

	It("selects workloads by kind and name or labels, and refuses --pod with either", func() {
		flags := javaDeployFlags{install: agentinstall.Options{Destination: agentinstall.Destination{Namespace: "shop", Container: "app"}}, workloadKind: "statefulset", workload: "orders"}
		built, err := flags.builtDeployer()
		Expect(err).NotTo(HaveOccurred())
		Expect(built.Spec.Selector).To(Equal(deploy.Selector{Kind: deploy.WorkloadStatefulSet, Namespace: "shop", Name: "orders", Container: "app"}))
		flags.install.Destination.Pod = "orders-0"
		_, err = flags.builtDeployer()
		Expect(err).To(MatchError(ContainSubstring("--pod selects one pod")))
		_, err = javaDeployFlags{workloadKind: "daemonset"}.builtDeployer()
		Expect(err).To(MatchError(ContainSubstring("want Deployment, StatefulSet or Pod")))
	})

	DescribeTable("refuses flags the chosen mode would ignore",
		func(message string, args ...string) {
			flags, set := parsedFlags(args...)
			_, err := flags.mode(set)
			Expect(err).To(MatchError(ContainSubstring(message)))
		},
		Entry("a hub with a built Deployer", "need no hub", "--issuer", "connection://x/y", "--hub-url", "https://hub.example"),
		Entry("an SSH host with a Deployer document", "only a single attach reads these", "--deployer", "kind: Deployer", "--host", "app"),
		Entry("selector flags with a Deployer document", "the Deployer document already says them", "--deployer", "kind: Deployer", "--pod", "orders-0"),
		Entry("listener flags without an issuer", "which needs a target or --issuer", "--port", "8080"),
		Entry("a non-Kubernetes destination for a Deployer", "--destination must be kubernetes", "--issuer", "connection://x/y", "--destination", "ssh"),
	)

	It("runs a single attach dry run under either name, printing the previewed artifact", func() {
		for _, path := range [][]string{{"jvm", "deploy"}, {"agents", "install", "java"}} {
			root := NewRoot("test")
			var out bytes.Buffer
			root.SetArgs(append(path, "--pid", "4242", "--directory", "/opt/telltale", "--dry-run"))
			root.SetOut(&out)
			root.SetErr(&bytes.Buffer{})
			Expect(root.ExecuteContext(context.Background())).To(Succeed(), path)
			var result agentinstall.Result
			Expect(json.Unmarshal(out.Bytes(), &result)).To(Succeed(), out.String())
			Expect(result).To(HaveField("DryRun", true))
			Expect(result.Jar).To(HavePrefix("/opt/telltale/agent-"))
		}
	})
})

// changed is a flag set in which names were given on the command line.
func changed(names ...string) *pflag.FlagSet {
	set := pflag.NewFlagSet("test", pflag.ContinueOnError)
	for _, name := range names {
		set.String(name, "", "")
		Expect(set.Set(name, "given")).To(Succeed())
	}
	return set
}

var _ = Describe("a Deployer from a [kind/]name target", func() {
	DescribeTable("reads kubectl-style targets",
		func(target string, kind deploy.WorkloadKind, name string) {
			gotKind, gotName, err := parseTarget(target)
			Expect(err).NotTo(HaveOccurred())
			Expect([]string{string(gotKind), gotName}).To(Equal([]string{string(kind), name}))
		},
		Entry("sts/", "sts/orders", deploy.WorkloadStatefulSet, "orders"),
		Entry("statefulset/", "StatefulSet/orders", deploy.WorkloadStatefulSet, "orders"),
		Entry("deploy/", "deploy/orders-web", deploy.WorkloadDeployment, "orders-web"),
		Entry("deployments/", "deployments/orders-web", deploy.WorkloadDeployment, "orders-web"),
		Entry("pod/", "pod/orders-0", deploy.WorkloadPod, "orders-0"),
		Entry("a bare pod name", "orders-0", deploy.WorkloadPod, "orders-0"),
	)

	DescribeTable("refuses targets it cannot read",
		func(target, message string) {
			_, _, err := parseTarget(target)
			Expect(err).To(MatchError(ContainSubstring(message)))
		},
		Entry("an unknown kind", "ds/orders", `kind "ds" is not sts, deploy or pod`),
		Entry("no name", "sts/", "want [kind/]name"),
		Entry("a nested name", "sts/orders/0", "want [kind/]name"),
	)

	It("defaults a StatefulSet target to JVM discovery, the default issuer, /tmp/telltale and its own name", func() {
		flags := javaDeployFlags{target: "sts/orders", install: agentinstall.Options{Destination: agentinstall.Destination{Namespace: "shop"}, Service: "java"}, bind: "127.0.0.1", port: 7070}
		mode, err := flags.mode(changed("namespace"))
		Expect(err).NotTo(HaveOccurred())
		Expect(mode).To(Equal(modeDeployerFlags))
		flags, err = flags.withDefaults(changed("namespace"))
		Expect(err).NotTo(HaveOccurred())
		built, err := flags.builtDeployer()
		Expect(err).NotTo(HaveOccurred())
		container, err := deploy.NewJavaContainerSpec(deploy.AttachSpec{Kind: deploy.KindAttach, URL: "portforward://{{.Pod.Name}}.{{.Pod.Namespace}}:7070?kind=pod",
			PID: 0, Directory: "/tmp/telltale", Bind: "127.0.0.1", Port: 7070, Service: "orders"})
		Expect(err).NotTo(HaveOccurred())
		Expect(*built).To(Equal(deploy.Deployer{TypeMeta: metav1.TypeMeta{APIVersion: definition.APIVersion, Kind: deploy.Kind}, ObjectMeta: metav1.ObjectMeta{Name: "orders"},
			Spec: deploy.Spec{Selector: deploy.Selector{Kind: deploy.WorkloadStatefulSet, Namespace: "shop", Name: "orders"}, JavaContainer: container,
				Auth: deploy.AuthSpec{Issuer: deploy.DefaultIssuer}}}))
	})

	It("defaults a bare pod target to that pod, and keeps what was given", func() {
		flags := javaDeployFlags{target: "orders-0", issuer: "connection://shop/signing", install: agentinstall.Options{PID: 7, Directory: "/opt/telltale", Service: "orders"}}
		flags, err := flags.withDefaults(changed("issuer", "pid", "directory", "service"))
		Expect(err).NotTo(HaveOccurred())
		Expect([]any{flags.install.Destination.Pod, flags.issuer, flags.install.PID, flags.install.Directory, flags.install.Service}).
			To(Equal([]any{"orders-0", "connection://shop/signing", 7, "/opt/telltale", "orders"}))
	})

	DescribeTable("refuses a target with another way of selecting pods",
		func(flags javaDeployFlags, set *pflag.FlagSet, message string) {
			_, err := flags.mode(set)
			Expect(err).To(MatchError(ContainSubstring(message)))
		},
		Entry("--pod", javaDeployFlags{target: "sts/orders"}, changed("pod"), "target sts/orders already selects the pods"),
		Entry("--labels", javaDeployFlags{target: "sts/orders"}, changed("labels"), "target sts/orders already selects the pods"),
		Entry("a Deployer document", javaDeployFlags{target: "sts/orders", deployer: "kind: Deployer"}, changed("deployer"), "the Deployer document already selects its pods"),
	)

	It("accepts the target positionally under both names, and at most one", func() {
		for _, path := range [][]string{{"jvm", "deploy"}, {"agents", "install", "java"}} {
			Expect(commandAtPath(path...).Args(commandAtPath(path...), []string{"sts/orders"})).To(Succeed())
			Expect(commandAtPath(path...).Args(commandAtPath(path...), []string{"sts/orders", "pod/orders-0"})).To(HaveOccurred())
		}
	})
})
