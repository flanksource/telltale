package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/internal/server"
	"github.com/flanksource/telltale/pkg/traces"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("trace replay viewer", func() {
	It("lists and serves only the selected runs", func() {
		dataDir := GinkgoT().TempDir()
		ids := []string{"first", "second", "unselected"}
		for _, id := range ids {
			dir := traces.StoreDir(dataDir, id)
			Expect(os.MkdirAll(dir, 0o755)).To(Succeed())
			data, err := json.Marshal(telltale.Result{RunID: id, Dir: dir, StartedAt: time.Now(), EndedAt: time.Now()})
			Expect(err).NotTo(HaveOccurred())
			Expect(os.WriteFile(filepath.Join(dir, telltale.ManifestFile), data, 0o600)).To(Succeed())
		}
		viewer, err := server.StartViewer(server.ViewerOptions{DataDir: dataDir, RunIDs: ids[:2]})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(viewer.Close(context.Background())).To(Succeed()) })
		address, err := url.Parse(viewer.URL)
		Expect(err).NotTo(HaveOccurred())
		base := address.Scheme + "://" + address.Host
		response, err := http.Get(base + "/api/traces")
		Expect(err).NotTo(HaveOccurred())
		var listed telltale.RunsList
		Expect(json.NewDecoder(response.Body).Decode(&listed)).To(Succeed())
		Expect(response.Body.Close()).To(Succeed())
		Expect(listed.Runs).To(HaveExactElements(HaveField("RunID", ids[0]), HaveField("RunID", ids[1])))
		for _, path := range []string{"/api/traces/unselected/records", "/api/traces/unselected/processes", "/api/traces/unselected/api/v1/traces/process/export", "/api/traces/first/api/v1/traces/process/start", "/api/traces/start", "/api/v1/trace/run"} {
			response, err := http.Get(base + path)
			Expect(err).NotTo(HaveOccurred())
			Expect(response.StatusCode).To(Equal(http.StatusNotFound), path)
			Expect(response.Body.Close()).To(Succeed())
		}
		for _, id := range ids[:2] {
			response, err := http.Get(base + "/api/traces/" + id + "/processes")
			Expect(err).NotTo(HaveOccurred())
			Expect(response.StatusCode).To(Equal(http.StatusOK))
			Expect(response.Body.Close()).To(Succeed())
			response, err = http.Get(base + "/api/traces/" + id + "/records")
			Expect(err).NotTo(HaveOccurred())
			Expect(response.StatusCode).To(Equal(http.StatusOK))
			Expect(response.Body.Close()).To(Succeed())
		}
	})
})
