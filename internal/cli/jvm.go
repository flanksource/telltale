package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/flanksource/clicky/rpc"
	"github.com/flanksource/telltale/internal/jvm"
	"github.com/flanksource/telltale/pkg/agentclient"
	jvmops "github.com/flanksource/telltale/pkg/tracing/jvm"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// newJVMCommand is `jvm` with `call`, for operations only the live catalog
// knows, such as extension operations. The registry's operations and the
// kinds' JVM presets are placed under it by addJVMOperations and
// addKindCommands.
func newJVMCommand() *cobra.Command {
	root := &cobra.Command{Use: "jvm", Short: "Inspect and operate a connected Java runtime"}
	root.AddCommand(newJVMCallCommand(), newJVMDeployCommand(), newJVMDeployersCommand())
	setDeployerResolver(agentclient.Default)
	return root
}

// addJVMOperations places every built-in operation of the Java registry at
// the command it declares, such as jvm.classes.patch at `jvm patch apply`.
func addJVMOperations(root *cobra.Command) {
	for _, operation := range jvm.Operations() {
		command := commandAt(root, operation.Command)
		if command.RunE != nil {
			panic(fmt.Sprintf("JVM operation %s is declared as the %q command, which already runs", operation.Name, command.CommandPath()))
		}
		bindJVMOperation(command, operation)
	}
}

// commandAt is the command at path below root, creating the groups and the
// command that do not exist yet.
func commandAt(root *cobra.Command, path []string) *cobra.Command {
	command := root
	for _, name := range path {
		var next *cobra.Command
		for _, child := range command.Commands() {
			if child.Name() == name {
				next = child
			}
		}
		if next == nil {
			next = &cobra.Command{Use: name, Short: name + " commands", Args: cobra.NoArgs}
			command.AddCommand(next)
		}
		command = next
	}
	return command
}

func bindJVMOperation(cmd *cobra.Command, operation jvm.Operation) {
	request := jvm.Request{Operation: operation.Name}
	cmd.Short = operation.Description
	cmd.Long = operation.Description + "\n\nRuns the " + operation.Name + " operation."
	cmd.Flags().StringVar(&request.Agent, "agent", "", agentFlagUsage)
	if operation.Mutates {
		cmd.Long += " It changes the runtime, so it needs --allow-mutation and an agent installed with mutations allowed."
		cmd.Flags().BoolVar(&request.AllowMutation, "allow-mutation", false, "Allow this call to change the runtime")
	}
	params, err := bindParamFlags(cmd.Flags(), operation.Params)
	if err != nil {
		panic(fmt.Sprintf("JVM operation %s: %v", operation.Name, err))
	}
	files := bindParamFiles(cmd, params)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		for _, file := range files {
			if err := file.load(cmd); err != nil {
				return err
			}
		}
		var err error
		if request.Params, err = params.params(); err != nil {
			return err
		}
		value, err := callJVM(cmd.Context(), request)
		if err != nil {
			return err
		}
		return printJVMResult(cmd, request, value)
	}
}

// printJVMResult prints an operation's result through its typed model in any
// output format, or as the agent sent it when it has none.
func printJVMResult(cmd *cobra.Command, request jvm.Request, value json.RawMessage) error {
	result, err := jvmops.Result(request.Operation, request.Params, value)
	if err != nil {
		return err
	}
	if err := printRecords(cmd, result); err != nil {
		return err
	}
	if failed, ok := result.(interface{ Failure() error }); ok {
		return failed.Failure()
	}
	return nil
}

// agentFlagUsage describes --agent wherever a command names a Java agent.
const agentFlagUsage = "Java agent: its id, or a short name (service, pod, deployer, pid, or a unique prefix); omit it when only one agent is connected or deployed"

// callJVM sends a typed command's request to its agent; internal tests
// replace it to read the request a command assembles.
var callJVM = jvm.Call

// paramFile is a --<param>-file flag that fills a source or binary string
// param from a local file: text for a textarea, base64 for a byte string.
type paramFile struct {
	flag   *pflag.Flag
	param  *pflag.Flag
	base64 bool
}

func bindParamFiles(cmd *cobra.Command, params paramFlags) []paramFile {
	var files []paramFile
	for _, param := range params.bound {
		if param.schema.Type != "string" || (param.schema.Format != "byte" && param.schema.Format != "textarea") {
			continue
		}
		name := param.flag.Name + "-file"
		cmd.Flags().String(name, "", "Read --"+param.flag.Name+" from this local file")
		cmd.MarkFlagsMutuallyExclusive(param.flag.Name, name)
		if _, required := param.flag.Annotations[cobra.BashCompOneRequiredFlag]; required {
			delete(param.flag.Annotations, cobra.BashCompOneRequiredFlag)
			cmd.MarkFlagsOneRequired(param.flag.Name, name)
		}
		files = append(files, paramFile{flag: cmd.Flags().Lookup(name), param: param.flag, base64: param.schema.Format == "byte"})
	}
	return files
}

func (f paramFile) load(cmd *cobra.Command) error {
	if !f.flag.Changed {
		return nil
	}
	if _, remote := rpc.RequestFromContext(cmd.Context()); remote {
		return fmt.Errorf("--%s reads a file on the machine running the command; send %s in the request instead", f.flag.Name, f.param.Name)
	}
	content, err := jvm.ReadParamFile(f.flag.Value.String(), f.base64)
	if err != nil {
		return fmt.Errorf("--%s: %w", f.flag.Name, err)
	}
	return cmd.Flags().Set(f.param.Name, content)
}

func newJVMCallCommand() *cobra.Command {
	var request jvm.Request
	var params, paramsFile string
	command := &cobra.Command{Use: "call", Short: "Invoke any operation from the live catalog, including extension operations", Args: cobra.NoArgs}
	command.RunE = func(cmd *cobra.Command, _ []string) error {
		if paramsFile != "" {
			data, err := os.ReadFile(paramsFile)
			if err != nil {
				return err
			}
			params = string(data)
		}
		request.Params = json.RawMessage(params)
		value, err := jvm.Call(cmd.Context(), request)
		if err != nil {
			return err
		}
		return printJVMResult(cmd, request, value)
	}
	command.Flags().StringVar(&request.Agent, "agent", "", agentFlagUsage)
	command.Flags().StringVar(&request.Operation, "operation", "", "Operation from the live catalog")
	command.Flags().StringVar(&params, "params", "{}", "JSON operation parameters")
	command.Flags().StringVar(&paramsFile, "params-file", "", "Read JSON operation parameters from a file")
	command.MarkFlagsMutuallyExclusive("params", "params-file")
	command.Flags().BoolVar(&request.AllowMutation, "allow-mutation", false, "Allow this call to change the runtime")
	return command
}
