package captain

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/flanksource/captain/pkg/ai"
	"github.com/flanksource/captain/pkg/ai/middleware"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestProbe(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Captain probe") }

type testSink struct{ records []any }

func (s *testSink) TryEmit(v any) bool                   { s.records = append(s.records, v); return true }
func (*testSink) Blob(string, string, []byte, any) error { return nil }
func (*testSink) Report(any)                             {}
func (*testSink) Warn(string)                            {}

var _ = Describe("llm probe", func() {
	It("rejects malformed model globs", func() {
		_, err := (probe{}).Arm(context.Background(), json.RawMessage(`{"models":"["}`), &testSink{})
		Expect(err).To(HaveOccurred())
	})
	It("includes request and response only when requested", func() {
		event := middleware.ProviderEvent{Request: &ai.Request{}, Response: &ai.Response{Text: "response"}}
		without, err := makeRecord(event, params{})
		Expect(err).NotTo(HaveOccurred())
		Expect(without.Prompt).To(BeEmpty())
		Expect(without.Response).To(BeEmpty())
		with, err := makeRecord(event, params{IncludePrompts: true, IncludeResponses: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(with.Prompt).NotTo(BeEmpty())
		Expect(with.Response).To(ContainSubstring("response"))
	})
	It("masks included prompts and responses before emission", func() {
		request := &ai.Request{}
		request.Prompt.User = "password=prompt-secret"
		record, err := makeRecord(middleware.ProviderEvent{Request: request, Response: &ai.Response{Text: "token=response-secret"}}, params{IncludePrompts: true, IncludeResponses: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(record.Prompt).NotTo(ContainSubstring("prompt-secret"))
		Expect(record.Response).NotTo(ContainSubstring("response-secret"))
	})
	It("includes a runner turn prompt only when requested", func() {
		turn := &ai.TurnStart{}
		turn.Request.Prompt.User = "turn prompt"
		event := middleware.ProviderEvent{Phase: "iteration", Event: &ai.Event{Kind: ai.EventTurnStart, Raw: turn}}
		without, err := makeRecord(event, params{})
		Expect(err).NotTo(HaveOccurred())
		Expect(without.Prompt).To(BeEmpty())
		with, err := makeRecord(event, params{IncludePrompts: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(with.Prompt).To(ContainSubstring("turn prompt"))
	})
})
