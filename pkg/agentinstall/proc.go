package agentinstall

import (
	"context"
	_ "embed"
	"fmt"
	"strconv"
	"strings"
)

//go:embed proc.sh
var javaProcessesScript string

func javaPID(ctx context.Context, transport Transport) (int, error) {
	output, err := transport.Run(ctx, []string{"sh", "-c", javaProcessesScript, "telltale-java-processes", "/proc"}, nil)
	if err != nil {
		return 0, fmt.Errorf("discover Java processes in /proc: %w", err)
	}
	processes := strings.Fields(string(output))
	if len(processes) == 0 {
		return 0, fmt.Errorf("no Java process found in /proc; check process permissions or specify --pid")
	}
	for _, process := range processes {
		pid, err := strconv.Atoi(process)
		if err != nil || pid <= 0 {
			return 0, fmt.Errorf("invalid Java process PID %q in /proc discovery", process)
		}
	}
	if len(processes) != 1 {
		return 0, fmt.Errorf("multiple Java processes found in /proc (%s); specify --pid", strings.Join(processes, ", "))
	}
	pid, err := strconv.Atoi(processes[0])
	return pid, err
}
