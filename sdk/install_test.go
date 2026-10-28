package telltale

import (
	"context"
	"encoding/json"
	"sync/atomic"

	"github.com/flanksource/telltale/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type installedProbe struct{ installs, releases atomic.Int32 }

func (*installedProbe) Spec() protocol.ProbeSpec { return protocol.ProbeSpec{Name: "install-test"} }
func (*installedProbe) Arm(context.Context, json.RawMessage, Sink) (func() error, error) {
	return func() error { return nil }, nil
}
func (p *installedProbe) Install() (func() error, error) {
	p.installs.Add(1)
	return func() error { p.releases.Add(1); return nil }, nil
}

var _ = Describe("probe installation", func() {
	It("installs once and releases after the last session", func() {
		p := &installedProbe{}
		RegisterProbe(p)
		a := Loopback()
		defer a.Close()
		b := Loopback()
		defer b.Close()
		firstStream, first := openAgent(a)
		defer firstStream.Close()
		secondStream, second := openAgent(b)
		defer secondStream.Close()
		for _, conn := range []*protocol.Conn{first, second} {
			Expect(conn.Send("arm", protocol.Arm{Session: "install", Probe: "install-test", Params: json.RawMessage(`{}`)})).To(Succeed())
			frame, _, err := conn.Read()
			Expect(err).NotTo(HaveOccurred())
			Expect(frame.Type).To(Equal("armed"))
		}
		Expect(p.installs.Load()).To(Equal(int32(1)))
		Expect(first.Send("disarm", protocol.Disarm{Session: "install"})).To(Succeed())
		frame, _, err := first.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(frame.Type).To(Equal("ended"))
		Expect(p.releases.Load()).To(Equal(int32(0)))
		Expect(second.Send("disarm", protocol.Disarm{Session: "install"})).To(Succeed())
		frame, _, err = second.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(frame.Type).To(Equal("ended"))
		Expect(p.releases.Load()).To(Equal(int32(1)))
	})
})
