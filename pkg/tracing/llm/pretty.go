package llm

import (
	"fmt"
	"time"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
)

func (r LLMCall) Pretty() api.Text {
	t := clicky.Text(r.Phase, "text-blue-500 font-bold").Space().Append(r.Model, "font-bold").
		Space().Append(r.Backend, "text-muted").Space().
		Append("tokens in ", "text-muted").Add(api.HumanNumber(int64(r.Usage.InputTokens), "text-blue-500")).
		Append(" out ", "text-muted").Add(api.HumanNumber(int64(r.Usage.OutputTokens), "text-blue-500")).
		Space().Append(fmt.Sprintf("$%.6f", r.Cost), "text-yellow-500").Space().
		Add(api.Human(time.Duration(r.DurationMs*float64(time.Millisecond)), "text-muted"))
	if r.Error != "" {
		t = t.Space().Append(r.Error, "text-red-500")
	}
	return t
}

func (r LLMCall) PrettyFull() api.Textable {
	t := r.Pretty()
	if r.Prompt != "" {
		t = t.NewLine().Append("Prompt", "font-bold").NewLine().Add(clicky.CodeBlock("text", r.Prompt))
	}
	if r.Response != "" {
		t = t.NewLine().Append("Response", "font-bold").NewLine().Add(clicky.CodeBlock("json", r.Response))
	}
	return t
}
