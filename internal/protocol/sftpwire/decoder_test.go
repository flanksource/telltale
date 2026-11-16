package sftpwire

import (
	"encoding/binary"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSFTP(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "SFTP operations") }
func u32(n uint32) []byte   { return binary.BigEndian.AppendUint32(nil, n) }
func text(s string) []byte  { return append(u32(uint32(len(s))), s...) }
func message(kind byte, id uint32, payload []byte) []byte {
	return append(append([]byte{kind}, u32(id)...), payload...)
}

var _ = Describe("SFTP decoding", func() {
	It("correlates out-of-order replies and keeps file contents opaque", func() {
		var events []Event
		d := Decoder{Emit: func(e Event) { events = append(events, e) }}
		open := append(text("/data/report.txt"), u32(1)...)
		open = append(open, u32(0)...)
		Expect(d.Request(message(3, 7, open))).To(Succeed())
		Expect(d.Response(message(102, 7, text("handle-1")))).To(Succeed())
		read := append(text("handle-1"), binary.BigEndian.AppendUint64(nil, 128)...)
		read = append(read, u32(4)...)
		Expect(d.Request(message(5, 8, read))).To(Succeed())
		Expect(d.Request(message(17, 9, text("/data/report.txt")))).To(Succeed())
		Expect(d.Response(message(101, 9, append(append(u32(2), text("missing")...), text("")...)))).To(Succeed())
		Expect(d.Response(message(103, 8, text("data")))).To(Succeed())
		status := uint32(2)
		Expect(events).To(Equal([]Event{{RequestID: 7, Operation: "open", Path: "/data/report.txt"}, {RequestID: 9, Operation: "stat", Path: "/data/report.txt", Status: &status}, {RequestID: 8, Operation: "read", Path: "/data/report.txt", Offset: 128, Bytes: 4}}))
	})
	It("rejects malformed and unmatched responses", func() {
		d := Decoder{}
		Expect(d.Request([]byte{3})).NotTo(Succeed())
		Expect(d.Response(message(101, 99, u32(0)))).NotTo(Succeed())
	})
})
