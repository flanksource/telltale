package rules

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/flanksource/telltale/pkg/tracing/groups"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type memoryJournal struct {
	mu     sync.Mutex
	events []Event
	fail   bool
}

func (j *memoryJournal) Append(_ context.Context, event Event) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.fail {
		return fmt.Errorf("disk unavailable")
	}
	j.events = append(j.events, event)
	return nil
}
func (j *memoryJournal) Events(context.Context) ([]Event, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]Event(nil), j.events...), nil
}
func (*memoryJournal) Close() error { return nil }

type testSource struct{ reads atomic.Int64 }

func (s *testSource) Snapshot(context.Context) (groups.Snapshot, error) {
	s.reads.Add(1)
	return testSnapshot(time.Now().UTC(), new(200.0)), nil
}
func (*testSource) Close() {}

type testRunner struct{ calls atomic.Int64 }

func (r *testRunner) Run(context.Context, Decision) HookResult {
	r.calls.Add(1)
	return HookResult{Status: "succeeded"}
}

var _ = Describe("continuous rules service", func() {
	It("coalesces identical built-in targets while retaining both incident results", func() {
		base, err := Compile([]byte(timedRule))
		Expect(err).NotTo(HaveOccurred())
		rule := base.Config.Rules[0]
		rule.For = "0s"
		rule.Steps = rule.Steps[:1]
		other := rule
		other.Name = "other"
		data, err := json.Marshal(Config{Version: 1, Rules: []Rule{rule, other}})
		Expect(err).NotTo(HaveOccurred())
		program, err := Compile(data)
		Expect(err).NotTo(HaveOccurred())
		journal := &memoryJournal{}
		service, err := NewService(ServiceOptions{Program: program, Source: &testSource{}, Journal: journal, Runner: &testRunner{}, Every: 20 * time.Millisecond})
		Expect(err).NotTo(HaveOccurred())
		service.Start(context.Background())
		DeferCleanup(service.Close)
		Eventually(func() []bool {
			events, err := journal.Events(context.Background())
			Expect(err).NotTo(HaveOccurred())
			coalesced := []bool{}
			for _, event := range events {
				if event.Result != nil {
					coalesced = append(coalesced, event.Result.Coalesced)
				}
			}
			return coalesced
		}).Should(Equal([]bool{false, true}))
	})
	It("breaks pending timing immediately on a failed observation", func() {
		program, err := Compile([]byte(timedRule))
		Expect(err).NotTo(HaveOccurred())
		service, err := NewService(ServiceOptions{Program: program, Source: &testSource{}, Journal: &memoryJournal{}, Runner: &testRunner{}, Every: 2 * time.Second})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(service.Close)
		at := time.Now().UTC()
		service.observe(context.Background(), testSnapshot(at, new(200.0)), nil)
		service.observe(context.Background(), groups.Snapshot{}, fmt.Errorf("sampling failed"))
		service.observe(context.Background(), testSnapshot(at.Add(4*time.Second), new(200.0)), nil)
		Expect(service.due).To(BeEmpty())
		Expect(service.engine.States()[0].Since).To(Equal(at.Add(4 * time.Second)))
	})
	It("refuses a second enforcing instance until the user lock is released", func() {
		unlock, err := acquireLock()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(unlock)
		_, err = acquireLock()
		Expect(err).To(MatchError(ContainSubstring("another rules enforcer")))
	})
	It("samples without requests and records observation without executing a hook", func() {
		program, err := Compile([]byte(strings.ReplaceAll(timedRule, "4s", "0s")))
		Expect(err).NotTo(HaveOccurred())
		source := &testSource{}
		journal := &memoryJournal{}
		runner := &testRunner{}
		service, err := NewService(ServiceOptions{Program: program, Source: source, Journal: journal, Runner: runner, Every: 20 * time.Millisecond})
		Expect(err).NotTo(HaveOccurred())
		service.Start(context.Background())
		DeferCleanup(service.Close)
		Eventually(source.reads.Load).Should(BeNumerically(">=", 3))
		Eventually(func() int {
			events, err := journal.Events(context.Background())
			Expect(err).NotTo(HaveOccurred())
			count := 0
			for _, event := range events {
				if event.Kind == "result" && event.Result.Status == "observed" {
					count++
				}
			}
			return count
		}).Should(Equal(1))
		Expect(runner.calls.Load()).To(BeZero())
		Expect(service.Status().Error).To(BeEmpty())
	})
	It("blocks dispatch when intent persistence fails", func() {
		program, err := Compile([]byte(strings.ReplaceAll(timedRule, "4s", "0s")))
		Expect(err).NotTo(HaveOccurred())
		journal := &memoryJournal{fail: true}
		runner := &testRunner{}
		service, err := NewService(ServiceOptions{Program: program, Source: &testSource{}, Journal: journal, Runner: runner, Every: 20 * time.Millisecond})
		Expect(err).NotTo(HaveOccurred())
		service.Start(context.Background())
		DeferCleanup(service.Close)
		Eventually(func() string { return service.Status().Error }).Should(ContainSubstring("persistence failed"))
		Expect(runner.calls.Load()).To(BeZero())
	})
	It("restores unfinished attempts without replaying their stage", func() {
		program, err := Compile([]byte(timedRule))
		Expect(err).NotTo(HaveOccurred())
		engine := NewEngine(program, 2*time.Second)
		at := time.Now().UTC()
		state := Incident{ID: "unfinished", Rule: "cpu", Group: "session:claude:test", Revision: program.Revision, State: "breached", Step: 0}
		engine.Restore([]Event{{Kind: "intent", At: at, Incident: state}})
		decisions, _, err := engine.Advance(context.Background(), testSnapshot(at, new(200.0)))
		Expect(err).NotTo(HaveOccurred())
		Expect(decisions).To(BeEmpty())
		decisions, _, err = engine.Advance(context.Background(), testSnapshot(at.Add(2*time.Second), new(200.0)))
		Expect(err).NotTo(HaveOccurred())
		Expect(decisions).To(HaveLen(1))
		Expect(decisions[0].Hook.Signal).To(Equal("SIGTERM"))
	})
})
