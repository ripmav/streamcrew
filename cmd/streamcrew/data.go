// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"text/tabwriter"
	"time"

	"github.com/ripmav/streamcrew/internal/app"
	"github.com/ripmav/streamcrew/internal/backup"
	"github.com/ripmav/streamcrew/internal/buildinfo"
	"github.com/ripmav/streamcrew/internal/config"
	"github.com/ripmav/streamcrew/internal/lockfile"
	"github.com/ripmav/streamcrew/internal/profile"
	"github.com/ripmav/streamcrew/internal/store"
	"github.com/ripmav/streamcrew/internal/vault"
)

// profiles returns the profile manager; profiles it opens get a backup
// before migrations, like in the core.
func (e *runEnv) profiles(cfg *config.Config) *profile.Manager {
	return profile.NewManager(cfg.DataDir,
		store.WithBeforeMigrate(app.PreMigrationBackup(app.BackupDir(cfg.DataDir), buildinfo.Read().Version, discardLogger())))
}

// lock takes the data directory lock for commands that change profiles.
func (e *runEnv) lock(cfg *config.Config) (*lockfile.Lock, error) {
	return app.LockDataDir(cfg.DataDir)
}

// profileID returns --profile or the active profile.
func (e *runEnv) profileID(cfg *config.Config) (string, error) {
	if cfg.Profile != "" {
		return cfg.Profile, nil
	}
	return e.profiles(cfg).Active()
}

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// snapshot backs up a profile read-only, without migrating it.
func snapshot(ctx context.Context, cfg *config.Config, id string, kind backup.Kind) (backup.Info, error) {
	ro, err := store.OpenReadOnly(ctx, profile.NewManager(cfg.DataDir).Path(id))
	if err != nil {
		return backup.Info{}, err
	}
	defer ro.Close()
	return backup.Create(ctx, ro, app.BackupDir(cfg.DataDir), backup.Request{
		ProfileID:   id,
		ProfileName: ro.Meta(profile.MetaName),
		Kind:        kind,
		AppVersion:  buildinfo.Read().Version,
	})
}

type profileCmd struct {
	List   profileListCmd   `cmd:"" help:"List the profiles."`
	Create profileCreateCmd `cmd:"" help:"Create a profile."`
	Rename profileRenameCmd `cmd:"" help:"Change the display name of a profile."`
	Use    profileUseCmd    `cmd:"" help:"Make a profile the active one."`
	Delete profileDeleteCmd `cmd:"" help:"Delete a profile; a backup is made first."`
}

type profileListCmd struct {
	output
}

