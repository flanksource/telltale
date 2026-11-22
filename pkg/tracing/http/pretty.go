package http

import (
	"encoding/json"
	"net/url"
	"strings"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/commons/har/harpretty"
)

func DirectionArrow(direction Direction) string {
	switch direction {
	case Incoming:
		return "←"
	case Outgoing:
		return "→"
	default:
		return ""
	}
}

func (e Exchange) Pretty() api.Text {
	entry := harpretty.Entry(e.Entry)
	var t api.Text
	if e.Protocol == "" {
		t = entry.Pretty()
	} else {
		operation := e.Operation
		if operation == "" {
			operation = e.Request.Method
		}
		if e.Protocol == "kubernetes" {
			operation = strings.ToUpper(operation)
		}
		entry.Request.Method, entry.Request.URL = "", e.decodedPath()
		t = clicky.Text(e.Protocol, "text-muted").
			Space().Append(operation, "font-bold text-green-500").Add(entry.Pretty())
	}
	if arrow := DirectionArrow(e.Direction); arrow != "" {
		t = t.Prefix(arrow + " ")
	}
	if e.Error != "" {
		t = t.Space().Append(e.Error, "text-red-500")
	}
	return t
}

func (e Exchange) decodedPath() string {
	if decoded := e.Decoded; decoded != nil {
		switch {
		case decoded.Git != nil && decoded.Repository != "":
			return (&url.URL{Path: "/" + decoded.Repository}).EscapedPath()
		case decoded.Bucket != "":
			bucket := "/" + url.PathEscape(decoded.Bucket)
			if decoded.ObjectKey == "" {
				return bucket
			}
			return bucket + (&url.URL{Path: "/" + decoded.ObjectKey}).EscapedPath()
		case decoded.Kubernetes != nil && decoded.Kubernetes.Resource != "":
			resource := decoded.Kubernetes
			var parts []string
			for _, segment := range []string{resource.Namespace, resource.Resource, resource.Name, resource.Subresource} {
				if segment != "" {
					parts = append(parts, url.PathEscape(segment))
				}
			}
			return "/" + strings.Join(parts, "/")
		}
	}
	if address, err := url.Parse(e.Request.URL); err == nil {
		if path := address.EscapedPath(); path != "" {
			return path
		}
	}
	return "/"
}

func (e Exchange) PrettyFull() api.Textable {
	t := e.Pretty()
	if e.Decoded != nil {
		if body, err := json.MarshalIndent(e.Decoded, "", "  "); err == nil {
			t = t.NewLine().Add(clicky.CodeBlock("json", string(body)))
		}
	}
	if detail := harpretty.Entry(e.Entry).RowDetail(); detail != nil {
		t = t.NewLine().Add(detail)
	}
	return t
}
