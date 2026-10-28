package goprof_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/goprof"
	"github.com/flanksource/telltale/pkg/tracing/stacks"
	"github.com/flanksource/telltale/pkg/tracing/target"
	"github.com/flanksource/telltale/pkg/tracing/tracingtest"
	"github.com/flanksource/telltale/sdk/protocol"
	"github.com/google/pprof/profile"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("agent capture end to end", func() {
	It("records mutex and block contention accumulated while the self capture is armed", func() {
		GinkgoT().Setenv("TELLTALE_ALLOW_MUTATION", "1")
		registry := agentclient.NewRegistry()
		registry.Dir = GinkgoT().TempDir()
		env := tracingtest.NewEnv(map[string]tracing.TracePlugin{goprof.Kind: goprof.NewKind(goprof.Addresses{Registry: registry})})
		capture, err := env.Runtime.Start(dbcontext.New(), tracing.StartRequest{Kind: goprof.Kind, Params: json.RawMessage(`{"target":{"seam":"self","allowMutation":true}}`)})
		Expect(err).NotTo(HaveOccurred())
		tracingtest.Running(capture.Session())
		var lock sync.Mutex
		count := 0
		ready, done := make(chan struct{}), make(chan struct{})
		lock.Lock()
		go func() { close(ready); lock.Lock(); count++; lock.Unlock(); close(done) }()
		<-ready
		time.Sleep(50 * time.Millisecond)
		lock.Unlock()
		<-done
		Expect(count).To(Equal(1))
		Expect(checkpoint(env, capture, `{"profile":"mutex"}`)).To(Succeed())
		Expect(checkpoint(env, capture, `{"profile":"block"}`)).To(Succeed())
		Expect(profiled(env, capture)).To(ContainElement(HaveField("Profile", "mutex")))
		Expect(profiled(env, capture)).To(ContainElement(HaveField("Profile", "block")))
	})
	It("falls back to process when the PID's discovery file is stale", func() {
		dir := GinkgoT().TempDir()
		path := filepath.Join(dir, "stale.json")
		endpoint := protocol.Endpoint{PID: os.Getpid(), Addr: "/example.sock", Token: "token", Started: time.Now()}
		data, err := json.Marshal(endpoint)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(path, data, 0o600)).To(Succeed())
		old := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
		Expect(os.Chtimes(path, old, old)).To(Succeed())
		registry := agentclient.NewRegistry()
		registry.Dir = dir
		plugin := goprof.NewKind(goprof.Addresses{Registry: registry})
		kinds := tracing.NewKinds()
		Expect(kinds.RegisterKind(goprof.Kind, plugin)).To(Succeed())
		resolved, err := plugin.ResolveTarget(json.RawMessage(fmt.Sprintf(`{"target":{"pid":%d}}`, os.Getpid())))
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved.Seam).To(Equal(target.Process))
	})
	It("profiles and dumps a child agent, prefers it for PID, and fails on disconnect", func() {
		config := GinkgoT().TempDir()
		child := tracingtest.StartGoChild(goChild, "-agent-config", config)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		registry := agentclient.NewRegistry()
		registry.Dir = filepath.Join(config, "telltale", "agents")
		dumpers, err := stacks.NewDumpers()
		Expect(err).NotTo(HaveOccurred())
		profiler := goprof.NewKind(goprof.Addresses{Registry: registry})
		env := tracingtest.NewEnv(map[string]tracing.TracePlugin{goprof.Kind: profiler, stacks.Kind: stacks.NewKind(registry, dumpers)})
		implicit := json.RawMessage(fmt.Sprintf(`{"target":{"pid":%d}}`, child.PID))
		resolved, err := profiler.ResolveTarget(implicit)
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved.Seam).To(Equal(target.Agent))
		params := json.RawMessage(fmt.Sprintf(`{"target":{"seam":"agent","pid":%d}}`, child.PID))
		capture, err := env.Runtime.Start(dbcontext.New(), tracing.StartRequest{Kind: goprof.Kind, Params: params})
		Expect(err).NotTo(HaveOccurred())
		tracingtest.Running(capture.Session())
		Expect(checkpoint(env, capture, `{"profile":"heap","reason":"manual"}`)).To(Succeed())
		Expect(profiled(env, capture)).To(ContainElement(And(HaveField("Profile", "heap"), HaveField("Reason", "manual"))))
		var exported bytes.Buffer
		Expect(env.Runtime.Export(context.Background(), ref(capture), goprof.PProf.Name, &exported)).To(Succeed())
		_, err = profile.Parse(&exported)
		Expect(err).NotTo(HaveOccurred())
		Expect(checkpoint(env, capture, `{"profile":"cpu","seconds":1}`)).To(Succeed())
		Expect(profiled(env, capture)).To(ContainElement(HaveField("Profile", "cpu")))
		stackCapture, err := env.Runtime.Start(dbcontext.New(), tracing.StartRequest{Kind: stacks.Kind, Params: params})
		Expect(err).NotTo(HaveOccurred())
		Expect(env.Runtime.Checkpoint(tracing.WithCheckpointParams(ctx, json.RawMessage(`{"reason":"manual"}`)), stackCapture)).To(Succeed())
		var rows []stacks.Stack
		stackRef := tracing.TraceRef{Kind: stacks.Kind, Stream: stackCapture.Session().Snapshot().Events.Stream}
		Expect(tracing.ReadRecords(ctx, env.Runtime, stackRef, func(row stacks.Stack) error { rows = append(rows, row); return nil })).To(Succeed())
		Expect(rows).NotTo(BeEmpty())
		Expect(rows).To(HaveEach(HaveField("Reason", "manual")))
		Expect(syscall.Kill(child.PID, syscall.SIGKILL)).To(Succeed())
		info := tracingtest.Ended(capture.Session())
		Expect(info.State).To(Equal(query.SessionFailed))
		Expect(info.Error).To(ContainSubstring("connection"))
	})
})
