package processcapture

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	clickyprocess "github.com/flanksource/clicky/process"
	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/commons-db/recordstore/metrics"
	"github.com/flanksource/commons-db/recordstore/sqlite"
	"github.com/flanksource/telltale/pkg/tracing/process"
)

func BenchmarkProcessCollection(b *testing.B) {
	b.Run("process_listing", func(b *testing.B) {
		for b.Loop() {
			if _, err := clickyprocess.Discover(context.Background(), clickyprocess.SnapshotOptions{}); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("descriptors", func(b *testing.B) {
		for b.Loop() {
			if _, err := clickyprocess.Descriptors(context.Background(), os.Getpid()); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkProcessObservationStorage(b *testing.B) {
	for _, format := range []string{"snapshot", "unified"} {
		b.Run(format, func(b *testing.B) {
			ctx := context.Background()
			path := filepath.Join(b.TempDir(), "records.sqlite")
			schemas, err := Schemas()
			if err != nil {
				b.Fatal(err)
			}
			for kind, plugin := range map[string]interface {
				Schema(string) (recordstore.KindSchema, error)
			}{process.TreeKind: process.NewTreeKind(process.NewSampler(), process.TreeKindOptions{}), process.SamplesKind: process.NewSamplesKind(process.NewSampler())} {
				schema, err := plugin.Schema(kind)
				if err != nil {
					b.Fatal(err)
				}
				schemas = append(schemas, schema)
			}
			backend, err := sqlite.Open(sqlite.Options{Path: path, SweepInterval: time.Hour, Schema: func(kind string) (recordstore.KindSchema, error) {
				for _, schema := range schemas {
					if schema.Kind == kind {
						return schema, nil
					}
				}
				return recordstore.KindSchema{}, fmt.Errorf("unknown benchmark kind %s", kind)
			}})
			if err != nil {
				b.Fatal(err)
			}
			defer func() {
				if err := backend.Close(); err != nil {
					b.Error(err)
				}
			}()
			store, err := NewStore(StoreOptions{Backend: backend, Stream: "capture"})
			if err != nil {
				b.Fatal(err)
			}
			const processes, files = 20, 100
			at := time.Unix(100, 0)
			descriptors := make([]clickyprocess.Descriptor, files)
			for i := range descriptors {
				descriptors[i] = clickyprocess.Descriptor{FD: strconv.Itoa(i), Type: "REG", Name: "/work/checkout/source/" + strconv.Itoa(i) + ".go"}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for tick := 1; tick <= b.N; tick++ {
				descriptors[0].Name = "/work/output/" + strconv.Itoa(tick) + ".log"
				o := Observation{At: at.Add(time.Duration(tick) * time.Second), Tick: int64(tick)}
				nodes, samples := []recordstore.Row{}, []recordstore.Row{}
				for pid := 1; pid <= processes; pid++ {
					key := strconv.Itoa(pid)
					identity := Identity{Key: key, PID: pid, Name: "worker", Command: "worker --mode compile --config /work/checkout/config.yaml", FirstObservedAt: at, StartedAt: at, Environment: map[string]string{"MODE": "compile", "PATH": "/usr/bin:/usr/local/bin"}}
					if format == "unified" {
						o.Processes = append(o.Processes, identity)
						o.Metrics = append(o.Metrics, metrics.Sample{Entity: key, Values: map[string]any{"rssBytes": 1024 * tick, "cpuPercent": tick % 100}})
						o.Facets = append(o.Facets, FacetObservation{Process: key, Complete: true, Descriptors: descriptors})
					} else {
						node, err := recordstore.EncodeRow(process.Node{Key: key, PID: pid, Name: identity.Name, Command: identity.Command, Environment: identity.Environment, StartedAt: at, LastSeenAt: o.At, RSSBytes: uint64(1024 * tick)})
						if err != nil {
							b.Fatal(err)
						}
						sample, err := recordstore.EncodeRow(process.Sample{Key: key, PID: pid, SampledAt: o.At, Tick: o.Tick, RSSBytes: uint64(1024 * tick), OpenFileList: descriptors})
						if err != nil {
							b.Fatal(err)
						}
						nodes, samples = append(nodes, node), append(samples, sample)
					}
				}
				if format == "unified" {
					if err := store.Apply(ctx, o); err != nil {
						b.Fatal(err)
					}
				} else {
					if _, err := backend.Append(ctx, "tree", process.TreeKind, nodes); err != nil {
						b.Fatal(err)
					}
					if _, err := backend.Append(ctx, "samples", process.SamplesKind, samples); err != nil {
						b.Fatal(err)
					}
				}
			}
			b.StopTimer()
			if err := backend.Close(); err != nil {
				b.Fatal(err)
			}
			info, err := os.Stat(backend.Path())
			if err != nil {
				b.Fatal(err)
			}
			b.ReportMetric(float64(info.Size())/float64(b.N), "bytes/tick")
		})
	}
}
