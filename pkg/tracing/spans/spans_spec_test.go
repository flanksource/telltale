package spans_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	_ "github.com/flanksource/commons-db/query/providers/sqlite"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/spans"
	"github.com/flanksource/telltale/pkg/tracing/tracingtest"
	telltale "github.com/flanksource/telltale/sdk"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSpans(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Spans") }

var _ = Describe("spans", func() {
	It("stores a manually recorded span and its resource", func() {
		env := tracingtest.NewEnv(map[string]tracing.TracePlugin{spans.Kind: spans.NewKind(nil)})
		session := env.Start(spans.Kind, `{}`)
		tracingtest.Running(session)
		_, finish := telltale.StartSpan(context.Background(), "checkout", "order", "example")
		finish(nil)
		Eventually(func() string { data, _ := json.Marshal(env.Rows(session.Snapshot())); return string(data) }).WithTimeout(5 * time.Second).Should(And(ContainSubstring("checkout"), ContainSubstring("example"), ContainSubstring("process.pid")))
	})
})
