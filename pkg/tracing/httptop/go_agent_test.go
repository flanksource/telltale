package httptop_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/flanksource/commons/http/middlewares"
	"github.com/flanksource/telltale/pkg/agentclient"
	tracehttp "github.com/flanksource/telltale/pkg/tracing/http"
	"github.com/flanksource/telltale/pkg/tracing/httptop"
	"github.com/flanksource/telltale/pkg/tracing/seam"
	sdk "github.com/flanksource/telltale/sdk"
	"github.com/flanksource/telltale/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Go HTTP Top", func() {
	It("samples incoming and outgoing requests and clears completed operations", func() {
		agent := sdk.Loopback()
		DeferCleanup(agent.Close)
		stream := agent.Connect()
		DeferCleanup(stream.Close)
		Expect(stream.SetDeadline(time.Now().Add(5 * time.Second))).To(Succeed())
		wire, hello, err := protocol.Authenticate(stream, "")
		Expect(err).NotTo(HaveOccurred())
		const session = "go-http-top"
		Expect(wire.Send("arm", protocol.Arm{Session: session, Probe: httptop.Kind, Params: json.RawMessage(`{"pollIntervalMs":60000,"limit":50}`)})).To(Succeed())
		frame, _, err := wire.Read()
		Expect(err).NotTo(HaveOccurred())
		var armed protocol.Armed
		Expect(json.Unmarshal(frame.Body, &armed)).To(Succeed())
		Expect(armed.Error).To(BeEmpty())

		started := make(chan struct{}, 2)
		release := make(chan struct{})
		done := make(chan struct{}, 2)
		incoming := sdk.Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			started <- struct{}{}
			<-release
		}))
		outgoing := sdk.Transport(middlewares.RoundTripperFunc(func(_ *http.Request) (*http.Response, error) {
			started <- struct{}{}
			<-release
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
		}))
		go func() {
			incoming.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/projects", nil))
			done <- struct{}{}
		}()
		go func() {
			response, err := outgoing.RoundTrip(httptest.NewRequest(http.MethodPost, "https://example.test/items?token=hidden", nil))
			if err == nil {
				_ = response.Body.Close()
			}
			done <- struct{}{}
		}()
		defer close(release)
		Eventually(started).Should(Receive())
		Eventually(started).Should(Receive())
		snapshot := goTopCheckpoint(wire, session, "active", hello.Resource)
		Expect(snapshot.Rows).To(ConsistOf(
			And(HaveField("Direction", tracehttp.Incoming), HaveField("Method", http.MethodGet), HaveField("URL", HaveSuffix("/projects"))),
			And(HaveField("Direction", tracehttp.Outgoing), HaveField("Method", http.MethodPost), HaveField("URL", "https://example.test/items")),
		))
		Expect(snapshot.Frame.Observed).To(Equal(2))
		release <- struct{}{}
		release <- struct{}{}
		Eventually(done).Should(Receive())
		Eventually(done).Should(Receive())
		empty := goTopCheckpoint(wire, session, "finished", hello.Resource)
		Expect(empty.Rows).To(BeEmpty())
		Expect(empty.Frame.Tick).To(BeNumerically(">", snapshot.Frame.Tick))
	})
})

func goTopCheckpoint(wire *protocol.Conn, session, id string, resource map[string]string) seam.DecodedBlob[httptop.Sample] {
	GinkgoHelper()
	Expect(wire.Send("checkpoint", protocol.Checkpoint{Session: session, ID: id, Params: json.RawMessage(`{}`)})).To(Succeed())
	frame, data, err := wire.Read()
	Expect(err).NotTo(HaveOccurred())
	Expect(frame.Type).To(Equal("blob"))
	var info protocol.Blob
	Expect(json.Unmarshal(frame.Body, &info)).To(Succeed())
	Expect(info.Checkpoint).To(Equal(id))
	decoded, err := httptop.Decode(agentclient.Blob{Info: info, Data: data}, resource)
	Expect(err).NotTo(HaveOccurred())
	Expect(decoded.Frame).NotTo(BeNil())
	frame, _, err = wire.Read()
	Expect(err).NotTo(HaveOccurred())
	Expect(frame.Type).To(Equal("checkpointed"))
	return decoded
}
