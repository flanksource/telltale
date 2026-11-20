package agentops

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/flanksource/clicky/api"
)

const (
	// GroovyEvalOp runs a Groovy script in the application's class loader.
	GroovyEvalOp = "jvm.groovy.eval"
	// OgnlEvalOp evaluates an OGNL expression in the application's class loader.
	OgnlEvalOp = "jvm.ognl.eval"
)

// Evaluation is what an expression returned: Value for a null, string, number
// or boolean (as JSON), Graph for any other object.
type Evaluation struct {
	Value json.RawMessage `json:"value,omitempty"`
	Graph *Node           `json:"graph,omitempty"`
}

// GroovyResult is a script's evaluation with what it printed and how long it ran.
type GroovyResult struct {
	Evaluation
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	ElapsedMs int64  `json:"elapsedMs"`
}

// DecodeGroovy decodes a script's result, refusing one without a value or graph.
func DecodeGroovy(raw json.RawMessage) (GroovyResult, error) {
	var result GroovyResult
	if err := decode(GroovyEvalOp, raw, &result, "stdout", "stderr", "elapsedMs"); err != nil {
		return result, err
	}
	return result, requireEvaluated(GroovyEvalOp, raw, result.Evaluation)
}

// DecodeOgnl decodes an expression's result, refusing one without a value or graph.
func DecodeOgnl(raw json.RawMessage) (Evaluation, error) {
	var result Evaluation
	if err := decode(OgnlEvalOp, raw, &result); err != nil {
		return result, err
	}
	return result, requireEvaluated(OgnlEvalOp, raw, result)
}

func requireEvaluated(op string, raw json.RawMessage, e Evaluation) error {
	if len(e.Value) == 0 && e.Graph == nil {
		return fmt.Errorf("%s returned neither a value nor a graph: %s", op, raw)
	}
	return nil
}

// Pretty is "= value" on one line, or the graph's type over its member tree.
func (e Evaluation) Pretty() api.Text { return e.summary("") }

func (e Evaluation) summary(elapsed string) api.Text {
	line := api.Text{}.Append("= ")
	if e.Graph != nil {
		line = line.Append(shortType(e.Graph.Type), "font-bold")
	} else {
		line = line.Append(string(e.Value), "font-bold")
	}
	if elapsed != "" {
		line = line.Append(" · "+elapsed, "text-muted")
	}
	if e.Graph != nil {
		line = line.Add(e.Graph.tree(""))
	}
	return line
}

// Pretty is the evaluation, then the script's stdout and, in yellow, its stderr.
func (g GroovyResult) Pretty() api.Text {
	text := g.summary(fmt.Sprintf("%dms", g.ElapsedMs))
	if out := strings.TrimRight(g.Stdout, "\n"); out != "" {
		text = text.NewLine().Append(out)
	}
	if errs := strings.TrimRight(g.Stderr, "\n"); errs != "" {
		text = text.NewLine().Append(errs, "text-yellow-600")
	}
	return text
}
