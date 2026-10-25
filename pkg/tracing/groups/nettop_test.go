package groups_test

import (
	"strings"

	"github.com/flanksource/telltale/pkg/tracing/groups"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// nettopDeltas is `nettop -P -d -x -J bytes_in,bytes_out` output: the first
// block counts each process's open flows so far, every later one the bytes
// since the block before it.
const nettopDeltas = `,bytes_in,bytes_out,
launchd.1,0,0,
com.apple.WebKit.Networking.4321,9000,800,
cmux.715,103279,129134,
,bytes_in,bytes_out,
launchd.1,0,0,
com.apple.WebKit.Networking.4321,100,20,
cmux.715,2013,2470,
,bytes_in,bytes_out,
cmux.715,5,7,
`

var _ = Describe("ParseNettop", func() {
	It("hands each block of per-process byte deltas, naming the process by the pid after its last dot", func() {
		var blocks []map[int]groups.NetBytes
		err := groups.ParseNettop(strings.NewReader(nettopDeltas), func(block map[int]groups.NetBytes) {
			blocks = append(blocks, block)
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(blocks).To(Equal([]map[int]groups.NetBytes{
			{1: {}, 4321: {Rx: 9000, Tx: 800}, 715: {Rx: 103279, Tx: 129134}},
			{1: {}, 4321: {Rx: 100, Tx: 20}, 715: {Rx: 2013, Tx: 2470}},
			{715: {Rx: 5, Tx: 7}},
		}))
	})

	It("refuses a row it cannot read rather than miscount it", func() {
		err := groups.ParseNettop(strings.NewReader(",bytes_in,bytes_out,\nnopid,1,2,\n"), func(map[int]groups.NetBytes) {})

		Expect(err).To(MatchError(ContainSubstring(`"nopid,1,2,"`)))
	})
})

var _ = Describe("NetTotals", func() {
	It("adds every block after the first into each pid's bytes since the monitor started", func() {
		totals := groups.NewNetTotals()
		Expect(groups.ParseNettop(strings.NewReader(nettopDeltas), totals.Add)).To(Succeed())

		Expect(totals.Counters()).To(Equal(map[int]groups.NetBytes{1: {}, 4321: {Rx: 100, Tx: 20}, 715: {Rx: 2018, Tx: 2477}}))
		Expect(totals.Ready()).To(BeClosed())
	})

	It("counts nothing, rather than zero bytes, until a block past the baseline arrives", func() {
		totals := groups.NewNetTotals()
		totals.Add(map[int]groups.NetBytes{715: {Rx: 103279, Tx: 129134}})

		Expect(totals.Counters()).To(BeNil())
		Expect(totals.Ready()).ToNot(BeClosed())
	})
})
