package otelreceiver

import (
	"context"
	"time"

	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/otlp/profiles"
	"github.com/flanksource/telltale/pkg/traces/read"
	"github.com/flanksource/telltale/pkg/tracing"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.opentelemetry.io/collector/pdata/pprofile"
)

func decodedTop(snapshot profiles.Snapshot) profiles.Snapshot {
	GinkgoHelper()
	data, err := profiles.Encode([]profiles.Snapshot{snapshot})
	Expect(err).NotTo(HaveOccurred())
	decoded, err := profiles.Decode(data, profiles.Options{})
	Expect(err).NotTo(HaveOccurred())
	return decoded[0]
}

var _ = Describe("durable Profiles snapshots", func() {
	It("replays a JMS-only receiver without advertising a nonexistent spans stream", func() {
		_, store := testReceiver(DefaultOptions())
		snapshot := topSnapshot(1)
		snapshot.Kind = "jms_top"
		snapshot.Rows[0].Attributes = map[string]any{"destination": "orders", "handler": "OrderListener.accept", "state": "idle", "activeCount": 0}
		outcome, err := store.PersistProfiles(context.Background(), []profiles.Snapshot{decodedTop(snapshot)})
		Expect(err).NotTo(HaveOccurred())
		Expect(outcome.Accepted).To(Equal(int64(1)))
		selected, err := read.ResolveRuns(read.Scope{DataDir: store.options.DataDir, Runs: []string{SourceID}})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(selected.Close)
		page, err := read.FeedPage(context.Background(), selected.Refs, read.FeedQuery{})
		Expect(err).NotTo(HaveOccurred())
		Expect(page.Rows).To(ContainElement(And(HaveField("Kind", "jms_top"), HaveField("Result", ""))))
		Expect(selected.Refs).NotTo(ContainElement(HaveField("Kind", "spans")))
		latest, err := read.Top(context.Background(), read.Ref{Runtime: store.Runtime(), TraceRef: tracing.TraceRef{Kind: "jms_top", Stream: profileStream("jms_top")}}, read.TopQuery{})
		Expect(err).NotTo(HaveOccurred())
		Expect(latest.Rows).To(ConsistOf(And(HaveKeyWithValue("handler", "OrderListener.accept"), HaveKeyWithValue("state", "idle"))))
		Expect(latest.Display.RowDetail).To(ConsistOf(Not(BeNil())))
		Expect(latest.Display.Rows[0]["activeCount"].String()).To(Equal("0"))
	})
	It("rejects unsealed snapshots without leaving partial rows or frames", func() {
		_, store := testReceiver(DefaultOptions())
		_, err := store.PersistProfiles(context.Background(), []profiles.Snapshot{topSnapshot(1)})
		Expect(err).To(MatchError(ContainSubstring("native")))
		stored, err := store.ReadProfiles(context.Background(), ProfileReadOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(stored).To(BeEmpty())
	})
	It("recovers committed identities after restart and rejects profile or sequence conflicts", func() {
		ctx := context.Background()
		options := StoreOptions{DataDir: GinkgoT().TempDir(), Retention: time.Hour, Writable: true}
		store, err := OpenStore(options)
		Expect(err).NotTo(HaveOccurred())
		original := decodedTop(topSnapshot(1))
		outcome, err := store.PersistProfiles(ctx, []profiles.Snapshot{original})
		Expect(err).NotTo(HaveOccurred())
		Expect(outcome).To(Equal(Outcome{Accepted: 1}))
		Expect(store.Close()).To(Succeed())
		store, err = OpenStore(options)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(store.Close)
		outcome, err = store.PersistProfiles(ctx, []profiles.Snapshot{original})
		Expect(err).NotTo(HaveOccurred())
		Expect(outcome).To(Equal(Outcome{Duplicates: 1}))
		native, err := (&pprofile.ProtoUnmarshaler{}).UnmarshalProfiles(original.Native)
		Expect(err).NotTo(HaveOccurred())
		native.ResourceProfiles().At(0).ScopeProfiles().At(0).Profiles().At(0).SetPeriod(77)
		wire, err := (&pprofile.ProtoMarshaler{}).MarshalProfiles(native)
		Expect(err).NotTo(HaveOccurred())
		metadataChange, err := profiles.Decode(wire, profiles.Options{})
		Expect(err).NotTo(HaveOccurred())
		outcome, err = store.PersistProfiles(ctx, metadataChange)
		Expect(err).NotTo(HaveOccurred())
		Expect(outcome.Rejected).To(Equal(int64(1)))
		changed := topSnapshot(1)
		changed.Rows[0].ElapsedNS++
		outcome, err = store.PersistProfiles(ctx, []profiles.Snapshot{decodedTop(changed)})
		Expect(err).NotTo(HaveOccurred())
		Expect(outcome.Rejected).To(Equal(int64(1)))
		changed.ProfileID = topSnapshot(2).ProfileID
		outcome, err = store.PersistProfiles(ctx, []profiles.Snapshot{decodedTop(changed)})
		Expect(err).NotTo(HaveOccurred())
		Expect(outcome.Rejected).To(Equal(int64(1)))
		stored, err := store.ReadProfiles(ctx, ProfileReadOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(stored).To(ConsistOf(original))
		identities := 0
		Expect(store.results.Backend.Scan(ctx, profileIdentityStream, 0, func(_ int64, _ recordstore.Row) error {
			identities++
			return nil
		})).To(Succeed())
		Expect(identities).To(Equal(2))
	})
	DescribeTable("projects SQL active operations using the shared Top and standing receiver seams", func(api string) {
		_, store := testReceiver(DefaultOptions())
		snapshot := topSnapshot(1)
		snapshot.Kind = "sql_top"
		snapshot.Rows[0].Attributes["api"] = api
		snapshot.Rows[0].Attributes["sql"] = "select * from orders"
		outcome, err := store.PersistProfiles(context.Background(), []profiles.Snapshot{decodedTop(snapshot)})
		Expect(err).NotTo(HaveOccurred())
		Expect(outcome.Accepted).To(Equal(int64(1)))
		ref := read.Ref{Runtime: store.Runtime(), TraceRef: tracing.TraceRef{Kind: "sql_top_active", Stream: profileStream("sql_top")}}
		latest, err := read.Top(context.Background(), ref, read.TopQuery{})
		Expect(err).NotTo(HaveOccurred())
		Expect(latest.Rows).To(ConsistOf(And(HaveKeyWithValue("operationId", "request-a"), HaveKeyWithValue("api", api), HaveKeyWithValue("sql", "select * from orders"))))
		info, err := telltale.Get(telltale.GetOptions{DataDir: store.options.DataDir, RunID: SourceID})
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Captures).To(ContainElement(HaveField("Kind", "sql_top_active")))
	}, Entry("JDBC", "jdbc"), Entry("R2DBC", "r2dbc"))
	It("keeps an empty latest frame despite late older delivery and exposes retained history", func() {
		_, store := testReceiver(DefaultOptions())
		ctx := context.Background()
		empty := topSnapshot(3)
		empty.Rows, empty.Observed = nil, 0
		outcome, err := store.PersistProfiles(ctx, []profiles.Snapshot{decodedTop(empty), decodedTop(topSnapshot(1)), decodedTop(topSnapshot(2))})
		Expect(err).NotTo(HaveOccurred())
		Expect(outcome.Accepted).To(Equal(int64(3)))
		ref := read.Ref{Runtime: store.Runtime(), TraceRef: tracing.TraceRef{Kind: "http_top", Stream: profileStream("http_top")}}
		latest, err := read.Top(ctx, ref, read.TopQuery{})
		Expect(err).NotTo(HaveOccurred())
		Expect(latest.Rows).To(BeEmpty())
		Expect(latest.Frame.Tick).To(Equal(int64(3)))
		Expect(latest.Frame.Summary).To(HaveKeyWithValue("complete", true))
		history, err := read.Top(ctx, ref, read.TopQuery{Frame: topSnapshot(1).ProfileID})
		Expect(err).NotTo(HaveOccurred())
		Expect(history.Rows).To(ConsistOf(HaveKeyWithValue("requestId", "request-a")))
		info, err := telltale.Get(telltale.GetOptions{DataDir: store.options.DataDir, RunID: SourceID})
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Captures).To(ContainElement(HaveField("Kind", "http_top")))
	})
	It("expires identity metadata and native frames together by receive time", func() {
		options := StoreOptions{DataDir: GinkgoT().TempDir(), Retention: 150 * time.Millisecond, Writable: true}
		store, err := OpenStore(options)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(store.Close)
		original := decodedTop(topSnapshot(1))
		outcome, err := store.PersistProfiles(context.Background(), []profiles.Snapshot{original})
		Expect(err).NotTo(HaveOccurred())
		Expect(outcome.Accepted).To(Equal(int64(1)))
		expiry := time.Now().Add(2 * options.Retention)
		Eventually(func() bool { return time.Now().After(expiry) }).Should(BeTrue())
		outcome, err = store.PersistProfiles(context.Background(), []profiles.Snapshot{original})
		Expect(err).NotTo(HaveOccurred())
		Expect(outcome).To(Equal(Outcome{Accepted: 1}))
		stored, err := store.ReadProfiles(context.Background(), ProfileReadOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(stored).To(HaveLen(1))
	})
})
