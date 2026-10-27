package rules

import (
	"time"

	"github.com/flanksource/telltale/pkg/tracing/groups"
)

type Resources struct {
	Cores *float64 `json:"cores" cel:"cores"`
	CPU   *float64 `json:"cpu" cel:"cpu"`
	RSS   int64    `json:"rss" cel:"rss"`
	Mem   float64  `json:"mem" cel:"mem"`
}
type Host struct {
	Cores            float64 `json:"cores" cel:"cores"`
	MemoryTotalBytes int64   `json:"memoryTotalBytes" cel:"memoryTotalBytes"`
}
type ProcessRef struct {
	Key     string         `json:"key" cel:"key"`
	PID     int            `json:"pid" cel:"pid"`
	PPID    int            `json:"ppid" cel:"ppid"`
	Name    string         `json:"name" cel:"name"`
	Command string         `json:"command" cel:"command"`
	Context groups.Context `json:"context" cel:"context"`
	Resources
	GroupName string   `json:"groupName" cel:"groupName"`
	GroupRoot bool     `json:"groupRoot" cel:"groupRoot"`
	GroupPath []string `json:"groupPath" cel:"groupPath"`
}
type SessionRef struct {
	Error     string   `json:"error,omitempty" cel:"error"`
	Key       string   `json:"key" cel:"key"`
	Agent     string   `json:"agent" cel:"agent"`
	ID        string   `json:"id" cel:"id"`
	Workspace string   `json:"workspace" cel:"workspace"`
	Surface   string   `json:"surface" cel:"surface"`
	Anchors   []string `json:"anchors" cel:"anchors"`
}
type MonitorGroup struct {
	Key      string         `json:"key" cel:"key"`
	Name     string         `json:"name" cel:"name"`
	Kind     string         `json:"kind" cel:"kind"`
	Context  groups.Context `json:"context" cel:"context"`
	Members  []ProcessRef   `json:"members" cel:"members"`
	Sessions []SessionRef   `json:"sessions" cel:"sessions"`
	Resources
}
type Config struct {
	Version int    `json:"version" yaml:"version"`
	Rules   []Rule `json:"rules" yaml:"rules"`
}
type Rule struct {
	Name       string `json:"name" yaml:"name"`
	GroupBy    string `json:"groupBy" yaml:"groupBy"`
	Match      string `json:"match" yaml:"match"`
	For        string `json:"for" yaml:"for"`
	Recover    string `json:"recover,omitempty" yaml:"recover,omitempty"`
	RecoverFor string `json:"recoverFor" yaml:"recoverFor"`
	Steps      []Step `json:"steps" yaml:"steps"`
}
type Step struct {
	Wait string   `json:"wait" yaml:"wait"`
	Hook HookSpec `json:"hook" yaml:"hook"`
}
type HookSpec struct {
	Type     string   `json:"type" yaml:"type"`
	Targets  string   `json:"targets" yaml:"targets"`
	Nice     *int     `json:"nice,omitempty" yaml:"nice,omitempty"`
	Signal   string   `json:"signal,omitempty" yaml:"signal,omitempty"`
	Via      string   `json:"via,omitempty" yaml:"via,omitempty"`
	Key      string   `json:"key,omitempty" yaml:"key,omitempty"`
	Endpoint string   `json:"endpoint,omitempty" yaml:"endpoint,omitempty"`
	Command  []string `json:"command,omitempty" yaml:"command,omitempty"`
	Timeout  string   `json:"timeout,omitempty" yaml:"timeout,omitempty"`
}
type Incident struct {
	ID           string    `json:"id" cel:"id"`
	Rule         string    `json:"rule" cel:"rule"`
	Group        string    `json:"group" cel:"group"`
	Revision     string    `json:"revision" cel:"revision"`
	State        string    `json:"state" cel:"state"`
	Step         int       `json:"step" cel:"step"`
	Since        time.Time `json:"since" cel:"since"`
	ReadyAt      time.Time `json:"readyAt" cel:"readyAt"`
	RecoveringAt time.Time `json:"recoveringAt,omitempty" cel:"recoveringAt"`
	UpdatedAt    time.Time `json:"updatedAt" cel:"updatedAt"`
	Error        string    `json:"error,omitempty" cel:"error"`
}
type Targets struct {
	Processes []ProcessRef `json:"processes,omitempty"`
	Sessions  []SessionRef `json:"sessions,omitempty"`
}
type Decision struct {
	Incident  Incident     `json:"incident"`
	Group     MonitorGroup `json:"group"`
	Host      Host         `json:"host"`
	Hook      HookSpec     `json:"hook"`
	Targets   Targets      `json:"targets"`
	SampledAt time.Time    `json:"sampledAt"`
}
type TargetResult struct {
	Key   string `json:"key"`
	Error string `json:"error,omitempty"`
}
type HookResult struct {
	Coalesced bool           `json:"coalesced,omitempty"`
	Status    string         `json:"status"`
	Targets   []TargetResult `json:"targets,omitempty"`
	ExitCode  *int           `json:"exitCode,omitempty"`
	Stdout    string         `json:"stdout,omitempty"`
	Stderr    string         `json:"stderr,omitempty"`
	Error     string         `json:"error,omitempty"`
}
