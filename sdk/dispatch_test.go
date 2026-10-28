package telltale

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/flanksource/telltale/sdk/protocol"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type panicOp struct{}

func (panicOp) Spec() protocol.OpSpec { return protocol.OpSpec{Name: "panic-op"} }
func (panicOp) Call(context.Context, json.RawMessage) (any, []protocol.Blob, error) {
	panic("operation broke")
}

type echoOp struct{}

func (echoOp) Spec() protocol.OpSpec { return protocol.OpSpec{Name: "echo-op"} }
func (echoOp) Call(_ context.Context, params json.RawMessage) (any, []protocol.Blob, error) {
	var value any
	err := json.Unmarshal(params, &value)
	return value, nil, err
}

type panicProbe struct{}

func (panicProbe) Spec() protocol.ProbeSpec { return protocol.ProbeSpec{Name: "panic-probe"} }
func (panicProbe) Arm(context.Context, json.RawMessage, Sink) (func() error, error) {
	panic("arm broke")
}

type failingProbe struct{}

func (failingProbe) Spec() protocol.ProbeSpec { return protocol.ProbeSpec{Name: "failing-probe"} }
func (failingProbe) Arm(context.Context, json.RawMessage, Sink) (func() error, error) {
	return func() error { return errors.New("revert failure") }, nil
}
func (failingProbe) Checkpoint(context.Context, json.RawMessage, Sink) error {
	return errors.New("checkpoint failure")
}

type noCheckpointProbe struct{}

func (noCheckpointProbe) Spec() protocol.ProbeSpec { return protocol.ProbeSpec{Name: "no-checkpoint"} }
func (noCheckpointProbe) Arm(context.Context, json.RawMessage, Sink) (func() error, error) {
	return func() error { return nil }, nil
}

type panicSpecProbe struct{}

func (panicSpecProbe) Spec() protocol.ProbeSpec { panic("spec broke") }
func (panicSpecProbe) Arm(context.Context, json.RawMessage, Sink) (func() error, error) {
	return func() error { return nil }, nil
}

type panicCheckpointProbe struct{}

func (panicCheckpointProbe) Spec() protocol.ProbeSpec {
	return protocol.ProbeSpec{Name: "panic-checkpoint"}
}
func (panicCheckpointProbe) Arm(context.Context, json.RawMessage, Sink) (func() error, error) {
	return func() error { return nil }, nil
}
func (panicCheckpointProbe) Checkpoint(context.Context, json.RawMessage, Sink) error {
	panic("checkpoint broke")
}

type selectiveProbe struct{}

type checkpointRecordProbe struct{}

func (checkpointRecordProbe) Spec() protocol.ProbeSpec {
	return protocol.ProbeSpec{Name: "checkpoint-record", Checkpoints: true}
}
func (checkpointRecordProbe) Arm(context.Context, json.RawMessage, Sink) (func() error, error) {
	return func() error { return nil }, nil
}
func (checkpointRecordProbe) Checkpoint(_ context.Context, _ json.RawMessage, sink Sink) error {
	sink.TryEmit(map[string]string{"value": "reading"})
	return nil
}

func (selectiveProbe) Spec() protocol.ProbeSpec {
	return protocol.ProbeSpec{Name: "selective-probe", Mutates: true}
}
func (selectiveProbe) Arm(context.Context, json.RawMessage, Sink) (func() error, error) {
	return func() error { return nil }, nil
}
func (selectiveProbe) Mutating(raw json.RawMessage) (bool, error) {
	var params struct {
		Mutate bool `json:"mutate"`
	}
	err := json.Unmarshal(raw, &params)
	return params.Mutate, err
}

