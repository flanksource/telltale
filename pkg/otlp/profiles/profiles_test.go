package profiles_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/flanksource/telltale/pkg/otlp/profiles"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/collector/pdata/pprofile"
)

func TestProfiles(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "OTLP top profiles") }

var _ = Describe("active operation profiles", func() {
	fixture := func() profiles.Snapshot {
		at := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
		return profiles.Snapshot{Kind: "http_top", CaptureID: "capture-a", Source: "jvm-a", ProfileID: "10f5dd68-2656-4c07-abd7-bc884147e020", Sequence: 1,
			StartedAt: at, CompletedAt: at.Add(time.Millisecond), Observed: 1, Limit: 50, Complete: true,
			Resource: map[string]any{"service.name": "checkout"}, Rows: []profiles.Row{{OperationID: "request-a", ElapsedNS: 250000000,
				SampledAt: at, Attributes: map[string]any{"direction": "incoming", "route": "/orders/{id}", "threadId": int64(9007199254740993)},
				TraceID: "0102030405060708090a0b0c0d0e0f10", SpanID: "0102030405060708", Stack: []string{"application.handle", "application.run"}}}}
	}
	It("carries active, updated and empty snapshots without losing producer identity or integer precision", func() {
		first := fixture()
		second := fixture()
		second.Sequence = 2
		second.ProfileID = "20f5dd68-2656-4c07-abd7-bc884147e020"
		second.Rows[0].ElapsedNS = 500000000
		empty := fixture()
		empty.Sequence = 3
		empty.ProfileID = "30f5dd68-2656-4c07-abd7-bc884147e020"
		empty.Rows = nil
		empty.Observed = 0
		data, err := profiles.Encode([]profiles.Snapshot{first, second, empty})
		Expect(err).NotTo(HaveOccurred())
		native, err := (&pprofile.ProtoUnmarshaler{}).UnmarshalProfiles(data)
		Expect(err).NotTo(HaveOccurred())
		Expect(native.ProfileCount()).To(Equal(3))
		decoded, err := profiles.Decode(data, profiles.Options{ExpectedKind: "http_top", ExpectedCaptureID: "capture-a"})
		Expect(err).NotTo(HaveOccurred())
		Expect(decoded).To(HaveLen(3))
		Expect(decoded[0].Rows).To(Equal(first.Rows))
		Expect(decoded[0].StartedAt).To(Equal(first.StartedAt))
		Expect(decoded[0].Source).To(Equal(first.Source))
		Expect(decoded[1].Rows[0].ElapsedNS).To(Equal(int64(500000000)))
		Expect(decoded[2].Rows).To(BeEmpty())
		Expect(decoded[2].Sequence).To(Equal(int64(3)))
		Expect(decoded[0].Native).NotTo(BeEmpty())
		Expect(decoded[0].Digest).NotTo(BeEmpty())
	})
	It("rejects foreign capture identities and oversized input", func() {
		data, err := profiles.Encode([]profiles.Snapshot{fixture()})
		Expect(err).NotTo(HaveOccurred())
		_, err = profiles.Decode(data, profiles.Options{ExpectedCaptureID: "other"})
		Expect(err).To(MatchError(ContainSubstring("capture")))
		_, err = profiles.Decode(data, profiles.Options{MaxBytes: 1})
		Expect(err).To(MatchError(ContainSubstring("byte limit")))
	})
	It("deduplicates semantic content independently of sample and dictionary ordering", func() {
		first := fixture()
		first.Resource["service.instance.id"] = "instance-a"
		other := first.Rows[0]
		other.OperationID, other.ElapsedNS, other.Stack = "request-b", 750000000, []string{"worker.run"}
		first.Rows = append(first.Rows, other)
		first.Observed = 2
		data, err := profiles.Encode([]profiles.Snapshot{first})
		Expect(err).NotTo(HaveOccurred())
		original, err := profiles.Decode(data, profiles.Options{})
		Expect(err).NotTo(HaveOccurred())
		first.Rows[0], first.Rows[1] = first.Rows[1], first.Rows[0]
		reordered, err := profiles.Encode([]profiles.Snapshot{first})
		Expect(err).NotTo(HaveOccurred())
		Expect(reordered).NotTo(Equal(data))
		decoded, err := profiles.Decode(reordered, profiles.Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(decoded[0].Digest).To(Equal(original[0].Digest))
		Expect(decoded[0].Rows).To(Equal(original[0].Rows))
		value, err := (&pprofile.ProtoUnmarshaler{}).UnmarshalProfiles(data)
		Expect(err).NotTo(HaveOccurred())
		attributes := value.ResourceProfiles().At(0).Resource().Attributes()
		attributes.Clear()
		attributes.PutStr("service.instance.id", "instance-a")
		attributes.PutStr("service.name", "checkout")
		reordered, err = (&pprofile.ProtoMarshaler{}).MarshalProfiles(value)
		Expect(err).NotTo(HaveOccurred())
		decoded, err = profiles.Decode(reordered, profiles.Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(decoded[0].Digest).To(Equal(original[0].Digest))
	})
	It("preserves native profile detail while redacting both native and projected attributes", func() {
		data, err := profiles.Encode([]profiles.Snapshot{fixture()})
		Expect(err).NotTo(HaveOccurred())
		value, err := (&pprofile.ProtoUnmarshaler{}).UnmarshalProfiles(data)
		Expect(err).NotTo(HaveOccurred())
		resource := value.ResourceProfiles().At(0)
		resource.SetSchemaUrl("https://example.test/resource-schema")
		scope := resource.ScopeProfiles().At(0)
		scope.Scope().Attributes().PutStr("password", "private-scope-value")
		profile := scope.Profiles().At(0)
		profile.SetPeriod(77)
		profile.SetDroppedAttributesCount(3)
		dictionary := value.Dictionary()
		dictionary.StringTable().Append("authorization")
		attribute := dictionary.AttributeTable().AppendEmpty()
		attribute.SetKeyStrindex(int32(dictionary.StringTable().Len() - 1))
		attribute.Value().SetStr("private-attribute-value")
		profile.Samples().At(0).AttributeIndices().Append(int32(dictionary.AttributeTable().Len() - 1))
		data, err = (&pprofile.ProtoMarshaler{}).MarshalProfiles(value)
		Expect(err).NotTo(HaveOccurred())
		Expect(bytes.Contains(data, []byte("private-attribute-value"))).To(BeTrue())
		decoded, err := profiles.Decode(data, profiles.Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(decoded[0].Rows[0].Attributes).To(HaveKeyWithValue("authorization", "****"))
		Expect(string(decoded[0].Native)).NotTo(ContainSubstring("private-"))
		native, err := (&pprofile.ProtoUnmarshaler{}).UnmarshalProfiles(decoded[0].Native)
		Expect(err).NotTo(HaveOccurred())
		Expect(native.ResourceProfiles().At(0).SchemaUrl()).To(Equal(resource.SchemaUrl()))
		preserved := native.ResourceProfiles().At(0).ScopeProfiles().At(0).Profiles().At(0)
		Expect(preserved.Period()).To(Equal(int64(77)))
		Expect(preserved.DroppedAttributesCount()).To(Equal(uint32(3)))
	})
	It("rejects invalid dictionary references and opaque payloads before projection", func() {
		data, err := profiles.Encode([]profiles.Snapshot{fixture()})
		Expect(err).NotTo(HaveOccurred())
		value, err := (&pprofile.ProtoUnmarshaler{}).UnmarshalProfiles(data)
		Expect(err).NotTo(HaveOccurred())
		profile := value.ResourceProfiles().At(0).ScopeProfiles().At(0).Profiles().At(0)
		profile.Samples().At(0).SetLinkIndex(999)
		data, err = (&pprofile.ProtoMarshaler{}).MarshalProfiles(value)
		Expect(err).NotTo(HaveOccurred())
		_, err = profiles.Decode(data, profiles.Options{})
		Expect(err).To(MatchError(ContainSubstring("link index")))
		profile.Samples().At(0).SetLinkIndex(0)
		profile.OriginalPayload().FromRaw([]byte("opaque-secret"))
		data, err = (&pprofile.ProtoMarshaler{}).MarshalProfiles(value)
		Expect(err).NotTo(HaveOccurred())
		_, err = profiles.Decode(data, profiles.Options{})
		Expect(err).To(MatchError(ContainSubstring("opaque")))
	})
	It("requires reserved zero entries even for empty snapshots", func() {
		snapshot := fixture()
		snapshot.Rows, snapshot.Observed = nil, 0
		data, err := profiles.Encode([]profiles.Snapshot{snapshot})
		Expect(err).NotTo(HaveOccurred())
		value, err := (&pprofile.ProtoUnmarshaler{}).UnmarshalProfiles(data)
		Expect(err).NotTo(HaveOccurred())
		value.Dictionary().StackTable().RemoveIf(func(pprofile.Stack) bool { return true })
		data, err = (&pprofile.ProtoMarshaler{}).MarshalProfiles(value)
		Expect(err).NotTo(HaveOccurred())
		_, err = profiles.Decode(data, profiles.Options{})
		Expect(err).To(MatchError(ContainSubstring("zero entries")))
	})
	DescribeTable("includes native metadata in semantic conflicts", func(change func(pprofile.Profiles)) {
		data, err := profiles.Encode([]profiles.Snapshot{fixture()})
		Expect(err).NotTo(HaveOccurred())
		original, err := profiles.Decode(data, profiles.Options{})
		Expect(err).NotTo(HaveOccurred())
		value, err := (&pprofile.ProtoUnmarshaler{}).UnmarshalProfiles(data)
		Expect(err).NotTo(HaveOccurred())
		change(value)
		data, err = (&pprofile.ProtoMarshaler{}).MarshalProfiles(value)
		Expect(err).NotTo(HaveOccurred())
		changed, err := profiles.Decode(data, profiles.Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(changed[0].Digest).NotTo(Equal(original[0].Digest))
	}, Entry("period", func(value pprofile.Profiles) {
		value.ResourceProfiles().At(0).ScopeProfiles().At(0).Profiles().At(0).SetPeriod(7)
	}), Entry("resource schema", func(value pprofile.Profiles) {
		value.ResourceProfiles().At(0).SetSchemaUrl("https://example.test/new-schema")
	}), Entry("scope attributes", func(value pprofile.Profiles) {
		value.ResourceProfiles().At(0).ScopeProfiles().At(0).Scope().Attributes().PutStr("extra", "changed")
	}), Entry("native stack address", func(value pprofile.Profiles) {
		value.Dictionary().LocationTable().At(1).SetAddress(1234)
	}))
})
