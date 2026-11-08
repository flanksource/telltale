package processcapture_test

import (
	"context"
	"fmt"
	_ "github.com/flanksource/commons-db/query/providers/sqlite"
	"os"
	"time"

	"github.com/flanksource/commons-db/recordstore"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/process"
	"github.com/flanksource/telltale/pkg/tracing/processcapture"
	"github.com/flanksource/telltale/pkg/tracing/tracingtest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("unified process runtime", func() {
	It("records startup once, numeric metrics separately, and seals all results", func() {
		env := tracingtest.NewEnv(map[string]tracing.TracePlugin{processcapture.Kind: processcapture.NewKind(process.NewSampler(), processcapture.KindOptions{})})
		session := env.Start(processcapture.Kind, fmt.Sprintf(`{"target":{"pid":%d},"interval":"250ms"}`, os.Getpid()))
		tracingtest.Running(session)
		Eventually(func() int64 {
			meta, _ := env.Results.Backend.Meta(context.Background(), session.Snapshot().Events.Stream+"-metrics")
			return meta.Total
		}, 10*time.Second).Should(BeNumerically(">=", 2))
		session.Stop("test")
		info := tracingtest.Ended(session)
		Expect(info.Error).To(BeEmpty())
		stream := info.Events.Stream
		for _, name := range []string{stream, stream + "-metrics", stream + "-facets"} {
			meta, err := env.Results.Backend.Meta(context.Background(), name)
			Expect(err).NotTo(HaveOccurred())
			Expect(meta.Sealed).To(BeTrue())
		}
		identities := env.Rows(info)
		Expect(identities).To(ContainElement(HaveKeyWithValue("pid", BeNumerically("==", os.Getpid()))))
		Expect(env.Results.Backend.Scan(context.Background(), stream+"-metrics", 0, func(_ int64, row recordstore.Row) error {
			Expect(row).To(HaveKey("rssBytes"))
			Expect(row).NotTo(HaveKey("command"))
			return nil
		})).To(Succeed())
	})
})
