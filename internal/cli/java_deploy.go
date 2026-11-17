package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/rpc"
	dbkubernetes "github.com/flanksource/commons-db/kubernetes"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/agentinstall"
	"github.com/flanksource/telltale/pkg/definition"
	"github.com/flanksource/telltale/pkg/deploy"
)

// javaDeployFlags are the inputs of the one command `telltale jvm deploy` and
// `telltale agents install java` both name. Which inputs are given picks how
// the agent gets into the JVM:
//   - a Deployer document (--file or --deployer) deploys it;
//   - a [kind/]name target or --issuer builds a Deployer from the flags,
//     filling what they leave out (withDefaults), and deploys that;
//   - otherwise the agent is attached to one --pid at a local, SSH or
//     Kubernetes destination, verified locally or through a hub.
type javaDeployFlags struct {
	file, deployer string
	// target is the positional [kind/]name: sts/cycle, deploy/oipa, pod/cycle-0 or cycle-0.
	target  string
	install agentinstall.Options
	// A Deployer built from flags: the selector, the attach listener and auth.
	name, issuer, jwksURL  string
	jwks                   deploy.JWKSSource
	workloadKind, workload string
	labels                 map[string]string
	url, bind              string
	port                   int
}

type javaDeployMode int

const (
	modeAttach           javaDeployMode = iota // one PID, local or hub verified
	modeDeployerDocument                       // a Deployer document
	modeDeployerFlags                          // a Deployer built from flags
)

// The flags only some modes read; any other mode refuses them rather than
// silently ignoring what was asked.
var (
	documentFlags    = []string{"file", "deployer"}
	attachOnlyFlags  = []string{"hub-url", "token-file", "host", "ssh-config"}
	builtOnlyFlags   = []string{"name", "issuer", "jwks-url", "jwks-mode", "jwks-kind", "jwks-name", "jwks-key", "kind", "workload", "labels", "url", "bind", "port"}
	builtSharedFlags = []string{"pid", "directory", "java-home", "service", "allow-mutation", "extensions-dir", "namespace", "pod", "container"}
)

