// SPDX-License-Identifier: MIT

package cli

import (
	"github.com/ripmav/streamcrew/internal/tui"
)

// tuiCmd runs the TUI against a local or remote core.
type tuiCmd struct {
	// Theme is auto, light or dark.
	Theme string `help:"Theme: auto, light or dark" enum:"auto,light,dark" default:"auto"`
}

// Run starts the TUI. Server and token come from the global flags
// (--server, --token) or the environment; without a token the local token
// file of the data directory is used (ADR-0013 §5).
func (t tuiCmd) Run(e *Env) error {
	cfg, err := e.resolve()
	if err != nil {
		return err
	}
	return tui.Run(tui.Options{
		Server:  e.Server,
		DataDir: cfg.DataDir,
		Token:   e.token(),
		Theme:   t.Theme,
	})
}
