package seam_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing/seam"
	"github.com/flanksource/telltale/pkg/tracing/target"
	telltale "github.com/flanksource/telltale/sdk"
	"github.com/flanksource/telltale/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type emitter struct {
	values   []string
	warnings chan string
}

type partialProbe struct{ attempts atomic.Int32 }

func (*partialProbe) Spec() protocol.ProbeSpec { return protocol.ProbeSpec{Name: "partial-arm"} }
func (p *partialProbe) Arm(context.Context, json.RawMessage, telltale.Sink) (func() error, error) {
	if p.attempts.Add(1) > 1 {
		return nil, errors.New("agent refused arm")
	}
	return func() error { return nil }, nil
}

type refusingProbe struct{}

func (refusingProbe) Spec() protocol.ProbeSpec { return protocol.ProbeSpec{Name: "refusing-arm"} }
func (refusingProbe) Arm(context.Context, json.RawMessage, telltale.Sink) (func() error, error) {
	return nil, errors.New("mutation not allowed by agent")
}

func testRegistry() *agentclient.Registry {
	registry := agentclient.NewRegistry()
	registry.Dir = GinkgoT().TempDir()
	return registry
}

func (e *emitter) Emit(_ context.Context, value string) error {
	e.values = append(e.values, value)
	return nil
}
func (e *emitter) TryEmit(value string) bool { e.values = append(e.values, value); return true }
func (*emitter) Report(any)                  {}
func (e *emitter) Warn(message string) {
	if e.warnings != nil {
		e.warnings <- message
	}
}
func (*emitter) Describe(string, ...query.SessionMetadata) {}
func (*emitter) Export(string, string)                     {}

