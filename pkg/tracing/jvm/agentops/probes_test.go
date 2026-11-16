package agentops_test

import (
	"encoding/json"

	"github.com/flanksource/telltale/pkg/tracing/jvm/agentops"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const agentInstrumented = `{"class":"Idle","loader":"18a70f16","digest":"sha256:6d8883aba2914eab63e4f3ef18dc10a084b3945211f2917643ff2294e049accf","originalDigest":"sha256:8ee7b08b76749dc3475e5bcad8a829a7f261d1d70d9abc5d4b7945e6356d84d2","handedDigest":"sha256:6d8883aba2914eab63e4f3ef18dc10a084b3945211f2917643ff2294e049accf","patched":true,"leaseExpiresAt":1794805019014,"methods":[{"name":"step","descriptor":"(J)J","instrumented":true}],"probeKey":"agentops-values","generation":1794804899014,"capacity":4096,"alloc":false,"gc":false,"instrumented":true}`

const agentProbes = `{"keys":[{"key":"agentops-args","owners":["18a70f16|Idle"],"generation":1794804880129,"capacity":4096,"writeSeq":228,"oldestSeq":0,"degraded":false,"armings":[{"owner":"18a70f16|Idle","method":"work","calls":false}]},{"key":"agentops-values","owners":["18a70f16|Idle"],"generation":1794804899014,"capacity":4096,"writeSeq":126,"oldestSeq":0,"degraded":false,"armings":[{"owner":"18a70f16|Idle","method":"step","calls":false}]}]}`

const agentProbeRead = `{"key":"agentops-values","generation":1794804899014,"events":[` +
	`"{\"seq\":0,\"ts\":1794804899088676209,\"kind\":\"enter\",\"key\":\"agentops-values\",\"threadId\":3,\"thread\":\"main\",\"depth\":1,\"frame\":155,\"parent\":154,\"class\":\"Idle\",\"method\":\"step\",\"descriptor\":\"(J)J\",\"args\":[{\"label\":\"arg0\",\"kind\":\"scalar\",\"type\":\"java.lang.Long\",\"value\":\"101626\"}]}",` +
	`"{\"seq\":1,\"ts\":1794804899089983750,\"kind\":\"return\",\"key\":\"agentops-values\",\"threadId\":3,\"thread\":\"main\",\"depth\":2,\"frame\":155,\"value\":{\"label\":\"return\",\"kind\":\"scalar\",\"type\":\"java.lang.Long\",\"value\":\"304878\"}}",` +
	`"{\"seq\":2,\"ts\":1794804899089991917,\"kind\":\"exit\",\"key\":\"agentops-values\",\"threadId\":3,\"thread\":\"main\",\"depth\":1,\"frame\":155,\"durationNs\":1316791}",` +
	`"{\"seq\":3,\"ts\":1794804899090000417,\"kind\":\"enter\",\"key\":\"agentops-values\",\"threadId\":3,\"thread\":\"main\",\"depth\":1,\"frame\":156,\"parent\":154,\"class\":\"Idle\",\"method\":\"step\",\"descriptor\":\"(J)J\",\"args\":[{\"label\":\"arg0\",\"kind\":\"scalar\",\"type\":\"java.lang.Long\",\"value\":\"50813\"}]}"` +
	`],"nextSeq":4,"oldestSeq":0,"writeSeq":6,"dropped":0,"truncated":true,"suppressed":0,"filtered":0,"omitted":0,"degraded":false,"capacity":4096,"active":true,"closed":false,"complete":false,"maxEvents":1000,"owners":["18a70f16|Idle"]}`

const agentProbeClosed = `{"key":"agentops-values","closed":true,"generation":1794804899014,"writeSeq":570,"capacity":4096,"filtered":0,"omitted":0,"active":false,"events":[]}`

const agentFrozen = `{"key":"agentops-fixture","frozen":true,"changed":true,"generation":1794804857514,"writeSeq":120,"capacity":4096}`

var _ = Describe("method probes", func() {
	It("decodes an instrument result and acknowledges it on one line", func() {
		result, err := agentops.DecodeInstrument(json.RawMessage(agentInstrumented))
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(agentops.Instrumented{
			Class: "Idle", Loader: "18a70f16", Patched: true, Instrumented: true,
			Digest:         "sha256:6d8883aba2914eab63e4f3ef18dc10a084b3945211f2917643ff2294e049accf",
			OriginalDigest: "sha256:8ee7b08b76749dc3475e5bcad8a829a7f261d1d70d9abc5d4b7945e6356d84d2",
			HandedDigest:   "sha256:6d8883aba2914eab63e4f3ef18dc10a084b3945211f2917643ff2294e049accf",
			LeaseExpiresAt: 1794805019014, ProbeKey: "agentops-values", Generation: 1794804899014, Capacity: 4096,
			Methods: []agentops.InstrumentedMethod{{Name: "step", Descriptor: "(J)J", Instrumented: true}},
		}))
		Expect(plain(result)).To(Equal([]string{"✓ instrumented Idle.step(J)J · probe agentops-values · lease until 04:56:59 · ring 4096"}))
	})

	It("names the methods an instrument skipped and why", func() {
		result := agentops.Instrumented{Class: "Idle", ProbeKey: "p", Capacity: 4096, Instrumented: true, Methods: []agentops.InstrumentedMethod{
			{Name: "work", Descriptor: "(J)J", Instrumented: true},
			{Name: "<init>", Descriptor: "()V", Skipped: "constructor"},
		}}
		Expect(plain(result)).To(Equal([]string{"✓ instrumented Idle.work(J)J · probe p · ring 4096 · skipped Idle.<init>()V: constructor"}))
	})

	It("reports an instrument that armed no method as a failure, so the command exits non-zero", func() {
		armed := agentops.Instrumented{Class: "Idle", ProbeKey: "p", Instrumented: true}
		Expect(armed.Failure()).To(Succeed())
		skipped := agentops.Instrumented{Class: "Idle", ProbeKey: "p", Methods: []agentops.InstrumentedMethod{{Name: "<init>", Descriptor: "()V", Skipped: "constructor"}}}
		Expect(skipped.Failure()).To(MatchError("Idle was not instrumented: <init>()V skipped: constructor"))
	})

	It("tabulates each open probe with its class, method and buffered events", func() {
		result, err := agentops.DecodeProbes(json.RawMessage(agentProbes))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Keys[0].Armings).To(Equal([]map[string]any{{"owner": "18a70f16|Idle", "method": "work", "calls": false}}))
		Expect(rows(result.Keys)).To(Equal([]map[string]any{
			{"key": "agentops-args", "class": "Idle", "method": "work", "events": int64(228), "capacity": 4096, "degraded": ""},
			{"key": "agentops-values", "class": "Idle", "method": "step", "events": int64(126), "capacity": 4096, "degraded": ""},
		}))
		Expect(plain(result)[0]).To(Equal("Probes 2 open"))
	})

	It("says there are no probes on one line", func() {
		result, err := agentops.DecodeProbes(json.RawMessage(`{"keys":[]}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(plain(result)).To(Equal([]string{"no probes"}))
	})

	It("prints a probe's events one per line, naming the method an exit closes", func() {
		result, err := agentops.DecodeProbeRead(json.RawMessage(agentProbeRead))
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Events).To(HaveLen(4))
		Expect(result.Events[1]).To(Equal(agentops.ProbeEvent{
			Seq: 1, TS: 1794804899089983750, Kind: "return", Key: "agentops-values", ThreadID: 3, Thread: "main", Depth: 2, Frame: 155,
			Value: &agentops.Node{Label: "return", Kind: "scalar", Type: "java.lang.Long", Value: "304878"},
		}))
		Expect(plain(result)).To(Equal([]string{
			"Probe agentops-values 4 events, seq 0–3 of 6 · recording · more from --since 4",
			"04:54:59.088 main enter Idle.step(101626)",
			"04:54:59.089 main return Idle.step → 304878",
			"04:54:59.089 main exit Idle.step 1.3ms",
			"04:54:59.090 main enter Idle.step(50813)",
		}))
	})

	It("reports a closed probe's end on one line", func() {
		result, err := agentops.DecodeProbeRead(json.RawMessage(agentProbeClosed))
		Expect(err).NotTo(HaveOccurred())
		Expect(plain(result)).To(Equal([]string{"Probe agentops-values closed · 570 events written"}))
	})

	It("fails on a read of a probe that is not open rather than printing no events", func() {
		_, err := agentops.DecodeProbeRead(json.RawMessage(`{"key":"no-such-key","active":false,"closed":false,"error":"no probe is open for key no-such-key"}`))
		Expect(err).To(MatchError(ContainSubstring("no probe is open for key no-such-key")))
	})

	It("acknowledges a freeze on one line", func() {
		result, err := agentops.DecodeFreeze(json.RawMessage(agentFrozen))
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(agentops.Frozen{Key: "agentops-fixture", Frozen: true, Changed: true, Generation: 1794804857514, WriteSeq: 120, Capacity: 4096}))
		Expect(plain(result)).To(Equal([]string{"✓ froze probe agentops-fixture · 120 events written"}))
		result.Changed = false
		Expect(plain(result)).To(Equal([]string{"✓ probe agentops-fixture was already frozen · 120 events written"}))
	})
})
