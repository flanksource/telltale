package captain

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"time"

	"github.com/flanksource/captain/pkg/ai"
	"github.com/flanksource/captain/pkg/ai/middleware"
	telltale "github.com/flanksource/telltale/sdk"
	"github.com/flanksource/telltale/sdk/protocol"
)

type Record struct {
	Phase      string   `json:"phase"`
	Model      string   `json:"model"`
	Backend    string   `json:"backend"`
	Iter       int      `json:"iter,omitempty"`
	Prompt     string   `json:"prompt,omitempty"`
	Tools      any      `json:"tools,omitempty"`
	Response   string   `json:"response,omitempty"`
	Usage      ai.Usage `json:"usage"`
	Cost       float64  `json:"cost"`
	DurationMs float64  `json:"durationMs"`
	Error      string   `json:"error,omitempty"`
}
type params struct {
	Models           string `json:"models"`
	IncludePrompts   bool   `json:"includePrompts"`
	IncludeResponses bool   `json:"includeResponses"`
}
type probe struct{}

func init()                            { telltale.RegisterProbe(probe{}) }
func (probe) Spec() protocol.ProbeSpec { return protocol.ProbeSpec{Name: "llm", Title: "LLM calls"} }
func (probe) Arm(_ context.Context, raw json.RawMessage, sink telltale.Sink) (func() error, error) {
	var p params
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("llm params: %w", err)
	}
	if _, err := path.Match(p.Models, ""); err != nil {
		return nil, fmt.Errorf("llm models glob: %w", err)
	}
	release := middleware.ObserveProvider(func(event middleware.ProviderEvent) {
		match, _ := path.Match(p.Models, event.Model)
		if p.Models != "" && !match {
			return
		}
		record, err := makeRecord(event, p)
		if err != nil {
			sink.Warn(err.Error())
			return
		}
		sink.TryEmit(record)
	})
	return func() error { release(); return nil }, nil
}

func makeRecord(event middleware.ProviderEvent, p params) (Record, error) {
	r := Record{Phase: event.Phase, Model: event.Model, Backend: event.Backend, Iter: event.Iter, Usage: event.Usage, Cost: event.Cost, DurationMs: float64(event.Duration) / float64(time.Millisecond)}
	if event.Err != nil {
		r.Error = telltale.Mask(event.Err.Error())
	}
	if event.Event != nil && event.Event.Tool != "" {
		r.Tools = []string{event.Event.Tool}
	}
	request := event.Request
	if event.Event != nil && event.Event.Kind == ai.EventTurnStart {
		if turn, ok := event.Event.Raw.(*ai.TurnStart); ok && turn != nil {
			request = &turn.Request
		}
	}
	if p.IncludePrompts && request != nil {
		value, err := json.Marshal(struct {
			Prompt   any `json:"prompt"`
			Messages any `json:"messages,omitempty"`
		}{request.Prompt, request.Messages})
		if err != nil {
			return r, fmt.Errorf("llm prompt: %w", err)
		}
		r.Prompt = telltale.Mask(string(value))
	}
	if p.IncludeResponses && event.Response != nil {
		value, err := json.Marshal(event.Response)
		if err != nil {
			return r, fmt.Errorf("llm response: %w", err)
		}
		r.Response = telltale.Mask(string(value))
	}
	return r, nil
}
