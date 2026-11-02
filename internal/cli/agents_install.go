package cli

import (
	"encoding/json"
	"github.com/flanksource/telltale/pkg/agentinstall"
	"github.com/spf13/cobra"
)

func newAgentInstallCommand() *cobra.Command {
	install := &cobra.Command{Use: "install", Short: "Install a runtime agent"}
	var o agentinstall.Options
	java := &cobra.Command{Use: "java", Short: "Attach the Java agent to a local, Kubernetes or SSH JVM", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		result, err := (agentinstall.Installer{Runtime: agentinstall.Java{Home: o.JavaHome}}).Install(cmd.Context(), o)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}}
	f := java.Flags()
	f.StringVar(&o.Destination.Kind, "destination", "local", "Destination: local, kubernetes or ssh")
	f.IntVar(&o.PID, "pid", 0, "Target JVM PID")
	f.StringVar(&o.Directory, "directory", "", "Absolute destination directory for versioned agent artifacts")
	f.StringVar(&o.JavaHome, "java-home", "", "Target-compatible JDK home, including Attach API")
	f.StringVar(&o.Destination.Host, "host", "", "SSH host from the native SSH configuration")
	f.StringVar(&o.Destination.SSHConfig, "ssh-config", "", "Native SSH configuration file")
	f.StringVar(&o.Destination.Context, "context", "", "Kubernetes context")
	f.StringVar(&o.Destination.Namespace, "namespace", "", "Kubernetes namespace")
	f.StringVar(&o.Destination.Pod, "pod", "", "Kubernetes pod")
	f.StringVar(&o.Destination.Container, "container", "", "Kubernetes container")
	f.StringVar(&o.HubURL, "hub-url", "", "Hub URL reachable from the target JVM")
	f.StringVar(&o.TokenFile, "token-file", "", "Agent-scope hub token file; contents are not printed")
	f.StringVar(&o.Service, "service", "java", "Service identity advertised by the agent")
	f.BoolVar(&o.DryRun, "dry-run", false, "Preview the installation without changing the target")
	install.AddCommand(java)
	return install
}
