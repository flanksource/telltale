package cli

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"
)

// The help's styles, as gavel's help uses them.
const (
	headingStyle = "font-bold text-purple-600"
	flagStyle    = "text-cyan-600 font-bold"
	codeStyle    = "text-green-500"
	mutedStyle   = "text-muted"
)

// setHelp renders cmd's help with render: colored on a terminal unless
// --no-color, NO_COLOR or TERM=dumb say otherwise, plain everywhere else.
func setHelp(cmd *cobra.Command, render func(*cobra.Command) api.Text) {
	cmd.SetHelpFunc(func(c *cobra.Command, _ []string) {
		text, out := render(c), c.OutOrStdout()
		if colored(out) {
			_, _ = fmt.Fprintln(out, text.ANSI()) // help has nowhere to report a failed write
			return
		}
		_, _ = fmt.Fprintln(out, text.String())
	})
}

func colored(w io.Writer) bool {
	file, ok := w.(*os.File)
	return ok && !clicky.Flags.NoColor && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb" && term.IsTerminal(int(file.Fd()))
}

// helpText is a help page under construction, one section after another.
type helpText struct{ api.Text }

func newHelp(summary string) helpText {
	return helpText{clicky.Text(summary, "font-bold").NewLine()}
}

func (h helpText) section(title string) helpText {
	return helpText{h.NewLine().Append(strings.ToUpper(title), headingStyle).NewLine()}
}

// paragraph is text indented under its section.
func (h helpText) paragraph(text string) helpText {
	return helpText{h.Append("  ").Append(text, mutedStyle).NewLine()}
}

// command is a command line and what it does.
func (h helpText) command(line, says string) helpText {
	t := h.Append("  ").Append(line, codeStyle).NewLine()
	if says != "" {
		t = t.Append("      ").Append(says, mutedStyle).NewLine()
	}
	return helpText{t}
}

// term is a name, such as a kind or a variable, and what it is.
func (h helpText) term(name, says string, width int) helpText {
	return helpText{h.Append("  ").Append(fmt.Sprintf("%-*s", width, name), flagStyle).Append("  ").Append(says, mutedStyle).NewLine()}
}

// flags lists cmd's flags by group, each group's descriptions aligned: its
// own, the trace params, then the formatting and global ones.
func (h helpText) flags(cmd *cobra.Command) helpText {
	groups := clicky.CommandFlagGroups(cmd)
	if params := slices.IndexFunc(groups, func(group clicky.FlagGroup) bool { return group.Title == paramsCategory }); params > 0 {
		group := groups[params]
		groups = slices.Insert(slices.Delete(groups, params, params+1), min(1, params), group)
	}
	for _, group := range groups {
		h = h.section(group.Title)
		width := 0
		group.Flags.VisitAll(func(flag *pflag.Flag) { width = max(width, len(flagUsage(flag))) })
		group.Flags.VisitAll(func(flag *pflag.Flag) {
			h = helpText{h.Append("  ").Append(fmt.Sprintf("%-*s", width, flagUsage(flag)), flagStyle).Append("  ").Append(flagDescription(flag), mutedStyle).NewLine()}
		})
	}
	return h
}

func flagUsage(flag *pflag.Flag) string {
	usage := "--" + flag.Name
	if flag.Shorthand != "" {
		usage = "-" + flag.Shorthand + ", " + usage
	}
	if name, _ := pflag.UnquoteUsage(flag); name != "" {
		usage += " " + name
	}
	return usage
}

func flagDescription(flag *pflag.Flag) string {
	_, usage := pflag.UnquoteUsage(flag)
	switch flag.DefValue {
	case "", "false", "0", "[]", "0s":
		return usage
	}
	return usage + " (default " + flag.DefValue + ")"
}
