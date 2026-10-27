package rules

import (
	"context"
	captaincmux "github.com/flanksource/captain/pkg/cmux"
	clickyprocess "github.com/flanksource/clicky/process"
	"github.com/flanksource/telltale/pkg/tracing/process"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"time"
)

var _ = Describe("enforcement hooks", func() {
	It("interrupts only the verified cmux surface and refuses moved anchors", func() {
		calls := []string{}
		executor := &Executor{cmuxTop: func(context.Context) (captaincmux.TopSnapshot, error) {
			return captaincmux.TopSnapshot{Windows: []captaincmux.TopWindow{{Workspaces: []captaincmux.TopWorkspace{{ID: "workspace-1", Panes: []captaincmux.TopPane{{Surfaces: []captaincmux.TopSurface{{ID: "surface-1", Resources: captaincmux.ProcessResources{PIDs: []int{123}}}}}}}}}}}, nil
		}, cmuxSend: func(_ context.Context, workspace, surface, key string) error {
			calls = append(calls, workspace, surface, key)
			return nil
		}}
		session := SessionRef{Key: "claude:session-1", Workspace: "workspace-1", Surface: "surface-1", Anchors: []string{"123@1"}}
		Expect(executor.interruptCmux(context.Background(), HookSpec{Key: "Escape"}, session)).To(Succeed())
		Expect(calls).To(Equal([]string{"workspace-1", "surface-1", "Escape"}))
		session.Anchors = []string{"456@1"}
		Expect(executor.interruptCmux(context.Background(), HookSpec{Key: "Escape"}, session)).To(MatchError(ContainSubstring("moved")))
		Expect(calls).To(HaveLen(3))
	})
	It("passes typed invocation JSON to an executable and reports its output and failure", func() {
		d := Decision{Incident: Incident{Rule: "acme"}, Hook: HookSpec{Type: "exec", Command: []string{"jq", "-r", ".incident.rule"}}, Targets: Targets{Processes: []ProcessRef{{Key: "123@1"}}}}
		result := runExecutable(context.Background(), d)
		Expect(result.Status).To(Equal("succeeded"))
		Expect(result.Stdout).To(Equal("acme\n"))
		d.Hook.Command = []string{"false"}
		result = runExecutable(context.Background(), d)
		Expect(result.Status).To(Equal("failed"))
		Expect(result.ExitCode).To(HaveValue(Equal(1)))
	})
	It("cancels a timed-out executable", func() {
		result := runExecutable(context.Background(), Decision{Hook: HookSpec{Type: "exec", Command: []string{"sleep", "60"}, Timeout: "10ms"}})
		Expect(result.Status).To(Equal("failed"))
		Expect(result.Error).NotTo(BeEmpty())
	})
	It("checks Captain capability and interrupts the exact session endpoint", func() {
		calls := []string{}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls = append(calls, r.Method+" "+r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			if r.Method == http.MethodGet {
				_, err := w.Write([]byte(`{"capabilities":{"interrupt":true},"state":{"status":"running"}}`))
				Expect(err).NotTo(HaveOccurred())
				return
			}
			w.WriteHeader(http.StatusAccepted)
		}))
		DeferCleanup(server.Close)
		err := interruptCaptain(context.Background(), HookSpec{Endpoint: server.URL}, SessionRef{ID: "session-1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(calls).To(Equal([]string{"GET /api/captain/sessions/session-1/chat", "POST /api/captain/sessions/session-1/interrupt"}))
	})
	It("signals only a verified disposable process and rejects stale identities", func() {
		child := exec.Command("sleep", "60")
		Expect(child.Start()).To(Succeed())
		DeferCleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
		snapshot, err := clickyprocess.Discover(context.Background(), clickyprocess.SnapshotOptions{})
		Expect(err).NotTo(HaveOccurred())
		p, ok := snapshot.Get(child.Process.Pid)
		Expect(ok).To(BeTrue())
		d := Decision{SampledAt: time.Now(), Hook: HookSpec{Type: "signal", Signal: "SIGTERM"}, Targets: Targets{Processes: []ProcessRef{{Key: process.Key(p), PID: p.PID}}}}
		executor := NewExecutor()
		result := executor.Run(context.Background(), d)
		Expect(result.Status).To(Equal("succeeded"))
		Expect(child.Wait()).NotTo(Succeed())
		d.Targets.Processes[0].Key += "0"
		result = executor.Run(context.Background(), d)
		Expect(result.Status).To(Equal("failed"))
		d.Targets.Processes[0] = ProcessRef{Key: "1@1", PID: 1}
		result = executor.Run(context.Background(), d)
		Expect(result.Status).To(Equal("failed"))
		Expect(os.Getpid()).NotTo(Equal(1))
	})
})
