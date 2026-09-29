// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/ripmav/streamcrew/internal/backup"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/lockfile"
	"github.com/ripmav/streamcrew/internal/profile"
	"github.com/ripmav/streamcrew/internal/store"
	"github.com/ripmav/streamcrew/internal/supervisor"
)

// LockFileName is the lock file in the data directory (ADR-0012).
const LockFileName = "streamcrew.lock"

// Event types of the core itself.
const (
	TypeAppStarted       event.Type = "app.started"
	TypeAppStopping      event.Type = "app.stopping"
	TypeSupervisorStatus event.Type = "supervisor.status"
)

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
		event.Register[Started](c, TypeAppStarted),
		event.Register[Stopping](c, TypeAppStopping),
		event.Register[supervisor.Status](c, TypeSupervisorStatus),
	)
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
