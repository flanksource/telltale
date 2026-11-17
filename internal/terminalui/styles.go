// Package terminalui holds the common visual language of Telltale's terminal monitors.
package terminalui

import "charm.land/lipgloss/v2"

var Title = lipgloss.NewStyle().Bold(true)
var Muted = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
var Selected = lipgloss.NewStyle().Reverse(true)
var Heading = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("244"))
var Status = lipgloss.NewStyle().Foreground(lipgloss.Color("208"))
var CPU = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
var Kernel = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
var Memory = lipgloss.NewStyle().Foreground(lipgloss.Color("78"))
var Swap = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
