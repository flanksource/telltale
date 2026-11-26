package records_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/recordstore/sampleframe"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/traceserve/records"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/target"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type sampledFeed struct {
	feed
	frames chan []reading
}

func (s *sampledFeed) Handle(ctx dbcontext.Context, _ params, out tracing.Emitter[reading], _ tracing.Records[reading]) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case rows := <-s.frames:
			at := time.Now().UTC()
			s.ack <- tracing.PublishFrame(ctx, out, sampleframe.Frame{Source: "fixture", StartedAt: at, CompletedAt: at, Observed: len(rows)}, rows)
		}
	}
}

var sampled = &sampledFeed{}

func registerTopFixture() error {
	return traces.Register(traces.Registration{Name: topFixtureKind, Title: "Top fixture", Summary: "Controlled sampled records", Seams: []target.Seam{target.Self}, DefaultSeam: target.Self, New: func(traces.KindDeps) tracing.TracePlugin {
		return tracing.NewHandler[params, reading](sampled, tracing.Capabilities{Live: true}, target.Self).WithTop(tracing.TopSpec{Identity: []string{"label"}})
	}})
}

var _ = Describe("streaming Top", func() {
	It("waits for the first sample, replaces it with an empty committed frame, and ends on seal", func(ctx SpecContext) {
		sampled = &sampledFeed{feed: feed{ack: make(chan error, 1)}, frames: make(chan []reading)}
		dir := GinkgoT().TempDir()
		host, err := traces.Open(traces.OpenOptions{DataDir: dir, RunID: "top-http"})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(host.Close, context.Background())
		capture, err := host.Start(traces.StartOptions{Kind: topFixtureKind, Params: json.RawMessage(`{}`)})
		Expect(err).NotTo(HaveOccurred())
		manifest, err := json.Marshal(telltale.RunInfo{RunID: host.RunID(), Dir: host.Dir(), Status: telltale.StatusRunning, StartedAt: time.Now(), Captures: []telltale.CaptureResult{{Kind: topFixtureKind, Ref: capture.Ref(0, 0)}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(host.Dir(), telltale.ManifestFile), manifest, 0o600)).To(Succeed())
		mux := http.NewServeMux()
		mux.Handle("GET /api/traces/{run}/traces/{kind}/records", records.Handler(dir))
		server := httptest.NewServer(mux)
		DeferCleanup(server.Close)
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/traces/top-http/traces/"+topFixtureKind+"/records?view=top&follow=1", nil)
		Expect(err).NotTo(HaveOccurred())
		response, err := server.Client().Do(request)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(response.Body.Close)
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		Expect(response.Header.Get("Content-Type")).To(Equal("text/event-stream"))
		reader := bufio.NewScanner(response.Body)
		readUpdate := func() string {
			for reader.Scan() {
				if data, ok := strings.CutPrefix(reader.Text(), "data: "); ok {
					return data
				}
			}
			Fail("stream ended before delivering a data frame")
			return ""
		}
		Expect(readUpdate()).To(MatchJSON(`{"frames":[],"reset":true}`))
		sampled.frames <- []reading{{At: time.Now().UTC(), Value: 1, Label: "active request"}}
		Expect(<-sampled.ack).To(Succeed())
		Expect(readUpdate()).To(ContainSubstring(`"count":1`))
		sampled.frames <- nil
		Expect(<-sampled.ack).To(Succeed())
		Expect(readUpdate()).To(ContainSubstring(`"count":0`))
		_, err = capture.Stop(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(readUpdate()).To(Equal("{}"))
	}, SpecTimeout(8*time.Second))
})
