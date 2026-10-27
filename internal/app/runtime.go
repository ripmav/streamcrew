// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ripmav/streamcrew/internal/apitoken"
	"github.com/ripmav/streamcrew/internal/config"
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
	data, err := json.Marshal(info, jsontext.WithIndent("  "))
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

// ensureAPIToken guarantees that the api has at least one admin token, so
// the fail-closed tokenAuth never starts with an open API (ADR-0013 §5):
// desktop mode writes the desktop token file, daemon mode the local admin
// token in <data-dir>/api.token that the CLI and TUI read. Server mode
// creates a token only from STREAMCREW_TOKEN; without one the api stays
// closed until the operator creates the first token on the server.
// An existing token is never replaced, so the clients are not cut off from
// a running core. The tokens live in the profile database, per the active
// profile (ADR-0013 §5).
func (a *App) ensureAPIToken(ctx context.Context) error {
	if err := a.importLegacyTokens(ctx); err != nil {
		return err
	}
	store := apitoken.New(a.store)
	if a.cfg.Mode == config.ModeDesktop {
		return a.ensureDesktopToken(ctx, store)
	}
	switch a.cfg.Mode {
	case config.ModeDaemon:
		return a.ensureLocalToken(ctx, store, a.cfg.APITokenPath())
	default: // ModeServer
		not, err := store.List(ctx)
		if err != nil {
			return fmt.Errorf("list api tokens: %w", err)
		}
		if len(not) > 0 {
			return nil
		}
		if a.cfg.BootstrapToken != "" {
			return a.registerToken(ctx, store, "bootstrap", a.cfg.BootstrapToken, true)
		}
		a.logger.WarnContext(ctx, "the api has no tokens and is closed until the first one is created",
			"hint", "run 'streamcrew token create <name> --scope admin' on the server, or set STREAMCREW_TOKEN before the core starts")
		return nil
	}
}

// importLegacyTokens moves the API tokens from the file in the data
// directory (before the tokens belonged to the profile, ADR-0013 §5) into
// the profile database, once; the file is renamed to *.migrated, so the
// import never runs twice (code review 2026-10-24, S5).
func (a *App) importLegacyTokens(ctx context.Context) error {
	path := filepath.Join(a.cfg.DataDir, apitoken.LegacyFileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read legacy api tokens: %w", err)
	}
	legacy, err := apitoken.LegacyLoad(data)
	if err != nil {
		return fmt.Errorf("read legacy api tokens: %w", err)
	}
	store := apitoken.New(a.store)
	have, err := store.List(ctx)
	if err != nil {
		return fmt.Errorf("list api tokens: %w", err)
	}
	if len(have) == 0 {
		for _, tok := range legacy {
			if err := store.Import(ctx, tok); err != nil {
				return fmt.Errorf("import legacy api token %q: %w", tok.Name, err)
			}
		}
	}
	if err := os.Rename(path, path+".migrated"); err != nil {
		return fmt.Errorf("rename legacy api tokens: %w", err)
	}
	if len(legacy) > 0 {
		a.logger.InfoContext(ctx, "moved the api tokens from the data directory file into the profile database",
			"count", len(legacy))
	}
	return nil
}

// ensureLocalToken guarantees the local admin token of daemon mode: the
// file with the value (0600) and the hash in the token store (ADR-0013 §5).
func (a *App) ensureLocalToken(ctx context.Context, store *apitoken.Store, path string) error {
	data, err := os.ReadFile(path)
	var value string
	if err == nil {
		value = strings.TrimSpace(string(data))
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read api token: %w", err)
	}
	if value != "" {
		if _, ok := store.Validate(ctx, value); ok {
			return nil // file and store are in sync
		}
		stored, err := store.List(ctx)
		if err != nil {
			return fmt.Errorf("list api tokens: %w", err)
		}
		if len(stored) > 0 {
			// The token was revoked: the store is the truth, and a
			// revoked token stays revoked across restarts (Nachprüfung
			// 2026-10-24, N2). The file keeps the value for a new token
			// create.
			a.logger.WarnContext(ctx, "the local admin token was revoked; it stays revoked until a new one is created", "path", path)
			return nil
		}
		// An empty store does not prove a revocation: it was deleted or
		// lost. Re-register the value from the file (Nachprüfung
		// 2026-10-25, N2a).
		a.logger.InfoContext(ctx, "the token store is empty; re-registering the local admin token", "path", path)
		return a.registerToken(ctx, store, "admin", value, false)
	}
	if a.cfg.BootstrapToken != "" {
		value = a.cfg.BootstrapToken
	} else {
		v, _, err := apitoken.NewValue()
		if err != nil {
			return fmt.Errorf("generate api token: %w", err)
		}
		value = v
	}
	if err := os.WriteFile(path, []byte(value+"\n"), 0o600); err != nil {
		return fmt.Errorf("write api token: %w", err)
	}
	a.logger.InfoContext(ctx, "created the local admin token", "path", path)
	return a.registerToken(ctx, store, "admin", value, false)
}

// registerToken stores a token with the given value in the token store;
// bootstrap says the value came from STREAMCREW_TOKEN, which the log names
// (the self-generated local token does not, Nachprüfung 2026-10-24, N3).
func (a *App) registerToken(ctx context.Context, store *apitoken.Store, name, value string, bootstrap bool) error {
	tok, err := store.CreateWith(ctx, name, []apitoken.Scope{apitoken.Admin}, value)
	if err != nil {
		return fmt.Errorf("register api token: %w", err)
	}
	if bootstrap {
		a.logger.InfoContext(ctx, "registered the bootstrap admin token from STREAMCREW_TOKEN", "token", tok.ID)
	} else {
		a.logger.InfoContext(ctx, "registered the local admin token", "token", tok.ID)
	}
	return nil
}

// ensureDesktopToken creates the desktop API token in the data directory
// (ADR-0005): an admin token whose value the desktop app reads from the
// file. An existing file is never replaced, so the app is not cut off from
// a running core.
func (a *App) ensureDesktopToken(ctx context.Context, store *apitoken.Store) error {
	path := a.cfg.DesktopTokenPath()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		tok, value, err := store.Create(ctx, "desktop", []apitoken.Scope{apitoken.Admin})
		if err != nil {
			return fmt.Errorf("create desktop token: %w", err)
		}
		if err := os.WriteFile(path, []byte(value+"\n"), 0o600); err != nil {
			return fmt.Errorf("write desktop token: %w", err)
		}
		a.logger.InfoContext(ctx, "created desktop token", "token", tok.ID, "path", path)
		return nil
	}
	if err != nil {
		return fmt.Errorf("read desktop token: %w", err)
	}
	value := strings.TrimSpace(string(data))
	if _, ok := store.Validate(ctx, value); ok {
		return nil // file and store are in sync
	}
	have, err := store.List(ctx)
	if err != nil {
		return fmt.Errorf("list api tokens: %w", err)
	}
	if len(have) > 0 {
		// The token was revoked in this profile: the store is the truth,
		// like the local admin token (Nachprüfung 2026-10-24, N2).
		a.logger.WarnContext(ctx, "the desktop token was revoked; it stays revoked until a new one is created", "path", path)
		return nil
	}
	// An empty store is a new profile or a lost table: re-register the
	// value from the file, so the desktop app is not cut off.
	return a.registerToken(ctx, store, "desktop", value, false)
}
