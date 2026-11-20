package gitwire

import (
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestDecoder(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Git wire decoder") }

func packet(payload string) string { return fmt.Sprintf("%04x%s", len(payload)+4, payload) }

var _ = Describe("Git packet decoding", func() {
	It("decodes fragmented v2 packets and controls", func() {
		var events []Event
		d := Decoder{Emit: func(e Event) { events = append(events, e) }}
		wire := packet("version 2\n") + packet("command=ls-refs\n") + "00010000"
		for _, b := range []byte(wire) {
			_, err := d.Write([]byte{b})
			Expect(err).NotTo(HaveOccurred())
		}
		Expect(d.Close()).To(Succeed())
		Expect(events).To(Equal([]Event{{Kind: "version", Text: "2"}, {Kind: "command", Text: "ls-refs"}, {Kind: "delimiter"}, {Kind: "flush"}}))
	})
	It("keeps pack payloads opaque while retaining progress and errors", func() {
		var events []Event
		d := Decoder{Emit: func(e Event) { events = append(events, e) }}
		_, err := d.Write([]byte(packet("\x01PACK-private-object") + packet("\x02counting objects\n") + packet("\x03rejected\n")))
		Expect(err).NotTo(HaveOccurred())
		Expect(events).To(Equal([]Event{{Kind: "pack", Bytes: 19}, {Kind: "progress", Text: "counting objects"}, {Kind: "error", Text: "rejected"}}))
	})
	It("decodes ref capabilities and push updates", func() {
		var events []Event
		d := Decoder{Emit: func(e Event) { events = append(events, e) }}
		oid := "0123456789012345678901234567890123456789"
		_, err := d.Write([]byte(packet(oid+" refs/heads/main\x00side-band-64k\n") + packet(oid+" "+oid+" refs/heads/main\n")))
		Expect(err).NotTo(HaveOccurred())
		Expect(events).To(Equal([]Event{{Kind: "ref", Text: oid + " refs/heads/main"}, {Kind: "capabilities", Text: "side-band-64k"}, {Kind: "update", Text: oid + " " + oid + " refs/heads/main"}}))
	})
	It("rejects malformed and incomplete frames", func() {
		d := Decoder{}
		_, err := d.Write([]byte("0003"))
		Expect(err).To(HaveOccurred())
		d = Decoder{}
		_, err = d.Write([]byte("0008ab"))
		Expect(err).NotTo(HaveOccurred())
		Expect(d.Close()).To(MatchError(ErrIncomplete))
	})
	It("counts unframed pack bytes without interpreting objects", func() {
		var events []Event
		d := Decoder{Emit: func(e Event) { events = append(events, e) }}
		_, err := d.Write([]byte("PACKopaque"))
		Expect(err).NotTo(HaveOccurred())
		_, err = d.Write([]byte("more"))
		Expect(err).NotTo(HaveOccurred())
		Expect(d.Close()).To(Succeed())
		total := 0
		for _, event := range events {
			Expect(event.Kind).To(Equal("pack"))
			Expect(event.Text).To(BeEmpty())
			total += event.Bytes
		}
		Expect(total).To(Equal(14))
	})
})
