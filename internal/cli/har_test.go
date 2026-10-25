package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/flanksource/commons/har"
	"github.com/flanksource/commons/properties"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// withProperty sets a -P property for one spec. Properties are process-global,
// so every spec that sets one must restore it.
func withProperty(key, value string) {
	properties.Set(key, value)
	DeferCleanup(func() { properties.Set(key, "") })
}

// withCleanRegistry gives a spec its own collectors. The package registry is
// process-wide because http.DefaultTransport is, and it retains collectors so a
// second Flush rewrites rather than truncates — which across specs would mean
// one spec flushing another's archive into a deleted temp dir.
func withCleanRegistry() {
	previous := harRegistry
	harRegistry = har.NewRegistry(nil)
	DeferCleanup(func() { harRegistry = previous })
}

// captureThrough issues one request against a JSON server through the HAR
// transport telltale installs, and returns the entries written to path.
func captureThrough(path string) []har.Entry {
	withCleanRegistry()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	DeferCleanup(server.Close)

	transport, err := harRegistry.Transport(harFeature, http.DefaultTransport)
	Expect(err).ToNot(HaveOccurred())
	req, err := http.NewRequest(http.MethodGet, server.URL+"/v1/models", nil)
	Expect(err).ToNot(HaveOccurred())
	resp, err := transport.RoundTrip(req)
	Expect(err).ToNot(HaveOccurred())
	_, _ = io.Copy(io.Discard, resp.Body)
	Expect(resp.Body.Close()).To(Succeed())

	FlushHAR()

	data, err := os.ReadFile(path)
	Expect(err).ToNot(HaveOccurred(), "no HAR written to %s", path)
	var file har.File
	Expect(json.Unmarshal(data, &file)).To(Succeed())
	return file.Log.Entries
}

var _ = Describe("HAR capture", func() {
	It("leaves the transport chain unchanged without -Phttp.har", func() {
		base := &http.Transport{}
		transport, err := harRegistry.Transport(harFeature, base)
		Expect(err).ToNot(HaveOccurred())
		Expect(transport).To(BeIdenticalTo(base))
	})

	It("writes a request through the default transport into a readable archive", func() {
		path := filepath.Join(GinkgoT().TempDir(), "trace.har")
		withProperty("http.har", path)

		entries := captureThrough(path)
		Expect(entries).To(HaveLen(1))
		Expect(entries[0].Request.URL).To(HaveSuffix("/v1/models"))
		Expect(entries[0].Response.Content.Text).To(Equal(`{"data":[]}`))
	})

	It("writes only the feature archive when -Phttp.telltale.har overrides the global one", func() {
		dir := GinkgoT().TempDir()
		featurePath := filepath.Join(dir, "telltale.har")
		withProperty("http.har", filepath.Join(dir, "global.har"))
		withProperty("http."+harFeature+".har", featurePath)

		Expect(captureThrough(featurePath)).To(HaveLen(1))
		Expect(filepath.Join(dir, "global.har")).ToNot(BeAnExistingFile())
	})

	It("fails fast on an unrecognised http.har.level", func() {
		withProperty("http.har", filepath.Join(GinkgoT().TempDir(), "trace.har"))
		withProperty("http.har.level", "verbose")

		_, err := harRegistry.Transport(harFeature, http.DefaultTransport)
		Expect(err).To(HaveOccurred())
	})
})
