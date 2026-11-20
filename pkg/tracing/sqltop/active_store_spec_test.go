package sqltop

import (
	"context"
	"fmt"

	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/commons-db/recordstore/recordresults"
	"github.com/flanksource/commons-db/recordstore/recordresults/recordresultstest"
	"github.com/flanksource/commons-db/recordstore/sampleframe"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/tracingtest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("SQL top active storage", func() {
	It("stores database details and the common top projection with an empty follow-up frame", func() {
		env := tracingtest.NewEnv(map[string]tracing.TracePlugin{"sql_top": Kind()})
		store, err := newActiveStore(env.Results.Backend, "sql-active-test")
		Expect(err).NotTo(HaveOccurred())
		native := []Sample{{SampledAt: serverTime, SessionID: 7, RequestID: 2, SQL: "SELECT id FROM orders", ElapsedMs: 50}}
		frame := sampleframe.Frame{Source: "connection://orders", StartedAt: serverTime, CompletedAt: serverTime, Observed: 1}
		Expect(store.append(context.Background(), frame, projectDatabase(frame.Source, native), native)).To(Succeed())
		frame.Observed = 0
		Expect(store.append(context.Background(), frame, nil, nil)).To(Succeed())
		Expect(env.Read("sql_top", "sql-active-test", query.PageRequest{Limit: 20}, nil)).To(HaveLen(1))
		Expect(env.Read("sql_top_active", "sql-active-test-active", query.PageRequest{Limit: 20}, nil)).To(HaveLen(1))
		frames := env.Read("sql_top_frames", "sql-active-test-frames", query.PageRequest{Limit: 20}, nil)
		Expect(frames).To(HaveLen(2))
		Expect(frames[0]).To(HaveKeyWithValue("result", "active"))
		Expect(frames[1]).To(HaveKeyWithValue("retained", BeNumerically("==", 0)))
		Expect(frames[1]).To(HaveKeyWithValue("afterSeq", BeNumerically("==", 1)))
		Expect(frames[1]).To(HaveKeyWithValue("toSeq", BeNumerically("==", 1)))
	})

	It("deduplicates retried profile frames and rejects conflicting sequences", func() {
		env := tracingtest.NewEnv(map[string]tracing.TracePlugin{"sql_top": Kind()})
		store, err := newActiveStore(env.Results.Backend, "sql-profile-retry")
		Expect(err).NotTo(HaveOccurred())
		frame := profileStoreFrame(1, "java")
		Expect(store.append(context.Background(), frame, nil, nil)).To(Succeed())
		Expect(store.append(context.Background(), frame, nil, nil)).To(Succeed())
		Expect(env.Read("sql_top_frames", "sql-profile-retry-frames", query.PageRequest{Limit: 20}, nil)).To(HaveLen(1))
		frame.Summary["digest"] = fmt.Sprintf("%064x", 2)
		Expect(store.append(context.Background(), frame, nil, nil)).To(MatchError(ContainSubstring("conflicting")))
	})

	It("preserves interleaved producer ticks and completeness while retaining reordered history and old retries", func() {
		env := tracingtest.NewEnv(map[string]tracing.TracePlugin{"sql_top": Kind()})
		store, err := newActiveStore(env.Results.Backend, "sql-profile-order")
		Expect(err).NotTo(HaveOccurred())
		for _, frame := range []sampleframe.Frame{profileStoreFrame(7, "java-a"), profileStoreFrame(2, "java-b"), profileStoreFrame(9, "java-a"), profileStoreFrame(8, "java-a")} {
			Expect(store.append(context.Background(), frame, nil, nil)).To(Succeed())
		}
		restarted, err := newActiveStore(env.Results.Backend, "sql-profile-order")
		Expect(err).NotTo(HaveOccurred())
		retry := profileStoreFrame(7, "java-a")
		retry.Summary["receivedAt"] = "retry"
		Expect(restarted.append(context.Background(), retry, nil, nil)).To(Succeed())
		frames := env.Read("sql_top_frames", "sql-profile-order-frames", query.PageRequest{Limit: 20}, nil)
		Expect(frames).To(HaveLen(4))
		for index, tick := range []int64{7, 2, 9, 8} {
			Expect(frames[index]).To(HaveKeyWithValue("tick", BeNumerically("==", tick)))
			Expect(frames[index]["summary"]).To(HaveKeyWithValue("complete", true))
		}
		Expect(frames[0]["summary"]).To(HaveKeyWithValue("receivedAt", "first"))
	})

	It("rejects a profile UUID reused across producer sources and sequences", func() {
		env := tracingtest.NewEnv(map[string]tracing.TracePlugin{"sql_top": Kind()})
		store, err := newActiveStore(env.Results.Backend, "sql-profile-uuid")
		Expect(err).NotTo(HaveOccurred())
		first := profileStoreFrame(3, "java-a")
		Expect(store.append(context.Background(), first, nil, nil)).To(Succeed())
		conflict := profileStoreFrame(4, "java-b")
		conflict.Summary["profileId"] = first.Summary["profileId"]
		Expect(store.append(context.Background(), conflict, nil, nil)).To(MatchError(ContainSubstring("conflicting")))
		Expect(env.Read("sql_top_frames", "sql-profile-uuid-frames", query.PageRequest{Limit: 20}, nil)).To(HaveLen(1))
	})

	It("deduplicates an old retry after closing and reopening the registered results store", func() {
		catalog := tracing.NewKinds()
		Expect(catalog.RegisterKind("sql_top", Kind())).To(Succeed())
		settings := recordresultstest.LocalSettings(recordstore.BackendSQLite)
		open := func() *recordresults.Results {
			results, err := recordresults.Open(recordresults.OpenOptions{Prefix: "profiles", ConnectionName: "profiles", Settings: settings, Register: catalog.RegisterResultTypes})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(results.Close)
			return results
		}
		first := open()
		store, err := newActiveStore(first.Backend, "sql-profile-reopen")
		Expect(err).NotTo(HaveOccurred())
		Expect(store.append(context.Background(), profileStoreFrame(3, "java"), nil, nil)).To(Succeed())
		Expect(store.append(context.Background(), profileStoreFrame(5, "java"), nil, nil)).To(Succeed())
		Expect(first.Close()).To(Succeed())
		second := open()
		store, err = newActiveStore(second.Backend, "sql-profile-reopen")
		Expect(err).NotTo(HaveOccurred())
		Expect(store.append(context.Background(), profileStoreFrame(3, "java"), nil, nil)).To(Succeed())
		var frames []recordstore.Row
		Expect(second.Backend.Scan(context.Background(), "sql-profile-reopen-frames", 0, func(_ int64, row recordstore.Row) error { frames = append(frames, row); return nil })).To(Succeed())
		Expect(frames).To(HaveLen(2))
		Expect(frames[0]).To(HaveKeyWithValue("tick", BeNumerically("==", 3)))
	})
})

func profileStoreFrame(sequence int64, source string) sampleframe.Frame {
	return sampleframe.Frame{Source: source + "/capture", Tick: sequence, StartedAt: serverTime, CompletedAt: serverTime,
		Summary: map[string]any{"profileId": fmt.Sprintf("00000000-0000-4000-8000-%012d", sequence), "captureId": "capture", "digest": fmt.Sprintf("%064x", sequence), "complete": true, "receivedAt": "first"}}
}
