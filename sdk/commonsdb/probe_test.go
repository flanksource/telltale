package commonsdb

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/flanksource/commons-db/connection"
	dbcontext "github.com/flanksource/commons-db/context"
	"github.com/flanksource/commons-db/observability"
	"github.com/flanksource/commons/har"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestProbes(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Commons DB probes") }

type sink struct{ records chan any }

func (s sink) TryEmit(v any) bool                   { s.records <- v; return true }
func (sink) Blob(string, string, []byte, any) error { return nil }
func (sink) Report(any)                             {}
func (sink) Warn(string)                            {}

var _ = Describe("commons-db probes", func() {
	It("observes selected SQL statements and stops observing on release", func() {
		out := sink{records: make(chan any, 2)}
		release, err := (sqlProbe{}).Arm(context.Background(), json.RawMessage(`{"connections":["orders"],"minDuration":"10ms"}`), out)
		Expect(err).NotTo(HaveOccurred())
		connection.PublishSQL(connection.Statement{Connection: "orders", SQL: "SELECT 1", Duration: 20 * time.Millisecond})
		Expect((<-out.records).(connection.Statement).SQL).To(Equal("SELECT 1"))
		Expect(release()).To(Succeed())
		connection.PublishSQL(connection.Statement{Connection: "orders", SQL: "SELECT 2", Duration: time.Second})
		Expect(out.records).To(BeEmpty())
	})
	It("observes selected HTTP features and stops on release", func() {
		out := sink{records: make(chan any, 2)}
		release, err := armHTTP(context.Background(), json.RawMessage(`{"features":["orders"]}`), out)
		Expect(err).NotTo(HaveOccurred())
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
		defer server.Close()
		client := http.Client{Transport: observability.ApplyHTTPObservability(dbcontext.New(), "orders", http.DefaultTransport, nil)}
		resp, err := client.Get(server.URL)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Body.Close()).To(Succeed())
		record := (<-out.records).(HTTPEntry)
		Expect(record.Feature).To(Equal("orders"))
		Expect(record.Entry).To(BeAssignableToTypeOf(har.Entry{}))
		Expect(record.Request.URL).To(Equal(server.URL))
		Expect(release()).To(Succeed())
		resp, err = client.Get(server.URL)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Body.Close()).To(Succeed())
		Expect(out.records).To(BeEmpty())
	})
})
