package history_test

import (
	deadlockgraph "github.com/flanksource/telltale/pkg/tracing/sqlserver/deadlocks"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"os"
	"testing"
	"time"
)

func TestHistory(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Deadlock history") }

func decoded(name string) deadlockgraph.Graph {
	GinkgoHelper()
	data, err := os.ReadFile("../deadlocks/testdata/" + name)
	Expect(err).NotTo(HaveOccurred())
	graph, err := deadlockgraph.DecodeReport(time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC), string(data))
	Expect(err).NotTo(HaveOccurred())
	return graph
}
