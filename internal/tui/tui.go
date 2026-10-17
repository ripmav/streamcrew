// SPDX-License-Identifier: MIT

// Package tui is the Bubble Tea frontend of streamcrew. It talks only to
// the generated API client, never to internal packages.
package tui

import (
	tea "charm.land/bubbletea/v2"
)

// Run starts the TUI and blocks until it quits.
func Run(opts Options) error {
	return run(opts, nil)
}

// run starts the program with additional options (the tests use them).
func run(opts Options, extra []tea.ProgramOption) error {
	p := tea.NewProgram(newModel(opts), extra...)
	_, err := p.Run()
	return err
}
