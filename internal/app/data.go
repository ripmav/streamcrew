// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/ripmav/streamcrew/internal/auth"
	"github.com/ripmav/streamcrew/internal/backup"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/eventservice"
	"github.com/ripmav/streamcrew/internal/httpclient"
	"github.com/ripmav/streamcrew/internal/lockfile"
	"github.com/ripmav/streamcrew/internal/profile"
	"github.com/ripmav/streamcrew/internal/store"
	"github.com/ripmav/streamcrew/internal/supervisor"
)

// LockFileName is the lock file in the data directory (ADR-0012).
const LockFileName = "streamcrew.lock"

// TypeSupervisorStatus is the event type of the supervisor's state changes.
// The application events app.started and app.stopping are in the domain
// catalog (internal/domain/eventtype), because event commands react to them.
const TypeSupervisorStatus event.Type = "supervisor.status"

// Started is the payload of "app.started".
type Started struct {
	Version string `json:"version"`
	Mode    string `json:"mode"`
	Profile string `json:"profile"`
}

// Stopping is the payload of "app.stopping".
type Stopping struct{}

func newCatalog() (*event.Catalog, error) {
	c := event.NewCatalog()
	return c, errors.Join(
		event.Register[Started](c, eventtype.AppStarted),
		event.Register[Stopping](c, eventtype.AppStopping),
		event.Register[supervisor.Status](c, TypeSupervisorStatus),
		auth.RegisterEvents(c),
		engine.RegisterEvents(c),
		eventservice.RegisterEvents(c),
	)
}

// authFlows returns the login flows of the platforms (roadmap 4.1); only
// Twitch has one so far. Its token calls run through the resilient HTTP
// client (Code-ADR-0014) with the breakers twitch.auth and twitch.helix
// (Code-ADR-0007); the Helix endpoints of phase 4.2 join the same
// instances.
func authFlows(client *http.Client, twitchAuth, twitchHelix *httpclient.Client) func(p platform.Name, c auth.Credentials) (auth.Flow, error) {
	return func(p platform.Name, c auth.Credentials) (auth.Flow, error) {
		switch p {
		case platform.Twitch:
			return auth.NewTwitch(c, client, twitchAuth, twitchHelix)
		default:
			return nil, fmt.Errorf("no login flow for platform %q", p)
		}
	}
}

// BackupDir returns the backup directory of a data directory.
func BackupDir(dataDir string) string {
	return filepath.Join(dataDir, "backups")
}

// LockDataDir takes the data directory lock. If a core or another command
// holds it, the error says so.
func LockDataDir(dataDir string) (*lockfile.Lock, error) {
	l, err := lockfile.Acquire(filepath.Join(dataDir, LockFileName))
	if held, ok := errors.AsType[*lockfile.LockedError](err); ok {
		return nil, fmt.Errorf("the data directory %s is in use by another streamcrew process; stop it first: %w", dataDir, held)
	}
	return l, err
}

// PreMigrationBackup returns the store hook that backs a profile up before
// migrations change it (Code-ADR-0008).
func PreMigrationBackup(dir, appVersion string, logger *slog.Logger) func(ctx context.Context, s *store.Store, from, to int64) error {
	return func(ctx context.Context, s *store.Store, from, to int64) error {
		id := strings.TrimSuffix(filepath.Base(s.Path()), filepath.Ext(s.Path()))
		name, err := s.Meta(ctx, profile.MetaName)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		info, err := backup.Create(ctx, s, dir, backup.Request{
			ProfileID: id, ProfileName: name, Kind: backup.KindPreMigration, AppVersion: appVersion,
		})
		if err != nil {
			return err
		}
		logger.InfoContext(ctx, "backup before migration created", "path", info.Path, "from", from, "to", to)
		return nil
	}
}
