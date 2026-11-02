package traces_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/flanksource/commons-db/cmd/query/profiles"
	dbquery "github.com/flanksource/commons-db/query"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/telltale/pkg/datadir"
	"github.com/flanksource/telltale/pkg/traces"
)

// sqliteRead reads statement with the sqlite engine over an in-memory DSN.
const sqliteRead = `{"provider":{"type":"sql","sql":{"type":"sqlite","url":":memory:"},"query":%q}}`

// readTimeout bounds a read, long enough for one whose connection starts the
// managed Prometheus.
const readTimeout = 30 * time.Second

// readOnce runs a query capture with params in dataDir to its end and summarizes it.
func readOnce(dataDir string, params json.RawMessage) map[string]any {
	host := openHost(dataDir)
	capture, err := host.Start(traces.StartOptions{Kind: traces.QueryKind, Params: params})
	Expect(err).ToNot(HaveOccurred())
	Eventually(capture.Finished).WithTimeout(readTimeout).Should(BeTrue())
	seq, err := capture.Stop(context.Background())
	Expect(err).ToNot(HaveOccurred())
	summary, err := host.Summarize(context.Background(), capture.Ref(0, seq))
	Expect(err).ToNot(HaveOccurred())
	return summary
}

var _ = Describe("the query kind", func() {
	It("reads a provider in place once, each row a record, through the store's BeforeExecute", func() {
		statement := "SELECT '2026-11-01T00:00:00Z' AS time, 1 AS n UNION ALL SELECT '2026-11-01T00:00:01Z', 2"
		Expect(readOnce(GinkgoT().TempDir(), json.RawMessage(fmt.Sprintf(sqliteRead, statement)))).To(HaveKeyWithValue("count", BeNumerically("==", 2)))
	})

	It("reads a saved profile from <config dir>/profiles", func() {
		config := GinkgoT().TempDir()
		GinkgoT().Setenv("XDG_CONFIG_HOME", config)
		store, err := profiles.NewFileStore(filepath.Join(config, datadir.App, traces.ProfilesDir))
		Expect(err).ToNot(HaveOccurred())
		Expect(store.Save(context.Background(), dbquery.Profile{
			Name:     "one-row",
			Provider: dbquery.ProviderConfig{Type: "sql", Options: map[string]any{"type": "sqlite", "url": ":memory:"}},
			Query:    "SELECT '2026-11-01T00:00:00Z' AS time, 1 AS n",
		})).To(Succeed())
		Expect(readOnce(GinkgoT().TempDir(), json.RawMessage(`{"profile":"one-row"}`))).To(HaveKeyWithValue("count", BeNumerically("==", 1)))
	})

	It("refuses a profile no config dir holds before the capture starts, naming the dirs searched", func() {
		GinkgoT().Setenv("XDG_CONFIG_HOME", GinkgoT().TempDir())
		host := openHost(GinkgoT().TempDir())
		_, err := host.Start(traces.StartOptions{Kind: traces.QueryKind, Params: json.RawMessage(`{"profile":"missing"}`)})
		Expect(err).To(MatchError(ContainSubstring(`no query profile "missing" in [`)))
	})
})
