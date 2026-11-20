package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/flanksource/clicky/rpc"

	"github.com/flanksource/telltale/internal/jvm/snapshot"
	"github.com/spf13/cobra"
)

func newJVMSnapshotCommand() *cobra.Command {
	var options snapshot.Options
	var pid int
	var output, javaHome, server string
	command := &cobra.Command{Use: "snapshot", Short: "Build a redacted JVM support ZIP", Args: cobra.NoArgs}
	command.RunE = func(cmd *cobra.Command, _ []string) error {
		if _, remote := rpc.RequestFromContext(cmd.Context()); remote {
			return errors.New("use POST /api/v1/jvm/snapshot to download a snapshot")
		}
		if err := options.Validate(); err != nil {
			return err
		}
		// The collectors and a JFR recording take up to 7m; workload sampling runs after them.
		ctx, cancel := context.WithTimeout(cmd.Context(), 12*time.Minute)
		defer cancel()
		if output == "" {
			identifier := strconv.Itoa(pid)
			if options.Agent != "" {
				identifier = strings.Map(func(r rune) rune {
					if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' {
						return r
					}
					return '-'
				}, options.Agent)
			}
			output = fmt.Sprintf("telltale-jvm-%s-%s.zip", identifier, time.Now().UTC().Format("20060102T150405.000000000Z"))
		}
		file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		saved := false
		defer func() {
			_ = file.Close()
			if !saved {
				_ = os.Remove(output)
			}
		}()
		var data []byte
		var complete bool
		if server != "" {
			if pid != 0 || options.Agent == "" {
				return errors.New("--server requires --agent and cannot use --pid")
			}
			data, complete, err = remoteSnapshot(ctx, server, options)
		} else {
			options.Agent, err = snapshot.Resolve(ctx, options.Agent, pid, javaHome)
			if err == nil {
				var result *snapshot.Result
				result, err = snapshot.Collect(ctx, options)
				if err == nil {
					data, err = result.Zip()
					complete = result.Manifest.Complete
				}
			}
		}
		if err != nil {
			return err
		}
		if _, err = file.Write(data); err != nil {
			return err
		}
		if err = file.Close(); err != nil {
			return err
		}
		saved = true
		if _, err = fmt.Fprintln(cmd.OutOrStdout(), output); err != nil {
			return err
		}
		if !complete {
			return fmt.Errorf("partial support bundle saved to %s; see summary.md for collection failures", output)
		}
		return nil
	}
	flags := command.Flags()
	flags.StringVar(&options.Agent, "agent", "", "Connected Java agent ID")
	flags.IntVar(&pid, "pid", 0, "Local JVM PID; attaches the agent if needed")
	flags.StringVar(&javaHome, "java-home", "", "Target-compatible JDK for attachment")
	flags.StringVar(&server, "server", "", "Telltale hub URL for --agent")
	flags.StringVarP(&output, "output", "o", "", "Destination ZIP (must not exist)")
	flags.StringVar(&options.JFR, "jfr", "", "Include a bounded JFR recording, e.g. 30s (maximum 5m)")
	flags.BoolVar(&options.HeapHistogram, "heap-histogram", false, "Include all-object heap histogram; may pause the JVM")
	flags.StringVar(&options.Workload, "workload", "1m", "Sample CPU, memory, GC and threads every 5s for this long into workload.html (5s to 5m; empty disables)")
	command.MarkFlagsOneRequired("agent", "pid")
	command.MarkFlagsMutuallyExclusive("agent", "pid")
	return command
}

func remoteSnapshot(ctx context.Context, server string, options snapshot.Options) (data []byte, complete bool, err error) {
	address, err := url.Parse(server)
	if err != nil || address.Host == "" || (address.Scheme != "http" && address.Scheme != "https") || address.User != nil || address.RawQuery != "" || address.Fragment != "" {
		return nil, false, fmt.Errorf("invalid server URL")
	}
	body, err := json.Marshal(options)
	if err != nil {
		return nil, false, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(server, "/")+"/api/v1/jvm/snapshot", bytes.NewReader(body))
	if err != nil {
		return nil, false, err
	}
	request.Header.Set("Content-Type", "application/json")
	if token := os.Getenv("TELLTALE_API_TOKEN"); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, false, err
	}
	defer func() { err = errors.Join(err, response.Body.Close()) }()
	if response.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("snapshot HTTP %d", response.StatusCode)
	}
	if response.Header.Get("Content-Type") != "application/zip" {
		return nil, false, fmt.Errorf("snapshot returned unexpected content type %q", response.Header.Get("Content-Type"))
	}
	if status := response.Header.Get("X-Telltale-Snapshot-Status"); status != "complete" && status != "partial" {
		return nil, false, fmt.Errorf("snapshot returned invalid completion status %q", status)
	}
	data, err = io.ReadAll(io.LimitReader(response.Body, 128<<20+1))
	if len(data) > 128<<20 {
		return nil, false, fmt.Errorf("snapshot exceeds 128 MiB")
	}
	return data, response.Header.Get("X-Telltale-Snapshot-Status") == "complete", err
}
