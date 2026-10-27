package protocol_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/telltale/sdk/protocol"
)

var _ = Describe("framed protocol", func() {
	It("times out a write when the peer stops reading", func() {
		left, right := net.Pipe()
		defer left.Close()
		defer right.Close()
		err := protocol.NewConn(left).Send("ping", protocol.Ping{})
		Expect(errors.Is(err, os.ErrDeadlineExceeded)).To(BeTrue())
	})
	It("refuses discovery files that target non-loopback TCP", func() {
		path := filepath.Join(GinkgoT().TempDir(), "agent.json")
		data, err := json.Marshal(protocol.Endpoint{PID: 123, Addr: "192.0.2.1:1234", Token: "test", Started: time.Now()})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(path, data, 0600)).To(Succeed())
		_, err = protocol.ReadEndpoint(context.Background(), path)
		Expect(errors.Is(err, protocol.ErrNonLocalEndpoint)).To(BeTrue())
	})
	It("round trips an envelope and its raw blob", func() {
		var wire bytes.Buffer
		conn := protocol.NewConn(&wire)
		body, err := json.Marshal(protocol.Blob{Session: "capture", Name: "heap", ContentType: protocol.ContentTypePprof, Len: 3})
		Expect(err).NotTo(HaveOccurred())
		want := protocol.Envelope{Type: "blob", ID: "capture", Seq: 1, Body: body}
		Expect(conn.Write(want, []byte{1, 2, 3})).To(Succeed())
		got, blob, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(want))
		Expect(blob).To(Equal([]byte{1, 2, 3}))
	})

	It("rejects oversized frames before allocating them", func() {
		var wire bytes.Buffer
		Expect(binary.Write(&wire, binary.BigEndian, uint32(protocol.MaxFrameSize+1))).To(Succeed())
		_, _, err := protocol.NewConn(&wire).Read()
		Expect(errors.Is(err, protocol.ErrFrameTooLarge)).To(BeTrue())
	})
	It("applies the smaller pre-authentication frame limit", func() {
		var wire bytes.Buffer
		Expect(binary.Write(&wire, binary.BigEndian, uint32(4097))).To(Succeed())
		_, _, err := protocol.NewConn(&wire).ReadLimit(4096)
		Expect(errors.Is(err, protocol.ErrFrameTooLarge)).To(BeTrue())
	})

	It("names both versions when the protocol major differs", func() {
		Expect(protocol.CheckVersion(1)).To(Succeed())
		Expect(protocol.CheckVersion(2)).To(MatchError(ContainSubstring("2")))
		Expect(protocol.CheckVersion(2)).To(MatchError(ContainSubstring("1")))
	})
})
