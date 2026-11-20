package profiles

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/google/uuid"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pprofile"
)

func Decode(data []byte, options Options) ([]Snapshot, error) {
	if options.MaxBytes < 0 || options.MaxProfiles < 0 || options.MaxSamples < 0 {
		return nil, fmt.Errorf("profile limits must be nonnegative")
	}
	if options.MaxBytes == 0 {
		options.MaxBytes = 4 << 20
	}
	if options.MaxProfiles == 0 {
		options.MaxProfiles = 256
	}
	if options.MaxSamples == 0 {
		options.MaxSamples = 10000
	}
	if len(data) > options.MaxBytes {
		return nil, fmt.Errorf("%w: profile byte limit exceeded", ErrLimit)
	}
	value, err := (&pprofile.ProtoUnmarshaler{}).UnmarshalProfiles(data)
	if err != nil {
		return nil, fmt.Errorf("decode OTLP profiles: %w", err)
	}
	if value.ProfileCount() > options.MaxProfiles || value.SampleCount() > options.MaxSamples {
		return nil, fmt.Errorf("%w: profile or sample limit exceeded", ErrLimit)
	}
	if value.Dictionary().StringTable().Len() == 0 || value.Dictionary().StringTable().At(0) != "" {
		return nil, fmt.Errorf("profile dictionary requires a zero string entry")
	}
	d := value.Dictionary()
	if d.MappingTable().Len() == 0 || d.FunctionTable().Len() == 0 || d.LocationTable().Len() == 0 || d.LinkTable().Len() == 0 || d.AttributeTable().Len() == 0 || d.StackTable().Len() == 0 {
		return nil, fmt.Errorf("profile dictionary requires reserved zero entries")
	}
	return decodeProfiles(value, options)
}

func decodeProfiles(value pprofile.Profiles, options Options) ([]Snapshot, error) {
	result := []Snapshot{}
	expanded := 0
	for _, resource := range value.ResourceProfiles().All() {
		for _, scope := range resource.ScopeProfiles().All() {
			if scope.Scope().Name() != Scope {
				return nil, fmt.Errorf("unsupported profile scope %q", scope.Scope().Name())
			}
			for _, profile := range scope.Profiles().All() {
				snapshot, err := decodeSnapshot(value.Dictionary(), profile)
				if err != nil {
					return nil, err
				}
				snapshot.Resource = resource.Resource().Attributes().AsRaw()
				for key, item := range snapshot.Resource {
					snapshot.Resource[key] = tracing.MaskAttribute(key, item)
				}
				if err = validateIdentity(snapshot, options); err != nil {
					return nil, err
				}
				snapshot.Native, err = nativeProfile(value.Dictionary(), resource, scope, profile)
				if err != nil {
					return nil, err
				}
				expanded += len(snapshot.Native)
				if expanded > options.MaxBytes*8 {
					return nil, fmt.Errorf("%w: expanded profile byte limit exceeded", ErrLimit)
				}
				if err = seal(&snapshot); err != nil {
					return nil, err
				}
				result = append(result, snapshot)
			}
		}
	}
	return result, nil
}

func validateIdentity(s Snapshot, o Options) error {
	if o.ExpectedKind != "" && s.Kind != o.ExpectedKind {
		return fmt.Errorf("profile kind %q does not match authenticated capture", s.Kind)
	}
	if o.ExpectedCaptureID != "" && s.CaptureID != o.ExpectedCaptureID {
		return fmt.Errorf("profile capture %q does not match authenticated session", s.CaptureID)
	}
	if o.ExpectedSource != "" && s.Source != o.ExpectedSource {
		return fmt.Errorf("profile source %q does not match authenticated agent", s.Source)
	}
	return validate(s)
}

