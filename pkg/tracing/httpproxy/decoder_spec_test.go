package httpproxy

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"time"

	"github.com/elazarl/goproxy"
	tracehttp "github.com/flanksource/telltale/pkg/tracing/http"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("decoded HTTP proxy exchanges", func() {
	DescribeTable("relays the first frame while the upstream waits to finish",
		func(method, address, mime, first, last, protocol, operation string) {
			gate := make(chan struct{})
			release := sync.OnceFunc(func() { close(gate) })
			client, rows := decoderProxy(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", mime)
				_, _ = io.WriteString(w, first)
				w.(http.Flusher).Flush()
				<-gate
				_, _ = io.WriteString(w, last)
			})
			DeferCleanup(release)
			req, err := http.NewRequestWithContext(context.Background(), method, address, nil)
			Expect(err).NotTo(HaveOccurred())
			resp, err := client.Do(req)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(resp.Body.Close)
			prefix := make([]byte, len(first))
			_, err = io.ReadFull(resp.Body, prefix)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(prefix)).To(Equal(first))
			release()
			tail, err := io.ReadAll(resp.Body)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(tail)).To(Equal(last))
			var exchange tracehttp.Exchange
			Eventually(rows).Should(Receive(&exchange))
			Expect(exchange.Protocol).To(Equal(protocol))
			Expect(exchange.Operation).To(Equal(operation))
			Expect(exchange.Decoded.Partial).To(BeFalse())
			if protocol == "openai" {
				Expect(exchange.Decoded.AI.Usage.TotalTokens).To(HaveValue(Equal(int64(5))))
			}
		},
		Entry("for an OpenAI stream", "POST", "http://api.openai.com/v1/responses", "text/event-stream",
			"data: {\"type\":\"response.created\",\"response\":{\"id\":\"response-example\"}}\n\n",
			"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"total_tokens\":5}}}\n\n", "openai", "responses"),
		Entry("for a Kubernetes JSON watch", "GET", "http://cluster.example/api/v1/namespaces/demo/pods?watch=1", "application/json",
			"{\"type\":\"ADDED\"}\n", "{\"type\":\"MODIFIED\"}\n", "kubernetes", "watch"),
	)

	It("keeps AWS signing inputs intact upstream and masks them in the record and HAR", func() {
		const (
			credential = "test-client-id/20200101/eu-west-1/s3/aws4_request"
			signature  = "fixture-signature"
			session    = "fixture-session"
		)
		rawQuery := url.Values{
			"X-Amz-Credential": {credential}, "X-Amz-Signature": {signature}, "X-Amz-Security-Token": {session},
		}.Encode()
		upstream := make(chan string, 1)
		client, rows := decoderProxy(func(w http.ResponseWriter, req *http.Request) {
			upstream <- req.URL.RequestURI()
			w.Header().Set("X-Amz-Request-Id", "s3-request")
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, "<Error><Code>AccessDenied</Code></Error>")
		})
		resp, err := client.Get("http://archive.s3.eu-west-1.amazonaws.com/a%2Fb?" + rawQuery)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(resp.Body.Close)
		_, err = io.Copy(io.Discard, resp.Body)
		Expect(err).NotTo(HaveOccurred())
		Eventually(upstream).Should(Receive(Equal("/a%2Fb?" + rawQuery)))

		var exchange tracehttp.Exchange
		Eventually(rows).Should(Receive(&exchange))
		Expect(exchange.Operation).To(Equal("GetObject"))
		Expect(exchange.Decoded.ObjectKey).To(Equal("a/b"))
		Expect(exchange.Decoded.ErrorCode).To(Equal("AccessDenied"))
		Expect(exchange.Decoded.RequestID).To(Equal("s3-request"))
		harEntry := tracehttp.ToEntry(exchange)
		Expect(harEntry.Metadata.Protocol).To(Equal("s3"))
		Expect(harEntry.Metadata.Decoded).To(Equal(exchange.Decoded))
		encoded, err := json.Marshal([]any{exchange, harEntry})
		Expect(err).NotTo(HaveOccurred())
		for _, secret := range []string{"test-client-id", signature, session} {
			Expect(string(encoded)).NotTo(ContainSubstring(secret))
		}
	})
})

func decoderProxy(handler http.HandlerFunc) (*http.Client, <-chan tracehttp.Exchange) {
	upstream := httptest.NewServer(handler)
	DeferCleanup(upstream.Close)
	rows := make(chan tracehttp.Exchange, 1)
	r := &recorder{rules: newPolicy(4096, nil), emit: func(exchange tracehttp.Exchange) { rows <- exchange },
		streams: map[*streamCapture]tracehttp.Exchange{}}
	proxy := goproxy.NewProxyHttpServer()
	proxy.Logger = silentLogger{}
	proxy.Tr = &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
	}}
	DeferCleanup(proxy.Tr.CloseIdleConnections)
	proxy.OnRequest().DoFunc(r.onRequest)
	proxy.OnResponse().DoFunc(r.onResponse)
	server := httptest.NewServer(proxy)
	DeferCleanup(server.Close)
	address, err := url.Parse(server.URL)
	Expect(err).NotTo(HaveOccurred())
	transport := &http.Transport{Proxy: http.ProxyURL(address)}
	DeferCleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 3 * time.Second}, rows
}
