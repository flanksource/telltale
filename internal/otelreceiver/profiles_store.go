package otelreceiver

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/commons-db/recordstore/sampleframe"
	"github.com/flanksource/telltale/pkg/otlp/profilepublication"
	"github.com/flanksource/telltale/pkg/otlp/profiles"
	"github.com/flanksource/telltale/pkg/tracing/httptop"
	"github.com/flanksource/telltale/pkg/tracing/jmstop"
	"github.com/flanksource/telltale/pkg/tracing/sqltop"
)

const profileIdentityStream = profilepublication.IdentityStream

var errInvalidProfile = errors.New("invalid active operation profile")

func profileIdentityResult() recordresults.ResultType[profilepublication.Identity] {
	return profilepublication.ResultType()
}

func profileStream(kind string) string {
	if kind == jmstop.Kind {
		return "received-jms-top"
	}
	if kind == "sql_top" {
		return "received-sql-top-active"
	}
	return "received-http-top"
}

func profileStorageKind(kind string) string {
	if kind == jmstop.Kind {
		return jmstop.Kind
	}
	if kind == "sql_top" {
		return "sql_top_active"
	}
	return httptop.Kind
}

func (s *Store) PersistProfiles(ctx context.Context, snapshots []profiles.Snapshot) (Outcome, error) {
	s.lifecycle.RLock()
	defer s.lifecycle.RUnlock()
	if s.closed || !s.options.Writable {
		return Outcome{}, fmt.Errorf("OTLP store is closed or read-only")
	}
	s.write.Lock()
	defer s.write.Unlock()
	if err := s.trimProfiles(ctx); err != nil {
		return Outcome{}, err
	}
	outcome := Outcome{}
	for _, snapshot := range snapshots {
		if len(snapshot.Native) == 0 || len(snapshot.Digest) != sha256.Size*2 || (snapshot.Kind != "http_top" && snapshot.Kind != "sql_top" && snapshot.Kind != jmstop.Kind) {
			return Outcome{}, fmt.Errorf("%w: profile requires validated native payload, digest and supported kind", errInvalidProfile)
		}
		published, err := s.appendProfile(ctx, snapshot)
		if errors.Is(err, profilepublication.ErrConflict) {
			outcome.Rejected++
			outcome.Reason = err.Error()
			continue
		}
		if err != nil {
			return Outcome{}, err
		}
		if published {
			outcome.Accepted++
		} else {
			outcome.Duplicates++
		}
	}
	return outcome, nil
}

func (s *Store) trimProfiles(ctx context.Context) error {
	cutoff := time.Now().Add(-s.options.Retention)
	for _, stream := range []string{profileStream("http_top"), profileStream("http_top") + "-frames", profileStream("sql_top"), profileStream("sql_top") + "-frames", profileStream(jmstop.Kind), profileStream(jmstop.Kind) + "-frames", profileIdentityStream} {
		_, err := s.results.Backend.Trim(ctx, stream, cutoff)
		if err != nil && !errors.Is(err, recordstore.ErrNotFound) {
			return err
		}
	}
	return nil
}

func (s *Store) appendProfile(ctx context.Context, snapshot profiles.Snapshot) (bool, error) {
	rows, schema, err := profileRows(snapshot)
	if err != nil {
		return false, fmt.Errorf("%w: %v", errInvalidProfile, err)
	}
	stream := profileStream(snapshot.Kind)
	frames, err := sampleframe.Schema(profileStorageKind(snapshot.Kind) + "_frames")
	if err != nil {
		return false, err
	}
	return profilepublication.Append(ctx, s.results.Backend, profilepublication.Publication{Kind: snapshot.Kind, Stream: stream, FrameStream: stream + "-frames", Schema: schema, FrameSchema: frames, Frame: snapshot.Frame(), Rows: rows})
}

func profileRows(snapshot profiles.Snapshot) ([]recordstore.Row, recordstore.KindSchema, error) {
	if snapshot.Kind == jmstop.Kind {
		samples, err := jmstop.FromSnapshot(snapshot)
		if err != nil {
			return nil, recordstore.KindSchema{}, err
		}
		rows, err := profileSampleRows(samples)
		schema, schemaErr := jmstop.ResultType().KindSchema()
		return rows, schema, errors.Join(err, schemaErr)
	}
	if snapshot.Kind == "http_top" {
		samples, err := httptop.FromSnapshot(snapshot)
		if err != nil {
			return nil, recordstore.KindSchema{}, err
		}
		rows, err := profileSampleRows(samples)
		schema, schemaErr := httptop.ResultType().KindSchema()
		return rows, schema, errors.Join(err, schemaErr)
	}
	samples, err := sqltop.FromSnapshot(snapshot)
	if err != nil {
		return nil, recordstore.KindSchema{}, err
	}
	rows, err := profileSampleRows(samples)
	schema, schemaErr := sqltop.ActiveResult().KindSchema()
	return rows, schema, errors.Join(err, schemaErr)
}

func profileSampleRows[T any](samples []T) ([]recordstore.Row, error) {
	rows := make([]recordstore.Row, len(samples))
	for i, sample := range samples {
		row, err := recordstore.EncodeRow(sample)
		if err != nil {
			return nil, err
		}
		rows[i] = row
	}
	return rows, nil
}
