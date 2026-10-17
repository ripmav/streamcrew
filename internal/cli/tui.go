// SPDX-License-Identifier: MIT

package cli

import (
	"os"
	"strings"

	"github.com/ripmav/streamcrew/internal/tui"
)

// tuiCmd runs the TUI against a local or remote core.
type tuiCmd struct {
	// Theme is auto, light or dark.
	Theme string `help:"Theme: auto, light or dark" enum:"auto,light,dark" default:"auto"`
}

// Run starts the TUI. Server and token come from the global flags
// (--server, --token) or the environment.
func (t tuiCmd) Run(e *Env) error {
	cfg, err := e.resolve()
	if err != nil {
		return err
	}
	token := e.APIToken
	if token == "" {
		// try the desktop token file in the data directory
		if data, err := os.ReadFile(cfg.DesktopTokenPath()); err == nil {
			token = strings.TrimSpace(string(data))
		}
	}
	return tui.Run(tui.Options{
		Server:  e.Server,
		DataDir: cfg.DataDir,
		Token:   token,
		Theme:   t.Theme,
	})
}
