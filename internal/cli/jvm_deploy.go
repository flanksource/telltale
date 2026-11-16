package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"

	"github.com/flanksource/clicky/rpc"
	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/datadir"
	"github.com/flanksource/telltale/pkg/deploy"
	"github.com/spf13/cobra"
)

// deployerStore keeps applied deployers in telltale's global config dir.
func deployerStore() (deploy.Store, error) {
	dir, err := datadir.Config()
	if err != nil {
		return deploy.Store{}, err
	}
	return deploy.Store{Dir: filepath.Join(dir, "deployers")}, nil
}

// deployerContext is a context the deployer's connection references, its
// issuer's and its port-forwards', resolve in.
func deployerContext(ctx context.Context) (dbcontext.Context, error) {
	connections, err := telltale.OpenConnections()
	if err != nil {
		return dbcontext.Context{}, err
	}
	return connections.Attach(dbcontext.New().Wrap(ctx)), nil
}

// operator is the principal execution tokens name: this user on this host.
func operator() (string, error) {
	current, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("token subject: %w", err)
	}
	host, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("token subject: %w", err)
	}
	return current.Username + "@" + host, nil
}

// setDeployerResolver lets every agent lookup of this process reach a stored
// deployer's targets by their id, with tokens minted per request.
func setDeployerResolver(registry *agentclient.Registry) {
	registry.SetResolver(deploy.TargetPrefix, func(ctx context.Context, registry *agentclient.Registry, id string) error {
		store, err := deployerStore()
		if err != nil {
			return err
		}
		subject, err := operator()
		if err != nil {
			return err
		}
		return deploy.Resolver{Store: store, Subject: subject, Context: deployerContext}.Resolve(ctx, registry, id)
	})
	registry.SetLister(deploy.TargetPrefix, func(context.Context) ([]agentclient.Known, error) {
		store, err := deployerStore()
		if err != nil {
			return nil, err
		}
		return store.Known()
	})
}

// deployFlags are what `jvm deploy` deploys and how.
type deployFlags struct {
	file, deployer string
	kubeconfig     []string
	dryRun         bool
}

// load is the Deployer the flags give: the --deployer document, or the
// --file one, which only a local invocation may read.
func (f deployFlags) load(ctx context.Context) (*deploy.Deployer, error) {
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

func newJVMDeployCommand() *cobra.Command {
	var flags deployFlags
	cmd := &cobra.Command{
		Use:   "deploy (-f <deployer.yaml> | --deployer <document>)",
		Short: "Install the agent into the JVMs a Deployer selects, and keep it for later commands",
		Long: "Deploys the Deployer to the running pods its selector picks, with your kubeconfig, and keeps it with each target's state. " +
			"Every jvm command then reaches a deployed pod with --agent deployer/<deployer>/<namespace>/<pod>, signing each request with the deployer's issuer. " +
			"--dry-run only lists the pods the selector picks: it installs nothing and keeps nothing.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runJVMDeploy(cmd, flags) },
	}
	cmd.Flags().StringVarP(&flags.file, "file", "f", "", "Deployer YAML file (kind: Deployer); local invocations only")
	cmd.Flags().StringVar(&flags.deployer, "deployer", "", "Deployer document, YAML or JSON (kind: Deployer)")
	cmd.MarkFlagsOneRequired("file", "deployer")
	cmd.MarkFlagsMutuallyExclusive("file", "deployer")
	cmd.Flags().BoolVar(&flags.dryRun, "dry-run", false, "List the pods the selector picks without installing or keeping anything")
	cmd.Flags().StringSliceVar(&flags.kubeconfig, "kubeconfig", nil, "Kubeconfig files (default: KUBECONFIG or ~/.kube/config)")
	return cmd
}

func runJVMDeploy(cmd *cobra.Command, flags deployFlags) error {
	deployer, err := flags.load(cmd.Context())
	if err != nil {
		return err
	}
	ctx, err := deployerContext(cmd.Context())
	if err != nil {
		return err
	}
	client, err := ctx.LocalKubernetes(flags.kubeconfig...)
	if err != nil {
		return fmt.Errorf("kubernetes client: %w", err)
	}
	options := deploy.Options{Kubernetes: client, DryRun: flags.dryRun}
	if !flags.dryRun {
		if options.Issuer, err = deploy.NewIssuer(ctx, deployer.Spec.Auth); err != nil {
			return fmt.Errorf("deployer %s: %w", deployer.Name, err)
		}
	}
	status, deployErr := deploy.Deploy(ctx, *deployer, options)
	if len(status.Targets) == 0 {
		return deployErr
	}
	if !flags.dryRun {
		store, err := deployerStore()
		if err != nil {
			return err
		}
		deployer.Status = status
		if err := store.Save(*deployer); err != nil {
			return err
		}
	}
	return errors.Join(printRecords(cmd, status.Targets), deployErr)
}

func newJVMDeployersCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "deployers",
		Short: "List the deployed targets jvm commands can reach, by deployer",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := deployerStore()
			if err != nil {
				return err
			}
			deployers, err := store.List()
			if err != nil {
				return err
			}
			// An empty list, not null, when nothing was deployed yet.
			targets := []deploy.TargetStatus{}
			for _, deployer := range deployers {
				targets = append(targets, deployer.Status.Targets...)
			}
			return printRecords(cmd, targets)
		},
	}
}
