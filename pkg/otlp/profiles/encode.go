package profiles

import (
	"encoding/hex"
	"fmt"
	"math"
	"slices"

	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/google/uuid"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pprofile"
)

type dictionary struct {
	value   pprofile.ProfilesDictionary
	strings map[string]int32
}

func newDictionary(value pprofile.ProfilesDictionary) *dictionary {
	value.StringTable().Append("")
	value.MappingTable().AppendEmpty()
	value.LocationTable().AppendEmpty()
	value.FunctionTable().AppendEmpty()
	value.LinkTable().AppendEmpty()
	value.AttributeTable().AppendEmpty()
	value.StackTable().AppendEmpty()
	return &dictionary{value: value, strings: map[string]int32{"": 0}}
}

func (d *dictionary) text(value string) int32 {
	if index, found := d.strings[value]; found {
		return index
	}
	index := int32(d.value.StringTable().Len())
	d.value.StringTable().Append(value)
	d.strings[value] = index
	return index
}

func (d *dictionary) attributes(values map[string]any, indices pcommon.Int32Slice) error {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		attribute := d.value.AttributeTable().AppendEmpty()
		attribute.SetKeyStrindex(d.text(key))
		if err := attribute.Value().FromRaw(tracing.MaskAttribute(key, values[key])); err != nil {
			return fmt.Errorf("profile attribute %s: %w", key, err)
		}
		indices.Append(int32(d.value.AttributeTable().Len() - 1))
	}
	return nil
}

func metadata(s Snapshot) map[string]any {
	attributes := map[string]any{"telltale.schema.version": conventionVersion, "telltale.kind": s.Kind, "telltale.capture.id": s.CaptureID,
		"telltale.source.instance": s.Source, "telltale.snapshot.sequence": s.Sequence, "telltale.snapshot.observed": int64(s.Observed),
		"telltale.snapshot.limit": int64(s.Limit), "telltale.snapshot.dropped": int64(s.Dropped), "telltale.snapshot.complete": s.Complete,
		"telltale.checkpoint.id": s.Checkpoint}
	if s.PollIntervalMS > 0 {
		attributes["telltale.snapshot.poll_interval_ms"] = s.PollIntervalMS
	}
	return attributes
}

func validate(s Snapshot) error {
	if s.Kind != "http_top" && s.Kind != "sql_top" {
		return fmt.Errorf("unsupported top profile kind %q", s.Kind)
	}
	if s.CaptureID == "" || s.Source == "" || s.Sequence <= 0 {
		return fmt.Errorf("profile capture, source and positive sequence are required")
	}
	if id, err := uuid.Parse(s.ProfileID); err != nil || id == uuid.Nil {
		return fmt.Errorf("profile ID must be a nonzero UUID")
	}
	if s.StartedAt.IsZero() || s.CompletedAt.Before(s.StartedAt) || s.StartedAt.UnixNano() <= 0 {
		return fmt.Errorf("profile requires ordered producer timestamps")
	}
	if s.Observed < len(s.Rows) || s.Limit < 0 || (s.Limit > 0 && len(s.Rows) > s.Limit) || s.Dropped > math.MaxInt64 {
		return fmt.Errorf("profile observed/retained counts exceed snapshot limits")
	}
	if s.Observed > 0 && len(s.Rows) == 0 {
		return fmt.Errorf("a positive profile observed count requires a retained operation")
	}
	seen := map[string]bool{}
	for _, row := range s.Rows {
		if _, exists := row.Attributes[sampleKindAttribute]; exists {
			return fmt.Errorf("sample kind is reserved for snapshot control")
		}
		if row.OperationID == "" || seen[row.OperationID] || row.ElapsedNS < 0 || row.SampledAt.Before(s.StartedAt) || row.SampledAt.After(s.CompletedAt) {
			return fmt.Errorf("profile requires distinct operation IDs, nonnegative elapsed time and bounded sample timestamps")
		}
		seen[row.OperationID] = true
	}
	return nil
}

func Encode(snapshots []Snapshot) ([]byte, error) {
	value := pprofile.NewProfiles()
	dictionary := newDictionary(value.Dictionary())
	for _, snapshot := range snapshots {
		if err := validate(snapshot); err != nil {
			return nil, err
		}
		if err := encodeSnapshot(value, dictionary, snapshot); err != nil {
			return nil, err
		}
	}
	return (&pprofile.ProtoMarshaler{}).MarshalProfiles(value)
}

func encodeSnapshot(value pprofile.Profiles, d *dictionary, s Snapshot) error {
	resource := value.ResourceProfiles().AppendEmpty()
	masked := map[string]any{}
	for key, item := range s.Resource {
		masked[key] = tracing.MaskAttribute(key, item)
	}
	if err := resource.Resource().Attributes().FromRaw(masked); err != nil {
		return err
	}
	scope := resource.ScopeProfiles().AppendEmpty()
	scope.Scope().SetName(Scope)
	scope.Scope().SetVersion("1")
	profile := scope.Profiles().AppendEmpty()
	id, _ := uuid.Parse(s.ProfileID)
	profile.SetProfileID(pprofile.ProfileID(id))
	profile.SetTime(pcommon.NewTimestampFromTime(s.StartedAt))
	profile.SetDurationNano(uint64(s.CompletedAt.Sub(s.StartedAt)))
	profile.SampleType().SetTypeStrindex(d.text(sampleTypes[s.Kind]))
	profile.SampleType().SetUnitStrindex(d.text("ns"))
	if err := d.attributes(metadata(s), profile.AttributeIndices()); err != nil {
		return err
	}
	if len(s.Rows) == 0 {
		return encodeControl(d, profile.Samples().AppendEmpty(), s.CompletedAt)
	}
	for _, row := range s.Rows {
		if err := encodeRow(d, profile.Samples().AppendEmpty(), row); err != nil {
			return err
		}
	}
	return nil
}

func encodeRow(d *dictionary, sample pprofile.Sample, row Row) error {
	attributes := map[string]any{}
	for key, value := range row.Attributes {
		attributes[key] = value
	}
	attributes["telltale.operation.id"] = row.OperationID
	if err := d.attributes(attributes, sample.AttributeIndices()); err != nil {
		return err
	}
	sample.Values().Append(row.ElapsedNS)
	sample.TimestampsUnixNano().Append(uint64(row.SampledAt.UnixNano()))
	if row.TraceID != "" || row.SpanID != "" {
		trace, err := hex.DecodeString(row.TraceID)
		if err != nil || len(trace) != 16 {
			return fmt.Errorf("profile requires a 16-byte trace ID")
		}
		span, err := hex.DecodeString(row.SpanID)
		if err != nil || len(span) != 8 {
			return fmt.Errorf("profile requires an 8-byte span ID")
		}
		link := d.value.LinkTable().AppendEmpty()
		link.SetTraceID(pcommon.TraceID(trace))
		link.SetSpanID(pcommon.SpanID(span))
		sample.SetLinkIndex(int32(d.value.LinkTable().Len() - 1))
	}
	if len(row.Stack) > 0 {
		stack := d.value.StackTable().AppendEmpty()
		sample.SetStackIndex(int32(d.value.StackTable().Len() - 1))
		for _, name := range row.Stack {
			function := d.value.FunctionTable().AppendEmpty()
			function.SetNameStrindex(d.text(name))
			location := d.value.LocationTable().AppendEmpty()
			location.Lines().AppendEmpty().SetFunctionIndex(int32(d.value.FunctionTable().Len() - 1))
			stack.LocationIndices().Append(int32(d.value.LocationTable().Len() - 1))
		}
	}
	return nil
}
