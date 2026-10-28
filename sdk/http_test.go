package telltale

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/flanksource/commons/har"
	"github.com/flanksource/telltale/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("HTTP transport", func() {
	It("reports an inbound capture failure without panicking", func() {
		agent := Loopback()
		defer agent.Close()
		stream, conn := openAgent(agent)
		defer stream.Close()
		Expect(conn.Send("arm", protocol.Arm{Session: "capture-error", Probe: "http", Params: json.RawMessage(`{}`)})).To(Succeed())
		_, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		request := httptest.NewRequest(http.MethodGet, "http://example.test/items", nil)
		failure := errors.New("capture failed")
		Expect(func() {
			captureInbound(request, &capturedBody{limit: 1024}, time.Now(), har.DefaultConfig(), func(*http.Request) (*http.Response, error) { return nil, failure })
		}).NotTo(Panic())
		var warning protocol.Warn
		var entries []json.RawMessage
		for warning.Message == "" || len(entries) == 0 {
			frame, _, readErr := conn.Read()
			Expect(readErr).NotTo(HaveOccurred())
			if frame.Type == "warn" {
				Expect(json.Unmarshal(frame.Body, &warning)).To(Succeed())
			}
			if frame.Type == "records" {
				var batch protocol.Records
				Expect(json.Unmarshal(frame.Body, &batch)).To(Succeed())
				entries = append(entries, batch.Items...)
			}
		}
		Expect(warning.Message).To(ContainSubstring(failure.Error()))
		var captured har.Entry
		Expect(json.Unmarshal(entries[0], &captured)).To(Succeed())
		Expect(captured.Error).To(ContainSubstring(failure.Error()))
	})
	It("records an inbound request without changing its response", func() {
		agent := Loopback()
		defer agent.Close()
		stream, conn := openAgent(agent)
		defer stream.Close()
		Expect(conn.Send("arm", protocol.Arm{Session: "handler-test", Probe: "http", Params: json.RawMessage(`{}`)})).To(Succeed())
		_, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		server := httptest.NewServer(Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, readErr := io.ReadAll(r.Body)
			Expect(readErr).NotTo(HaveOccurred())
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write(body)
		})))
		defer server.Close()
		response, err := http.Post(server.URL+"/items", "application/json", strings.NewReader(`{"item":"one","password":"secret-value"}`))
		Expect(err).NotTo(HaveOccurred())
		body, err := io.ReadAll(response.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.Body.Close()).To(Succeed())
		Expect(string(body)).To(Equal(`{"item":"one","password":"secret-value"}`))
		frame, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		var batch protocol.Records
		Expect(json.Unmarshal(frame.Body, &batch)).To(Succeed())
		Expect(batch.Items).To(HaveLen(1))
		var entry har.Entry
		Expect(json.Unmarshal(batch.Items[0], &entry)).To(Succeed())
		Expect(entry.Request.Method).To(Equal(http.MethodPost))
		Expect(entry.Request.URL).To(HaveSuffix("/items"))
		Expect(entry.Request.PostData.Text).To(ContainSubstring(`"item":"one"`))
		Expect(entry.Request.PostData.Text).NotTo(ContainSubstring("secret-value"))
		Expect(entry.Response.Status).To(Equal(http.StatusCreated))
		Expect(entry.Response.Content.Text).NotTo(ContainSubstring("secret-value"))
	})
	It("emits a HAR entry for an armed request", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"result":"ok"}`)
		}))
		defer server.Close()
		agent := Loopback()
		defer agent.Close()
		stream, conn := openAgent(agent)
		defer stream.Close()
		Expect(conn.Send("arm", protocol.Arm{Session: "http-test", Probe: "http", Params: json.RawMessage(`{}`)})).To(Succeed())
		frame, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		var armed protocol.Armed
		Expect(json.Unmarshal(frame.Body, &armed)).To(Succeed())
		Expect(armed.Error).To(BeEmpty())
		client := &http.Client{Transport: Transport(http.DefaultTransport)}
		response, err := client.Get(server.URL)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.Body.Close()).To(Succeed())
		frame, _, err = conn.Read()
		Expect(err).NotTo(HaveOccurred())
		var batch protocol.Records
		Expect(json.Unmarshal(frame.Body, &batch)).To(Succeed())
		Expect(batch.Items).To(HaveLen(1))
		var entry har.Entry
		Expect(json.Unmarshal(batch.Items[0], &entry)).To(Succeed())
		Expect(entry.Request.URL).To(Equal(server.URL))
		Expect(entry.Response.Status).To(Equal(http.StatusOK))
	})
})
