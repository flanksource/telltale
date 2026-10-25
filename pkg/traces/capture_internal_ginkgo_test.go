package traces

import (
	"context"
	"errors"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/telltale/pkg/tracing/process"
)

var _ = Describe("marking a capture whose session failed", func() {
	It("names the failure that ended the session", func() {
		host, err := Open(OpenOptions{WorkDir: GinkgoT().TempDir(), RunID: RunID(time.Now())})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(host.Close(context.Background())).To(Succeed()) })
		capture, err := host.startWith(process.SamplesKind, process.SampleParams{Params: process.Params{RootPID: os.Getpid(), Interval: time.Hour.String()}})
		Expect(err).ToNot(HaveOccurred())
		cause := errors.New("read disk I/O for process 5675: operation not permitted")
		_, _ = capture.managed.Abort(cause)

		_, err = capture.Mark(context.Background())

		Expect(errors.Is(err, query.ErrSessionEnded)).To(BeTrue(), "%v", err)
		Expect(err).To(MatchError(ContainSubstring(cause.Error())))
	})
})
