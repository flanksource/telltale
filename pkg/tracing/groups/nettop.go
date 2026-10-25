package groups

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// ErrNetUnsupported refuses network byte counting where nettop is missing:
// only darwin counts each process's network bytes without privileges.
var ErrNetUnsupported = errors.New("per-process network bytes are read with nettop, which only darwin has")

// NetBytes are bytes a process received and sent.
type NetBytes struct {
	Rx, Tx uint64
}

// nettopHeader starts each block nettop prints.
const nettopHeader = ",bytes_in,bytes_out,"

// ParseNettop reads `nettop -P -x -J bytes_in,bytes_out` output, handing
// each block of rows, by pid, to block as the next one starts or the output
// ends. A row it cannot read fails the parse.
func ParseNettop(r io.Reader, block func(map[int]NetBytes)) error {
	scanner := bufio.NewScanner(r)
	var current map[int]NetBytes
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case line == "":
			continue
		case line == nettopHeader:
			if current != nil {
				block(current)
			}
			current = map[int]NetBytes{}
			continue
		case current == nil:
			return fmt.Errorf("nettop row %q before its header", line)
		}
		pid, bytes, err := parseNettopRow(line)
		if err != nil {
			return err
		}
		current[pid] = bytes
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read nettop: %w", err)
	}
	if current != nil {
		block(current)
	}
	return nil
}

// parseNettopRow reads `<name>.<pid>,<bytes in>,<bytes out>,`.
func parseNettopRow(line string) (int, NetBytes, error) {
	fields := strings.Split(strings.TrimSuffix(line, ","), ",")
	dot := strings.LastIndex(fields[0], ".")
	if len(fields) != 3 || dot < 0 {
		return 0, NetBytes{}, fmt.Errorf("nettop row %q is not <name>.<pid>,<bytes in>,<bytes out>", line)
	}
	pid, pidErr := strconv.Atoi(fields[0][dot+1:])
	rx, rxErr := strconv.ParseUint(fields[1], 10, 64)
	tx, txErr := strconv.ParseUint(fields[2], 10, 64)
	if err := errors.Join(pidErr, rxErr, txErr); err != nil {
		return 0, NetBytes{}, fmt.Errorf("nettop row %q: %w", line, err)
	}
	return pid, NetBytes{Rx: rx, Tx: tx}, nil
}

// NetTotals adds nettop's delta blocks into each pid's bytes since the
// first: that block counts flows opened before the monitor started, so it is
// only the baseline.
type NetTotals struct {
	mu     sync.Mutex
	seen   bool
	ready  chan struct{}
	totals map[int]NetBytes
}

func NewNetTotals() *NetTotals {
	return &NetTotals{totals: map[int]NetBytes{}, ready: make(chan struct{})}
}

// Add takes the next delta block.
func (t *NetTotals) Add(block map[int]NetBytes) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for pid, delta := range block {
		total := t.totals[pid]
		if t.seen {
			total.Rx, total.Tx = total.Rx+delta.Rx, total.Tx+delta.Tx
		}
		t.totals[pid] = total
	}
	if t.seen && !t.isReady() {
		close(t.ready)
	}
	t.seen = true
}

func (t *NetTotals) isReady() bool {
	select {
	case <-t.ready:
		return true
	default:
		return false
	}
}

// Ready is closed once a delta block past the baseline has been added.
func (t *NetTotals) Ready() <-chan struct{} { return t.ready }

// Counters are each pid's bytes so far; nil until Ready, as until then they
// count nothing.
func (t *NetTotals) Counters() map[int]NetBytes {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.isReady() {
		return nil
	}
	counters := make(map[int]NetBytes, len(t.totals))
	for pid, total := range t.totals {
		counters[pid] = total
	}
	return counters
}
