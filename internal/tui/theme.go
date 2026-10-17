// SPDX-License-Identifier: MIT

package tui

import (
	"charm.land/lipgloss/v2"
)

// Theme is the color scheme of the TUI.
type Theme struct {
	// Dark is true for the dark scheme.
	Dark bool

	border    lipgloss.Style
	borderErr lipgloss.Style
	header    lipgloss.Style
	title     lipgloss.Style
	dim       lipgloss.Style
	ok        lipgloss.Style
	err       lipgloss.Style
	key       lipgloss.Style
}

// newTheme returns the dark or light theme.
func newTheme(dark bool) *Theme {
	t := &Theme{Dark: dark}
	if dark {
		t.border = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("240"))
		t.borderErr = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("196"))
		t.header = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Bold(true)
		t.title = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
		t.dim = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
		t.ok = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
		t.err = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
		t.key = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Bold(true)
	} else {
		t.border = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("247"))
		t.borderErr = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("160"))
		t.header = lipgloss.NewStyle().Foreground(lipgloss.Color("235")).Bold(true)
		t.title = lipgloss.NewStyle().Foreground(lipgloss.Color("31")).Bold(true)
		t.dim = lipgloss.NewStyle().Foreground(lipgloss.Color("249"))
		t.ok = lipgloss.NewStyle().Foreground(lipgloss.Color("28"))
		t.err = lipgloss.NewStyle().Foreground(lipgloss.Color("160"))
		t.key = lipgloss.NewStyle().Foreground(lipgloss.Color("235")).Bold(true)
	}
	return t
}
