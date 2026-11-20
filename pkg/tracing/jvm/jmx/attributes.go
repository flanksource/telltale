package jmx

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/flanksource/clicky/api"
)

// MBeanAttributes is every readable attribute of one MBean, as jvm.jmx.read returns it
// when no attribute is named.
type MBeanAttributes struct {
	ObjectName string           `json:"objectName"`
	Attributes []AttributeValue `json:"attributes"`
}

type AttributeValue struct {
	Name  string `json:"name"`
	Value Value  `json:"value"`
}

// DecodeRead decodes either shape jvm.jmx.read returns: an MBean's attributes when no
// attribute was named, otherwise the named attribute's value verbatim.
func DecodeRead(raw json.RawMessage) (any, error) {
	value, err := decodeValue(ReadOp, raw)
	if err != nil {
		return nil, err
	}
	var shape map[string]json.RawMessage
	if json.Unmarshal(raw, &shape) != nil || shape["objectName"] == nil || shape["attributes"] == nil {
		return value, nil
	}
	var all MBeanAttributes
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, fmt.Errorf("decode %s result: %w", ReadOp, err)
	}
	if all.ObjectName == "" || all.Attributes == nil {
		return nil, fmt.Errorf("%s returned no attributes: %s", ReadOp, raw)
	}
	return all, nil
}

func (a MBeanAttributes) Pretty() api.Text {
	text := api.Text{}.Append(a.ObjectName, "font-bold").Append(fmt.Sprintf(" %d attributes", len(a.Attributes)), "text-muted")
	for _, attribute := range a.Attributes {
		text = text.NewLine().Append(attribute.Name, "font-bold").Append(" = " + attribute.Value.String())
	}
	return text
}

// Invocation is what an MBean operation returned; a void operation returns
// null. Requested names the MBean and operation, which the agent does not echo.
type Invocation struct {
	Result     Value
	ObjectName string
	Operation  string
}

func (i Invocation) MarshalJSON() ([]byte, error) { return i.Result.MarshalJSON() }

func DecodeInvoke(raw json.RawMessage) (Invocation, error) {
	value, err := decodeValue(InvokeOp, raw)
	return Invocation{Result: value}, err
}

func (i Invocation) Requested(params json.RawMessage) (any, error) {
	var request struct {
		ObjectName string `json:"objectName"`
		Operation  string `json:"operation"`
	}
	if err := requiredParams(InvokeOp, params, &request, "objectName", "operation"); err != nil {
		return nil, err
	}
	i.ObjectName, i.Operation = request.ObjectName, request.Operation
	return i, nil
}

func (i Invocation) Pretty() api.Text {
	text := api.Text{}.Append("✓ invoked", "text-green-600")
	if i.Operation != "" {
		text = text.Append(" "+i.ObjectName, "text-muted").Append(" "+i.Operation, "font-bold")
	}
	if i.Result.IsNull() {
		return text
	}
	return text.Append(" → " + i.Result.String())
}

// Applied is the agent's acknowledgement of a change it made.
type Applied struct {
	Applied bool `json:"applied"`
}

// AttributeWritten acknowledges jvm.jmx.write; Requested names the attribute
// and the value written, which the agent does not echo.
type AttributeWritten struct {
	Applied
	ObjectName string          `json:"-"`
	Attribute  string          `json:"-"`
	Value      json.RawMessage `json:"-"`
}

// LevelSet acknowledges jvm.logging.set; Requested names the logger and its
// new level, null for one that inherits.
type LevelSet struct {
	Applied
	Name  *string `json:"-"`
	Level *string `json:"-"`
}

func DecodeWrite(raw json.RawMessage) (AttributeWritten, error) {
	applied, err := decodeApplied(WriteOp, raw)
	return AttributeWritten{Applied: applied}, err
}

func DecodeLevelSet(raw json.RawMessage) (LevelSet, error) {
	applied, err := decodeApplied(LoggingSetOp, raw)
	return LevelSet{Applied: applied}, err
}

func (w AttributeWritten) Requested(params json.RawMessage) (any, error) {
	var request struct {
		ObjectName string          `json:"objectName"`
		Attribute  string          `json:"attribute"`
		Value      json.RawMessage `json:"value"`
	}
	if err := requiredParams(WriteOp, params, &request, "objectName", "attribute"); err != nil {
		return nil, err
	}
	w.ObjectName, w.Attribute, w.Value = request.ObjectName, request.Attribute, request.Value
	return w, nil
}

func (s LevelSet) Requested(params json.RawMessage) (any, error) {
	var request struct {
		Name  *string `json:"name"`
		Level *string `json:"level"`
	}
	if err := requiredParams(LoggingSetOp, params, &request, "name"); err != nil {
		return nil, err
	}
	s.Name, s.Level = request.Name, request.Level
	return s, nil
}

// requiredParams decodes a request's params, refusing one without the named keys.
func requiredParams(op string, params json.RawMessage, into any, keys ...string) error {
	var present map[string]json.RawMessage
	if err := json.Unmarshal(params, &present); err != nil {
		return fmt.Errorf("%s params: %w", op, err)
	}
	for _, key := range keys {
		if _, ok := present[key]; !ok {
			return fmt.Errorf("%s params have no %s", op, key)
		}
	}
	return json.Unmarshal(params, into)
}

func decodeApplied(op string, raw json.RawMessage) (Applied, error) {
	var applied Applied
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&applied); err != nil {
		return applied, fmt.Errorf("decode %s result: %w", op, err)
	}
	if !applied.Applied {
		return applied, fmt.Errorf("%s was not applied: %s", op, raw)
	}
	return applied, nil
}

func (w AttributeWritten) Pretty() api.Text {
	if w.Attribute == "" {
		return api.Text{}.Append("✓ attribute written", "text-green-600")
	}
	value := string(w.Value)
	if len(w.Value) == 0 {
		value = "null"
	}
	return api.Text{}.Append("✓ ", "text-green-600").Append(w.ObjectName+" ", "text-muted").Append(w.Attribute, "font-bold").Append(" = " + value)
}

func (s LevelSet) Pretty() api.Text {
	if s.Name == nil {
		return api.Text{}.Append("✓ logger level set", "text-green-600")
	}
	name, level := *s.Name, "inherit"
	if name == "" {
		name = "(root)"
	}
	if s.Level != nil {
		level = *s.Level
	}
	return api.Text{}.Append("✓ logger ", "text-green-600").Append(name, "font-bold").Append(" → " + level)
}
