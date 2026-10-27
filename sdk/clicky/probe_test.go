package clicky

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/flanksource/clicky/exec"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestProbe(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Clicky probe") }

type testSink struct{ records []any }

func (s *testSink) TryEmit(v any) bool                   { s.records = append(s.records, v); return true }
func (*testSink) Blob(string, string, []byte, any) error { return nil }
func (*testSink) Report(any)                             {}
func (*testSink) Warn(string)                            {}

var _ = Describe("exec probe", func() {
	It("validates the glob and tail limit", func() {
		for _, raw := range []string{`{"name":"["}`, `{"tailBytes":-1}`, `{"tailBytes":65537}`} {
			_, err := (probe{}).Arm(context.Background(), json.RawMessage(raw), &testSink{})
			Expect(err).To(HaveOccurred())
		}
	})
	It("keeps only the configured tail and environment keys", func() {
		event := exec.ExecEvent{Name: "example", EnvKeys: []string{"TOKEN"}, Stdout: "abcdef", Stderr: "uvwxyz"}
		record := makeRecord(event, 3)
		Expect(record.EnvKeys).To(Equal([]string{"TOKEN"}))
		Expect(record.StdoutTail).To(Equal("def"))
		Expect(record.StderrTail).To(Equal("xyz"))
	})
	It("masks secrets before emitting command arguments and tails", func() {
		event := exec.ExecEvent{Name: "mysql", Args: []string{"--password", "hunter2", "-p", "private"}, Stdout: "password=output-secret", Stderr: "token=error-secret"}
		record := makeRecord(event, 4096)
		Expect(record.Args).To(Equal([]string{"--password", "****", "-p", "****"}))
		Expect(record.StdoutTail).NotTo(ContainSubstring("output-secret"))
		Expect(record.StderrTail).NotTo(ContainSubstring("error-secret"))
	})
})
