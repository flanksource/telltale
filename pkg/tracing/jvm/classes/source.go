package classes

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
)

// Source is a class decompiled from the bytes it is running. SourceLineNumbers
// gives the original line behind each decompiled row (0 where none maps);
// LineRows is the reverse, original line to its first decompiled row.
type Source struct {
	Class             string         `json:"class"`
	Loader            string         `json:"loader"`
	Source            string         `json:"source"`
	Methods           []SourceMethod `json:"methods"`
	SourceLineNumbers []int          `json:"sourceLineNumbers"`
	LineRows          map[int]int    `json:"lineRows"`
	Digest            string         `json:"digest"`
	Decompiler        string         `json:"decompiler"`
	DurationMs        int64          `json:"durationMs"`
	Cached            bool           `json:"cached"`
}

// SourceMethod is where one method's body sits in the decompiled source, 1-based.
type SourceMethod struct {
	Name       string `json:"name"`
	Descriptor string `json:"descriptor"`
	Line       int    `json:"line"`
	EndLine    int    `json:"endLine"`
}

func DecodeSource(raw json.RawMessage) (Source, error) {
	var source Source
	if err := decodeObject(SourceOp, raw, &source, "class", "loader", "source", "decompiler"); err != nil {
		return source, err
	}
	if source.Source == "" {
		return source, fmt.Errorf("%s returned no source for %s", SourceOp, source.Class)
	}
	return source, nil
}

func (s Source) Pretty() api.Text {
	detail := fmt.Sprintf(" · loader %s · %s · %dms · %s", s.Loader, s.Decompiler, s.DurationMs, plural(len(s.Methods), "method"))
	if s.Cached {
		detail += " · cached"
	}
	return api.Text{}.Append(s.Class, "font-bold").Append(detail, "text-muted").
		NewLine().Add(clicky.CodeBlock("java", s.Source))
}

// Bytecode is the class file a class is running; it travels as base64.
type Bytecode struct {
	Bytecode []byte `json:"bytecode"`
}

var classMagic = []byte{0xca, 0xfe, 0xba, 0xbe}

func DecodeBytecode(raw json.RawMessage) (Bytecode, error) {
	var bytecode Bytecode
	if err := decodeObject(BytecodeOp, raw, &bytecode, "bytecode"); err != nil {
		return bytecode, err
	}
	if len(bytecode.Bytecode) < 8 || !bytes.HasPrefix(bytecode.Bytecode, classMagic) {
		return bytecode, fmt.Errorf("%s returned %d bytes that are not a class file", BytecodeOp, len(bytecode.Bytecode))
	}
	return bytecode, nil
}

func (b Bytecode) Pretty() api.Text {
	minor := binary.BigEndian.Uint16(b.Bytecode[4:6])
	major := binary.BigEndian.Uint16(b.Bytecode[6:8])
	return api.Text{}.Append("Bytecode", "font-bold").
		Append(fmt.Sprintf(" %s · class file %d.%d (Java %d)", api.HumanizeBytes(int64(len(b.Bytecode))).String(), major, minor, int(major)-44), "text-muted").
		NewLine().Add(clicky.CodeBlock("hexdump", hex.Dump(b.Bytecode)))
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	if strings.HasSuffix(noun, "s") {
		return fmt.Sprintf("%d %ses", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
