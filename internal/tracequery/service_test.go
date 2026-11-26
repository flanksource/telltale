package tracequery

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/flanksource/commons-db/connection/crd"
	"github.com/flanksource/commons-db/connection/store"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("trace source dispatch", func() {
	It("installs the host reader only for recorded spans", func() {
		service := Service{}
		Expect(service.query("records").Read).NotTo(BeNil())
		Expect(service.query("connection://spans").Read).To(BeNil())
	})

	It("searches and opens traces from the configured OpenSearch source", func() {
		at := time.Now().UTC().Add(-time.Minute)
		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.Method == http.MethodHead:
				w.WriteHeader(http.StatusOK)
			case strings.HasSuffix(r.URL.Path, "/_field_caps"):
				_, _ = fmt.Fprintf(w, `{"fields":{%q:{"date":{"searchable":true,"aggregatable":true}}}}`, r.URL.Query().Get("fields"))
			case strings.Contains(r.URL.Path, "/_mapping/field/"):
				_, _ = fmt.Fprint(w, `{"spans":{"mappings":{"@timestamp":{"full_name":"@timestamp","mapping":{"@timestamp":{"type":"date"}}}}}}`)
			case strings.HasSuffix(r.URL.Path, "/_search/point_in_time"):
				_, _ = fmt.Fprint(w, `{"pit_id":"pit-1"}`)
			case strings.HasSuffix(r.URL.Path, "/_search"):
				_, _ = fmt.Fprintf(w, `{"hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"spans","_id":"span","_source":{"@timestamp":%q,"trace_id":"trace","span_id":"span","service":"checkout","operation":"order","duration_ms":0.25},"sort":[%d,"span"]}]}}`, at.Format(time.RFC3339Nano), at.UnixMilli())
			default:
				http.NotFound(w, r)
			}
		}))
		DeferCleanup(backend.Close)
		connections, err := store.New(store.Layer{Name: store.LayerCWD, Dir: GinkgoT().TempDir()})
		Expect(err).NotTo(HaveOccurred())
		Expect(connections.Save(store.LayerCWD, &crd.Connection{
			ObjectMeta: metav1.ObjectMeta{Name: "spans"},
			Spec:       crd.ConnectionSpec{OpenSearch: &crd.ConnectionOpenSearch{URLs: []string{backend.URL}, Index: "spans"}},
		})).To(Succeed())

		service := Service{Connections: connections}
		request := Request{Source: "connection://spans", Index: "spans"}
		result, err := service.Search(context.Background(), request)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Traces).To(HaveExactElements(And(HaveField("TraceID", "trace"), HaveField("SpanCount", 1))))
		trace, err := service.Trace(context.Background(), request, "trace")
		Expect(err).NotTo(HaveOccurred())
		Expect(trace.Spans).To(HaveExactElements(HaveField("SpanID", "span")))
	})
})
