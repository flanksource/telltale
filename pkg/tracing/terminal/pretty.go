package terminal

import (
	"fmt"
	"strings"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
)

func (r Event) Pretty() api.Text {
	t := clicky.Text(fmt.Sprintf("+%.3fs", r.Offset), "text-muted")
	switch r.Kind {
	case KindResize:
		t = t.Space().Append("resize", "text-blue-500").Space().Append(fmt.Sprintf("%d × %d", r.Width, r.Height))
	case KindExit:
		t = t.Space().Append(r.Command, "font-mono").Space().Append("exit", "text-muted")
		if r.ExitCode != nil {
			style := "text-green-500"
			if *r.ExitCode != 0 {
				style = "text-red-500"
			}
			t = t.Space().Append(*r.ExitCode, style)
		}
	case KindOutput, KindSnapshot:
		t = t.Space().Append(r.Kind, "text-blue-500").Space().Append(strings.Join(strings.Fields(r.Data), " "), "max-w-[120ch]")
	}
	if r.Truncated {
		t = t.Space().Append("(truncated)", "text-yellow-500")
	}
	return t
}

func (r Event) PrettyFull() api.Textable {
	if r.Data != "" {
		return r.Pretty().NewLine().Append(r.Data)
	}
	return r.Pretty()
}
