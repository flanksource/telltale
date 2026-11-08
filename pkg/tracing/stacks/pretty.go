package stacks

import (
	"fmt"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
)

func (r Stack) Pretty() api.Text {
	t := clicky.Text(fmt.Sprintf("pid %d", r.PID), "text-muted")
	if r.GoroutineID > 0 {
		t = t.Space().Append(fmt.Sprintf("goroutine %d", r.GoroutineID), "text-blue-500")
	}
	t = t.Space().Append(r.State, "text-yellow-500")
	if len(r.Frames) > 0 {
		t = t.Space().Append(r.Frames[0].Func, "font-mono")
	}
	if r.Error != "" {
		t = t.Space().Append(r.Error, "text-red-500")
	}
	return t
}

func (r Stack) PrettyFull() api.Textable {
	t := r.Pretty()
	for _, frame := range r.Frames {
		t = t.NewLine().Add(frame.Pretty())
	}
	if r.Raw != "" {
		t = t.NewLine().Add(clicky.CodeBlock("text", r.Raw))
	}
	return t
}

func (f Frame) Pretty() api.Text {
	t := clicky.Text(f.Func, "font-mono")
	if f.File != "" {
		t = t.Space().Append(f.File, "text-muted")
		if f.Line > 0 {
			t = t.Append(fmt.Sprintf(":%d", f.Line), "text-muted")
		}
	}
	return t
}
