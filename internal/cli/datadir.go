package cli

import (
	"fmt"

	"github.com/flanksource/telltale/pkg/datadir"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const dataDirFlag = "data-dir"

// bindDataDir binds --data-dir to dir; resolveDataDir fills it in when unset.
func bindDataDir(flags *pflag.FlagSet, dir *string) {
	flags.StringVar(dir, dataDirFlag, "", "Directory holding telltale's trace data (default: $XDG_CACHE_HOME/telltale or the OS cache dir)")
}

// resolveDataDir sets the --data-dir of cmd, when it has one left unset, to
// telltale's cache root.
func resolveDataDir(cmd *cobra.Command) error {
	flag := cmd.Flags().Lookup(dataDirFlag)
	if flag == nil || flag.Value.String() != "" {
		return nil
	}
	dir, err := datadir.Cache()
	if err != nil {
		return err
	}
	if err := cmd.Flags().Set(dataDirFlag, dir); err != nil {
		return fmt.Errorf("set --%s: %w", dataDirFlag, err)
	}
	return nil
}
