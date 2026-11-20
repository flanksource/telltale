package jvmruntime

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/flanksource/clicky/api"
	"github.com/flanksource/telltale/pkg/tracing/jvm/jmx"
)

const (
	// SettingsOp reads the JVM's system properties, environment and its Runtime
	// and OperatingSystem MBeans.
	SettingsOp = "jvm.settings"
	// ResourcesOp reads filesystem capacity and, on Linux, the process's limits
	// and cgroup settings.
	ResourcesOp = "jvm.resources"
	// FlagsOp reads every HotSpot VM flag (VM.flags -all).
	FlagsOp = "jvm.flags"
)

// Settings is the JVM's configuration as the agent reads it.
type Settings struct {
	Properties      map[string]string   `json:"properties"`
	Environment     map[string]string   `json:"environment"`
	Runtime         jmx.MBeanAttributes `json:"runtime"`
	OperatingSystem jmx.MBeanAttributes `json:"operatingSystem"`
}

// DecodeSettings decodes jvm.settings, refusing one without its properties or MBeans.
func DecodeSettings(raw json.RawMessage) (Settings, error) {
	settings, err := decodeObject[Settings](SettingsOp, raw, "properties", "environment", "runtime", "operatingSystem")
	if err == nil && (settings.Runtime.ObjectName == "" || settings.OperatingSystem.ObjectName == "") {
		err = fmt.Errorf("%s returned an MBean without its object name: %s", SettingsOp, raw)
	}
	return settings, err
}

// Pretty is a one-line summary, then the environment's variable names: their
// values stay in the structured formats, since environments carry secrets.
func (s Settings) Pretty() api.Text {
	p := s.Properties
	facts := []string{
		fmt.Sprintf("Java %s (%s)", p["java.version"], p["java.vendor"]),
		fmt.Sprintf("%s %s %s", p["os.name"], p["os.version"], p["os.arch"]),
		fmt.Sprintf("%d properties", len(s.Properties)),
		fmt.Sprintf("%d environment variables", len(s.Environment)),
	}
	text := api.Text{}.Append("JVM settings", "font-bold").Append(" "+strings.Join(facts, " · "), "text-muted")
	if len(s.Environment) > 0 {
		text = text.NewLine().Append("Environment", "font-bold").Append(" " + strings.Join(slices.Sorted(maps.Keys(s.Environment)), ", "))
	}
	return text
}

// Resources is the disk and, on Linux, process and cgroup resources the JVM
// runs under. Linux is set ({"status":"not-applicable"}) on any other OS, in
// place of the /proc files and Cgroups.
type Resources struct {
	Filesystems []Filesystem                 `json:"filesystems"`
	Linux       map[string]string            `json:"linux,omitempty"`
	Limits      string                       `json:"limits,omitempty"`
	Status      string                       `json:"status,omitempty"`
	Cgroup      string                       `json:"cgroup,omitempty"`
	Mountinfo   string                       `json:"mountinfo,omitempty"`
	Cgroups     map[string]map[string]string `json:"cgroups,omitempty"`
}

// Filesystem is one filesystem root's capacity in bytes.
type Filesystem struct {
	Path   string `json:"path"`
	Total  int64  `json:"total"`
	Usable int64  `json:"usable"`
	Free   int64  `json:"free"`
}

// DecodeResources decodes jvm.resources, refusing one that has neither the
// Linux /proc reads nor a reason it skipped them.
func DecodeResources(raw json.RawMessage) (Resources, error) {
	resources, err := decodeObject[Resources](ResourcesOp, raw, "filesystems")
	if err != nil {
		return resources, err
	}
	if resources.Linux == nil && resources.Cgroups == nil {
		return resources, fmt.Errorf("%s returned neither cgroups nor a linux status: %s", ResourcesOp, raw)
	}
	return resources, nil
}

// Pretty is a line per filesystem, then a line per cgroup with its limits, or
// why there are none.
func (r Resources) Pretty() api.Text {
	text := api.Text{}.Append("Resources", "font-bold")
	for _, fs := range r.Filesystems {
		text = text.NewLine().Append(fs.Path, "font-bold").
			Append(fmt.Sprintf(" %s usable of %s", api.HumanizeBytes(fs.Usable).String(), api.HumanizeBytes(fs.Total).String()), "text-muted")
	}
	if r.Linux != nil {
		return text.NewLine().Append("cgroups "+r.Linux["status"], "text-muted")
	}
	for _, group := range slices.Sorted(maps.Keys(r.Cgroups)) {
		var limits []string
		for _, name := range slices.Sorted(maps.Keys(r.Cgroups[group])) {
			limits = append(limits, name+"="+strings.TrimSpace(r.Cgroups[group][name]))
		}
		text = text.NewLine().Append("cgroup "+group, "font-bold").Append(" "+strings.Join(limits, " "), "text-muted")
	}
	return text
}

// VMFlags is every HotSpot flag as VM.flags -all prints it.
type VMFlags struct {
	Flags []VMFlag `json:"flags"`
}

// VMFlag is one flag line: its type, name and value, the kind of flag
// ("product", "manageable", …) and where its value came from ("default",
// "ergonomic", "command line", …).
type VMFlag struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Value  string `json:"value"`
	Kind   string `json:"kind"`
	Origin string `json:"origin"`
}

var flagLine = regexp.MustCompile(`^\s*(\S+)\s+(\S+)\s+= ?(.*?)\s*\{([^}]*)\} \{([^}]*)\}\s*$`)

// DecodeFlags decodes jvm.flags, a JSON string of VM.flags -all output,
// refusing any line it does not recognise rather than dropping it.
func DecodeFlags(raw json.RawMessage) (VMFlags, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return VMFlags{}, fmt.Errorf("decode %s result: %w", FlagsOp, err)
	}
	flags := VMFlags{Flags: []VMFlag{}}
	for number, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" || line == "[Global flags]" {
			continue
		}
		match := flagLine.FindStringSubmatch(line)
		if match == nil {
			return flags, fmt.Errorf("%s line %d is not a flag: %q", FlagsOp, number+1, line)
		}
		flags.Flags = append(flags.Flags, VMFlag{Type: match[1], Name: match[2], Value: match[3], Kind: match[4], Origin: match[5]})
	}
	if len(flags.Flags) == 0 {
		return flags, fmt.Errorf("%s returned no flags: %q", FlagsOp, text)
	}
	return flags, nil
}

// Pretty is a heading counting every flag, then one line per flag whose value
// did not come from the default, which is what a reader is looking for.
func (f VMFlags) Pretty() api.Text {
	var set []VMFlag
	for _, flag := range f.Flags {
		if flag.Origin != "default" {
			set = append(set, flag)
		}
	}
	text := api.Text{}.Append("VM flags", "font-bold").Append(fmt.Sprintf(" %d flags · %d not default", len(f.Flags), len(set)), "text-muted")
	for _, flag := range set {
		text = text.NewLine().Append(flag.Name, "font-bold").Append(" = " + flag.Value).Append(" "+flag.Origin, "text-muted")
	}
	return text
}
