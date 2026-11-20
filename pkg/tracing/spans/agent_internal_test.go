package spans

import (
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
)

var _ = Describe("Java span mirror blobs", func() {
	It("rebuilds authoritative fields and exact attributes from redacted native OTLP", func() {
		request := ptraceotlp.NewExportRequest()
		resource := request.Traces().ResourceSpans().AppendEmpty()
		resource.Resource().Attributes().PutStr("service.name", "original-service")
		scope := resource.ScopeSpans().AppendEmpty()
		scope.Scope().SetName("existing-sdk")
		span := scope.Spans().AppendEmpty()
		span.SetTraceID(pcommon.TraceID{1})
		span.SetSpanID(pcommon.SpanID{2})
		span.SetName("original-operation")
		span.SetStartTimestamp(pcommon.NewTimestampFromTime(time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)))
		span.SetEndTimestamp(span.StartTimestamp() + 100)
		span.Attributes().PutInt("large.integer", 9007199254740993)
		span.Attributes().PutStr("password", "private-native-value")
		span.Events().AppendEmpty().SetName("original-event")
		native, err := request.MarshalProto()
		Expect(err).NotTo(HaveOccurred())
		data, err := json.Marshal([]map[string]any{{"operation": "untrusted-json", "attributes": map[string]any{"large.integer": 1}, "native": base64.StdEncoding.EncodeToString(native)}})
		Expect(err).NotTo(HaveOccurred())
		decoded, err := decodeBlob(agentclient.Blob{Info: protocol.Blob{ContentType: "application/json"}, Data: data}, map[string]string{"process.pid": "42"})
		Expect(err).NotTo(HaveOccurred())
		Expect(decoded.Rows).To(HaveLen(1))
		Expect(decoded.Rows[0].Operation).To(Equal("original-operation"))
		Expect(decoded.Rows[0].Resource).To(HaveKeyWithValue("process.pid", "42"))
		Expect(decoded.Rows[0].Attributes).To(HaveKeyWithValue("large.integer", int64(9007199254740993)))
		Expect(decoded.Rows[0].Digest).NotTo(BeEmpty())
		stored, err := base64.StdEncoding.DecodeString(decoded.Rows[0].Native)
		Expect(err).NotTo(HaveOccurred())
		restored := ptraceotlp.NewExportRequest()
		Expect(restored.UnmarshalProto(stored)).To(Succeed())
		encoded, err := restored.MarshalJSON()
		Expect(err).NotTo(HaveOccurred())
		Expect(string(encoded)).NotTo(ContainSubstring("private-native-value"))
		Expect(restored.Traces().ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0).Events().Len()).To(Equal(1))
	})
	It("rejects a mirror record without native data", func() {
		_, err := decodeBlob(agentclient.Blob{Info: protocol.Blob{ContentType: "application/json"}, Data: []byte(`[{"operation":"missing-native"}]`)}, nil)
		Expect(err).To(MatchError(ContainSubstring("native")))
	})
})
