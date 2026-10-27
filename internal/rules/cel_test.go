package rules

import (
	"context"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"time"
)

var _ = Describe("CEL resource matching and targeting", func() {
	var group MonitorGroup
	host := Host{Cores: 8, MemoryTotalBytes: 8 * 1024 * 1024 * 1024}
	at := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	BeforeEach(func() {
		group = MonitorGroup{Key: "session:claude:test", Members: []ProcessRef{{Key: "123@1", PID: 123, Name: "node", Resources: Resources{Cores: new(2.0), CPU: new(25.0), RSS: 1024}}}, Resources: Resources{Cores: new(2.0), CPU: new(25.0), RSS: 1024}}
	})
	It("evaluates consumed cores and host percentage with strict boundaries", func() {
		for expression, expected := range map[string]bool{"cores > 1": true, "cpu < 25": false, "cpu == 25.0": true, "cores / host.cores > 0.25": false, "cores / host.cores == 0.25": true} {
			compiled, err := compileExpression(expression, "bool", false)
			Expect(err).NotTo(HaveOccurred())
			actual, known, err := compiled.predicate(context.Background(), group, host, at, Incident{})
			Expect(err).NotTo(HaveOccurred())
			Expect(known).To(BeTrue())
			Expect(actual).To(Equal(expected), expression)
		}
	})
	It("preserves unknown group and member CPU rather than matching zero", func() {
		group.Cores, group.CPU, group.Members[0].Cores = nil, nil, nil
		compiled, err := compileExpression("cores < 1", "bool", false)
		Expect(err).NotTo(HaveOccurred())
		_, known, err := compiled.predicate(context.Background(), group, host, at, Incident{})
		Expect(err).NotTo(HaveOccurred())
		Expect(known).To(BeFalse())
		target, err := compileExpression("group.members.filter(p, p.cores < 1)", "process", true)
		Expect(err).NotTo(HaveOccurred())
		_, err = target.targets(context.Background(), group, host, at, Incident{})
		Expect(err).To(MatchError(ContainSubstring("unknown")))
	})
	It("filters typed process targets and rejects wrong types and unknown fields", func() {
		target, err := compileExpression(`group.members.filter(p, p.name == "node")`, "process", true)
		Expect(err).NotTo(HaveOccurred())
		selected, err := target.targets(context.Background(), group, host, at, Incident{})
		Expect(err).NotTo(HaveOccurred())
		Expect(selected.Processes).To(Equal(group.Members))
		for _, expression := range []string{"group.members", "cores.typo > 1", "missing > 1"} {
			_, err := compileExpression(expression, "bool", false)
			Expect(err).To(HaveOccurred())
		}
		_, err = compileExpression("group.sessions", "process", true)
		Expect(err).To(HaveOccurred())
	})
	It("rejects forged references and surfaces runtime errors", func() {
		compiled, err := compileExpression(`[rules.ProcessRef{key: "999@1", pid: 999}]`, "process", true)
		Expect(err).NotTo(HaveOccurred())
		_, err = compiled.targets(context.Background(), group, host, at, Incident{})
		Expect(err).To(MatchError(ContainSubstring("forged")))
		compiled, err = compileExpression(`10 / (rss - rss) > 1`, "bool", false)
		Expect(err).NotTo(HaveOccurred())
		_, _, err = compiled.predicate(context.Background(), group, host, at, Incident{})
		Expect(err).To(HaveOccurred())
	})
	It("keeps a known memory branch decisive when CPU is unknown", func() {
		group.Cores = nil
		compiled, err := compileExpression("cores > 1 || rss > 0", "bool", false)
		Expect(err).NotTo(HaveOccurred())
		value, known, err := compiled.predicate(context.Background(), group, host, at, Incident{})
		Expect(err).NotTo(HaveOccurred())
		Expect(value).To(BeTrue())
		Expect(known).To(BeTrue())
	})
})
