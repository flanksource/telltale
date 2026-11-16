package jvmruntime

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/flanksource/clicky/api"
)

// ThreadDumpOp dumps every thread's stack as jstack text, then any deadlocks.
const ThreadDumpOp = "jvm.threads.dump"

// ThreadDump is the agent's jstack text read back into threads. Deadlocks is
// the jstack deadlock report's lines, verbatim, when there is one.
type ThreadDump struct {
	Threads   []DumpedThread `json:"threads"`
	Deadlocks []string       `json:"deadlocks,omitempty"`
}

// DumpedThread is one jstack block. Stack is its frame ("at ...") and lock
// ("- ...") lines in order; Priority is 0 when the thread ended mid-dump.
type DumpedThread struct {
	Name          string   `json:"name"`
	ID            int64    `json:"id"`
	Daemon        bool     `json:"daemon"`
	Priority      int      `json:"priority,omitempty"`
	State         string   `json:"state"`
	Stack         []string `json:"stack"`
	Synchronizers []string `json:"synchronizers,omitempty"`
}

var (
	threadHeader   = regexp.MustCompile(`^"(.*)" #(\d+)( daemon)?(?: prio=(\d+))?$`)
	deadlockHeader = regexp.MustCompile(`^Found \d+ deadlocks?:$`)
)

const (
	stateLine        = "   java.lang.Thread.State: "
	synchronizerLine = "   Locked ownable synchronizers:"
)

// DecodeThreadDump decodes the agent's dump, a JSON string of jstack text,
// refusing any line it does not recognise rather than dropping it.
func DecodeThreadDump(raw json.RawMessage) (ThreadDump, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return ThreadDump{}, fmt.Errorf("decode %s result: %w", ThreadDumpOp, err)
	}
	return parseThreadDump(ThreadDumpOp, text)
}

func parseThreadDump(op, text string) (ThreadDump, error) {
	var dump ThreadDump
	synchronizers := false
	for number, line := range strings.Split(text, "\n") {
		if dump.Deadlocks != nil || deadlockHeader.MatchString(line) {
			if line != "" {
				dump.Deadlocks = append(dump.Deadlocks, line)
			}
			continue
		}
		if match := threadHeader.FindStringSubmatch(line); match != nil {
			thread, err := newDumpedThread(match)
			if err != nil {
				return dump, fmt.Errorf("%s line %d: %w", op, number+1, err)
			}
			dump.Threads = append(dump.Threads, thread)
			synchronizers = false
			continue
		}
		if line == "" {
			continue
		}
		if len(dump.Threads) == 0 {
			return dump, fmt.Errorf("%s line %d is outside any thread: %q", op, number+1, line)
		}
		var err error
		if synchronizers, err = dump.Threads[len(dump.Threads)-1].parseLine(line, synchronizers); err != nil {
			return dump, fmt.Errorf("%s line %d: %w", op, number+1, err)
		}
	}
	for _, thread := range dump.Threads {
		if thread.State == "" {
			return dump, fmt.Errorf("%s thread %q #%d has no state", op, thread.Name, thread.ID)
		}
	}
	if len(dump.Threads) == 0 {
		return dump, fmt.Errorf("%s returned no threads: %q", op, text)
	}
	return dump, nil
}

func newDumpedThread(header []string) (DumpedThread, error) {
	thread := DumpedThread{Name: header[1], Daemon: header[3] != "", Stack: []string{}}
	var err error
	if thread.ID, err = strconv.ParseInt(header[2], 10, 64); err != nil {
		return thread, fmt.Errorf("thread %q id: %w", header[1], err)
	}
	if header[4] != "" {
		if thread.Priority, err = strconv.Atoi(header[4]); err != nil {
			return thread, fmt.Errorf("thread %q priority: %w", header[1], err)
		}
	}
	return thread, nil
}

// parseLine files one line of a thread block, reporting whether the
// synchronizer list has started.
func (t *DumpedThread) parseLine(line string, synchronizers bool) (bool, error) {
	switch {
	case strings.HasPrefix(line, stateLine):
		t.State = strings.TrimPrefix(line, stateLine)
	case line == synchronizerLine:
		return true, nil
	case synchronizers && line == "\t- None":
	case synchronizers && strings.HasPrefix(line, "\t- "):
		t.Synchronizers = append(t.Synchronizers, strings.TrimPrefix(line, "\t- "))
	case !synchronizers && (strings.HasPrefix(line, "\tat ") || strings.HasPrefix(line, "\t- ")):
		t.Stack = append(t.Stack, strings.TrimPrefix(line, "\t"))
	default:
		return synchronizers, fmt.Errorf("unexpected line in thread %q #%d: %q", t.Name, t.ID, line)
	}
	return synchronizers, nil
}

// Pretty is a heading over each thread, then the deadlock report in red.
func (d ThreadDump) Pretty() api.Text {
	text := api.Text{}.Append("Thread dump", "font-bold").Append(fmt.Sprintf(" %d threads", len(d.Threads)), "text-muted")
	if len(d.Deadlocks) > 0 {
		text = text.Append(" · DEADLOCK", "text-red-600")
	}
	for _, thread := range d.Threads {
		text = text.NewLine().Add(thread.Pretty())
	}
	for _, line := range d.Deadlocks {
		text = text.NewLine().Append(line, "text-red-600")
	}
	return text
}

// Pretty is the thread's header over its indented frames, locks muted.
func (t DumpedThread) Pretty() api.Text {
	return t.render("")
}

func (t DumpedThread) render(suffix string) api.Text {
	facts := []string{fmt.Sprintf("#%d", t.ID)}
	if t.Daemon {
		facts = append(facts, "daemon")
	}
	if t.Priority > 0 {
		facts = append(facts, fmt.Sprintf("prio %d", t.Priority))
	}
	text := api.Text{}.Append(t.Name, "font-bold").Append(" "+strings.Join(facts, " · ")+" · ", "text-muted").
		Append(t.State, stateClass(t.State)).Append(suffix, "text-muted")
	for _, line := range t.Stack {
		class := ""
		if strings.HasPrefix(line, "- ") {
			class = "text-muted"
		}
		text = text.NewLine().Append("  "+line, class)
	}
	if len(t.Synchronizers) > 0 {
		text = text.NewLine().Append("  locked synchronizers: "+strings.Join(t.Synchronizers, ", "), "text-muted")
	}
	return text
}