func decodeSnapshot(d pprofile.ProfilesDictionary, p pprofile.Profile) (Snapshot, error) {
	if p.OriginalPayload().Len() > 0 {
		return Snapshot{}, fmt.Errorf("active-operation profiles must not contain opaque payloads")
	}
	attributes, err := readAttributes(d, p.AttributeIndices())
	if err != nil {
		return Snapshot{}, err
	}
	if attributes["telltale.schema.version"] != conventionVersion {
		return Snapshot{}, fmt.Errorf("unsupported Telltale profile convention version")
	}
	s := Snapshot{Kind: text(attributes, "telltale.kind"), CaptureID: text(attributes, "telltale.capture.id"), Source: text(attributes, "telltale.source.instance"),
		ProfileID: uuid.UUID(p.ProfileID()).String(), Checkpoint: text(attributes, "telltale.checkpoint.id"), StartedAt: p.Time().AsTime(), Rows: []Row{}}
	if p.Time() == 0 || uint64(p.Time()) > math.MaxInt64 || p.DurationNano() > math.MaxInt64-uint64(p.Time()) {
		return s, fmt.Errorf("invalid profile time bounds")
	}
	s.CompletedAt = s.StartedAt.Add(time.Duration(p.DurationNano()))
	if poll, found := attributes["telltale.snapshot.poll_interval_ms"]; found {
		var valid bool
		s.PollIntervalMS, valid = poll.(int64)
		if !valid || s.PollIntervalMS <= 0 {
			return s, fmt.Errorf("profile poll interval must be positive")
		}
	}
	var ok bool
	if s.Sequence, ok = attributes["telltale.snapshot.sequence"].(int64); !ok {
		return s, fmt.Errorf("profile sequence must be an integer")
	}
	observed, ok := attributes["telltale.snapshot.observed"].(int64)
	if !ok || observed < 0 || observed > math.MaxInt {
		return s, fmt.Errorf("profile observed count must be nonnegative")
	}
	s.Observed = int(observed)
	limit, ok := attributes["telltale.snapshot.limit"].(int64)
	if !ok || limit < 0 || limit > math.MaxInt {
		return s, fmt.Errorf("profile limit must be nonnegative")
	}
	s.Limit = int(limit)
	dropped, ok := attributes["telltale.snapshot.dropped"].(int64)
	if !ok || dropped < 0 {
		return s, fmt.Errorf("profile dropped count must be nonnegative")
	}
	s.Dropped = uint64(dropped)
	if s.Complete, ok = attributes["telltale.snapshot.complete"].(bool); !ok {
		return s, fmt.Errorf("profile completeness is required")
	}
	metric, err := readString(d, p.SampleType().TypeStrindex())
	if err != nil {
		return s, err
	}
	unit, err := readString(d, p.SampleType().UnitStrindex())
	if err != nil {
		return s, err
	}
	if metric != sampleTypes[s.Kind] || unit != "ns" {
		return s, fmt.Errorf("profile sample type must describe elapsed nanoseconds")
	}
	s.Rows, err = decodeSamples(d, p, s)
	return s, err
}

func decodeRow(d pprofile.ProfilesDictionary, sample pprofile.Sample, attributes map[string]any) (Row, error) {
	if sample.Values().Len() != 1 || sample.TimestampsUnixNano().Len() != 1 {
		return Row{}, fmt.Errorf("top samples require exactly one value and timestamp")
	}
	if sample.TimestampsUnixNano().At(0) > math.MaxInt64 {
		return Row{}, fmt.Errorf("sample timestamp exceeds signed nanoseconds")
	}
	row := Row{OperationID: text(attributes, "telltale.operation.id"), ElapsedNS: sample.Values().At(0), SampledAt: time.Unix(0, int64(sample.TimestampsUnixNano().At(0))).UTC(), Attributes: attributes}
	delete(row.Attributes, "telltale.operation.id")
	if index := sample.LinkIndex(); index != 0 {
		if index < 0 || int(index) >= d.LinkTable().Len() {
			return row, fmt.Errorf("profile link index is out of range")
		}
		link := d.LinkTable().At(int(index))
		row.TraceID = link.TraceID().String()
		row.SpanID = link.SpanID().String()
		if link.TraceID().IsEmpty() || link.SpanID().IsEmpty() {
			return row, fmt.Errorf("profile link requires trace and span IDs")
		}
	}
	stack, err := readStack(d, sample.StackIndex())
	row.Stack = stack
	return row, err
}

func readString(d pprofile.ProfilesDictionary, index int32) (string, error) {
	if index < 0 || int(index) >= d.StringTable().Len() {
		return "", fmt.Errorf("profile string index is out of range")
	}
	return d.StringTable().At(int(index)), nil
}

