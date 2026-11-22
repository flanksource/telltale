package telltale

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/flanksource/commons/har"
	commonshttp "github.com/flanksource/commons/http"
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
		Expect(string(batch.Items[0])).To(ContainSubstring(`"direction":"incoming"`))
		Expect(entry.Request.URL).To(HaveSuffix("/items"))
		Expect(entry.Request.PostData.Text).To(ContainSubstring(`"item":"one"`))
		Expect(entry.Request.PostData.Text).NotTo(ContainSubstring("secret-value"))
		Expect(entry.Response.Status).To(Equal(http.StatusCreated))
		Expect(entry.Response.Content.Text).NotTo(ContainSubstring("secret-value"))
	})
	It("captures a commons client configured for TLS after instrumenting the default transport", func() {
		previous := http.DefaultTransport
		DeferCleanup(func() { http.DefaultTransport = previous })
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "ok")
		}))
		defer server.Close()
		agent := Loopback()
		defer agent.Close()
		stream, conn := openAgent(agent)
		defer stream.Close()
		Expect(conn.Send("arm", protocol.Arm{Session: "tls-test", Probe: "http", Params: json.RawMessage(`{}`)})).To(Succeed())
		_, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		InstrumentDefaultTransport()
		response, err := commonshttp.NewClient().InsecureSkipVerify(true).R(GinkgoT().Context()).Get(server.URL + "/widgets")
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		frame, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		var batch protocol.Records
		Expect(json.Unmarshal(frame.Body, &batch)).To(Succeed())
		Expect(batch.Items).To(HaveLen(1))
		var entry har.Entry
		Expect(json.Unmarshal(batch.Items[0], &entry)).To(Succeed())
		Expect(entry.Request.URL).To(Equal(server.URL + "/widgets"))
		Expect(entry.Response.Status).To(Equal(http.StatusOK))
	})
	It("records only matching incoming requests without bodies", func() {
		agent := Loopback()
		DeferCleanup(agent.Close)
		stream, conn := openAgent(agent)
		DeferCleanup(stream.Close)
		params := json.RawMessage(`{"direction":"incoming","paths":["/projects"],"bodies":"0"}`)
		Expect(conn.Send("arm", protocol.Arm{Session: "incoming-metadata", Probe: "http", Params: params})).To(Succeed())
		frame, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		var armed protocol.Armed
		Expect(json.Unmarshal(frame.Body, &armed)).To(Succeed())
		Expect(armed.Error).To(BeEmpty())
		handler := Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.Copy(w, r.Body)
		}))
		for _, path := range []string{"/other", "/projects"} {
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"name":"project"}`))
			request.Header.Set("Content-Type", "application/json")
			handler.ServeHTTP(httptest.NewRecorder(), request)
		}
		recordHTTP(har.Entry{Request: har.Request{URL: "https://example.test/projects"}}, protocol.HTTPOutgoing)
		frame, _, err = conn.Read()
		Expect(err).NotTo(HaveOccurred())
		var batch protocol.Records
		Expect(json.Unmarshal(frame.Body, &batch)).To(Succeed())
		Expect(batch.Items).To(HaveLen(1))
		var exchange HTTPExchange
		Expect(json.Unmarshal(batch.Items[0], &exchange)).To(Succeed())
		Expect(exchange.Direction).To(Equal(protocol.HTTPIncoming))
		Expect(exchange.Request.URL).To(HaveSuffix("/projects"))
		Expect(exchange.Request.PostData).To(BeNil())
		Expect(exchange.Response.Content.Text).To(BeEmpty())
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
		Expect(string(batch.Items[0])).To(ContainSubstring(`"direction":"outgoing"`))
	})
	It("captures a commons client configured for TLS after InstrumentDefaultTransport", func() {
		original := http.DefaultTransport
		DeferCleanup(func() { http.DefaultTransport = original })
		const path = "/tls-items"
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"result":"ok"}`)
		}))
		defer server.Close()
		agent := Loopback()
		defer agent.Close()
		stream, conn := openAgent(agent)
		defer stream.Close()
		Expect(conn.Send("arm", protocol.Arm{Session: "commons-http-test", Probe: "http", Params: json.RawMessage(`{}`)})).To(Succeed())
		_, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())

		InstrumentDefaultTransport()
		response, err := commonshttp.NewClient().InsecureSkipVerify(true).R(context.Background()).Get(server.URL + path)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.AsString()).To(Equal(`{"result":"ok"}`))

		frame, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		var batch protocol.Records
		Expect(json.Unmarshal(frame.Body, &batch)).To(Succeed())
		Expect(batch.Items).To(HaveLen(1))
		var entry har.Entry
		Expect(json.Unmarshal(batch.Items[0], &entry)).To(Succeed())
		Expect(entry.Request.URL).To(HaveSuffix(path))
		Expect(entry.Response.Status).To(Equal(http.StatusOK))
	})
})
