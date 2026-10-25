package cli

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/flanksource/clicky/rpc"
	"github.com/flanksource/telltale"
	"github.com/flanksource/telltale/pkg/traces"
	"github.com/spf13/cobra"
)

// targets title each traces.Target's kinds, in the order help lists them.
var targets = []struct {
	target traces.Target
	title  string
	says   string
}{
	{traces.TargetCommand, "Kinds a command is pointed at",
		"The command is started with the variables the capture exports. They cannot attach to a running process."},
	{traces.TargetProcess, "Kinds that observe a process tree",
		"They observe telltale's own tree, which holds the command, or the process --pid attaches to."},
	{traces.TargetSelf, "Kinds that record telltale itself",
		"They record telltale's own sources while the run lasts, nothing of the command."},
	{traces.TargetHost, "Kinds that observe the whole host",
		"They record every process on the host: run alone they need no command and run until Ctrl+C or --duration."},
}

func targetOf(target traces.Target) (string, string) {
	for _, entry := range targets {
		if entry.target == target {
			return entry.title, entry.says
		}
	}
	panic(fmt.Sprintf("trace target %q has no help", target))
}

func kindNameWidth() int {
	width := 0
	for _, registration := range traces.Registrations() {
		width = max(width, len(registration.Name))
	}
	return width
}

func runHelp(cmd *cobra.Command) helpText {
	h := newHelp("Trace a command, or a running process, with one trace kind.").
		section("Usage").
		command("telltale run <kind> [flags] -- command [args...]", "start the command and trace it").
		command("telltale run <kind> [flags] --pid PID", "attach to a running process until Ctrl+C, --duration, or it exits")
	width := kindNameWidth()
	for _, entry := range targets {
		h = h.section(entry.title).paragraph(entry.says)
		for _, registration := range traces.Registrations() {
			if registration.Target == entry.target {
				h = h.term(registration.Name, registration.Summary, width)
			}
		}
	}
	h = h.section("Presets").paragraph("Named runs of several kinds, each its own command:")
	for _, preset := range telltale.Presets() {
		h = h.term("telltale "+preset.Name, preset.Summary, width)
	}
	return h.section("Examples").
		command("telltale run http_proxy --mode mitm -- curl -sS https://example.com", "decrypt and record a request").
		command("telltale run process_samples --pid 1234 --facets ports --duration 1m", "sample a running process for a minute").
		command("telltale run stacks --every 10s -- ./server", "dump a server's stacks every 10 seconds").
		section("See also").
		command("telltale run <kind> --help", "a kind's params, exports and examples").
		command("telltale trace run --help", "arm several kinds around one command").
		command("telltale trace list", "the runs recorded").
		flags(cmd)
}

func kindHelp(cmd *cobra.Command, registration traces.Registration) helpText {
	title, says := targetOf(registration.Target)
	h := newHelp(registration.Summary + ".").
		section("Description").paragraph(registration.Description).
		section("Usage")
	if registration.Target == traces.TargetHost {
		h = h.command("telltale run "+registration.Name+" [flags]", "")
	}
	h = h.command("telltale run "+registration.Name+" [flags] -- command [args...]", "")
	if registration.Target != traces.TargetCommand && registration.Target != traces.TargetHost {
		h = h.command("telltale run "+registration.Name+" [flags] --pid PID", "")
	}
	h = h.section("Target: " + title).paragraph(says)
	if len(registration.Exports) > 0 {
		h = h.section("Exports").paragraph(strings.Join(registration.Exports, " "))
	}
	if registration.Checkpoint != nil {
		checkpoint, err := json.Marshal(registration.Checkpoint)
		if err != nil {
			panic(fmt.Sprintf("trace kind %s checkpoint params: %v", registration.Name, err))
		}
		h = h.section("Checkpoints").
			paragraph("Read every --every, and once as an attached run stops, with " + string(checkpoint) + ".")
	}
	h = h.section("Examples")
	for _, example := range registration.Examples {
		h = h.command("telltale run "+registration.Name+" "+example.Args, example.Says)
	}
	return h.section("See also").
		command("telltale run --help", "every trace kind").
		command("telltale trace list --kind "+registration.Name, "the runs that armed it").
		flags(cmd)
}

