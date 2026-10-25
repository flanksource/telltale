package groups

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// readJobs reads the running jobs of the system domain, the host's daemons,
// and of this user's gui domain, its agents; neither needs root.
func readJobs(ctx context.Context) (Jobs, error) {
	jobs := Jobs{}
	for domain, target := range map[string]string{DomainSystem: "system", DomainUser: "gui/" + strconv.Itoa(os.Getuid())} {
		var stdout, stderr bytes.Buffer
		cmd := exec.CommandContext(ctx, "launchctl", "print", target)
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("launchctl print %s: %w: %s", target, err, strings.TrimSpace(stderr.String()))
		}
		read, err := ParseLaunchctlPrint(&stdout, domain)
		if err != nil {
			return nil, err
		}
		for pid, job := range read {
			jobs[pid] = job
		}
	}
	return jobs, nil
}
