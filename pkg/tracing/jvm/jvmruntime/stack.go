package jvmruntime

import (
	"encoding/json"
	"fmt"

	"github.com/flanksource/clicky/api"
)

// ThreadStackOp reads one thread's row, jstack text and frames at full depth.
const ThreadStackOp = "jvm.threads.stack"

// ThreadStack is the agent's jvm.threads.stack. Dump is Text read back into a
// jstack block for rendering; it carries the lock lines Frames does not.
type ThreadStack struct {
	Thread Thread       `json:"thread"`
	Text   string       `json:"text"`
	Frames []StackFrame `json:"frames"`
	Dump   DumpedThread `json:"-"`
}

// StackFrame is one stack element. FileName is null when the class carries no
// source name; LineNumber is -2 for a native method and -1 when unknown.
type StackFrame struct {
	ClassName  string  `json:"className"`
	MethodName string  `json:"methodName"`
	FileName   *string `json:"fileName"`
	LineNumber int     `json:"lineNumber"`
}

// DecodeThreadStack decodes the agent's stack, refusing one whose text is not
// exactly the requested thread's jstack block.
func DecodeThreadStack(raw json.RawMessage) (ThreadStack, error) {
	stack, err := decodeObject[ThreadStack](ThreadStackOp, raw, "thread", "text", "frames")
	if err != nil {
		return stack, err
	}
	dump, err := parseThreadDump(ThreadStackOp, stack.Text)
	if err != nil {
		return stack, err
	}
	if len(dump.Threads) != 1 || dump.Threads[0].ID != stack.Thread.ID {
		return stack, fmt.Errorf("%s text is not thread %d's stack: %q", ThreadStackOp, stack.Thread.ID, stack.Text)
	}
	stack.Dump = dump.Threads[0]
	return stack, nil
}

// Pretty is the thread's header, with its CPU time, over its frames and locks.
func (s ThreadStack) Pretty() api.Text {
	suffix := ""
	if s.Thread.CPUTimeMs != nil {
		suffix = " · cpu " + millis(*s.Thread.CPUTimeMs)
	}
	return s.Dump.render(suffix)
}
