// Runs the prometheus kind's specs: params, file_sd target groups, the scrape
// records read from a fake Prometheus API, and one live managed Prometheus.

package prometheus_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	// The specs open results stores, whose profiles page through the sqlite
	// provider.
	_ "github.com/flanksource/commons-db/query/providers/sqlite"
)

func TestPrometheus(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Prometheus Trace Suite")
}