func traceRunHelp(cmd *cobra.Command) helpText {
	h := newHelp("Run a command under trace, recording its traffic and process tree into .telltale/traces.").
		section("Usage").
		command("telltale trace run [flags] -- command [args...]", "").
		section("How it works").
		paragraph("Each --trace arms one capture before the command starts. A proxy capture exports the variables that point the command at it.").
		paragraph("The process tree under telltale, which holds the command, is always recorded with its samples and stacks.").
		paragraph("Everything is kept in a store at <work-dir>/.telltale/traces/<run id>; the newest 20 runs are kept.").
		paragraph("The command's exit code is the run's: a command that fails fails trace run.").
		section("Trace kinds").
		paragraph("Arm one as --trace kind, or with params as --trace 'kind:{\"param\": value}'.")
	kinds, err := traces.Kinds()
	if err != nil {
		panic(fmt.Sprintf("trace kind catalog: %v", err))
	}
	width := kindNameWidth()
	for _, registration := range traces.Registrations() {
		h = h.term(registration.Name, registration.Summary, width)
		plugin, err := traces.Lookup(kinds, registration.Name)
		if err != nil {
			panic(err)
		}
		schema, err := plugin.Params()
		if err != nil {
			panic(fmt.Sprintf("trace kind %s params: %v", registration.Name, err))
		}
		if params := paramSummary(schema, ""); len(params) > 0 {
			h = helpText{h.Append(strings.Repeat(" ", width+4)).Append("params: "+strings.Join(params, ", "), codeStyle).NewLine()}
		}
	}
	return h.section("Examples").
		command("telltale trace run -- curl -sS https://example.com", "record the tunnel to example.com through http_proxy, the default").
		command(`telltale trace run --trace 'http_proxy:{"mode":"mitm"}' -- curl -sS https://example.com`, "decrypt and record the exchange in full").
		command(`telltale trace run --trace http_proxy --trace 'pg_proxy:{"env":"ORDERS_DB_URL"}' -- go test ./...`, "record the tests' HTTP and SQL").
		command("telltale trace run --facets -- make build", "record every process's open files, sockets and environment").
		section("See also").
		command("telltale run <kind> --help", "one kind, its params as flags").
		command("telltale trace list --status failed --since 2h", "the runs recorded").
		command("telltale serve", "browse the runs in the web UI").
		flags(cmd)
}

// paramSummary lists schema's params as name type (choices), an object's
// prefixed by its name.
func paramSummary(schema *rpc.OpenAPISchema, prefix string) []string {
	var params []string
	for _, name := range slices.Sorted(maps.Keys(schema.Properties)) {
		property := schema.Properties[name]
		if property.Type == "object" {
			params = append(params, paramSummary(property, prefix+name+".")...)
			continue
		}
		param := prefix + name + " " + describeType(property)
		if choices := enumOf(property); len(choices) > 0 {
			param += " (" + strings.Join(choices, "|") + ")"
		}
		params = append(params, param)
	}
	return params
}

func presetHelp(cmd *cobra.Command, preset telltale.Preset) helpText {
	h := newHelp(preset.Summary + ".").
		section("Description").paragraph(preset.Description).
		section("Usage").
		command("telltale "+preset.Name+" [flags] PID", "attach to a running process until Ctrl+C, --duration, or it exits").
		command("telltale "+preset.Name+" [flags] -- command [args...]", "start the command and trace it").
		section("Arms")
	if len(preset.Traces) == 0 {
		h = h.paragraph("The run's own process trace: its tree, samples and stacks.")
	}
	width := kindNameWidth()
	for _, spec := range preset.Traces {
		registration, err := traces.Registered(spec.Kind)
		if err != nil {
			panic(err)
		}
		h = h.term(spec.Kind, registration.Summary, width)
	}
	if preset.Facets {
		h = h.paragraph("Every process's open files, sockets, ports, connections and environment.")
	}
	if preset.Every > 0 {
		h = h.paragraph(fmt.Sprintf("Checkpoints are read every %s unless --every says otherwise.", preset.Every))
	}
	h = h.section("Examples")
	for _, example := range preset.Examples {
		h = h.command("telltale "+preset.Name+" "+example.Args, example.Says)
	}
	return h.section("See also").
		command("telltale run --help", "every trace kind, one at a time").
		command("telltale trace list -q pid", "the attached runs recorded").
		flags(cmd)
}
