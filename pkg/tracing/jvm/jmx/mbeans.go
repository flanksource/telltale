package jmx

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/flanksource/clicky/api"
)

// MBeanNames are the canonical object names jvm.jmx.search matched, in the agent's order.
type MBeanNames []string

func DecodeSearch(raw json.RawMessage) (MBeanNames, error) {
	var names MBeanNames
	if err := json.Unmarshal(raw, &names); err != nil {
		return nil, fmt.Errorf("decode %s result: %w", SearchOp, err)
	}
	if names == nil {
		return nil, fmt.Errorf("%s returned no MBean list: %s", SearchOp, raw)
	}
	return names, nil
}

func (n MBeanNames) Pretty() api.Text {
	if len(n) == 0 {
		return api.Text{}.Append("no MBeans matched", "text-muted")
	}
	text := api.Text{}.Append("MBeans", "font-bold").Append(fmt.Sprintf(" %d matched", len(n)), "text-muted")
	for _, name := range slices.Sorted(slices.Values(n)) {
		domain, properties, _ := strings.Cut(name, ":")
		text = text.NewLine().Append(domain, "font-bold").Append(":" + properties)
	}
	return text
}

// MBeanInfo is an MBean's management interface as jvm.jmx.info lowers it.
type MBeanInfo struct {
	ObjectName    string              `json:"objectName"`
	ClassName     string              `json:"className"`
	Description   string              `json:"description"`
	Attributes    []MBeanAttribute    `json:"attributes"`
	Operations    []MBeanOperation    `json:"operations"`
	Notifications []MBeanNotification `json:"notifications"`
}

type MBeanAttribute struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Readable    bool   `json:"readable"`
	Writable    bool   `json:"writable"`
	Is          bool   `json:"is"`
}

// MBeanOperation's Impact is the JMX constant: 0 INFO, 1 ACTION, 2 ACTION_INFO, 3 UNKNOWN.
type MBeanOperation struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	ReturnType  string           `json:"returnType"`
	Impact      int              `json:"impact"`
	Signature   []MBeanParameter `json:"signature"`
}

type MBeanParameter struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
}

type MBeanNotification struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Types       []string `json:"types"`
}

func DecodeInfo(raw json.RawMessage) (MBeanInfo, error) {
	var info MBeanInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return info, fmt.Errorf("decode %s result: %w", InfoOp, err)
	}
	if info.ObjectName == "" || info.ClassName == "" || info.Attributes == nil || info.Operations == nil || info.Notifications == nil {
		return info, fmt.Errorf("%s returned no MBean info: %s", InfoOp, raw)
	}
	return info, nil
}

func (MBeanAttribute) Columns() []api.ColumnDef {
	return []api.ColumnDef{
		api.Column("name").Label("Attribute").Build(),
		api.Column("type").Label("Type").Build(),
		api.Column("access").Label("Access").Build(),
		api.Column("description").Label("Description").MaxWidth(80).Build(),
	}
}

// Row leaves out a description that only repeats the attribute's name, as the platform MBeans' do.
func (a MBeanAttribute) Row() map[string]any {
	access := map[[2]bool]string{{true, true}: "read-write", {true, false}: "read-only", {false, true}: "write-only", {false, false}: "none"}
	description := a.Description
	if description == a.Name {
		description = ""
	}
	return map[string]any{"name": a.Name, "type": javaType(a.Type), "access": access[[2]bool{a.Readable, a.Writable}], "description": description}
}

func (o MBeanOperation) signature() string {
	types := make([]string, len(o.Signature))
	for i, parameter := range o.Signature {
		types[i] = javaType(parameter.Type)
	}
	return fmt.Sprintf("%s(%s) → %s", o.Name, strings.Join(types, ", "), javaType(o.ReturnType))
}

func (i MBeanInfo) Pretty() api.Text {
	text := api.Text{}.Append(i.ObjectName, "font-bold").Append(" "+strings.Join(nonEmpty(i.ClassName, i.Description), " · "), "text-muted")
	if len(i.Attributes) > 0 {
		text = text.NewLine().Add(api.NewTableFrom(i.Attributes))
	}
	if len(i.Operations) > 0 {
		text = text.NewLine().Append("Operations", "font-bold")
		for _, operation := range i.Operations {
			text = text.NewLine().Append(operation.signature())
		}
	}
	if len(i.Notifications) > 0 {
		text = text.NewLine().Append("Notifications", "font-bold")
		for _, notification := range i.Notifications {
			text = text.NewLine().Append(notification.Description).Append(" "+strings.Join(notification.Types, ", "), "text-muted")
		}
	}
	return text
}

func nonEmpty(values ...string) []string {
	return slices.DeleteFunc(values, func(value string) bool { return value == "" })
}
