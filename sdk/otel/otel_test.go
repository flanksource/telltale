package otel

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	telltale "github.com/flanksource/telltale/sdk"
	"github.com/flanksource/telltale/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestOtel(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "OpenTelemetry adapter") }

var _ = Describe("OpenTelemetry span capture", func() {
	It("refuses to arm when the global provider is not an SDK provider", func() {
		previous := otel.GetTracerProvider()
		DeferCleanup(otel.SetTracerProvider, previous)
		otel.SetTracerProvider(noop.NewTracerProvider())
		_, err := install()
		Expect(err).To(MatchError(ContainSubstring("not an SDK provider")))
	})
	It("records spans from the global SDK provider", func() {
		provider := sdktrace.NewTracerProvider()
		otel.SetTracerProvider(provider)
		DeferCleanup(provider.Shutdown, context.Background())
		agent := telltale.Loopback()
		DeferCleanup(agent.Close)
		stream := agent.Connect()
		DeferCleanup(stream.Close)
		Expect(stream.SetDeadline(time.Now().Add(5 * time.Second))).To(Succeed())
		conn, _, err := protocol.Authenticate(stream, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(conn.Send("arm", protocol.Arm{Session: "otel", Probe: "spans", Params: json.RawMessage(`{}`)})).To(Succeed())
		frame, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		var armed protocol.Armed
		Expect(json.Unmarshal(frame.Body, &armed)).To(Succeed())
		Expect(armed.Error).To(BeEmpty())
		_, span := provider.Tracer("example").Start(context.Background(), "checkout")
		span.End()
		frame, _, err = conn.Read()
		Expect(err).NotTo(HaveOccurred())
		var records protocol.Records
		Expect(json.Unmarshal(frame.Body, &records)).To(Succeed())
		Expect(records.Items).To(HaveLen(1))
		Expect(string(records.Items[0])).To(ContainSubstring("checkout"))
	})
	It("emits one record when the application also installs a span processor", func() {
		app := SpanProcessor()
		Expect(app == SpanProcessor()).To(BeFalse())
		provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(app))
		otel.SetTracerProvider(provider)
		DeferCleanup(provider.Shutdown, context.Background())
		agent := telltale.Loopback()
		DeferCleanup(agent.Close)
		stream := agent.Connect()
		DeferCleanup(stream.Close)
		Expect(stream.SetDeadline(time.Now().Add(5 * time.Second))).To(Succeed())
		conn, _, err := protocol.Authenticate(stream, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(conn.Send("arm", protocol.Arm{Session: "dedup", Probe: "spans", Params: json.RawMessage(`{}`)})).To(Succeed())
		_, _, err = conn.Read()
		Expect(err).NotTo(HaveOccurred())
		_, span := provider.Tracer("example").Start(context.Background(), "once")
		span.End()
		frame, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		var records protocol.Records
		Expect(json.Unmarshal(frame.Body, &records)).To(Succeed())
		Expect(records.Items).To(HaveLen(1))
		Expect(conn.Send("disarm", protocol.Disarm{Session: "dedup"})).To(Succeed())
		frame, _, err = conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(frame.Type).To(Equal("ended"))
		other := sdktrace.NewTracerProvider()
		otel.SetTracerProvider(other)
		DeferCleanup(other.Shutdown, context.Background())
		Expect(conn.Send("arm", protocol.Arm{Session: "after-disarm", Probe: "spans", Params: json.RawMessage(`{}`)})).To(Succeed())
		_, _, err = conn.Read()
		Expect(err).NotTo(HaveOccurred())
		_, after := provider.Tracer("example").Start(context.Background(), "after-disarm")
		after.End()
		frame, _, err = conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(string(frame.Body)).To(ContainSubstring("after-disarm"))
		provider.UnregisterSpanProcessor(app)
	})
	It("records spans from a separate application provider while the global provider is armed", func() {
		global := sdktrace.NewTracerProvider()
		provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(SpanProcessor()))
		otel.SetTracerProvider(global)
		DeferCleanup(global.Shutdown, context.Background())
		DeferCleanup(provider.Shutdown, context.Background())
		agent := telltale.Loopback()
		DeferCleanup(agent.Close)
		stream := agent.Connect()
		DeferCleanup(stream.Close)
		Expect(stream.SetDeadline(time.Now().Add(5 * time.Second))).To(Succeed())
		conn, _, err := protocol.Authenticate(stream, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(conn.Send("arm", protocol.Arm{Session: "other-provider", Probe: "spans", Params: json.RawMessage(`{}`)})).To(Succeed())
		_, _, err = conn.Read()
		Expect(err).NotTo(HaveOccurred())
		_, span := provider.Tracer("example").Start(context.Background(), "separate-provider")
		span.End()
		frame, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(string(frame.Body)).To(ContainSubstring("separate-provider"))
	})
})
