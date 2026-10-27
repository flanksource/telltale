package rules

import (
	"context"
	"encoding/json"
	"fmt"
	cmuxclient "github.com/flanksource/captain/pkg/ai/provider/cmux"
	captaincmux "github.com/flanksource/captain/pkg/cmux"
	clickyexec "github.com/flanksource/clicky/exec"
	"time"
)

func controllerCommand(ctx context.Context, cwd, binary string, timeout time.Duration, args ...string) (string, error) {
	process := clickyexec.NewExec(binary, args...).WithContext(ctx).WithTimeout(timeout).WithProcessGroup().WithCaptureLimit(64 * 1024)
	if cwd != "" {
		process = process.WithCwd(cwd)
	}
	result := process.Run().Result()
	if result.Error != nil {
		return "", result.Error
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("controller exited %d: %s", result.ExitCode, result.Stderr)
	}
	return result.Stdout, nil
}
func readCmuxTop(ctx context.Context) (captaincmux.TopSnapshot, error) {
	binary, err := captaincmux.CmuxBin()
	if err != nil {
		return captaincmux.TopSnapshot{}, err
	}
	output, err := controllerCommand(ctx, "", binary, 10*time.Second, "--json", "--id-format", "both", "top", "--all")
	if err != nil {
		return captaincmux.TopSnapshot{}, err
	}
	var snapshot captaincmux.TopSnapshot
	if err := json.Unmarshal([]byte(output), &snapshot); err != nil {
		return snapshot, fmt.Errorf("decode cmux top: %w", err)
	}
	return snapshot, nil
}
func sendCmuxKey(ctx context.Context, workspace, surface, key string) error {
	binary, err := captaincmux.CmuxBin()
	if err != nil {
		return err
	}
	client := cmuxclient.NewClient(binary)
	client.Runner = controllerCommand
	return client.SendKeySurface(ctx, workspace, surface, key)
}
