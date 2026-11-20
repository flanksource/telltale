package profiles_test

import (
	"math"
	"time"

	"github.com/flanksource/telltale/pkg/otlp/profiles"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pprofile"
)

const controlAttribute = "telltale.sample.kind"
const controlKind = "snapshot_control"

func controlSnapshot() profiles.Snapshot {
	at := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	return profiles.Snapshot{Kind: "http_top", CaptureID: "capture-control", Source: "jvm-control", ProfileID: "97fa6305-a036-4d96-8789-37c1bbe378f3", Sequence: 3,
		StartedAt: at, CompletedAt: at.Add(time.Millisecond), Limit: 50, Complete: true}
}

func nativeProfile(value pprofile.Profiles) pprofile.Profile {
	return value.ResourceProfiles().At(0).ScopeProfiles().At(0).Profiles().At(0)
}

func sampleAttribute(value pprofile.Profiles, sample pprofile.Sample, key string, item any) {
	GinkgoHelper()
	dictionary := value.Dictionary()
	dictionary.StringTable().Append(key)
	attribute := dictionary.AttributeTable().AppendEmpty()
	attribute.SetKeyStrindex(int32(dictionary.StringTable().Len() - 1))
	Expect(attribute.Value().FromRaw(item)).To(Succeed())
	sample.AttributeIndices().Append(int32(dictionary.AttributeTable().Len() - 1))
}

func profileAttribute(value pprofile.Profiles, key string) pcommon.Value {
	GinkgoHelper()
	for _, index := range nativeProfile(value).AttributeIndices().All() {
		attribute := value.Dictionary().AttributeTable().At(int(index))
		if value.Dictionary().StringTable().At(int(attribute.KeyStrindex())) == key {
			return attribute.Value()
		}
	}
	Fail("missing profile attribute " + key)
	return pcommon.Value{}
}

func nativeControl() pprofile.Profiles {
	GinkgoHelper()
	data, err := profiles.Encode([]profiles.Snapshot{controlSnapshot()})
	Expect(err).NotTo(HaveOccurred())
	value, err := (&pprofile.ProtoUnmarshaler{}).UnmarshalProfiles(data)
	Expect(err).NotTo(HaveOccurred())
	profileAttribute(value, "telltale.schema.version").SetInt(2)
	profile := nativeProfile(value)
	profile.Samples().RemoveIf(func(pprofile.Sample) bool { return true })
	sample := profile.Samples().AppendEmpty()
	sampleAttribute(value, sample, controlAttribute, controlKind)
	sample.Values().Append(0)
	sample.TimestampsUnixNano().Append(uint64(controlSnapshot().CompletedAt.UnixNano()))
	return value
}

