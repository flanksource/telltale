package terminalui

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"strings"
)

type Share struct {
	Percent float64
	Style   lipgloss.Style
}

func Bar(width int, shares []Share) string {
	width = max(0, width)
	var b strings.Builder
	used := 0
	for _, part := range shares {
		cells := min(width-used, int(part.Percent/100*float64(width)+0.5))
		if cells > 0 {
			b.WriteString(part.Style.Render(strings.Repeat("█", cells)))
			used += cells
		}
	}
	return "[" + b.String() + Muted.Render(strings.Repeat("░", width-used)) + "]"
}

func Fit(text string, width int) string {
	width = max(0, width)
	text = ansi.Truncate(text, width, "…")
	return text + strings.Repeat(" ", max(0, width-ansi.StringWidth(text)))
}

func Clip(text string, width int) string { return ansi.Truncate(text, max(0, width), "") }

func PadLines(text string, height int) []string {
	lines := strings.Split(text, "\n")
	for len(lines) < height {
		lines = append(lines, "")
	}
	return lines[:max(0, height)]
}
