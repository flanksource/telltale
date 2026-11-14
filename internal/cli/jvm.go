package cli

import (
	"encoding/json"
	"github.com/flanksource/telltale/internal/jvm"
	jvmops "github.com/flanksource/telltale/pkg/tracing/jvm"
	"github.com/spf13/cobra"
	"os"
)

func newJVMCommand() *cobra.Command {
	root := &cobra.Command{Use: "jvm", Short: "Inspect and operate a connected Java runtime"}
	for _, action := range []string{"catalog", "call"} {
		var request jvm.Request
		var params string
		var paramsFile string
		command := &cobra.Command{Use: action, Short: "Read the live catalog or invoke a JVM operation", Args: cobra.NoArgs}
		command.RunE = func(cmd *cobra.Command, _ []string) error {
			if paramsFile != "" {
				data, err := os.ReadFile(paramsFile)
				if err != nil {
					return err
				}
				params = string(data)
			}
			request.Params = json.RawMessage(params)
			if cmd.Name() == "catalog" {
				request.Operation = "jvm.catalog"
			}
			value, err := jvm.Call(cmd.Context(), request)
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(value)
		}
		command.Flags().StringVar(&request.Agent, "agent", "", "Connected Java agent ID")
		command.Flags().StringVar(&request.Operation, "operation", "", "Operation from the live catalog")
		command.Flags().StringVar(&params, "params", "{}", "JSON operation parameters")
		command.Flags().StringVar(&paramsFile, "params-file", "", "Read JSON operation parameters from a file")
		command.MarkFlagsMutuallyExclusive("params", "params-file")
		command.Flags().BoolVar(&request.AllowMutation, "allow-mutation", false, "Allow this call to change the runtime")
		root.AddCommand(command)
	}
	root.AddCommand(newJVMScriptCommand(), newJVMExtensionsCommand(), newJVMHeapHistogramCommand())
	return root
}

func newJVMHeapHistogramCommand() *cobra.Command {
	var request jvm.Request
	var params jvmops.HeapHistogramParams
	command := &cobra.Command{Use: "heap-histogram", Short: "List the heap's classes by retained bytes (GC.class_histogram)", Args: cobra.NoArgs}
	command.Flags().StringVar(&request.Agent, "agent", "", "Connected Java agent ID")
	command.Flags().IntVar(&params.Top, "top", 0, "Rows to return, largest first (0 for the agent's default of 100)")
	command.Flags().StringVar(&params.ClassPattern, "class-pattern", "", "Glob over class names, such as com.example.*")
	command.Flags().BoolVar(&params.Live, "live", false, "Count only reachable objects; forces a full GC on the JVM first")
	command.RunE = func(cmd *cobra.Command, _ []string) error {
		if err := params.Validate(); err != nil {
			return err
		}
		var err error
		if request.Params, err = json.Marshal(params); err != nil {
			return err
		}
		request.Operation = jvmops.HeapHistogramOp
		value, err := jvm.Call(cmd.Context(), request)
		if err != nil {
			return err
		}
		histogram, err := jvmops.DecodeHeapHistogram(value)
		if err != nil {
			return err
		}
		return printRecords(cmd, histogram)
	}
	return command
}

func newJVMScriptCommand() *cobra.Command {
	var request jvm.Request
	var options jvm.ScriptOptions
	command := &cobra.Command{Use: "groovy", Short: "Execute a Groovy script once in a selected application loader", Args: cobra.NoArgs}
	command.Flags().StringVar(&request.Agent, "agent", "", "Connected Java agent ID")
	command.Flags().BoolVar(&request.AllowMutation, "allow-mutation", false, "Allow script execution")
	command.Flags().StringVar(&options.Source, "source", "", "Inline Groovy source")
	command.Flags().StringVar(&options.File, "file", "", "Groovy source file")
	command.Flags().StringVar(&options.Args, "args", "{}", "JSON object exposed as args")
	command.Flags().StringVar(&options.Loader, "loader", "", "Application loader hash from jvm.loaders")
	command.Flags().IntVar(&options.TimeoutSeconds, "timeout", 30, "Cooperative execution deadline in seconds")
	command.MarkFlagsMutuallyExclusive("source", "file")
	command.RunE = func(cmd *cobra.Command, _ []string) error {
		params, err := jvm.ScriptParameters(options)
		if err != nil {
			return err
		}
		request.Operation = "jvm.groovy.eval"
		request.Params = params
		value, err := jvm.Call(cmd.Context(), request)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(value)
	}
	return command
}

func newJVMExtensionsCommand() *cobra.Command {
	root := &cobra.Command{Use: "extensions", Short: "List, install, replace or unload diagnostic extensions"}
	for _, action := range []string{"list", "install", "replace", "unload"} {
		var request jvm.Request
		command := &cobra.Command{Use: action, Short: action + " diagnostic extensions", Args: cobra.NoArgs}
		if action != "list" {
			command.Use += " <jar-or-id>"
			command.Args = cobra.ExactArgs(1)
		}
		command.Flags().StringVar(&request.Agent, "agent", "", "Connected Java agent ID")
		command.Flags().BoolVar(&request.AllowMutation, "allow-mutation", false, "Allow this extension change")
		command.RunE = func(cmd *cobra.Command, args []string) error {
			request.Operation = "jvm.extensions." + cmd.Name()
			var err error
			switch cmd.Name() {
			case "install", "replace":
				request.Params, err = jvm.ExtensionParameters(args[0])
			case "unload":
				request.Params, err = json.Marshal(map[string]string{"id": args[0]})
			}
			if err != nil {
				return err
			}
			value, err := jvm.Call(cmd.Context(), request)
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(value)
		}
		root.AddCommand(command)
	}
	return root
}
