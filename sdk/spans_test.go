package telltale

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/flanksource/telltale/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("manual spans", func() {
	It("reports invalid attributes without panicking whether armed or not", func() {
		var output bytes.Buffer
		previous := log.Writer()
		log.SetOutput(&output)
		DeferCleanup(log.SetOutput, previous)
		ctx, finish := StartSpan(context.Background(), "example", "missing")
		Expect(ctx).NotTo(BeNil())
		finish(nil)
		Expect(output.String()).To(ContainSubstring("span attributes"))
		output.Reset()
		_, finish = StartSpan(context.Background(), "example", false, "value")
		finish(nil)
		Expect(output.String()).To(ContainSubstring("span attribute key"))
		output.Reset()
		agent := Loopback()
		DeferCleanup(agent.Close)
		stream := agent.Connect()
		DeferCleanup(stream.Close)
		Expect(stream.SetDeadline(time.Now().Add(5 * time.Second))).To(Succeed())
		conn, _, err := protocol.Authenticate(stream, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(conn.Send("arm", protocol.Arm{Session: "validation", Probe: "spans", Params: json.RawMessage(`{}`)})).To(Succeed())
		_, _, err = conn.Read()
		Expect(err).NotTo(HaveOccurred())
		ctx, finish = StartSpan(context.Background(), "example", "missing")
		Expect(ctx).NotTo(BeNil())
		finish(nil)
		Expect(output.String()).To(ContainSubstring("span attributes"))
	})
	It("records parent and child spans with attributes and errors", func() {
		agent := Loopback()
		defer agent.Close()
		stream := agent.Connect()
		defer stream.Close()
		Expect(stream.SetDeadline(time.Now().Add(5 * time.Second))).To(Succeed())
		conn, _, err := protocol.Authenticate(stream, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(conn.Send("arm", protocol.Arm{Session: "spans", Probe: "spans", Params: json.RawMessage(`{}`)})).To(Succeed())
		frame, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		var armed protocol.Armed
		Expect(json.Unmarshal(frame.Body, &armed)).To(Succeed())
		Expect(armed.Error).To(BeEmpty())
		parent, finishParent := StartSpan(context.Background(), "parent", "key", "value")
		_, finishChild := StartSpan(parent, "child")
		finishChild(nil)
		finishParent(context.Canceled)
		frame, _, err = conn.Read()
		Expect(err).NotTo(HaveOccurred())
		var batch protocol.Records
		Expect(json.Unmarshal(frame.Body, &batch)).To(Succeed())
		Expect(batch.Items).To(HaveLen(2))
		var child, root Span
		Expect(json.Unmarshal(batch.Items[0], &child)).To(Succeed())
		Expect(json.Unmarshal(batch.Items[1], &root)).To(Succeed())
		Expect(child.ParentID).To(Equal(root.SpanID))
		Expect(child.TraceID).To(Equal(root.TraceID))
		Expect(root.Attrs).To(HaveKeyWithValue("key", "value"))
		Expect(root.Status).To(Equal("error"))
	})
})
