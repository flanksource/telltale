package agentinstall

import (
	"context"
	"fmt"
	"io"
	"strings"

	clickyexec "github.com/flanksource/clicky/exec"
)

type Destination struct {
	Kind      string `json:"kind"`
	Host      string `json:"host,omitempty"`
	SSHConfig string `json:"sshConfig,omitempty"`
	Context   string `json:"context,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Pod       string `json:"pod,omitempty"`
	Container string `json:"container,omitempty"`
}

func (d Destination) Validate() error {
	switch d.Kind {
	case "local":
		if d.Host != "" || d.Pod != "" {
			return fmt.Errorf("local destination cannot name a remote host or pod")
		}
	case "ssh":
		if d.Host == "" || strings.HasPrefix(d.Host, "-") || strings.ContainsAny(d.Host, "\n\r\x00 ") {
			return fmt.Errorf("SSH host is required and must be a single host name")
		}
	case "kubernetes":
		if d.Namespace == "" || d.Pod == "" || d.Container == "" {
			return fmt.Errorf("kubernetes destination requires namespace, pod and container")
		}
	default:
		return fmt.Errorf("unknown installation destination %q", d.Kind)
	}
	return nil
}

type Transport interface {
	Run(context.Context, []string, io.Reader) ([]byte, error)
}
type CommandTransport struct{ Destination Destination }

func (t CommandTransport) Run(ctx context.Context, args []string, input io.Reader) ([]byte, error) {
	if err := t.Destination.Validate(); err != nil {
		return nil, err
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("empty remote command")
	}
	d := t.Destination
	switch d.Kind {
	case "ssh":
		quoted := make([]string, len(args))
		for i, arg := range args {
			quoted[i] = "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
		}
		prefix := []string{"ssh", "-o", "BatchMode=yes"}
		if d.SSHConfig != "" {
			prefix = append(prefix, "-F", d.SSHConfig)
		}
		args = append(prefix, d.Host, strings.Join(quoted, " "))
	case "kubernetes":
		prefix := []string{"kubectl"}
		if d.Context != "" {
			prefix = append(prefix, "--context", d.Context)
		}
		prefix = append(prefix, "--namespace", d.Namespace, "exec", "-i", d.Pod, "--container", d.Container, "--")
		args = append(prefix, args...)
	}
	process := clickyexec.NewExec(args[0], args[1:]...).WithContext(ctx).WithStdin(input).WithCaptureLimit(1 << 20).Run()
	if process.Err != nil {
		return nil, fmt.Errorf("%s command %s failed: %w: %s", d.Kind, args[0], process.Err, process.GetStderr())
	}
	return []byte(process.GetStdout()), nil
}
