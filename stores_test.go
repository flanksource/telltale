package telltale_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/tracing"
	"github.com/flanksource/telltale/pkg/tracing/httpproxy"
	"github.com/flanksource/telltale/pkg/tracing/spans"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fixtureStore is a standing store that exists in a data dir holding its
// marker file, standing or, with a "running" marker, running.
const fixtureStore = "fixture-standing"

var errFixtureOpen = errors.New("fixture store opened")

func init() {
	telltale.MustRegisterStore(telltale.StandingStore{
		ID: fixtureStore, Title: "Fixture standing store",
		Info: func(dataDir string) (telltale.RunInfo, bool, error) {
			if _, err := os.Stat(filepath.Join(dataDir, fixtureStore)); errors.Is(err, os.ErrNotExist) {
				return telltale.RunInfo{}, false, nil
			}
			status := telltale.StatusStanding
			if _, err := os.Stat(filepath.Join(dataDir, fixtureStore+".running")); err == nil {
				status = telltale.StatusRunning
			}
			return telltale.RunInfo{Status: status, Dir: filepath.Join(dataDir, fixtureStore), Captures: []telltale.CaptureResult{
				{Kind: spans.Kind, Ref: tracing.TraceRef{Kind: spans.Kind, Stream: "received"}},
			}}, true, nil
		},
		Open: func(string) (*tracing.Runtime, func() error, error) { return nil, nil, errFixtureOpen },
	})
}

var _ = Describe("standing stores", func() {
	var dataDir, run string

	BeforeEach(func() {
		dataDir = GinkgoT().TempDir()
		started, err := telltale.Start(context.Background(), telltale.StartOptions{DataDir: dataDir, Command: []string{"true"}, Traces: []telltale.TraceSpec{{Kind: httpproxy.Kind}}})
		Expect(err).ToNot(HaveOccurred())
		_, err = started.Wait()
		Expect(err).ToNot(HaveOccurred())
		run = started.ID()
		Expect(os.WriteFile(filepath.Join(dataDir, fixtureStore), nil, 0o600)).To(Succeed())
	})

	ids := func(options telltale.ListOptions) []string {
		options.DataDir = dataDir
		runs, err := telltale.List(options)
		Expect(err).ToNot(HaveOccurred())
		names := []string{}
		for _, info := range runs {
			names = append(names, info.RunID)
		}
		return names
	}

	It("lists an existing standing store after the command runs and leaves out one that does not exist", func() {
		Expect(ids(telltale.ListOptions{})).To(Equal([]string{run, fixtureStore}))
		Expect(ids(telltale.ListOptions{Limit: 1})).To(Equal([]string{run}))
		Expect(os.Remove(filepath.Join(dataDir, fixtureStore))).To(Succeed())
		Expect(ids(telltale.ListOptions{})).To(Equal([]string{run}))
	})

	DescribeTable("applies the list filters to standing stores",
		func(options telltale.ListOptions, expected []string) {
			Expect(ids(options)).To(Equal(expected))
		},
		Entry("kind it captures", telltale.ListOptions{Kind: spans.Kind}, []string{fixtureStore}),
		Entry("kind it does not capture", telltale.ListOptions{Kind: httpproxy.Kind, Query: "standing"}, []string{}),
		Entry("query on its id", telltale.ListOptions{Query: "STANDING"}, []string{fixtureStore}),
		Entry("since keeps the continuous store", telltale.ListOptions{Kind: spans.Kind, Since: time.Now().Add(time.Hour)}, []string{fixtureStore}),
		Entry("status standing", telltale.ListOptions{Status: []telltale.Status{telltale.StatusStanding}}, []string{fixtureStore}),
		Entry("status running", telltale.ListOptions{Status: []telltale.Status{telltale.StatusRunning}}, []string{}),
	)

	It("gets a standing store by id, with its status, and reopens it through its own opener", func() {
		info, err := telltale.Get(telltale.GetOptions{DataDir: dataDir, RunID: fixtureStore})
		Expect(err).ToNot(HaveOccurred())
		Expect(info).To(SatisfyAll(HaveField("RunID", fixtureStore), HaveField("Status", telltale.StatusStanding), HaveField("Standing", true)))
		Expect(os.WriteFile(filepath.Join(dataDir, fixtureStore+".running"), nil, 0o600)).To(Succeed())
		Expect(ids(telltale.ListOptions{Status: []telltale.Status{telltale.StatusRunning}})).To(Equal([]string{fixtureStore}))
		_, _, err = telltale.Reopen(dataDir, fixtureStore)
		Expect(err).To(MatchError(errFixtureOpen))
		runtime, closeStore, err := telltale.Reopen(dataDir, run)
		Expect(err).ToNot(HaveOccurred())
		Expect(runtime.Results).ToNot(BeNil())
		Expect(closeStore()).To(Succeed())
	})

	It("reports a registered store absent from the data dir as not existing", func() {
		Expect(os.Remove(filepath.Join(dataDir, fixtureStore))).To(Succeed())
		_, err := telltale.Get(telltale.GetOptions{DataDir: dataDir, RunID: fixtureStore})
		Expect(err).To(MatchError(os.ErrNotExist))
	})

	DescribeTable("refuses a registration that could shadow a run or another store",
		func(store telltale.StandingStore, message string) {
			Expect(func() { telltale.MustRegisterStore(store) }).To(PanicWith(MatchError(ContainSubstring(message))))
		},
		Entry("a duplicate id", telltale.StandingStore{ID: fixtureStore, Info: func(string) (telltale.RunInfo, bool, error) { return telltale.RunInfo{}, false, nil }, Open: func(string) (*tracing.Runtime, func() error, error) { return nil, nil, nil }}, "registered twice"),
		Entry("a run-shaped id", telltale.StandingStore{ID: "run-2026-10-30T09-00-00Z", Info: func(string) (telltale.RunInfo, bool, error) { return telltale.RunInfo{}, false, nil }, Open: func(string) (*tracing.Runtime, func() error, error) { return nil, nil, nil }}, "run id"),
		Entry("no opener", telltale.StandingStore{ID: "no-opener", Info: func(string) (telltale.RunInfo, bool, error) { return telltale.RunInfo{}, false, nil }}, "Info and Open"),
	)
})
