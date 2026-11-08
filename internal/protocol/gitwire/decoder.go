package gitwire

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var ErrIncomplete = errors.New("incomplete git packet")

type Event struct {
	Kind  string `json:"kind"`
	Text  string `json:"text,omitempty"`
	Bytes int    `json:"bytes,omitempty"`
}

type Decoder struct {
	Emit func(Event)
	buf  []byte
	pack bool
	err  error
}

func (d *Decoder) Write(p []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}
	for consumed := 0; consumed < len(p); {
		if d.pack {
			d.emit(Event{Kind: "pack", Bytes: len(p) - consumed})
			return len(p), nil
		}
		need := 4
		if len(d.buf) >= 4 {
			n, err := strconv.ParseUint(string(d.buf[:4]), 16, 16)
			if err != nil || n < 4 || n > 65520 {
				d.err = fmt.Errorf("invalid git packet length %q", d.buf[:4])
				return consumed, d.err
			}
			need = int(n)
		}
		take := min(need-len(d.buf), len(p)-consumed)
		d.buf = append(d.buf, p[consumed:consumed+take]...)
		consumed += take
		if len(d.buf) < need {
			continue
		}
		if need == 4 {
			switch string(d.buf) {
			case "PACK":
				d.pack = true
				d.emit(Event{Kind: "pack", Bytes: 4})
				d.buf = nil
			case "0000":
				d.emit(Event{Kind: "flush"})
				d.buf = nil
			case "0001":
				d.emit(Event{Kind: "delimiter"})
				d.buf = nil
			case "0002":
				d.emit(Event{Kind: "response-end"})
				d.buf = nil
			default:
				n, err := strconv.ParseUint(string(d.buf), 16, 16)
				if err != nil || n < 4 || n > 65520 {
					d.err = fmt.Errorf("invalid git packet length %q", d.buf)
					return consumed, d.err
				}
				if n == 4 {
					d.emit(Event{Kind: "data"})
					d.buf = nil
				}
			}
			continue
		}
		d.decode(d.buf[4:])
		d.buf = nil
	}
	return len(p), nil
}

func (d *Decoder) Close() error {
	if d.err != nil {
		return d.err
	}
	if len(d.buf) != 0 {
		return ErrIncomplete
	}
	return nil
}

func (d *Decoder) emit(e Event) {
	if d.Emit != nil {
		d.Emit(e)
	}
}

func (d *Decoder) decode(p []byte) {
	if len(p) == 0 {
		d.emit(Event{Kind: "data"})
		return
	}
	if p[0] >= 1 && p[0] <= 3 {
		if p[0] == 1 {
			d.emit(Event{Kind: "pack", Bytes: len(p) - 1})
			return
		}
		kind := "progress"
		if p[0] == 3 {
			kind = "error"
		}
		d.emit(Event{Kind: kind, Text: strings.TrimSuffix(string(p[1:]), "\n")})
		return
	}
	text := strings.TrimSuffix(string(p), "\n")
	line, caps, hasCaps := strings.Cut(text, "\x00")
	kind := "data"
	switch {
	case strings.HasPrefix(line, "version "):
		kind, line = "version", strings.TrimPrefix(line, "version ")
	case strings.HasPrefix(line, "command="):
		kind, line = "command", strings.TrimPrefix(line, "command=")
	case strings.HasPrefix(line, "ERR "):
		kind, line = "error", strings.TrimPrefix(line, "ERR ")
	case strings.HasPrefix(line, "want "):
		kind = "want"
	case strings.HasPrefix(line, "have "):
		kind = "have"
	case strings.HasPrefix(line, "ACK ") || line == "NAK":
		kind = "ack"
	case strings.HasPrefix(line, "shallow ") || strings.HasPrefix(line, "unshallow "):
		kind = "shallow"
	case strings.HasPrefix(line, "ok ") || strings.HasPrefix(line, "ng ") || strings.HasPrefix(line, "unpack "):
		kind = "status"
	default:
		fields := strings.Fields(line)
		if len(fields) >= 2 && objectID(fields[0]) {
			kind = "ref"
			if objectID(fields[1]) {
				kind = "update"
			}
		}
	}
	d.emit(Event{Kind: kind, Text: line})
	if hasCaps {
		d.emit(Event{Kind: "capabilities", Text: caps})
	}
}

func objectID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
