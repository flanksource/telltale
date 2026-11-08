package processcapture

import (
	"github.com/flanksource/telltale/pkg/tracing/groups"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"time"
)

var _ = Describe("derived process groups", func() {
	It("rebuilds self and descendant totals without storing group snapshots", func() {
		at := time.Unix(100, 0)
		root := GroupInfo{Key: "tools", Name: "tools", Kind: groups.KindTool}
		child := GroupInfo{Key: "tests", ParentKey: root.Key, Name: "tests", Kind: groups.KindTest}
		identities := []Identity{{Key: "1", PID: 1, Name: "runner", Groups: []GroupInfo{root}}, {Key: "2", PID: 2, Name: "worker", Groups: []GroupInfo{child, root}}}
		cpu := 12.5
		rows := []MetricRow{{Entity: "1", At: at, Tick: 1, RSSBytes: 1024}, {Entity: "2", At: at, Tick: 1, RSSBytes: 2048, CPUPercent: &cpu}}
		snapshot, err := GroupSnapshot(identities, rows)
		Expect(err).NotTo(HaveOccurred())
		Expect(snapshot.Frame.Groups).To(HaveExactElements(
			And(HaveField("Key", root.Key), HaveField("Self", And(HaveField("Processes", 1), HaveField("RSSBytes", uint64(1024)))), HaveField("Total", And(HaveField("Processes", 2), HaveField("RSSBytes", uint64(3072)), HaveField("CPUPercent", Equal(&cpu))))),
			And(HaveField("Key", child.Key), HaveField("Total", And(HaveField("Processes", 1), HaveField("RSSBytes", uint64(2048))))),
		))
		Expect(snapshot.Processes).To(HaveLen(2))
	})
})
