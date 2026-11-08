package traceui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/traces/read"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("styled trace view", func() {
	It("keeps HTTP pretty rows when the kind also has schema columns", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			defer GinkgoRecover()
			_, err := w.Write([]byte("recorded orders"))
			Expect(err).NotTo(HaveOccurred())
		}))
		DeferCleanup(server.Close)
		dataDir := GinkgoT().TempDir()
		run, err := telltale.Start(context.Background(), telltale.StartOptions{DataDir: dataDir,
			Command: []string{"curl", "--noproxy", "", "-sS", server.URL + "/orders"},
			Traces:  []telltale.TraceSpec{{Kind: "http", Params: json.RawMessage(`{"target":{"seam":"proxy"}}`)}}})
		Expect(err).NotTo(HaveOccurred())
		_, err = run.Wait()
		Expect(err).NotTo(HaveOccurred())
		selected, err := read.Resolve("http", read.Scope{DataDir: dataDir, Runs: []string{run.ID()}})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(selected.Close)
		model := New(context.Background(), Options{Refs: selected.Refs, Replay: true})
		model.width, model.height = 180, 20
		loaded := model.load()().(pageMsg)
		Expect(loaded.err).NotTo(HaveOccurred())
		Expect(loaded.page.Rows).To(HaveLen(1))
		updated, _ := model.Update(loaded)
		text := strings.Join(strings.Fields(ansi.Strip(updated.(Model).View().Content)), " ")
		Expect(text).To(ContainSubstring("http #1 GET"))
		Expect(text).To(ContainSubstring(server.URL + "/orders"))
	})

	It("keeps terminal event rows visible alongside snapshots", func() {
		model := New(context.Background(), Options{Replay: true})
		model.width, model.height = 100, 20
		model.tabs = []tab{{name: "terminal", page: read.FeedResult{Rows: []read.Record{
			{Kind: "terminal", Seq: 1, Record: query.Row{"kind": "r", "width": 120, "height": 40}},
			{Kind: "terminal", Seq: 2, Record: query.Row{"kind": "snapshot", "data": "recorded terminal screen"}},
		}}}}
		text := strings.Join(strings.Fields(ansi.Strip(model.View().Content)), " ")
		Expect(text).To(And(ContainSubstring("terminal #1"), ContainSubstring("terminal #2")))
	})
})
