// Runs the prometheus capture's parameter, target, and VictoriaMetrics specs.

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