func readAttributes(d pprofile.ProfilesDictionary, indices pcommon.Int32Slice) (map[string]any, error) {
	attributes := map[string]any{}
	for _, index := range indices.All() {
		if index <= 0 || int(index) >= d.AttributeTable().Len() {
			return nil, fmt.Errorf("profile attribute index is out of range")
		}
		attribute := d.AttributeTable().At(int(index))
		key, err := readString(d, attribute.KeyStrindex())
		if err != nil {
			return nil, err
		}
		if key == "" {
			return nil, fmt.Errorf("profile attribute key is empty")
		}
		if _, found := attributes[key]; found {
			return nil, fmt.Errorf("duplicate profile attribute %q", key)
		}
		attributes[key] = tracing.MaskAttribute(key, attribute.Value().AsRaw())
	}
	return attributes, nil
}

func readStack(d pprofile.ProfilesDictionary, index int32) ([]string, error) {
	if index == 0 {
		return nil, nil
	}
	if index < 0 || int(index) >= d.StackTable().Len() {
		return nil, fmt.Errorf("profile stack index is out of range")
	}
	frames := []string{}
	for _, locationIndex := range d.StackTable().At(int(index)).LocationIndices().All() {
		if locationIndex <= 0 || int(locationIndex) >= d.LocationTable().Len() {
			return nil, fmt.Errorf("profile location index is out of range")
		}
		for _, line := range d.LocationTable().At(int(locationIndex)).Lines().All() {
			functionIndex := line.FunctionIndex()
			if functionIndex <= 0 || int(functionIndex) >= d.FunctionTable().Len() {
				return nil, fmt.Errorf("profile function index is out of range")
			}
			name, err := readString(d, d.FunctionTable().At(int(functionIndex)).NameStrindex())
			if err != nil {
				return nil, err
			}
			frames = append(frames, name)
		}
	}
	return frames, nil
}

func seal(s *Snapshot) error {
	slices.SortFunc(s.Rows, func(a, b Row) int {
		if a.OperationID < b.OperationID {
			return -1
		}
		if a.OperationID > b.OperationID {
			return 1
		}
		return 0
	})
	canonical := *s
	canonical.Native = nil
	canonical.Digest = ""
	native, err := canonicalNative(s.Native)
	if err != nil {
		return fmt.Errorf("canonical native profile: %w", err)
	}
	data, err := json.Marshal(struct {
		Snapshot Snapshot
		Native   json.RawMessage
	}{canonical, native})
	if err != nil {
		return fmt.Errorf("canonical profile: %w", err)
	}
	digest := sha256.Sum256(data)
	s.Digest = hex.EncodeToString(digest[:])
	return nil
}

func nativeProfile(dictionary pprofile.ProfilesDictionary, resource pprofile.ResourceProfiles, scope pprofile.ScopeProfiles, profile pprofile.Profile) ([]byte, error) {
	value := pprofile.NewProfiles()
	dictionary.CopyTo(value.Dictionary())
	copyResource := value.ResourceProfiles().AppendEmpty()
	resource.Resource().CopyTo(copyResource.Resource())
	copyResource.SetSchemaUrl(resource.SchemaUrl())
	for key, item := range copyResource.Resource().Attributes().All() {
		if err := item.FromRaw(tracing.MaskAttribute(key, item.AsRaw())); err != nil {
			return nil, err
		}
	}
	copyScope := copyResource.ScopeProfiles().AppendEmpty()
	scope.Scope().CopyTo(copyScope.Scope())
	for key, item := range copyScope.Scope().Attributes().All() {
		if err := item.FromRaw(tracing.MaskAttribute(key, item.AsRaw())); err != nil {
			return nil, err
		}
	}
	copyScope.SetSchemaUrl(scope.SchemaUrl())
	profile.CopyTo(copyScope.Profiles().AppendEmpty())
	for _, attribute := range value.Dictionary().AttributeTable().All() {
		key, err := readString(value.Dictionary(), attribute.KeyStrindex())
		if err != nil {
			return nil, err
		}
		if err = attribute.Value().FromRaw(tracing.MaskAttribute(key, attribute.Value().AsRaw())); err != nil {
			return nil, err
		}
	}
	// Rebuild references after masking so unused dictionary strings cannot retain secrets.
	redacted := pprofile.NewProfiles()
	newDictionary(redacted.Dictionary())
	if err := value.MergeTo(redacted); err != nil {
		return nil, fmt.Errorf("rebuild redacted profile dictionary: %w", err)
	}
	return (&pprofile.ProtoMarshaler{}).MarshalProfiles(redacted)
}

func text(attributes map[string]any, key string) string {
	value, _ := attributes[key].(string)
	return value
}
