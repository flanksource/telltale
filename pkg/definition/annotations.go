package definition

import (
	"fmt"
	"strings"

	"github.com/flanksource/telltale/pkg/traces"
)

// The annotations a definition's command help is read from: a one-line
// summary, and examples one per line as `<args> # <what it does>`.
const (
	AnnotationSummary  = Group + "/summary"
	AnnotationExamples = Group + "/examples"
)

// Summary is def's one-line summary, its description when it has none.
func (d *TraceDefinition) Summary() string {
	if summary := d.Annotations[AnnotationSummary]; summary != "" {
		return summary
	}
	return d.Spec.Description
}

// Examples are the invocations def's command help shows.
func (d *TraceDefinition) Examples() ([]traces.Example, error) {
	var examples []traces.Example
	for line := range strings.SplitSeq(d.Annotations[AnnotationExamples], "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		args, says, ok := strings.Cut(line, " # ")
		if !ok {
			return nil, fmt.Errorf("trace definition %s: example %q is not `<args> # <what it does>`", d.Name, line)
		}
		examples = append(examples, traces.Example{Args: strings.TrimSpace(args), Says: strings.TrimSpace(says)})
	}
	return examples, nil
}

// Builtins are the definitions telltale ships, by name; each is a command.
func Builtins() ([]*TraceDefinition, error) {
	read, err := NewStore(nil).readLayer(LayerBuiltin)
	if err != nil {
		return nil, err
	}
	defs := make([]*TraceDefinition, 0, len(read))
	for _, stored := range read {
		if stored.Problem != "" {
			return nil, fmt.Errorf("built-in trace definition %s: %s", stored.Name, stored.Problem)
		}
		defs = append(defs, stored.Definition)
	}
	return defs, nil
}
