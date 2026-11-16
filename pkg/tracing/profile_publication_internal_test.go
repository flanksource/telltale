package tracing

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/commons-db/recordstore/recordstoretest"
	"github.com/flanksource/commons-db/recordstore/sampleframe"
	"github.com/flanksource/commons-db/recordstore/sqlite"
	"github.com/flanksource/telltale/pkg/otlp/profilepublication"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type failingProfileBackend struct {
	*sqlite.Backend
	fail bool
}

func (b *failingProfileBackend) AppendBatch(ctx context.Context, batch recordstore.Batch) (recordstore.BatchResult, error) {
	if b.fail {
		return recordstore.BatchResult{}, errors.New("profile append unavailable")
	}
	return b.Backend.AppendBatch(ctx, batch)
}

func publicationFrame(sequence int64, source string) sampleframe.Frame {
	at := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	return sampleframe.Frame{Source: source + "/capture", Tick: sequence, StartedAt: at, CompletedAt: at, Observed: 1,
		Summary: map[string]any{"profileId": fmt.Sprintf("00000000-0000-4000-8000-%012d", sequence), "captureId": "capture", "digest": fmt.Sprintf("%064x", sequence), "complete": true, "receivedAt": "first-received"}}
}

var _ = Describe("direct profile publication", func() {
	var backend *failingProfileBackend
	var output *emitter[recordstore.Row]
	ctx := context.Background()
	newOutput := func() *emitter[recordstore.Row] {
		schema, err := recordstoretest.Schema(recordstoretest.Kind)
		Expect(err).NotTo(HaveOccurred())
		return &emitter[recordstore.Row]{source: newSource("profile-publication", 10, defaultBufferBytes, "", false), process: pipeline(nil), top: &TopSpec{}, frames: &sampleframe.Writer{Backend: backend, Schema: schema, Stream: "profile-publication"}}
	}
	frames := func() []recordstore.Row {
		var rows []recordstore.Row
		Expect(backend.Scan(ctx, "profile-publication-frames", 0, func(_ int64, row recordstore.Row) error { rows = append(rows, row); return nil })).To(Succeed())
		return rows
	}
	BeforeEach(func() {
		store, err := sqlite.Open(sqlite.Options{Path: filepath.Join(GinkgoT().TempDir(), "profiles.sqlite"), Schema: func(kind string) (recordstore.KindSchema, error) {
			if strings.HasSuffix(kind, "_frames") {
				return sampleframe.Schema(kind)
			}
			if kind == profilepublication.IdentityKind {
				return profilepublication.ResultType().KindSchema()
			}
			return recordstoretest.Schema(kind)
		}, TTL: time.Hour, SweepInterval: time.Hour})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(store.Close)
		backend = &failingProfileBackend{Backend: store}
		output = newOutput()
	})

	It("retains reordered history and deduplicates old retries after a writer restart without refreshing freshness", func() {
		for _, sequence := range []int64{7, 9, 8} {
			Expect(output.EmitFrame(ctx, publicationFrame(sequence, "java-a"), recordstoretest.SampleRows(int(sequence), int(sequence)))).To(Succeed())
		}
		retry := publicationFrame(7, "java-a")
		retry.Summary["receivedAt"] = "retried-later"
		Expect(newOutput().EmitFrame(ctx, retry, recordstoretest.SampleRows(7, 7))).To(Succeed())
		stored := frames()
		Expect(stored).To(HaveLen(3))
		Expect(stored[0]).To(HaveKeyWithValue("id", publicationFrame(7, "java-a").Summary["profileId"]))
		Expect(stored[0]["summary"]).To(HaveKeyWithValue("receivedAt", "first-received"))
		for index, sequence := range []int64{7, 9, 8} {
			Expect(stored[index]).To(HaveKeyWithValue("tick", BeNumerically("==", sequence)))
			Expect(stored[index]["summary"]).To(HaveKeyWithValue("complete", true))
		}
		meta, err := backend.Meta(ctx, "profile-publication")
		Expect(err).NotTo(HaveOccurred())
		Expect(meta.Total).To(Equal(int64(3)))
	})

	It("rejects UUID reuse and source-sequence conflicts before publishing rows or frames", func() {
		first := publicationFrame(3, "java-a")
		Expect(output.EmitFrame(ctx, first, recordstoretest.SampleRows(3, 3))).To(Succeed())
		uuidConflict := publicationFrame(4, "java-b")
		uuidConflict.Summary["profileId"] = first.Summary["profileId"]
		Expect(output.EmitFrame(ctx, uuidConflict, recordstoretest.SampleRows(4, 4))).To(MatchError(ContainSubstring("conflicting")))
		sequenceConflict := publicationFrame(3, "java-a")
		sequenceConflict.Summary["profileId"] = publicationFrame(5, "java-a").Summary["profileId"]
		sequenceConflict.Summary["digest"] = fmt.Sprintf("%064x", 5)
		Expect(output.EmitFrame(ctx, sequenceConflict, recordstoretest.SampleRows(5, 5))).To(MatchError(ContainSubstring("conflicting")))
		Expect(frames()).To(HaveLen(1))
		meta, err := backend.Meta(ctx, "profile-publication")
		Expect(err).NotTo(HaveOccurred())
		Expect(meta.Total).To(Equal(int64(1)))
	})

	It("admits a retry after failed atomic publication", func() {
		backend.fail = true
		Expect(output.EmitFrame(ctx, publicationFrame(6, "java-a"), recordstoretest.SampleRows(6, 6))).To(MatchError(ContainSubstring("profile append unavailable")))
		backend.fail = false
		Expect(output.EmitFrame(ctx, publicationFrame(6, "java-a"), recordstoretest.SampleRows(6, 6))).To(Succeed())
		Expect(frames()).To(HaveLen(1))
	})
})
