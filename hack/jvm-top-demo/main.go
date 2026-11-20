// Run after make build: go run ./hack/jvm-top-demo
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/flanksource/commons/logger"
)

func main() {
	if err := run(); err != nil {
		logger.Errorf("%v", err)
		os.Exit(1)
	}
}
func run() (err error) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := os.MkdirAll(".tmp", 0700); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(".tmp", "jvm-top-demo-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(dir)) }()
	dir, err = filepath.Abs(dir)
	if err != nil {
		return err
	}
	env := append(os.Environ(), "XDG_CONFIG_HOME="+filepath.Join(dir, "config"))
	tokenCommand := exec.CommandContext(ctx, ".bin/telltale", "agents", "token", "create", "demo")
	tokenCommand.Env = env
	token, err := tokenCommand.Output()
	if err != nil {
		return err
	}
	server := exec.CommandContext(ctx, ".bin/telltale", "serve", "--port", "0", "--data-dir", dir)
	server.Env = env
	server.Stderr = os.Stderr
	stdout, err := server.StdoutPipe()
	if err != nil {
		return err
	}
	if err = server.Start(); err != nil {
		return err
	}
	defer func() { cancel(); _ = server.Wait() }()
	scanner := bufio.NewScanner(stdout)
	address := ""
	for scanner.Scan() {
		if value, ok := strings.CutPrefix(scanner.Text(), "Telltale API: "); ok {
			address = strings.TrimSpace(value)
			break
		}
	}
	if address == "" {
		return fmt.Errorf("server did not report its address: %v", scanner.Err())
	}
	javac := exec.CommandContext(ctx, "javac", "-d", dir, "pkg/agentinstall/testdata/Workload.java")
	if data, err := javac.CombinedOutput(); err != nil {
		return fmt.Errorf("javac: %w: %s", err, data)
	}
	config, err := json.Marshal(map[string]any{"url": address, "token": strings.TrimSpace(string(token)), "service": "jvm-top-demo", "allowMutation": false})
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "agent.json")
	if err = os.WriteFile(path, config, 0600); err != nil {
		return err
	}
	jar, err := filepath.Abs("pkg/agentinstall/java/assets/telltale-agent.jar")
	if err != nil {
		return err
	}
	java := exec.CommandContext(ctx, "java", "-javaagent:"+jar+"="+path, "-cp", dir, "Workload")
	java.Stderr = os.Stderr
	if err = java.Start(); err != nil {
		return err
	}
	defer func() { cancel(); _ = java.Wait() }()
	fmt.Printf("Demo hub: %s\nTerminal: .bin/telltale jvm top --server %s --agent jvm-top-demo\n", address, address)
	<-ctx.Done()
	return nil
}
