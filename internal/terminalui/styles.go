// Package terminalui holds the common visual language of Telltale's terminal monitors.
package terminalui

import "charm.land/lipgloss/v2"

var Title = lipgloss.NewStyle().Bold(true)
var Muted = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
var Selected = lipgloss.NewStyle().Reverse(true)