var _ = Describe("protocol dispatch", func() {
	It("tags checkpoint records before the terminal frame", func() {
		RegisterProbe(checkpointRecordProbe{})
		a := Loopback()
		defer a.Close()
		stream, conn := openAgent(a)
		defer stream.Close()
		Expect(conn.Send("arm", protocol.Arm{Session: "record-session", Probe: "checkpoint-record", Params: json.RawMessage(`{}`)})).To(Succeed())
		_, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(conn.Send("checkpoint", protocol.Checkpoint{Session: "record-session", ID: "reading", Params: json.RawMessage(`{}`)})).To(Succeed())
		frame, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		var records protocol.Records
		Expect(json.Unmarshal(frame.Body, &records)).To(Succeed())
		Expect(records.Session).To(Equal("record-session"))
		Expect(records.Checkpoint).To(Equal("reading"))
		Expect(records.Seq).To(BeNumerically(">", 0))
		Expect(records.Items).To(HaveLen(1))
		frame, _, err = conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(frame.Type).To(Equal(protocol.TypeCheckpointed))
	})
	It("tags checkpoint blobs and terminates the matching checkpoint", func() {
		a := Loopback()
		defer a.Close()
		stream, conn := openAgent(a)
		defer stream.Close()
		Expect(conn.Send("arm", protocol.Arm{Session: "stacks-checkpoint", Probe: "stacks", Params: json.RawMessage(`{}`)})).To(Succeed())
		_, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(conn.Send("checkpoint", protocol.Checkpoint{Session: "stacks-checkpoint", ID: "reading-1", Params: json.RawMessage(`{}`)})).To(Succeed())
		frame, data, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(frame.Type).To(Equal(protocol.TypeBlob))
		var blob protocol.Blob
		Expect(json.Unmarshal(frame.Body, &blob)).To(Succeed())
		Expect(blob.Checkpoint).To(Equal("reading-1"))
		Expect(data).NotTo(BeEmpty())
		frame, _, err = conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(frame.Type).To(Equal(protocol.TypeCheckpointed))
		var done protocol.Checkpointed
		Expect(json.Unmarshal(frame.Body, &done)).To(Succeed())
		Expect(done).To(Equal(protocol.Checkpointed{Session: "stacks-checkpoint", ID: "reading-1"}))
	})
	It("executes an operation and returns its result", func() {
		RegisterOp(echoOp{})
		a := Loopback()
		defer a.Close()
		stream, conn := openAgent(a)
		defer stream.Close()
		Expect(conn.Send("call", protocol.Call{CallID: "echo", Op: "echo-op", Params: json.RawMessage(`{"name":"example"}`)})).To(Succeed())
		frame, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(frame.Type).To(Equal("result"))
		var result protocol.Result
		Expect(json.Unmarshal(frame.Body, &result)).To(Succeed())
		Expect(result.Error).To(BeEmpty())
		Expect(string(result.Value)).To(MatchJSON(`{"name":"example"}`))
	})
	It("uses a probe's mutation hook for each set of parameters", func() {
		RegisterProbe(selectiveProbe{})
		a := Loopback()
		defer a.Close()
		stream, conn := openAgent(a)
		defer stream.Close()
		for _, request := range []protocol.Arm{
			{Session: "read", Probe: "selective-probe", Params: json.RawMessage(`{"mutate":false}`)},
			{Session: "mutate", Probe: "selective-probe", Params: json.RawMessage(`{"mutate":true}`)},
		} {
			Expect(conn.Send("arm", request)).To(Succeed())
			frame, _, err := conn.Read()
			Expect(err).NotTo(HaveOccurred())
			var armed protocol.Armed
			Expect(json.Unmarshal(frame.Body, &armed)).To(Succeed())
			if request.Session == "read" {
				Expect(armed.Error).To(BeEmpty())
			} else {
				Expect(armed.Error).To(ContainSubstring(ErrAgentMutationDisabled.Error()))
			}
		}
	})
	It("turns a spec panic into the named extension error", func() {
		_, err := probeSpec(panicSpecProbe{})
		Expect(errors.Is(err, ErrProbePanic)).To(BeTrue())
	})
	It("turns a checkpoint panic into an ended frame", func() {
		RegisterProbe(panicCheckpointProbe{})
		a := Loopback()
		defer a.Close()
		stream, conn := openAgent(a)
		defer stream.Close()
		Expect(conn.Send("arm", protocol.Arm{Session: "panic-checkpoint", Probe: "panic-checkpoint", Params: json.RawMessage(`{}`)})).To(Succeed())
		_, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(conn.Send("checkpoint", protocol.Checkpoint{Session: "panic-checkpoint", ID: "reading"})).To(Succeed())
		frame, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(frame.Type).To(Equal("checkpointed"))
		frame, _, err = conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(frame.Type).To(Equal("ended"))
		Expect(string(frame.Body)).To(ContainSubstring(ErrProbePanic.Error()))
	})
	It("returns named errors for unknown messages and operations without disconnecting", func() {
		a := Loopback()
		defer a.Close()
		stream, conn := openAgent(a)
		defer stream.Close()
		Expect(conn.Send("unexpected", struct{}{})).To(Succeed())
		frame, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(frame.Type).To(Equal("error"))
		Expect(string(frame.Body)).To(ContainSubstring("unknown message"))
		Expect(conn.Send("checkpoint", protocol.Checkpoint{Session: "missing", ID: "reading"})).To(Succeed())
		frame, _, err = conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(frame.Type).To(Equal("checkpointed"))
		Expect(string(frame.Body)).To(ContainSubstring("unknown session"))
		RegisterOp(panicOp{})
		Expect(conn.Send("call", protocol.Call{CallID: "call", Op: "panic-op"})).To(Succeed())
		frame, _, err = conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(frame.Type).To(Equal("result"))
		Expect(string(frame.Body)).To(ContainSubstring("probe panic"))
	})
	It("turns an arm panic into a named ended frame", func() {
		RegisterProbe(panicProbe{})
		a := Loopback()
		defer a.Close()
		stream, conn := openAgent(a)
		defer stream.Close()
		Expect(conn.Send("arm", protocol.Arm{Session: "panic", Probe: "panic-probe", Params: json.RawMessage(`{}`)})).To(Succeed())
		frame, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(frame.Type).To(Equal("armed"))
		frame, _, err = conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(frame.Type).To(Equal("ended"))
		Expect(string(frame.Body)).To(ContainSubstring(ErrProbePanic.Error()))
	})
	It("retains both the probe failure and the revert failure", func() {
		RegisterProbe(failingProbe{})
		a := Loopback()
		defer a.Close()
		stream, conn := openAgent(a)
		defer stream.Close()
		Expect(conn.Send("arm", protocol.Arm{Session: "failures", Probe: "failing-probe", Params: json.RawMessage(`{}`)})).To(Succeed())
		_, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(conn.Send("checkpoint", protocol.Checkpoint{Session: "failures", ID: "reading"})).To(Succeed())
		frame, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(frame.Type).To(Equal("checkpointed"))
		frame, _, err = conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(frame.Type).To(Equal("ended"))
		var ended protocol.Ended
		Expect(json.Unmarshal(frame.Body, &ended)).To(Succeed())
		Expect(ended.Error).To(ContainSubstring("checkpoint failure"))
		Expect(ended.Error).To(ContainSubstring("revert failure"))
	})
	It("reports an unsupported checkpoint and keeps the connection usable", func() {
		RegisterProbe(noCheckpointProbe{})
		a := Loopback()
		defer a.Close()
		stream, conn := openAgent(a)
		defer stream.Close()
		Expect(conn.Send("arm", protocol.Arm{Session: "no-checkpoint", Probe: "no-checkpoint", Params: json.RawMessage(`{}`)})).To(Succeed())
		_, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(conn.Send("checkpoint", protocol.Checkpoint{Session: "no-checkpoint", ID: "reading"})).To(Succeed())
		frame, _, err := conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(frame.Type).To(Equal("checkpointed"))
		Expect(string(frame.Body)).To(ContainSubstring(ErrCheckpointUnsupported.Error()))
		Expect(conn.Send("ping", protocol.Ping{})).To(Succeed())
		frame, _, err = conn.Read()
		Expect(err).NotTo(HaveOccurred())
		Expect(frame.Type).To(Equal("pong"))
	})
})
