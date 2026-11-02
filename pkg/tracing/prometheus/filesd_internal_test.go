package prometheus

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("file_sd target groups", func() {
	It("renders one group per endpoint with its scheme, path, params, timing and labels", func() {
		params := Params{Options: Options{
			Endpoints: []string{"http://localhost:8080/metrics", "https://exporter.example.com:9115/probe?module=http_2xx"},
			Interval:  "5s", Timeout: "2s", Labels: map[string]string{"env": "dev"},
		}}
		rendered, err := targetGroups(params, session{run: "run-2026-10-31T10-00-00Z", stream: "STREAM1"})
		Expect(err).ToNot(HaveOccurred())
		Expect(string(rendered)).To(Equal(`[
  {
    "targets": [
      "localhost:8080"
    ],
    "labels": {
      "__metrics_path__": "/metrics",
      "__scheme__": "http",
      "__scrape_interval__": "5000ms",
      "__scrape_timeout__": "2000ms",
      "env": "dev",
      "telltale_endpoint": "http://localhost:8080/metrics",
      "telltale_run": "run-2026-10-31T10-00-00Z",
      "telltale_stream": "STREAM1"
    }
  },
  {
    "targets": [
      "exporter.example.com:9115"
    ],
    "labels": {
      "__metrics_path__": "/probe",
      "__param_module": "http_2xx",
      "__scheme__": "https",
      "__scrape_interval__": "5000ms",
      "__scrape_timeout__": "2000ms",
      "env": "dev",
      "telltale_endpoint": "https://exporter.example.com:9115/probe?module=http_2xx",
      "telltale_run": "run-2026-10-31T10-00-00Z",
      "telltale_stream": "STREAM1"
    }
  }
]
`))
	})

	It("defaults the port by scheme, the path to /, and the timeout to the shorter of 10s and the interval", func() {
		rendered, err := targetGroups(Params{Options: Options{Endpoints: []string{"https://example.com"}, Interval: "3s"}}, session{run: "run-1", stream: "S"})
		Expect(err).ToNot(HaveOccurred())
		Expect(string(rendered)).To(ContainSubstring(`"example.com:443"`))
		Expect(string(rendered)).To(ContainSubstring(`"__metrics_path__": "/"`))
		Expect(string(rendered)).To(ContainSubstring(`"__scrape_timeout__": "3000ms"`))
	})

	It("writes the session's file atomically and removes it on release", func() {
		dir := GinkgoT().TempDir()
		path, err := writeTargets(dir, "STREAM1", []byte("[]\n"))
		Expect(err).ToNot(HaveOccurred())
		Expect(path).To(Equal(filepath.Join(dir, "STREAM1.json")))
		Expect(os.ReadFile(path)).To(Equal([]byte("[]\n")))
		entries, err := os.ReadDir(dir)
		Expect(err).ToNot(HaveOccurred())
		Expect(entries).To(HaveLen(1), "no temp file is left behind")
		Expect(os.Remove(path)).To(Succeed())
	})
})
