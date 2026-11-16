package jvmruntime

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/flanksource/clicky/api"
)

// StatusOp reports whether the agent allows mutation, whether instrumentation
// is installed and, when it is, every class the agent has patched.
const StatusOp = "jvm.status"

// Status is the agent's jvm.status. Patches is nil when instrumentation is
// off: the agent cannot know about patches then, which is not "no patches".
type Status struct {
	AllowMutation   bool    `json:"allowMutation"`
	Instrumentation bool    `json:"instrumentation"`
	Patches         []Patch `json:"patches"`
}

// Patch is one class whose original bytecode the agent holds. LeaseExpiresAt
// is epoch millis when the agent will restore it; 0 is a patch nothing will undo.
type Patch struct {
	Class          string `json:"class"`
	Loader         string `json:"loader"`
	Digest         string `json:"digest,omitempty"`
	OriginalDigest string `json:"originalDigest,omitempty"`
	Patched        bool   `json:"patched"`
	LeaseExpiresAt int64  `json:"leaseExpiresAt,omitempty"`
	Error          string `json:"error,omitempty"`
}

// DecodeStatus decodes the agent's status, refusing one that leaves out what
// the agent always reports.
func DecodeStatus(raw json.RawMessage) (Status, error) {
	status, err := decodeObject[Status](StatusOp, raw, "allowMutation", "instrumentation")
	if err == nil && status.Instrumentation && status.Patches == nil {
		err = fmt.Errorf("%s reported instrumentation without its patches: %s", StatusOp, raw)
	}
	return status, err
}

// Pretty is the status as one line, with any patches listed compactly.
func (s Status) Pretty() api.Text {
	text := api.Text{}.Append("mutations allowed", "text-green-600")
	if !s.AllowMutation {
		text = api.Text{}.Append("mutations denied", "text-yellow-600")
	}
	text = text.Append(" · ")
	if !s.Instrumentation {
		return text.Append("instrumentation off", "text-red-600")
	}
	noun := "patches"
	if len(s.Patches) == 1 {
		noun = "patch"
	}
	text = text.Append("instrumentation on", "text-green-600").Append(fmt.Sprintf(" · %d %s", len(s.Patches), noun), "text-muted")
	separator := ": "
	for _, patch := range s.Patches {
		text = text.Append(separator).Add(patch.Pretty())
		separator = ", "
	}
	return text
}

// Pretty is the patched class and whether, and when, it will be restored.
func (p Patch) Pretty() api.Text {
	text := api.Text{}.Append(p.Class, "font-bold")
	switch {
	case p.Error != "":
		return text.Append(" "+p.Error, "text-red-600")
	case !p.Patched:
		return text.Append(" restored", "text-muted")
	case p.LeaseExpiresAt == 0:
		return text.Append(" no lease", "text-yellow-600")
	default:
		return text.Append(" lease until "+time.UnixMilli(p.LeaseExpiresAt).UTC().Format(time.RFC3339), "text-muted")
	}
}
