package otelreceiver

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/flanksource/telltale/pkg/otlp/profiles"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func topSnapshot(sequence int64) profiles.Snapshot {
	at := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC).Add(time.Duration(sequence) * time.Second)
	return profiles.Snapshot{Kind: "http_top", CaptureID: "capture-a", Source: "jvm-a", ProfileID: []string{"10f5dd68-2656-4c07-abd7-bc884147e020", "20f5dd68-2656-4c07-abd7-bc884147e020", "30f5dd68-2656-4c07-abd7-bc884147e020"}[sequence-1], Sequence: sequence,
		StartedAt: at, CompletedAt: at.Add(time.Millisecond), Observed: 1, Limit: 50, Complete: true, PollIntervalMS: 5000,
		Rows: []profiles.Row{{OperationID: "request-a", ElapsedNS: sequence * int64(time.Second), SampledAt: at, Attributes: map[string]any{"method": "GET", "url": "http://example.test/orders", "direction": "incoming", "startedAt": at.Add(-time.Duration(sequence) * time.Second).Format(time.RFC3339Nano)}}}}
}

var _ = Describe("OTLP Profiles receiver", func() {
	It("commits active, updated and empty snapshots once before acknowledging", func() {
		receiver, store := testReceiver(DefaultOptions())
		for sequence := int64(1); sequence <= 3; sequence++ {
			snapshot := topSnapshot(sequence)
			if sequence == 3 {
				snapshot.Rows = nil
				snapshot.Observed = 0
			}
			body, err := profiles.Encode([]profiles.Snapshot{snapshot})
			Expect(err).NotTo(HaveOccurred())
			for retry := 0; retry < 2; retry++ {
				request := httptest.NewRequest(http.MethodPost, "/v1/profiles", bytes.NewReader(body))
				request.Header.Set("Content-Type", protobufType)
				request.Header.Set("OTLP-Profiles-Development-Version", "1")
				response := httptest.NewRecorder()
				receiver.HTTPHandler(false).ServeHTTP(response, request)
				Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
			}
		}
		stored, err := store.ReadProfiles(context.Background(), ProfileReadOptions{Kind: "http_top"})
		Expect(err).NotTo(HaveOccurred())
		Expect(stored).To(HaveLen(3))
		Expect(stored[0].Rows[0].OperationID).To(Equal("request-a"))
		Expect(stored[1].Rows[0].ElapsedNS).To(Equal(int64(2 * time.Second)))
		Expect(stored[2].Rows).To(BeEmpty())
	})
	DescribeTable("rejects unsupported schema metadata before decoding", func(version string) {
		receiver, _ := testReceiver(DefaultOptions())
		request := httptest.NewRequest(http.MethodPost, "/v1/profiles", bytes.NewBufferString("bad protobuf"))
		request.Header.Set("Content-Type", protobufType)
		request.Header.Set("OTLP-Profiles-Development-Version", version)
		response := httptest.NewRecorder()
		receiver.HTTPHandler(false).ServeHTTP(response, request)
		Expect(response.Code).To(Equal(http.StatusBadRequest))
		Expect(response.Body.String()).To(ContainSubstring("version"))
	}, Entry("future version", "2"), Entry("malformed version", "01"))
	It("requires authentication for Profiles just as for traces", func() {
		receiver, _ := testReceiver(DefaultOptions())
		request := httptest.NewRequest(http.MethodPost, "/v1/profiles", bytes.NewBufferString("{}"))
		request.Header.Set("Content-Type", jsonType)
		response := httptest.NewRecorder()
		receiver.HTTPHandler(true).ServeHTTP(response, request)
		Expect(response.Code).To(Equal(http.StatusUnauthorized))
	})
})
