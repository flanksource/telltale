package processcapture

import (
	clickyprocess "github.com/flanksource/clicky/process"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"testing"
	"time"
)

func TestProcessCapture(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Unified process capture")
}

var _ = Describe("facet deltas", func() {
	It("keeps files and receiving sockets independent and infers inbound peers from a listener", func() {
		listener := clickyprocess.Descriptor{FD: "3", Type: "IPv4", Protocol: "TCP", State: "LISTEN", LocalAddr: "*:8080"}
		peer := clickyprocess.Descriptor{FD: "4", Type: "IPv4", Protocol: "TCP", State: "ESTABLISHED", LocalAddr: "127.0.0.1:8080", RemoteAddr: "127.0.0.1:51000"}
		o := FacetObservation{Process: "worker", At: time.Unix(1, 0), Tick: 1, Complete: true, Enabled: []string{Sockets, "connections"}, Descriptors: []clickyprocess.Descriptor{listener, peer, {FD: "5", Type: "REG", Name: "config"}}}
		events, state := NewFacetState().Diff(o)
		Expect(events).To(ConsistOf(HaveField("Facet", Sockets), And(HaveField("Facet", ConnectionIn), HaveField("DirectionSource", "inferred_listener"))))
		o.Exited, o.Complete = true, false
		events, _ = state.Diff(o)
		Expect(events).To(HaveEach(And(HaveField("Operation", "removed"), HaveField("Reason", "observed_exit"))))
	})
	It("infers outbound only after an initial complete observation and keeps reused process identities separate", func() {
		state := NewFacetState()
		o := FacetObservation{Process: "123@1", At: time.Unix(1, 0), Tick: 1, Complete: true}
		_, state = state.Diff(o)
		o.Descriptors = []clickyprocess.Descriptor{{Type: "IPv4", Protocol: "TCP", State: "ESTABLISHED", RemoteAddr: "127.0.0.1:5432", LocalAddr: "127.0.0.1:51000"}}
		events, state := state.Diff(o)
		Expect(events).To(ConsistOf(HaveField("Facet", ConnectionOut)))
		o.Process = "123@2"
		events, _ = state.Diff(o)
		Expect(events).To(ConsistOf(HaveField("Facet", ConnectionUnknown)))
	})
	It("aggregates parallel connections by peer and removes only the last one", func() {
		state := NewFacetState()
		first := clickyprocess.Descriptor{FD: "7u", Type: "IPv4", Protocol: "TCP", State: "ESTABLISHED", LocalAddr: "127.0.0.1:51000", RemoteAddr: "127.0.0.1:5432"}
		second := first
		second.FD = "8u"
		second.LocalAddr = "127.0.0.1:51001"
		observation := FacetObservation{Process: "p", At: time.Unix(1, 0), Tick: 1, Complete: true, Descriptors: []clickyprocess.Descriptor{first, second}}
		events, next := state.Diff(observation)
		Expect(events).To(HaveLen(1))
		Expect(events[0].Facet).To(Equal(ConnectionUnknown))
		Expect(events[0].Operation).To(Equal("added"))
		observation.Tick++
		observation.Descriptors = []clickyprocess.Descriptor{second}
		events, next = next.Diff(observation)
		Expect(events).To(BeEmpty())
		observation.Tick++
		observation.Descriptors = nil
		events, _ = next.Diff(observation)
		Expect(events).To(HaveLen(1))
		Expect(events[0].Operation).To(Equal("removed"))
	})
	It("preserves state for incomplete reads and ignores descriptor order", func() {
		state := NewFacetState()
		o := FacetObservation{Process: "p", At: time.Unix(1, 0), Tick: 1, Complete: true, Descriptors: []clickyprocess.Descriptor{{FD: "1", Type: "REG", Name: "a"}, {FD: "2", Type: "REG", Name: "b"}}}
		_, state = state.Diff(o)
		o.Complete = false
		o.Descriptors = nil
		events, next := state.Diff(o)
		Expect(events).To(BeEmpty())
		o.Complete = true
		o.Descriptors = []clickyprocess.Descriptor{{FD: "2", Type: "REG", Name: "b"}, {FD: "1", Type: "REG", Name: "a"}}
		events, _ = next.Diff(o)
		Expect(events).To(BeEmpty())
	})
})
