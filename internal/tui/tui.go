// SPDX-License-Identifier: MIT

// Package tui is the Bubble Tea frontend of streamcrew. It talks only to
// the generated API client, never to internal packages.
package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"
)

// Run starts the TUI and blocks until it quits. The context ends the stream
// subscription and the pending requests when the program quits
// (U6 of the code review 2026-10-26).
func Run(ctx context.Context, opts Options) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	p := tea.NewProgram(newModel(ctx, opts))
	_, err := p.Run()
	return err
}
