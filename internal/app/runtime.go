// SPDX-License-Identifier: MIT

package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/ripmav/streamcrew/internal/apitoken"
)

// RuntimeInfo is the runtime file of the core (ADR-0005): the address the
// core listens on, its PID and the core version, plus the mode, the profile
// and the start time. The desktop app uses it to detect a running core;
// diagnostic tools use it to find the core.
type RuntimeInfo struct {
	Address   string    `json:"address"`
	PID       int       `json:"pid"`
	Version   string    `json:"version"`
	Mode      string    `json:"mode"`
	Profile   string    `json:"profile"`
	StartedAt time.Time `json:"started_at"`
}

// writeRuntimeFile waits until the HTTP server listens and then writes the
// runtime file with the address, the PID and the version of the core.
func (a *App) writeRuntimeFile() {
	<-a.HTTPServer().Listening()
	addr, _ := a.HTTPServer().Addr()
	if addr == nil {
		return
	}
	info := RuntimeInfo{
		Address:   addr.String(),
		PID:       os.Getpid(),
		Version:   a.version,
		Mode:      string(a.cfg.Mode),
		Profile:   a.profile.Name,
		StartedAt: a.startedAt,
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		a.logger.Error("marshal runtime file", "error", err)
		return
	}
	if err := os.WriteFile(a.cfg.RuntimePath(), data, 0o600); err != nil {
		a.logger.Error("write runtime file", "path", a.cfg.RuntimePath(), "error", err)
	}
}

// removeRuntimeFile removes the runtime file after the HTTP server stopped.
func (a *App) removeRuntimeFile() {
	if err := os.Remove(a.cfg.RuntimePath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		a.logger.Error("remove runtime file", "path", a.cfg.RuntimePath(), "error", err)
	}
}

// ensureDesktopToken creates the desktop API token in the data directory
// (ADR-0005): an admin token whose value the desktop app reads from the
// file. An existing file is never replaced, so the app is not cut off from
// a running core.
func (a *App) ensureDesktopToken() error {
	path := a.cfg.DesktopTokenPath()
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat desktop token: %w", err)
	}
	tok, value, err := apitoken.Open(a.cfg.DataDir).Create("desktop", []apitoken.Scope{apitoken.Admin})
	if err != nil {
		return fmt.Errorf("create desktop token: %w", err)
	}
	if err := os.WriteFile(path, []byte(value+"\n"), 0o600); err != nil {
		return fmt.Errorf("write desktop token: %w", err)
	}
	a.logger.Info("created desktop token", "token", tok.ID, "path", path)
	return nil
}