func newJavaDeployCommand(use, short, route string) *cobra.Command {
	var flags javaDeployFlags
	cmd := &cobra.Command{
		Use:   use + " [target]",
		Short: short,
		Long: "Gets telltale's Java agent into running JVMs. `telltale jvm deploy` and `telltale agents install java` are the same command.\n\n" +
			"Given a target (sts/<name>, deploy/<name>, pod/<name> or a bare pod name), --issuer, or a Deployer (-f/--deployer), it deploys to the " +
			"Kubernetes pods the selector picks: each agent verifies short-lived tokens signed by the issuer against its JWKS, needs no hub, and is " +
			"reached later with --agent deployer/<name>/<namespace>/<pod>. A target or --issuer builds the Deployer from flags and defaults what they " +
			"leave out: the issuer to " + deploy.DefaultIssuer + " (created with a new key in the shared Flanksource connections on first use), the " +
			"namespace to the kubeconfig context's, the container to the pod's only one, --pid to JVM discovery through /proc, --directory to /tmp/telltale and --service and " +
			"--name to the target's name. The deployer is kept with its targets' state and the --kubeconfig/--context it used. --dry-run lists the " +
			"pods the selector picks.\n\n" +
			"Otherwise it attaches the agent to one --pid at a local, SSH or Kubernetes --destination, verified by local discovery or by a hub " +
			"(--hub-url and --token-file). --dry-run previews the artifact and its path without changing the target.",
		Example: "  telltale agents install java --namespace shop sts/orders\n" +
			"  telltale jvm deploy orders-0 --dry-run\n" +
			"  telltale agents install java --pid 1234 --directory ~/.local/share/telltale",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				flags.target = args[0]
			}
			mode, err := flags.mode(cmd.Flags())
			if err != nil {
				return err
			}
			switch mode {
			case modeAttach:
				return runJavaAttach(cmd, flags)
			case modeDeployerFlags:
				if flags, err = flags.withDefaults(cmd.Flags()); err != nil {
					return err
				}
			}
			return runJavaDeployer(cmd, flags, mode)
		},
	}
	// The optional [target] would otherwise be lifted into the route
	// (/api/v1/jvm/{target}/deploy); callers address the flat one.
	clicky.AnnotateRoutePath(cmd, route)
	f := cmd.Flags()
	f.StringVarP(&flags.file, "file", "f", "", "Deployer YAML file (kind: Deployer); local invocations only")
	f.StringVar(&flags.deployer, "deployer", "", "Deployer document, YAML or JSON (kind: Deployer)")
	cmd.MarkFlagsMutuallyExclusive("file", "deployer")

	f.StringVar(&flags.install.Destination.Kind, "destination", "local", "Destination of a single attach: local, kubernetes or ssh (a deployer is always kubernetes)")
	f.StringVar(&flags.install.Destination.Kubeconfig, "kubeconfig", "", "Kubeconfig file (default: KUBECONFIG or ~/.kube/config)")
	f.StringVar(&flags.install.Destination.Context, "context", "", "Kubernetes context (default: the kubeconfig's current context)")
	f.StringVar(&flags.install.Destination.Namespace, "namespace", "", "Kubernetes namespace")
	f.StringVar(&flags.install.Destination.Pod, "pod", "", "Kubernetes pod")
	f.StringVar(&flags.install.Destination.Container, "container", "", "Kubernetes container running the JVM")
	f.StringVar(&flags.install.Destination.Host, "host", "", "SSH host from the native SSH configuration")
	f.StringVar(&flags.install.Destination.SSHConfig, "ssh-config", "", "Native SSH configuration file")
	f.IntVar(&flags.install.PID, "pid", 0, "Target JVM PID (Kubernetes: discover the only Java process in /proc when omitted)")
	f.StringVar(&flags.install.Directory, "directory", "", "Absolute destination directory for versioned agent artifacts")
	f.StringVar(&flags.install.JavaHome, "java-home", "", "Target-compatible JDK home, including Attach API")
	f.StringVar(&flags.install.HubURL, "hub-url", "", "Hub URL reachable from the target JVM (single attach)")
	f.StringVar(&flags.install.TokenFile, "token-file", "", "Agent-scope hub token file; contents are not printed (single attach)")
	f.StringVar(&flags.install.Service, "service", "java", "Service identity advertised by the agent")
	f.BoolVar(&flags.install.AllowMutation, "allow-mutation", false, "Enable JVM mutation operations; each call also requires opt-in")
	f.StringVar(&flags.install.ExtensionsDir, "extensions-dir", "", "Absolute extension storage directory on the target JVM")
	f.BoolVar(&flags.install.DryRun, "dry-run", false, "Deployer: list the pods the selector picks; single attach: preview the artifact. Neither changes anything")

	f.StringVar(&flags.issuer, "issuer", "", "connection:// reference holding the signingKey secret: builds a Deployer from these flags")
	f.StringVar(&flags.name, "name", "", "Name of the built Deployer, part of its target ids (default: --service)")
	f.StringVar(&flags.jwksURL, "jwks-url", "", "Where the issuer runtime serves its JWKS, for key rotation without a redeploy")
	f.StringVar((*string)(&flags.jwks.Mode), "jwks-mode", "", "read or publish the JWKS in --jwks-kind/--jwks-name (unset: the issuer's own, inline)")
	f.StringVar(&flags.jwks.Kind, "jwks-kind", "ConfigMap", "ConfigMap or Secret holding the JWKS")
	f.StringVar(&flags.jwks.Name, "jwks-name", "", "Name of the ConfigMap or Secret holding the JWKS")
	f.StringVar(&flags.jwks.Key, "jwks-key", "", "Data key holding the JWKS (default jwks.json)")
	f.StringVar(&flags.workloadKind, "kind", "", "Workload the built Deployer selects: Deployment, StatefulSet or Pod (--pod implies Pod)")
	f.StringVar(&flags.workload, "workload", "", "Name of the one workload to select, instead of --labels")
	f.StringToStringVar(&flags.labels, "labels", nil, "Labels of the workloads to select, instead of --workload")
	f.StringVar(&flags.url, "url", "", "Listener URL template (default: a port-forward to --port of each pod)")
	f.StringVar(&flags.bind, "bind", "127.0.0.1", "Listener address in the container; the default is reachable only through a port-forward")
	f.IntVar(&flags.port, "port", 7070, "Listener port in the container")
	return cmd
}

