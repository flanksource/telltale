package seam

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/flanksource/commons-db/query"
	"github.com/flanksource/commons-db/recordstore/sampleframe"
	"github.com/flanksource/telltale/pkg/agentclient"
	"github.com/flanksource/telltale/pkg/tracing"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type frameOutput struct {
	frames []sampleframe.Frame
	rows   [][]string
}

func (*frameOutput) Emit(context.Context, string) error {
	return errors.New("unexpected individual row")
}
func (*frameOutput) TryEmit(string) bool                       { return false }
func (*frameOutput) Report(any)                                {}
func (*frameOutput) Warn(string)                               {}
func (*frameOutput) Describe(string, ...query.SessionMetadata) {}
func (*frameOutput) Export(string, string)                     {}
func (o *frameOutput) EmitFrame(_ context.Context, frame sampleframe.Frame, rows []string) error {
	o.frames = append(o.frames, frame)
	o.rows = append(o.rows, rows)
	return nil
}

var _ = Describe("decoded agent frames", func() {
	It("preserves producer frame metadata including an empty snapshot", func() {
		started := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
		frame := sampleframe.Frame{Source: "jvm-instance/http", Tick: 9, StartedAt: started, CompletedAt: started.Add(time.Millisecond), Observed: 0, Limit: 50, Summary: map[string]any{"complete": true}}
		out := &frameOutput{}
		s := AgentSeam[struct{}, string]{DecodeBlob: func(agentclient.Blob, map[string]string) (DecodedBlob[string], error) {
			return DecodedBlob[string]{Frame: &frame, Rows: []string{}}, nil
		}}
		Expect(s.blob(context.Background(), agentclient.Blob{}, map[string]string{"process.pid": "1"}, out)).To(Succeed())
		Expect(out.frames).To(Equal([]sampleframe.Frame{frame}))
		Expect(out.rows).To(Equal([][]string{{}}))
	})
	It("delegates named-result blobs without requiring a row decoder", func() {
		out := &frameOutput{}
		var received json.RawMessage
		s := AgentSeam[struct{}, string]{HandleBlob: func(_ context.Context, blob agentclient.Blob, resource map[string]string, emitter tracing.Emitter[string]) error {
			Expect(resource).To(HaveKeyWithValue("source", "jvm"))
			Expect(emitter).To(BeIdenticalTo(out))
			received = blob.Data
			return nil
		}}
		Expect(s.blob(context.Background(), agentclient.Blob{Data: []byte("snapshot")}, map[string]string{"source": "jvm"}, out)).To(Succeed())
		Expect(string(received)).To(Equal("snapshot"))
	})
})
