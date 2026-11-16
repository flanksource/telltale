// Package jmx holds the typed results of the Java agent's JMX, notification and logging operations.
package jmx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/flanksource/clicky/api"
)

const (
	SearchOp            = "jvm.jmx.search"
	InfoOp              = "jvm.jmx.info"
	ReadOp              = "jvm.jmx.read"
	WriteOp             = "jvm.jmx.write"
	InvokeOp            = "jvm.jmx.invoke"
	NotificationsListOp = "jvm.notifications.list"
	LoggingListOp       = "jvm.logging.list"
	LoggingSetOp        = "jvm.logging.set"
)

// Results decode this package's operation results into their typed models.
var Results = map[string]func(json.RawMessage) (any, error){
	SearchOp:            func(raw json.RawMessage) (any, error) { return DecodeSearch(raw) },
	InfoOp:              func(raw json.RawMessage) (any, error) { return DecodeInfo(raw) },
	ReadOp:              func(raw json.RawMessage) (any, error) { return DecodeRead(raw) },
	WriteOp:             func(raw json.RawMessage) (any, error) { return DecodeWrite(raw) },
	InvokeOp:            func(raw json.RawMessage) (any, error) { return DecodeInvoke(raw) },
	NotificationsListOp: func(raw json.RawMessage) (any, error) { return DecodeSubscriptions(raw) },
	LoggingListOp:       func(raw json.RawMessage) (any, error) { return DecodeLoggers(raw) },
	LoggingSetOp:        func(raw json.RawMessage) (any, error) { return DecodeLevelSet(raw) },
}

// Value is a JMX value as the agent lowered it to JSON, kept verbatim.
type Value json.RawMessage

func (v Value) MarshalJSON() ([]byte, error) { return json.RawMessage(v).MarshalJSON() }

func (v *Value) UnmarshalJSON(raw []byte) error {
	*v = append((*v)[:0], raw...)
	return nil
}

func (v Value) IsNull() bool { return string(bytes.TrimSpace(v)) == "null" }

// String is the value on one line: strings unquoted, everything else as compact JSON.
func (v Value) String() string {
	var text string
	if json.Unmarshal(v, &text) == nil {
		return text
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, v); err != nil {
		return string(v)
	}
	return compact.String()
}

func (v Value) Pretty() api.Text { return api.Text{}.Append(v.String()) }

func decodeValue(op string, raw json.RawMessage) (Value, error) {
	if !json.Valid(raw) {
		return nil, fmt.Errorf("%s returned invalid JSON: %q", op, raw)
	}
	return Value(bytes.TrimSpace(raw)), nil
}

var primitiveTypes = map[byte]string{'Z': "boolean", 'B': "byte", 'C': "char", 'S': "short", 'I': "int", 'J': "long", 'F': "float", 'D': "double"}

// javaType reads a JVM type name (including descriptors such as [J) as a simple Java type.
func javaType(name string) string {
	base := strings.TrimLeft(name, "[")
	dims := len(name) - len(base)
	switch {
	case dims > 0 && len(base) == 1 && primitiveTypes[base[0]] != "":
		base = primitiveTypes[base[0]]
	case dims > 0 && strings.HasPrefix(base, "L") && strings.HasSuffix(base, ";"):
		base = base[1 : len(base)-1]
	}
	return base[strings.LastIndex(base, ".")+1:] + strings.Repeat("[]", dims)
}
