package logs_test

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/logs"
	"github.com/flanksource/telltale/pkg/tracing/tracingtest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("child agent logs", func() {
	It("captures a slog record from an armed child with its process resource", func() {
		binary := tracingtest.GoChildBinary(GinkgoT().TempDir())
		config := GinkgoT().TempDir()
		child := tracingtest.StartGoChild(binary, "-agent-config", config)
		registry := agentclient.NewRegistry()
		registry.Dir = filepath.Join(config, "telltale", "agents")
		env := tracingtest.NewEnv(map[string]tracing.TracePlugin{logs.Kind: logs.NewKind(registry)})
		session := env.Start(logs.Kind, fmt.Sprintf(`{"target":{"pid":%d}}`, child.PID))
		tracingtest.Running(session)
		_, err := fmt.Fprintln(child.Stdin, "log example child message")
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() string { data, _ := json.Marshal(env.Rows(session.Snapshot())); return string(data) }).WithTimeout(10 * time.Second).Should(And(ContainSubstring("example child message"), ContainSubstring("process.pid"), ContainSubstring("child")))
	})
})
