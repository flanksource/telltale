package records_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/flanksource/telltale/pkg/traceserve/records"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/target"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRecords(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "HTTP records") }

const fixtureKind = "http_records_fixture"

type reading struct {
	At    time.Time `json:"at"`
	Value float64   `json:"value" filter:"range"`
	Label string    `json:"label" filter:"text"`
}

type params struct{}

func (params) Validate() error { return nil }

type feed struct {
	rows chan reading
	ack  chan error
}

func (*feed) Params() params { return params{} }
func (*feed) Schema() recordresults.ResultType[reading] {
	return recordresults.ResultType[reading]{Title: "HTTP fixture", TimeColumn: "at", Follow: true, SearchColumns: []string{"label"}, Views: []recordresults.ResultView{{Name: "totals", Title: "Totals", Query: "SELECT count(*) AS count FROM stream_rows", Columns: []query.ColumnDef{{Name: "count", Type: query.ColumnTypeNumber}}, Order: query.Order{{Column: "count", Unique: true}}}}}
}
func (f *feed) Handle(ctx dbcontext.Context, _ params, emit tracing.Emitter[reading], _ tracing.Records[reading]) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case row := <-f.rows:
			f.ack <- emit.Emit(ctx, row)
		}
	}
}

var feeding *feed
var _ = BeforeSuite(func() {
	Expect(traces.Register(traces.Registration{Name: fixtureKind, Title: "HTTP fixture", Summary: "Controlled records", Seams: []target.Seam{target.Self}, DefaultSeam: target.Self, New: func(traces.KindDeps) tracing.TracePlugin {
		return tracing.NewHandler[params, reading](feeding, tracing.Capabilities{Live: true}, "self")
	}})).To(Succeed())
})

type page struct {
	Rows       []query.Row `json:"rows"`
	NextCursor string      `json:"nextCursor"`
}

