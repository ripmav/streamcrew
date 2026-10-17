// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ripmav/streamcrew/internal/api"
	"github.com/ripmav/streamcrew/internal/backup"
)

// apiBackup is the api.Backup port over the backup package: it creates
// manual backups of the open store, lists the backups of the profile and
// refuses a restore while the app runs (ADR-0012).
type apiBackup struct {
	a *App
}

var _ api.Backup = apiBackup{}

func (a apiBackup) Create(ctx context.Context) (api.BackupInfo, error) {
	info, err := backup.Create(ctx, a.a.store, a.a.backups, backup.Request{
		ProfileID:   a.a.profile.ID,
		ProfileName: a.a.profile.Name,
		Kind:        backup.KindManual,
		AppVersion:  a.a.version,
	})
	if err != nil {
		return api.BackupInfo{}, fmt.Errorf("create backup: %w", err)
	}
	return backupView(info), nil
}

func (a apiBackup) List(_ context.Context) ([]api.BackupInfo, error) {
	infos, err := backup.List(a.a.backups, a.a.profile.ID)
	if err != nil {
		return nil, fmt.Errorf("list backups: %w", err)
	}
	out := make([]api.BackupInfo, 0, len(infos))
	for _, info := range infos {
		out = append(out, backupView(info))
	}
	return out, nil
}

func (a apiBackup) Restore(_ context.Context, _ string) (int64, error) {
	// shortcut: restore is refused while the app runs; the CLI performs it
	// while the app is stopped (ADR-0012). Upgrade when the app can stop,
	// restore and restart itself.
	return 0, fmt.Errorf("%w: stop the app and run `streamcrew data restore`", api.ErrRestoreRunning)
}

// backupView converts a backup.Info to its API view.
func backupView(info backup.Info) api.BackupInfo {
	return api.BackupInfo{
		ID:            strings.TrimSuffix(filepath.Base(info.Path), ".zip"),
		CreatedAt:     info.Manifest.CreatedAt,
		SchemaVersion: info.Manifest.SchemaVersion,
		Size:          info.Size,
		Kind:          string(info.Manifest.Kind),
	}
}
