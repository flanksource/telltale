package cli

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"path/filepath"

	"github.com/flanksource/clicky/entity"
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
}

func newJVMDeployCommand() *cobra.Command {
	var file string
	var kubeconfig []string
	cmd := &cobra.Command{
		Use:   "deploy -f <deployer.yaml>",
		Short: "Install the agent into the JVMs a Deployer selects, and keep it for later commands",
		Long: "Deploys the Deployer in the file to the running pods its selector picks, with your kubeconfig, and keeps it with each target's state. " +
			"Every jvm command then reaches a deployed pod with --agent deployer/<deployer>/<namespace>/<pod>, signing each request with the deployer's issuer.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			data, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			deployer, err := deploy.Decode(data)
			if err != nil {
				return err
			}
			store, err := deployerStore()
			if err != nil {
				return err
			}
			ctx, err := deployerContext(cmd.Context())
			if err != nil {
				return err
			}
			client, err := ctx.LocalKubernetes(kubeconfig...)
			if err != nil {
				return fmt.Errorf("kubernetes client: %w", err)
			}
			issuer, err := deploy.NewIssuer(ctx, deployer.Spec.Auth)
			if err != nil {
				return fmt.Errorf("deployer %s: %w", deployer.Name, err)
			}
			status, deployErr := deploy.Deploy(ctx, *deployer, deploy.Options{Kubernetes: client, Issuer: issuer})
			if len(status.Targets) > 0 {
				deployer.Status = status
				if err := store.Save(*deployer); err != nil {
					return err
				}
				if err := printRecords(cmd, status.Targets); err != nil {
					return err
				}
			}
			return deployErr
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Deployer YAML (kind: Deployer)")
	if err := cmd.MarkFlagRequired("file"); err != nil {
		panic(err)
	}
	cmd.Flags().StringSliceVar(&kubeconfig, "kubeconfig", nil, "Kubeconfig files (default: KUBECONFIG or ~/.kube/config)")
	// It reads a file and deploys with this user's kubeconfig: never over RPC.
	entity.MarkLocalOnly(cmd)
	return cmd
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
			var targets []deploy.TargetStatus
			for _, deployer := range deployers {
				targets = append(targets, deployer.Status.Targets...)
			}
			return printRecords(cmd, targets)
		},
	}
}
