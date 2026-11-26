package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/datadir"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"
)

const sessionStoreFlag = "session-store"

func sessionStoreReference(cmd *cobra.Command) (string, error) {
	if flag := cmd.Flags().Lookup(sessionStoreFlag); flag != nil && flag.Changed {
		return flag.Value.String(), nil
	}
	dirs, err := datadir.ConfigDirs()
	if err != nil {
		return "", err
	}
	path, err := datadir.ConfigFile(dirs, "config.yaml")
	if err != nil || path == "" {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read session store configuration: %w", err)
	}
	var config struct {
		SessionStore string `json:"sessionStore"`
	}
	if err := yaml.UnmarshalStrict(data, &config); err != nil {
		return "", errors.New("invalid config.yaml: expected a sessionStore connection reference")
	}
	return config.SessionStore, nil
}

func openSessionStores(cmd *cobra.Command, options telltale.StartOptions) (traces.SessionStores, func(), error) {
	reference, err := sessionStoreReference(cmd)
	if err != nil {
		return nil, nil, err
	}
	return traces.OpenSessionStores(cmd.Context(), options.DataDir, reference, options.Connections)
}

func finishSessionRun(run *telltale.Run) error {
	if ended(run.Done()) {
		return nil
	}
	stopErr := run.Stop()
	_, waitErr := run.Wait()
	return errors.Join(stopErr, waitErr)
}
