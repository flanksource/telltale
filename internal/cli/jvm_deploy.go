package cli

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"path/filepath"

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
