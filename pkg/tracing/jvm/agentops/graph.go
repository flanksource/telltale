package agentops

import (
	"fmt"

	"github.com/flanksource/clicky/api"
)

// Node is one Java value as the agent's object graph renders it: a scalar,
// map, list, bean, identity, opaque or error. ID and Path are unset in a probe
// snapshot; Raw carries an error node's reason.
type Node struct {
	ID         string `json:"id,omitempty"`
	Label      string `json:"label,omitempty"`
	Kind       string `json:"kind"`
	Path       string `json:"path,omitempty"`
	Expandable bool   `json:"expandable,omitempty"`
	Type       string `json:"type,omitempty"`
	Value      string `json:"value,omitempty"`
	Raw        string `json:"raw,omitempty"`
	Children   []Node `json:"children,omitempty"`
}

// compact is a scalar as itself, a string quoted, and anything else by its
// short type with its value or member count.
func (n Node) compact() string {
	switch {
	case n.Kind == "error":
		return "✗ " + shortError(n.Raw)
	case n.Kind == "scalar" && n.Type == "java.lang.String":
		return fmt.Sprintf("%q", n.Value)
	case n.Kind == "scalar" && n.Value == "":
		return "null"
	case n.Kind == "scalar":
		return n.Value
	case n.Value != "":
		return shortType(n.Type) + "{" + n.Value + "}"
	}
	return fmt.Sprintf("%s{%d}", shortType(n.Type), len(n.Children))
}

// tree is the node's children below it, one "label: Type = value" line each,
// nested members indented under their parent.
func (n Node) tree(indent string) api.Text {
	var lines api.Text
	for i, child := range n.Children {
		mark, next := "├─ ", "│  "
		if i == len(n.Children)-1 {
			mark, next = "└─ ", "   "
		}
		lines = lines.NewLine().Append(indent+mark, "text-muted").Append(child.line()).Add(child.tree(indent + next))
	}
	return lines
}

func (n Node) line() api.Text {
	line := api.Text{}.Append(n.Label, "font-bold")
	if n.Type != "" {
		line = line.Append(": " + shortType(n.Type))
	}
	switch {
	case n.Kind == "error":
		return line.Append(" ✗ "+shortError(n.Raw), "text-red-600")
	case n.Kind == "scalar":
		return line.Append(" = " + n.compact())
	case n.Value != "":
		return line.Append(" = "+n.Value, "text-muted")
	}
	return line
}
