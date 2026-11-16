package snapshot

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/agentinstall"
	"github.com/flanksource/telltale/pkg/datadir"
)

func Resolve(ctx context.Context, agent string, pid int, javaHome string) (string, error) {
	if (agent == "") == (pid == 0) || pid < 0 {
		return "", fmt.Errorf("specify exactly one connected agent or positive local PID")
	}
	if err := agentclient.Default.Discover(ctx, ""); err != nil {
		return "", err
	}
	if agent != "" {
		return agent, nil
	}
	if connected := agentclient.Default.ByPID(pid); connected != nil {
		return connected.Hello.AgentID, nil
	}
	directory, err := datadir.Cache()
	if err != nil {
		return "", err
	}
	result, err := (agentinstall.Installer{Runtime: agentinstall.Java{}}).Install(ctx, agentinstall.Options{
		Destination: agentinstall.Destination{Kind: "local"}, PID: pid, Directory: filepath.Join(directory, "java"), JavaHome: javaHome, Service: "java",
	})
	if err != nil {
		return "", err
	}
	if err := agentclient.Default.Discover(ctx, ""); err != nil {
		return "", err
	}
	if result.Agent == "" {
		return "", fmt.Errorf("no agent discovered for PID %s", strconv.Itoa(pid))
	}
	return result.Agent, nil
}