var _ = Describe("run records route", func() {
	var server *httptest.Server
	var capture *traces.Capture
	var path string
	var at time.Time
	BeforeEach(func() {
		feeding = &feed{rows: make(chan reading), ack: make(chan error, 1)}
		dir := GinkgoT().TempDir()
		host, err := traces.Open(traces.OpenOptions{WorkDir: dir, RunID: "run-http"})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(host.Close(context.Background())).To(Succeed()) })
		capture, err = host.Start(fixtureKind, json.RawMessage(`{}`))
		Expect(err).NotTo(HaveOccurred())
		data, err := json.Marshal(telltale.RunInfo{RunID: host.RunID(), Dir: host.Dir(), Status: telltale.StatusRunning, StartedAt: time.Now(), Captures: []telltale.CaptureResult{{Kind: fixtureKind, Ref: capture.Ref(0, 0)}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(host.Dir(), telltale.ManifestFile), data, 0o600)).To(Succeed())
		mux := http.NewServeMux()
		mux.Handle("GET /api/traces/{run}/traces/{kind}/records", records.Handler(dir))
		server = httptest.NewServer(mux)
		DeferCleanup(server.Close)
		path = server.URL + "/api/traces/run-http/traces/" + fixtureKind + "/records"
		at = time.Now().UTC().Truncate(time.Second)
	})
	appendRows := func(values ...float64) {
		for _, value := range values {
			feeding.rows <- reading{At: at.Add(time.Duration(value) * time.Second), Value: value, Label: "worker"}
			Expect(<-feeding.ack).To(Succeed())
			_, err := capture.Mark(context.Background())
			Expect(err).NotTo(HaveOccurred())
		}
	}
	get := func(params url.Values) page {
		response, err := server.Client().Get(path + "?" + params.Encode())
		Expect(err).NotTo(HaveOccurred())
		defer func() { Expect(response.Body.Close()).To(Succeed()) }()
		body, err := io.ReadAll(response.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode).To(Equal(http.StatusOK), string(body))
		var result page
		Expect(json.Unmarshal(body, &result)).To(Succeed(), string(body))
		return result
	}
	It("pages in profile order and applies repeated filters, time, seq, search and CEL before limit", func() {
		appendRows(0, 1, 2, 3)
		first := get(url.Values{"limit": {"2"}})
		Expect(first.Rows).To(HaveExactElements(HaveKeyWithValue("value", float64(3)), HaveKeyWithValue("value", float64(2))))
		Expect(first.NextCursor).NotTo(BeEmpty())
		second := get(url.Values{"limit": {"2"}, "cursor": {first.NextCursor}})
		Expect(second.Rows).To(HaveExactElements(HaveKeyWithValue("value", float64(1)), HaveKeyWithValue("value", float64(0))))
		Expect(second.NextCursor).To(BeEmpty())
		filtered := get(url.Values{"stream": {capture.Stream()}, "from": {at.Add(time.Second).Format(time.RFC3339)}, "until": {at.Add(4 * time.Second).Format(time.RFC3339)}, "afterSeq": {"1"}, "toSeq": {"4"}, "filter": {"value=>=1", "label=worker"}, "q": {"work"}, "where": {"row.value < 3"}, "limit": {"1"}})
		Expect(filtered.Rows).To(ConsistOf(And(HaveKeyWithValue("value", float64(2)), HaveKeyWithValue("runId", "run-http"))))
	})
	It("aggregates all matching rows by default and executes named views over seq windows", func() {
		appendRows(0, 1, 2, 3)
		stats := get(url.Values{"stats": {"value"}, "groupBy": {"label"}})
		Expect(stats.Rows).To(ConsistOf(Equal(query.Row{"label": "worker", "column": "value", "count": float64(4), "min": float64(0), "avg": 1.5, "max": float64(3), "p50": float64(1), "p90": float64(3), "p95": float64(3), "p99": float64(3), "approximate": false})))
		Expect(get(url.Values{"groupBy": {"label"}}).Rows).To(ConsistOf(Equal(query.Row{"label": "worker", "count": float64(4)})))
		Expect(get(url.Values{"view": {"totals"}, "afterSeq": {"1"}, "toSeq": {"3"}}).Rows).To(ConsistOf(And(HaveKeyWithValue("count", float64(2)), HaveKeyWithValue("runId", "run-http"))))
		Expect(get(url.Values{"stats": {"value"}, "limit": {"2"}}).Rows).To(ConsistOf(HaveKeyWithValue("count", float64(2))))
	})
	DescribeTable("returns bad requests with the error", func(params url.Values, message string) {
		appendRows(1)
		response, err := server.Client().Get(path + "?" + params.Encode())
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = response.Body.Close() }()
		body, err := io.ReadAll(response.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode).To(Equal(http.StatusBadRequest), string(body))
		Expect(string(body)).To(ContainSubstring(message))
	},
		Entry("bad CEL", url.Values{"where": {"row.value +"}}, "CEL"),
		Entry("follow bad CEL", url.Values{"follow": {"1"}, "where": {"row.value +"}}, "CEL"),
		Entry("unknown filter", url.Values{"filter": {"missing=1"}}, "missing"),
		Entry("follow unknown filter", url.Values{"follow": {"1"}, "filter": {"missing=1"}}, "missing"),
		Entry("bad sequence", url.Values{"afterSeq": {"oops"}}, "afterSeq"),
		Entry("bad window", url.Values{"afterSeq": {"4"}, "toSeq": {"1"}}, "seq"),
		Entry("bad cursor", url.Values{"cursor": {"oops"}}, "cursor"),
		Entry("follow stats", url.Values{"follow": {"1"}, "stats": {"value"}}, "--follow"),
		Entry("bad format", url.Values{"follow": {"1"}, "format": {"csv"}}, "format"),
		Entry("unknown parameter", url.Values{"limti": {"1"}}, "limti"),
		Entry("missing stream", url.Values{"stream": {"missing"}}, "stream"),
	)
	It("returns 400 for unknown kinds and 404 for unknown runs", func() {
		for _, tc := range []struct {
			path string
			code int
		}{
			{strings.Replace(path, fixtureKind, "missing-kind", 1), http.StatusBadRequest},
			{strings.Replace(path, "run-http", "missing-run", 1), http.StatusNotFound},
		} {
			response, err := server.Client().Get(tc.path)
			Expect(err).NotTo(HaveOccurred())
			Expect(response.Body.Close()).To(Succeed())
			Expect(response.StatusCode).To(Equal(tc.code))
		}
	})
	DescribeTable("follows appends after connecting, resumes after Last-Event-ID and ends on seal", func(format string) {
		appendRows(0, 1)
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, path+"?follow=1&format="+format, nil)
		Expect(err).NotTo(HaveOccurred())
		request.Header.Set("Last-Event-ID", "2")
		response, err := server.Client().Do(request)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = response.Body.Close() }()
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		if format == "ndjson" {
			Expect(response.Header.Get("Content-Type")).To(Equal("application/x-ndjson"))
		} else {
			Expect(response.Header.Get("Content-Type")).To(Equal("text/event-stream"))
		}
		appendRows(2, 3)
		_, err = capture.Stop(context.Background())
		Expect(err).NotTo(HaveOccurred())
		var rows []query.Row
		var ids []string
		var ended bool
		scanner := bufio.NewScanner(response.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "event: end" {
				ended = true
			}
			if strings.HasPrefix(line, "id: ") {
				ids = append(ids, strings.TrimPrefix(line, "id: "))
			}
			if format != "ndjson" && (!strings.HasPrefix(line, "data: ") || ended) {
				continue
			}
			var row query.Row
			Expect(json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &row)).To(Succeed(), line)
			rows = append(rows, row)
		}
		Expect(scanner.Err()).NotTo(HaveOccurred())
		Expect(rows).To(HaveExactElements(HaveKeyWithValue("value", float64(2)), HaveKeyWithValue("value", float64(3))))
		if format != "ndjson" {
			Expect(ids).To(Equal([]string{"3", "4"}))
			Expect(ended).To(BeTrue())
		}
	}, Entry("SSE", ""), Entry("NDJSON", "ndjson"))
	It("closes the follower on client disconnect", func() {
		appendRows(1)
		response, err := server.Client().Get(path + "?follow=1")
		Expect(err).NotTo(HaveOccurred())
		Expect(response.Body.Close()).To(Succeed())
		closed := make(chan struct{})
		go func() { server.Close(); close(closed) }()
		Eventually(closed).WithTimeout(3 * time.Second).Should(BeClosed())
	})
	It("connects before the first row and sends a heartbeat after 15 seconds", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, path+"?follow=1", nil)
		Expect(err).NotTo(HaveOccurred())
		started := time.Now()
		response, err := server.Client().Do(request)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = response.Body.Close() }()
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		lines := bufio.NewScanner(response.Body)
		Expect(lines.Scan()).To(BeTrue())
		Expect(lines.Text()).To(Equal(": heartbeat"))
		Expect(time.Since(started)).To(BeNumerically(">=", 15*time.Second))
		appendRows(1)
		_, err = capture.Stop(context.Background())
		Expect(err).NotTo(HaveOccurred())
		var remaining []string
		for lines.Scan() {
			remaining = append(remaining, lines.Text())
		}
		Expect(lines.Err()).NotTo(HaveOccurred())
		Expect(remaining).To(ContainElements("id: 1", "event: end"))
	})
	It("reads and follows an in-process live run until it finishes", func() {
		dir := GinkgoT().TempDir()
		run, err := telltale.Start(context.Background(), telltale.StartOptions{WorkDir: dir, PID: os.Getpid(), Duration: time.Second, Traces: []telltale.TraceSpec{{Kind: "process_samples"}}})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(run.Stop()).To(Succeed()); _, err := run.Wait(); Expect(err).NotTo(HaveOccurred()) })
		mux := http.NewServeMux()
		mux.Handle("GET /api/traces/{run}/traces/{kind}/records", records.Handler(dir))
		liveServer := httptest.NewServer(mux)
		DeferCleanup(liveServer.Close)
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, liveServer.URL+"/api/traces/"+run.ID()+"/traces/process_samples/records?follow=1", nil)
		Expect(err).NotTo(HaveOccurred())
		response, err := liveServer.Client().Do(request)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = response.Body.Close() }()
		body, err := io.ReadAll(response.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode).To(Equal(http.StatusOK), string(body))
		Expect(string(body)).To(ContainSubstring("data: {"))
		Expect(string(body)).To(ContainSubstring("event: end"))
	})
})
