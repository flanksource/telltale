package rules

import (
	"context"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"time"
)

var _ = Describe("durable rule history", func() {
	It("reopens complete intents and results without dropping target provenance", func() {
		dir := GinkgoT().TempDir()
		journal, err := OpenJournal(dir)
		Expect(err).NotTo(HaveOccurred())
		event := Event{Kind: "intent", At: time.Now().UTC(), Incident: Incident{ID: "incident", Revision: "revision", State: "breached"}, Decision: &Decision{Targets: Targets{Processes: []ProcessRef{{Key: "42@1", PID: 42}}}}}
		Expect(journal.Append(context.Background(), event)).To(Succeed())
		Expect(journal.Close()).To(Succeed())
		journal, err = OpenJournal(dir)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(journal.Close)
		events, err := journal.Events(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(events).To(Equal([]Event{event}))
	})
})