// mode is how the given flags deploy, refusing flags that mode would ignore.
func (f javaDeployFlags) mode(set *pflag.FlagSet) (javaDeployMode, error) {
	given := func(names []string) []string {
		var out []string
		for _, name := range names {
			if set.Changed(name) {
				out = append(out, "--"+name)
			}
		}
		return out
	}
	refuse := func(names []string, why string) error {
		if extra := given(names); len(extra) > 0 {
			return fmt.Errorf("%s: %s", strings.Join(extra, ", "), why)
		}
		return nil
	}
	destination := set.Changed("destination") && f.install.Destination.Kind != "kubernetes"
	switch {
	case len(given(documentFlags)) > 0:
		if destination {
			return 0, errors.New("a Deployer deploys to Kubernetes; --destination must be kubernetes")
		}
		if f.target != "" {
			return 0, fmt.Errorf("target %s builds a Deployer from flags; the Deployer document already selects its pods", f.target)
		}
		return modeDeployerDocument, errors.Join(refuse(attachOnlyFlags, "only a single attach reads these, not a Deployer"),
			refuse(slices.Concat(builtOnlyFlags, builtSharedFlags), "these build a Deployer from flags; the Deployer document already says them"))
	case f.issuer != "" || f.target != "":
		if destination {
			return 0, errors.New("a Deployer deploys to Kubernetes; --destination must be kubernetes")
		}
		var selectorErr error
		if f.target != "" {
			selectorErr = refuse([]string{"pod", "kind", "workload", "labels"}, "target "+f.target+" already selects the pods")
		}
		return modeDeployerFlags, errors.Join(selectorErr,
			refuse(attachOnlyFlags, "only a single attach reads these; a Deployer's agents verify issuer tokens and need no hub"))
	default:
		return modeAttach, refuse(slices.DeleteFunc(slices.Clone(builtOnlyFlags), func(name string) bool { return name == "issuer" }),
			"these build a Deployer, which needs a target or --issuer")
	}
}

// The defaults a Deployer built from flags takes for what they leave out.
const defaultDirectory = "/tmp/telltale"

// withDefaults fills what a Deployer built from flags leaves out: the
// target's selector, the default issuer, JVM discovery through /proc,
// defaultDirectory, and the target's name as --service. The namespace and
// container default at deploy time, from the kubeconfig and the pods.
func (f javaDeployFlags) withDefaults(set *pflag.FlagSet) (javaDeployFlags, error) {
	name := f.install.Destination.Pod
	if f.target != "" {
		kind, target, err := parseTarget(f.target)
		if err != nil {
			return f, err
		}
		name = target
		if kind == deploy.WorkloadPod {
			f.install.Destination.Pod = target
		} else {
			f.workloadKind, f.workload = string(kind), target
		}
	}
	if name == "" {
		name = f.workload
	}
	if f.issuer == "" {
		f.issuer = deploy.DefaultIssuer
	}
	if f.install.Directory == "" {
		f.install.Directory = defaultDirectory
	}
	if !set.Changed("service") && name != "" {
		f.install.Service = name
	}
	return f, nil
}

// parseTarget reads kubectl-style sts/<name>, deploy/<name> and pod/<name>
// (or their long and plural forms); a bare name is a pod.
func parseTarget(target string) (deploy.WorkloadKind, string, error) {
	prefix, name, typed := strings.Cut(target, "/")
	if !typed {
		prefix, name = "pod", target
	}
	if name == "" || strings.Contains(name, "/") {
		return "", "", fmt.Errorf("target %q: want [kind/]name, such as sts/orders, deploy/orders or orders-0", target)
	}
	switch strings.ToLower(prefix) {
	case "sts", "statefulset", "statefulsets":
		return deploy.WorkloadStatefulSet, name, nil
	case "deploy", "deployment", "deployments":
		return deploy.WorkloadDeployment, name, nil
	case "po", "pod", "pods":
		return deploy.WorkloadPod, name, nil
	}
	return "", "", fmt.Errorf("target %q: kind %q is not sts, deploy or pod", target, prefix)
}

// builtDeployer is the Deployer the flags describe: one attach listener per
// selected pod, verifying tokens --issuer signed.
func (f javaDeployFlags) builtDeployer() (*deploy.Deployer, error) {
	d := f.install.Destination
	selector := deploy.Selector{Namespace: d.Namespace, Container: d.Container, Name: f.workload, Labels: f.labels}
	if d.Pod != "" {
		if f.workload != "" || len(f.labels) > 0 || (f.workloadKind != "" && !strings.EqualFold(f.workloadKind, string(deploy.WorkloadPod))) {
			return nil, errors.New("--pod selects one pod; it cannot be combined with --kind, --workload or --labels")
		}
		selector.Kind, selector.Name = deploy.WorkloadPod, d.Pod
	} else {
		kind, err := workloadKind(f.workloadKind)
		if err != nil {
			return nil, err
		}
		selector.Kind = kind
	}
	url := f.url
	if url == "" {
		url = fmt.Sprintf("portforward://{{.Pod.Name}}.{{.Pod.Namespace}}:%d?kind=pod", f.port)
	}
	container, err := deploy.NewJavaContainerSpec(deploy.AttachSpec{Kind: deploy.KindAttach, URL: url, PID: f.install.PID, Directory: f.install.Directory,
		Bind: f.bind, Port: f.port, Service: f.install.Service, JavaHome: f.install.JavaHome, ExtensionsDir: f.install.ExtensionsDir, AllowMutation: f.install.AllowMutation})
	if err != nil {
		return nil, err
	}
	auth := deploy.AuthSpec{Issuer: f.issuer, JWKSURL: f.jwksURL}
	if f.jwks.Mode != "" || f.jwks.Name != "" {
		jwks := f.jwks
		auth.JWKS = &jwks
	}
	name := f.name
	if name == "" {
		name = f.install.Service
	}
	return &deploy.Deployer{TypeMeta: metav1.TypeMeta{APIVersion: definition.APIVersion, Kind: deploy.Kind}, ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: deploy.Spec{Selector: selector, JavaContainer: container, Auth: auth}}, nil
}