var _ = Describe("agent seam", func() {
	It("warns when one selector agent refuses to arm but another succeeds", func() {
		telltale.RegisterProbe(&partialProbe{})
		registry := testRegistry()
		first, second := telltale.Loopback(), telltale.Loopback()
		a, err := agentclient.Connect(context.Background(), first.Connect(), "")
		Expect(err).NotTo(HaveOccurred())
		b, err := agentclient.Connect(context.Background(), second.Connect(), "")
		Expect(err).NotTo(HaveOccurred())
		b.Hello.AgentID = "other-agent"
		DeferCleanup(func() { _ = a.Close(); _ = b.Close(); _ = first.Close(); _ = second.Close() })
		Expect(registry.Add(a)).To(Succeed())
		Expect(registry.Add(b)).To(Succeed())
		s := seam.AgentSeam[target.Target, string]{Probe: "partial-arm", Registry: registry, Target: func(t target.Target) target.Target { return t }}
		e := &emitter{warnings: make(chan string, 2)}
		params := target.Target{Seam: target.Agent, Selector: map[string]string{"process.pid": strconv.Itoa(os.Getpid())}}
		base, cancel := context.WithCancel(context.Background())
		ctx, release, err := s.Prepare(dbcontext.New().Wrap(base), params, e)
		Expect(err).NotTo(HaveOccurred())
		Eventually(e.warnings).Should(Receive(ContainSubstring("agent refused arm")))
		finished := make(chan error, 1)
		go func() { finished <- s.Handle(ctx, params, e) }()
		late := telltale.Loopback()
		c, err := agentclient.Connect(context.Background(), late.Connect(), "")
		Expect(err).NotTo(HaveOccurred())
		c.Hello.AgentID = "late-agent"
		DeferCleanup(func() { _ = c.Close(); _ = late.Close() })
		Expect(registry.Add(c)).To(Succeed())
		Eventually(e.warnings).Should(Receive(ContainSubstring("agent refused arm")))
		cancel()
		Eventually(finished).Should(Receive(BeNil()))
		Expect(release()).To(Succeed())
	})
	It("names why each selector agent refused when none arms", func() {
		telltale.RegisterProbe(refusingProbe{})
		registry := testRegistry()
		server := telltale.Loopback()
		a, err := agentclient.Connect(context.Background(), server.Connect(), "")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = a.Close(); _ = server.Close() })
		Expect(registry.Add(a)).To(Succeed())
		s := seam.AgentSeam[target.Target, string]{Probe: "refusing-arm", Registry: registry, Target: func(t target.Target) target.Target { return t }}
		params := target.Target{Seam: target.Agent, Selector: map[string]string{"process.pid": strconv.Itoa(os.Getpid())}}
		_, _, err = s.Prepare(dbcontext.New(), params, &emitter{warnings: make(chan string, 1)})
		Expect(err).To(MatchError(And(ContainSubstring("no agent armed probe refusing-arm"), ContainSubstring("mutation not allowed by agent"))))
	})
	It("keeps a selector capture open when all agents leave and arms a replacement", func() {
		registry := testRegistry()
		first, second := telltale.Loopback(), telltale.Loopback()
		a, err := agentclient.Connect(context.Background(), first.Connect(), "")
		Expect(err).NotTo(HaveOccurred())
		b, err := agentclient.Connect(context.Background(), second.Connect(), "")
		Expect(err).NotTo(HaveOccurred())
		b.Hello.AgentID = "second-agent"
		DeferCleanup(func() { _ = a.Close(); _ = b.Close(); _ = first.Close(); _ = second.Close() })
		Expect(registry.Add(a)).To(Succeed())
		Expect(registry.Add(b)).To(Succeed())
		s := seam.AgentSeam[target.Target, string]{Probe: "stacks", Registry: registry, Target: func(t target.Target) target.Target { return t }, DecodeBlob: func(_ agentclient.Blob, _ map[string]string) ([]string, error) { return []string{"stack"}, nil }}
		params := target.Target{Seam: target.Agent, Selector: map[string]string{"process.pid": strconv.Itoa(os.Getpid())}}
		e := &emitter{warnings: make(chan string, 2)}
		base, cancel := context.WithCancel(context.Background())
		ctx, release, err := s.Prepare(dbcontext.New().Wrap(base), params, e)
		Expect(err).NotTo(HaveOccurred())
		finished := make(chan error, 1)
		go func() { finished <- s.Handle(ctx, params, e) }()
		Expect(first.Close()).To(Succeed())
		Eventually(e.warnings).Should(Receive(ContainSubstring("connection")))
		Expect(s.Checkpoint(ctx, json.RawMessage(`{}`), e)).To(Succeed())
		Expect(e.values).To(Equal([]string{"stack"}))
		Expect(second.Close()).To(Succeed())
		Eventually(e.warnings).Should(Receive(ContainSubstring("connection")))
		Consistently(finished, 200*time.Millisecond).ShouldNot(Receive())
		replacement := telltale.Loopback()
		c, err := agentclient.Connect(context.Background(), replacement.Connect(), "")
		Expect(err).NotTo(HaveOccurred())
		c.Hello.AgentID = "replacement-agent"
		DeferCleanup(func() { _ = c.Close(); _ = replacement.Close() })
		Expect(registry.Add(c)).To(Succeed())
		Eventually(func() int {
			e.values = nil
			Expect(s.Checkpoint(ctx, json.RawMessage(`{}`), e)).To(Succeed())
			return len(e.values)
		}, 5*time.Second).Should(Equal(1))
		cancel()
		Eventually(finished).Should(Receive(BeNil()))
		Expect(release()).To(Succeed())
	})
	It("arms a matching agent that joins a running selector capture", func() {
		registry := testRegistry()
		first := telltale.Loopback()
		second := telltale.Loopback()
		DeferCleanup(func() { Expect(first.Close()).To(Succeed()); Expect(second.Close()).To(Succeed()) })
		a, err := agentclient.Connect(context.Background(), first.Connect(), "")
		Expect(err).NotTo(HaveOccurred())
		b, err := agentclient.Connect(context.Background(), second.Connect(), "")
		Expect(err).NotTo(HaveOccurred())
		b.Hello.AgentID = "late-agent"
		DeferCleanup(func() { Expect(a.Close()).To(Succeed()); Expect(b.Close()).To(Succeed()) })
		Expect(registry.Add(a)).To(Succeed())
		s := seam.AgentSeam[target.Target, string]{Probe: "stacks", Registry: registry, Target: func(t target.Target) target.Target { return t }, DecodeBlob: func(_ agentclient.Blob, _ map[string]string) ([]string, error) { return []string{"stack"}, nil }}
		params := target.Target{Seam: target.Agent, Selector: map[string]string{"process.pid": strconv.Itoa(os.Getpid())}}
		e := &emitter{}
		base, cancel := context.WithCancel(context.Background())
		ctx, release, err := s.Prepare(dbcontext.New().Wrap(base), params, e)
		Expect(err).NotTo(HaveOccurred())
		finished := make(chan error, 1)
		go func() { finished <- s.Handle(ctx, params, e) }()
		Expect(registry.Add(b)).To(Succeed())
		Eventually(func() int {
			e.values = nil
			Expect(s.Checkpoint(ctx, json.RawMessage(`{}`), e)).To(Succeed())
			return len(e.values)
		}, 5*time.Second).Should(Equal(2))
		cancel()
		Eventually(finished).Should(Receive(BeNil()))
		Expect(release()).To(Succeed())
	})
	It("refuses to start if the agent does not advertise the probe", func() {
		s := seam.AgentSeam[target.Target, string]{Probe: "missing", Target: func(t target.Target) target.Target { return t }}
		_, _, err := s.Prepare(dbcontext.New(), target.Target{Seam: target.Self}, &emitter{})
		Expect(err).To(MatchError(ContainSubstring("no agent armed")))
	})
	It("fails the capture when the agent connection drops", func() {
		server := telltale.Loopback()
		agent, err := agentclient.Connect(context.Background(), server.Connect(), "")
		Expect(err).NotTo(HaveOccurred())
		registry := testRegistry()
		Expect(registry.Add(agent)).To(Succeed())
		s := seam.AgentSeam[target.Target, string]{Probe: "stacks", Registry: registry, Target: func(t target.Target) target.Target { return t }}
		params := target.Target{Seam: target.Agent, Agent: agent.Hello.AgentID}
		ctx, release, err := s.Prepare(dbcontext.New(), params, &emitter{})
		Expect(err).NotTo(HaveOccurred())
		finished := make(chan error, 1)
		go func() { finished <- s.Handle(ctx, params, &emitter{}) }()
		Expect(server.Close()).To(Succeed())
		var failure error
		Eventually(finished, 5*time.Second).Should(Receive(&failure))
		Expect(failure).To(MatchError(ContainSubstring("connection")))
		Expect(release()).To(MatchError(ContainSubstring("connection")))
	})
	It("arms the self probe and decodes a checkpoint blob", func() {
		e := &emitter{}
		s := seam.AgentSeam[target.Target, string]{Probe: "stacks", Target: func(t target.Target) target.Target { return t }, DecodeBlob: func(_ agentclient.Blob, _ map[string]string) ([]string, error) { return []string{"stack"}, nil }}
		ctx, release, err := s.Prepare(dbcontext.New(), target.Target{Seam: target.Self}, e)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(release()).To(Succeed()) })
		Expect(s.Checkpoint(ctx, json.RawMessage(`{}`), e)).To(Succeed())
		Expect(e.values).To(Equal([]string{"stack"}))
	})
})
