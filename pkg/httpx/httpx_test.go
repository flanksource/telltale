package httpx_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/telltale/pkg/httpx"
	"github.com/flanksource/telltale/pkg/traces"
)

func TestHTTPX(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "httpx") }

var _ = Describe("an observable client", func() {
	It("is seen by an http trace of its feature only while the trace runs", func() {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("ok"))
		}))
		DeferCleanup(upstream.Close)
		host, err := traces.Open(GinkgoT().TempDir(), traces.RunID(time.Now()))
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(host.Close(context.Background())).To(Succeed()) })
		get := func(client *http.Client, path string) {
			response, err := client.Get(upstream.URL + path)
			Expect(err).ToNot(HaveOccurred())
			Expect(response.Body.Close()).To(Succeed())
		}

		get(httpx.Shared, "/before")
		capture, err := host.Start("http", nil)
		Expect(err).ToNot(HaveOccurred())
		get(httpx.Client(5*time.Second), "/observed")
		get(http.DefaultClient, "/untapped")
		end, err := capture.Stop(context.Background())
		Expect(err).ToNot(HaveOccurred())
		get(httpx.Shared, "/after")

		summary, err := host.Summarize(context.Background(), capture.Ref(0, end))
		Expect(err).ToNot(HaveOccurred())
		Expect(summary).To(HaveKeyWithValue("count", BeEquivalentTo(1)), "only the tapped call made while the trace ran is recorded")
	})

	It("keeps the timeout it was built with", func() {
		Expect(httpx.Client(3 * time.Second).Timeout).To(Equal(3 * time.Second))
		Expect(httpx.Shared.Timeout).To(BeZero())
	})
})