func workloadKind(value string) (deploy.WorkloadKind, error) {
	for _, kind := range []deploy.WorkloadKind{deploy.WorkloadDeployment, deploy.WorkloadStatefulSet, deploy.WorkloadPod} {
		if strings.EqualFold(value, string(kind)) {
			return kind, nil
		}
	}
	return "", fmt.Errorf("--kind %q: want Deployment, StatefulSet or Pod (or --pod)", value)
}

// loadDeployer is the Deployer a document mode names: the --deployer
// document, or the --file one, which only a local invocation may read.
func (f javaDeployFlags) loadDeployer(ctx context.Context) (*deploy.Deployer, error) {
	if f.deployer != "" {
		return deploy.Decode([]byte(f.deployer))
	}
	if _, remote := rpc.RequestFromContext(ctx); remote {
		return nil, errors.New("--file reads a file on the machine running the command; send deployer in the request instead")
	}
	data, err := os.ReadFile(f.file)
	if err != nil {
		return nil, err
	}
	return deploy.Decode(data)
}

// ensureDefaultIssuer creates deploy.DefaultIssuer in the shared connections
// on first use, saying so on stderr; an existing one keeps its key.
func ensureDefaultIssuer(cmd *cobra.Command) error {
	connections, err := telltale.OpenConnections()
	if err != nil {
		return err
	}
	created, err := deploy.EnsureIssuer(connections, deploy.DefaultIssuer)
	if created {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "created issuer %s with a new signing key in the shared Flanksource connections\n", deploy.DefaultIssuer)
	}
	return err
}

func runJavaAttach(cmd *cobra.Command, flags javaDeployFlags) error {
	result, err := (agentinstall.Installer{Runtime: agentinstall.Java{}}).Install(cmd.Context(), flags.install)
	if err != nil {
		return err
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
}

func runJavaDeployer(cmd *cobra.Command, flags javaDeployFlags, mode javaDeployMode) error {
	cluster := deploy.Cluster{Kubeconfig: flags.install.Destination.Kubeconfig, Context: flags.install.Destination.Context}
	var deployer *deploy.Deployer
	var err error
	if mode == modeDeployerDocument {
		deployer, err = flags.loadDeployer(cmd.Context())
	} else {
		if flags.install.Destination.Namespace == "" {
			if flags.install.Destination.Namespace, err = (dbkubernetes.Options{Kubeconfig: cluster.Kubeconfig, Kubecontext: cluster.Context}).Namespace(); err != nil {
				return fmt.Errorf("--namespace not given: %w", err)
			}
		}
		if !flags.install.DryRun && flags.issuer == deploy.DefaultIssuer {
			if err := ensureDefaultIssuer(cmd); err != nil {
				return err
			}
		}
		deployer, err = flags.builtDeployer()
	}
	if err != nil {
		return err
	}
	ctx, err := deployerContext(cmd.Context())
	if err != nil {
		return err
	}
	if ctx, err = cluster.Bind(ctx); err != nil {
		return fmt.Errorf("kubernetes client: %w", err)
	}
	client, err := ctx.LocalKubernetes()
	if err != nil {
		return fmt.Errorf("kubernetes client: %w", err)
	}
	options := deploy.Options{Kubernetes: client, Cluster: cluster, DryRun: flags.install.DryRun}
	if !options.DryRun {
		if options.Issuer, err = deploy.NewIssuer(ctx, deployer.Spec.Auth); err != nil {
			return fmt.Errorf("deployer %s: %w", deployer.Name, err)
		}
	}
	status, deployErr := deploy.Deploy(ctx, *deployer, options)
	if len(status.Targets) == 0 {
		return deployErr
	}
	if !options.DryRun {
		store, err := deployerStore()
		if err != nil {
			return err
		}
		cluster.Annotate(deployer)
		deployer.Status = status
		if err := store.Save(*deployer); err != nil {
			return err
		}
	}
	return errors.Join(printRecords(cmd, status.Targets), deployErr)
}