// Run lists the profiles.
func (c profileListCmd) Run(ctx context.Context, e *runEnv) error {
	cfg, err := e.resolve()
	if err != nil {
		return err
	}
	list, err := e.profiles(cfg).List(ctx)
	if err != nil {
		return err
	}
	if c.Output == "json" {
		return writeJSON(e.stdout, list)
	}
	tw := tabwriter.NewWriter(e.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ACTIVE\tID\tNAME\tCREATED")
	for _, p := range list {
		active := ""
		if p.Active {
			active = "*"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", active, p.ID, p.Name, formatTime(p.CreatedAt))
	}
	return tw.Flush()
}

type profileCreateCmd struct {
	Name string `arg:"" help:"Display name."`
	ID   string `help:"Profile ID (a-z, 0-9, -). Default: derived from the name."`
}

// Run creates a profile.
func (c profileCreateCmd) Run(ctx context.Context, e *runEnv) (err error) {
	cfg, err := e.resolve()
	if err != nil {
		return err
	}
	l, err := e.lock(cfg)
	if err != nil {
		return err
	}
	defer release(l, &err)
	p, err := e.profiles(cfg).Create(ctx, c.Name, c.ID)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(e.stdout, "created profile %s (%s)\n", p.ID, p.Name)
	return err
}

type profileRenameCmd struct {
	ID   string `arg:"" help:"Profile ID."`
	Name string `arg:"" help:"New display name."`
}

// Run renames a profile.
func (c profileRenameCmd) Run(ctx context.Context, e *runEnv) (err error) {
	cfg, err := e.resolve()
	if err != nil {
		return err
	}
	l, err := e.lock(cfg)
	if err != nil {
		return err
	}
	defer release(l, &err)
	if err := e.profiles(cfg).Rename(ctx, c.ID, c.Name); err != nil {
		return err
	}
	_, err = fmt.Fprintf(e.stdout, "renamed profile %s to %s\n", c.ID, c.Name)
	return err
}

type profileUseCmd struct {
	ID string `arg:"" help:"Profile ID."`
}

// Run makes a profile the active one.
func (c profileUseCmd) Run(e *runEnv) (err error) {
	cfg, err := e.resolve()
	if err != nil {
		return err
	}
	l, err := e.lock(cfg)
	if err != nil {
		return err
	}
	defer release(l, &err)
	if err := e.profiles(cfg).SetActive(c.ID); err != nil {
		return err
	}
	_, err = fmt.Fprintf(e.stdout, "profile %s is now active\n", c.ID)
	return err
}

type profileDeleteCmd struct {
	ID  string `arg:"" help:"Profile ID."`
	Yes bool   `help:"Confirm the deletion."`
}

// Run deletes a profile after backing it up.
func (c profileDeleteCmd) Run(ctx context.Context, e *runEnv) (err error) {
	cfg, err := e.resolve()
	if err != nil {
		return err
	}
	if !c.Yes {
		return &usageError{err: fmt.Errorf("deleting profile %q needs --yes", c.ID)}
	}
	l, err := e.lock(cfg)
	if err != nil {
		return err
	}
	defer release(l, &err)
	profiles := e.profiles(cfg)
	if _, err := profiles.Get(ctx, c.ID); err != nil {
		return err
	}
	info, err := snapshot(ctx, cfg, c.ID, backup.KindPreDelete)
	if err != nil {
		return fmt.Errorf("backup before deleting: %w", err)
	}
	if err := profiles.Delete(c.ID); err != nil {
		return err
	}
	_, err = fmt.Fprintf(e.stdout, "deleted profile %s; backup: %s\n", c.ID, info.Path)
	return err
}

type backupCmd struct {
	Create  backupCreateCmd  `cmd:"" help:"Back up the profile (--profile or the active one); works while the core runs."`
	List    backupListCmd    `cmd:"" help:"List the backups of the profile."`
	Restore backupRestoreCmd `cmd:"" help:"Restore a backup into its profile (or --profile); needs a stopped core."`
}

type backupCreateCmd struct{}

// Run backs a profile up.
func (backupCreateCmd) Run(ctx context.Context, e *runEnv) error {
	cfg, err := e.resolve()
	if err != nil {
		return err
	}
	id, err := e.profileID(cfg)
	if err != nil {
		return err
	}
	if !e.profiles(cfg).Exists(id) {
		return fmt.Errorf("%w: %q", profile.ErrNotFound, id)
	}
	info, err := snapshot(ctx, cfg, id, backup.KindManual)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(e.stdout, "%s\n", info.Path)
	return err
}

type backupListCmd struct {
	output
}

// Run lists the backups of a profile.
func (c backupListCmd) Run(e *runEnv) error {
	cfg, err := e.resolve()
	if err != nil {
		return err
	}
	id, err := e.profileID(cfg)
	if err != nil {
		return err
	}
	list, err := backup.List(app.BackupDir(cfg.DataDir), id)
	if err != nil {
		return err
	}
	if c.Output == "json" {
		if list == nil {
			list = []backup.Info{}
		}
		return writeJSON(e.stdout, list)
	}
	tw := tabwriter.NewWriter(e.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CREATED\tKIND\tSCHEMA\tSIZE\tFILE")
	for _, b := range list {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\n",
			formatTime(b.Manifest.CreatedAt), b.Manifest.Kind, b.Manifest.SchemaVersion, b.Size, b.Path)
	}
	return tw.Flush()
}

type backupRestoreCmd struct {
	File string `arg:"" type:"existingfile" help:"Backup file (.zip)."`
	Yes  bool   `help:"Confirm that the profile is replaced."`
}

// Run restores a backup.
func (c backupRestoreCmd) Run(ctx context.Context, e *runEnv) (err error) {
	cfg, err := e.resolve()
	if err != nil {
		return err
	}
	info, err := backup.Read(c.File)
	if err != nil {
		return err
	}
	id := info.Manifest.ProfileID
	if cfg.Profile != "" {
		id = cfg.Profile
	}
	if !profile.ValidID(id) {
		return fmt.Errorf("%w: %q", profile.ErrInvalidID, id)
	}
	if !c.Yes {
		return &usageError{err: fmt.Errorf("restoring replaces profile %q; confirm with --yes", id)}
	}
	l, err := e.lock(cfg)
	if err != nil {
		return err
	}
	defer release(l, &err)
	profiles := e.profiles(cfg)
	if profiles.Exists(id) {
		pre, err := snapshot(ctx, cfg, id, backup.KindPreRestore)
		if err != nil {
			return fmt.Errorf("backup before restoring: %w", err)
		}
		fmt.Fprintf(e.stdout, "backed up the current state: %s\n", pre.Path)
	}
	if _, err := backup.Restore(ctx, c.File, profiles.Path(id), store.LatestVersion()); err != nil {
		return err
	}
	_, err = fmt.Fprintf(e.stdout, "restored profile %s from %s\n", id, c.File)
	return err
}

type vaultCmd struct {
	Rotate vaultRotateCmd `cmd:"" help:"Create a new key and re-encrypt the tokens of all profiles; needs a stopped core."`
}

type vaultRotateCmd struct{}

// Run rotates the key of the data directory.
func (vaultRotateCmd) Run(ctx context.Context, e *runEnv) (err error) {
	cfg, err := e.resolve()
	if err != nil {
		return err
	}
	l, err := e.lock(cfg)
	if err != nil {
		return err
	}
	defer release(l, &err)

	keys := vault.NewKeys(cfg.DataDir, e.envKey, vault.SystemKeyring{}, discardLogger())
	ks, err := keys.Load(ctx)
	if err != nil {
		return err
	}
	profiles := e.profiles(cfg)
	list, err := profiles.List(ctx)
	if err != nil {
		return err
	}
	repos := make([]vault.Repository, 0, len(list))
	for _, p := range list {
		// openErr, not err: the deferred Close must add to the named result.
		s, openErr := store.Open(ctx, p.Path, store.WithBeforeMigrate(
			app.PreMigrationBackup(app.BackupDir(cfg.DataDir), buildinfo.Read().Version, discardLogger())))
		if openErr != nil {
			return openErr
		}
		defer func() { err = errors.Join(err, s.Close()) }()
		repos = append(repos, s)
	}
	next, err := vault.Rotate(ctx, keys, ks, repos...)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(e.stdout, "new key %s in the %s; %d profiles re-encrypted\n", next.Current, next.Source(), len(repos))
	return err
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

// release gives up the data directory lock and adds a failure to *err.
func release(l *lockfile.Lock, err *error) {
	*err = errors.Join(*err, l.Release())
}
