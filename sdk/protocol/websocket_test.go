package protocol_test

import (
	"encoding/json"
	"io"

	"github.com/flanksource/telltale/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type message struct {
	kind int
	data []byte
}
type messageStream struct{ messages []message }

func (s *messageStream) Read([]byte) (int, error)  { return 0, io.EOF }
func (s *messageStream) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func (s *messageStream) WriteMessage(kind int, data []byte) error {
	s.messages = append(s.messages, message{kind, data})
	return nil
}
func (s *messageStream) ReadMessage() (int, []byte, error) {
	m := s.messages[0]
	s.messages = s.messages[1:]
	return m.kind, m.data, nil
}

var _ = Describe("message framing", func() {
	It("sends envelopes as text and blobs as binary messages", func() {
		stream := &messageStream{}
		conn := protocol.NewConn(stream)
		body, err := json.Marshal(protocol.Blob{Len: 3})
		Expect(err).NotTo(HaveOccurred())
		want := protocol.Envelope{Type: "blob", Body: body}
		Expect(conn.Write(want, []byte("abc"))).To(Succeed())
		Expect(stream.messages[0].kind).To(Equal(1))
		Expect(stream.messages[1].kind).To(Equal(2))
		got, blob, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(want))
		Expect(blob).To(Equal([]byte("abc")))
	})
})
