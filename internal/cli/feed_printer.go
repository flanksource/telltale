package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/telltale/pkg/traces/read"
	"github.com/flanksource/telltale/pkg/tracing/jvmtrace"
)

// feedPrinter prints followed or replayed records one line each. A
// jvm_trace stream's method events fold into calls, printed as each outermost
// call exits; ndjson keeps every record as recorded.
type feedPrinter struct {
	out     io.Writer
	ndjson  bool
	calls   map[string]*jvmtrace.Calls
	streams []string
}

func newFeedPrinter(out io.Writer) *feedPrinter {
	return &feedPrinter{out: out, ndjson: clicky.Flags.ResolveFormat() == "ndjson", calls: map[string]*jvmtrace.Calls{}}
}

func (p *feedPrinter) print(record read.Record) error {
	if p.ndjson {
		return json.NewEncoder(p.out).Encode(record)
	}
	if record.Kind != "jvm_trace" || record.Result != "" {
		return p.line(record.Pretty())
	}
	event, err := jvmtrace.EventOf(record.Record)
	if err != nil {
		return fmt.Errorf("jvm_trace %s #%d: %w", record.Stream, record.Seq, err)
	}
	calls := p.calls[record.Stream]
	if calls == nil {
		calls = &jvmtrace.Calls{}
		p.calls[record.Stream] = calls
		p.streams = append(p.streams, record.Stream)
	}
	for _, line := range calls.Add(event) {
		if err := p.line(line); err != nil {
			return err
		}
	}
	return nil
}

// stats are a line per jvm_trace stream the printer folded, naming the stream
// only when there were several.
func (p *feedPrinter) stats(out io.Writer) error {
	for _, stream := range slices.Clone(p.streams) {
		name := "jvm_trace"
		if len(p.streams) > 1 {
			name += " " + stream
		}
		if err := writeLine(out, api.Text{}.Append(name+" · ", "font-bold").Add(p.calls[stream].Stats().Pretty())); err != nil {
			return err
		}
	}
	return nil
}

func (p *feedPrinter) line(text api.Textable) error { return writeLine(p.out, text) }

func writeLine(out io.Writer, text api.Textable) error {
	output, err := clicky.Format(text, clicky.FormatOptions{Format: "pretty"})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, output)
	return err
}