var _ = Describe("snapshot control convention", func() {
	DescribeTable("encodes a zero-operation snapshot as one control sample and retains zero projected rows", func(complete bool) {
		snapshot := controlSnapshot()
		snapshot.Complete = complete
		data, err := profiles.Encode([]profiles.Snapshot{snapshot})
		Expect(err).NotTo(HaveOccurred())
		value, err := (&pprofile.ProtoUnmarshaler{}).UnmarshalProfiles(data)
		Expect(err).NotTo(HaveOccurred())
		Expect(profileAttribute(value, "telltale.schema.version").Int()).To(Equal(int64(2)))
		Expect(nativeProfile(value).Samples().Len()).To(Equal(1))
		Expect(nativeProfile(value).Samples().At(0).Values().AsRaw()).To(Equal([]int64{0}))
		decoded, err := profiles.Decode(data, profiles.Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(decoded).To(HaveLen(1))
		Expect(decoded[0].Rows).To(BeEmpty())
		Expect(decoded[0].Observed).To(BeZero())
		Expect(decoded[0].Complete).To(Equal(complete))
		Expect(decoded[0].Native).NotTo(BeEmpty())
		retained, err := (&pprofile.ProtoUnmarshaler{}).UnmarshalProfiles(decoded[0].Native)
		Expect(err).NotTo(HaveOccurred())
		Expect(retained.SampleCount()).To(Equal(1))
	}, Entry("complete clear", true), Entry("incomplete observation", false))

	It("decodes an independently assembled exact control record", func() {
		data, err := (&pprofile.ProtoMarshaler{}).MarshalProfiles(nativeControl())
		Expect(err).NotTo(HaveOccurred())
		decoded, err := profiles.Decode(data, profiles.Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(decoded).To(HaveLen(1))
		Expect(decoded[0].Rows).To(BeEmpty())
		Expect(decoded[0].Digest).NotTo(BeEmpty())
	})

	It("rejects a positive observed count with no retained operations instead of emitting empty telemetry", func() {
		snapshot := controlSnapshot()
		snapshot.Observed = 1
		_, err := profiles.Encode([]profiles.Snapshot{snapshot})
		Expect(err).To(MatchError(ContainSubstring("retained operation")))
	})

	It("rejects operation attributes that claim the reserved control marker", func() {
		snapshot := controlSnapshot()
		snapshot.Observed = 1
		snapshot.Rows = []profiles.Row{{OperationID: "operation", SampledAt: snapshot.CompletedAt, Attributes: map[string]any{controlAttribute: controlKind}}}
		_, err := profiles.Encode([]profiles.Snapshot{snapshot})
		Expect(err).To(MatchError(ContainSubstring("reserved")))
	})

	DescribeTable("rejects malformed control samples before projecting an operation", func(change func(pprofile.Profiles)) {
		value := nativeControl()
		change(value)
		data, err := (&pprofile.ProtoMarshaler{}).MarshalProfiles(value)
		Expect(err).NotTo(HaveOccurred())
		_, err = profiles.Decode(data, profiles.Options{})
		Expect(err).To(HaveOccurred())
	}, Entry("unknown marker kind", func(value pprofile.Profiles) {
		dictionary := value.Dictionary()
		dictionary.AttributeTable().At(int(nativeProfile(value).Samples().At(0).AttributeIndices().At(0))).Value().SetStr("unknown")
	}), Entry("nonstrings marker", func(value pprofile.Profiles) {
		dictionary := value.Dictionary()
		dictionary.AttributeTable().At(int(nativeProfile(value).Samples().At(0).AttributeIndices().At(0))).Value().SetBool(true)
	}), Entry("missing marker", func(value pprofile.Profiles) {
		nativeProfile(value).Samples().At(0).AttributeIndices().FromRaw(nil)
	}), Entry("no samples", func(value pprofile.Profiles) {
		nativeProfile(value).Samples().RemoveIf(func(pprofile.Sample) bool { return true })
	}), Entry("missing value", func(value pprofile.Profiles) {
		nativeProfile(value).Samples().At(0).Values().FromRaw(nil)
	}), Entry("multiple values", func(value pprofile.Profiles) {
		nativeProfile(value).Samples().At(0).Values().Append(0)
	}), Entry("nonzero value", func(value pprofile.Profiles) {
		nativeProfile(value).Samples().At(0).Values().SetAt(0, 1)
	}), Entry("missing timestamp", func(value pprofile.Profiles) {
		nativeProfile(value).Samples().At(0).TimestampsUnixNano().FromRaw(nil)
	}), Entry("multiple timestamps", func(value pprofile.Profiles) {
		nativeProfile(value).Samples().At(0).TimestampsUnixNano().Append(uint64(controlSnapshot().CompletedAt.UnixNano()))
	}), Entry("wrong observation timestamp", func(value pprofile.Profiles) {
		nativeProfile(value).Samples().At(0).TimestampsUnixNano().SetAt(0, uint64(controlSnapshot().StartedAt.UnixNano()))
	}), Entry("out of range timestamp", func(value pprofile.Profiles) {
		nativeProfile(value).Samples().At(0).TimestampsUnixNano().SetAt(0, math.MaxUint64)
	}), Entry("stack reference", func(value pprofile.Profiles) {
		nativeProfile(value).Samples().At(0).SetStackIndex(1)
	}), Entry("trace link", func(value pprofile.Profiles) {
		nativeProfile(value).Samples().At(0).SetLinkIndex(1)
	}), Entry("operation ID", func(value pprofile.Profiles) {
		sampleAttribute(value, nativeProfile(value).Samples().At(0), "telltale.operation.id", "invented")
	}), Entry("HTTP operation metadata", func(value pprofile.Profiles) {
		sampleAttribute(value, nativeProfile(value).Samples().At(0), "method", "GET")
	}), Entry("SQL operation metadata", func(value pprofile.Profiles) {
		sampleAttribute(value, nativeProfile(value).Samples().At(0), "sql", "SELECT id FROM orders")
	}), Entry("unrecognized metadata", func(value pprofile.Profiles) {
		sampleAttribute(value, nativeProfile(value).Samples().At(0), "extra", "value")
	}), Entry("mixed with operation", func(value pprofile.Profiles) {
		profile := nativeProfile(value)
		operation := profile.Samples().AppendEmpty()
		sampleAttribute(value, operation, "telltale.operation.id", "actual-operation")
		operation.Values().Append(1)
		operation.TimestampsUnixNano().Append(uint64(controlSnapshot().CompletedAt.UnixNano()))
	}), Entry("positive observed count", func(value pprofile.Profiles) {
		profileAttribute(value, "telltale.snapshot.observed").SetInt(1)
	}), Entry("old schema", func(value pprofile.Profiles) {
		profileAttribute(value, "telltale.schema.version").SetInt(1)
	}))
})
