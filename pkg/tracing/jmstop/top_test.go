package jmstop

import (
	"github.com/flanksource/telltale/pkg/otlp/profiles"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"testing"
	"time"
)

func TestJMSTop(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "JMS Top") }

var _ = Describe("handler profiles", func() {
	It("rejects blank destination names before arming", func() {
		Expect((Params{Destinations: []string{"orders", " "}}).Validate()).To(MatchError(ContainSubstring("nonblank")))
	})
	It("round trips an idle mapping without losing its identity", func() {
		now := time.Now()
		snapshot := profiles.Snapshot{Kind: Kind, CaptureID: "capture", Source: "instance", ProfileID: uuid.NewString(), Sequence: 1, Complete: true, Observed: 1, Limit: 50, StartedAt: now, CompletedAt: now,
			Rows: []profiles.Row{{OperationID: "handler-1", SampledAt: now, Attributes: map[string]any{"destination": "orders", "handler": "OrderListener.onMessage", "state": "idle", "activeCount": 0}}}}
		data, err := profiles.Encode([]profiles.Snapshot{snapshot})
		Expect(err).NotTo(HaveOccurred())
		decoded, err := profiles.Decode(data, profiles.Options{ExpectedKind: Kind})
		Expect(err).NotTo(HaveOccurred())
		rows, err := FromSnapshot(decoded[0])
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(ConsistOf(And(HaveField("HandlerID", "handler-1"), HaveField("State", "idle"), HaveField("ActiveCount", 0))))
		Expect(rows[0].Pretty().String()).To(ContainSubstring("0 active"))
	})
})
