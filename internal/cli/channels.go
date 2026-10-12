// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ripmav/streamcrew/internal/app"
	"github.com/ripmav/streamcrew/internal/buildinfo"
	"github.com/ripmav/streamcrew/internal/config"
	"github.com/ripmav/streamcrew/internal/profile"
	"github.com/ripmav/streamcrew/internal/settings"
	"github.com/ripmav/streamcrew/internal/store"
)

// channelsCmd manages the additional channels of the profile (the
// settings section "channels", spec twitch-multi-channel.md, B1).
type channelsCmd struct {
	List   channelsListCmd   `cmd:"" help:"List the additional channels of the profile. Works while the core runs."`
	Add    channelsAddCmd    `cmd:"" help:"Add a channel the bot operates in. The bot account must moderate it. Needs a stopped core."`
	Remove channelsRemoveCmd `cmd:"" name:"remove" help:"Remove an additional channel. Needs a stopped core."`
}

type channelsListCmd struct {
	output
}

// Run lists the additional channels of the profile.
func (c channelsListCmd) Run(ctx context.Context, e *Env) error {
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
	st, err := store.OpenReadOnly(ctx, e.profiles(cfg).Path(id))
	if err != nil {
		return fmt.Errorf("open profile %q: %w", id, err)
	}
	defer st.Close()
	svc, err := settings.New(readOnlySettings{ro: st})
	if err != nil {
		return err
	}
	chs, err := settings.Load(ctx, svc, settings.DefaultChannels())
	if err != nil {
		return err
	}
	if c.Output == "json" {
		return writeJSON(e.Stdout, chs)
	}
	if len(chs.Additional) == 0 {
		_, err = fmt.Fprintln(e.Stdout, "no additional channels")
		return err
	}
	for _, login := range chs.Additional {
		if _, err := fmt.Fprintln(e.Stdout, login); err != nil {
			return err
		}
	}
	return nil
}

type channelsAddCmd struct {
	Login string `arg:"" help:"Login of the channel, e.g. the name after the slash in its URL."`
}

// Run adds the channel to the profile.
func (c channelsAddCmd) Run(ctx context.Context, e *Env) (err error) {
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
	l, err := e.lock(cfg)
	if err != nil {
		return err
	}
	defer release(l, &err)
	st, err := e.openProfile(ctx, cfg, id)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, st.Close()) }()
	svc, err := settings.New(st)
	if err != nil {
		return err
	}
	chs, err := settings.Load(ctx, svc, settings.DefaultChannels())
	if err != nil {
		return err
	}
	for _, l := range chs.Additional {
		if strings.EqualFold(l, c.Login) {
			return &usageError{err: fmt.Errorf("the channel %q is already listed", c.Login)}
		}
	}
	chs.Additional = append(chs.Additional, c.Login)
	if err := settings.Save(ctx, svc, chs); err != nil {
		return &usageError{err: err}
	}
	_, err = fmt.Fprintf(e.Stdout, "added channel %s\n", c.Login)
	return err
}

type channelsRemoveCmd struct {
	Login string `arg:"" help:"Login of the channel, case-insensitive."`
}

// Run removes the channel from the profile.
func (c channelsRemoveCmd) Run(ctx context.Context, e *Env) (err error) {
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
	l, err := e.lock(cfg)
	if err != nil {
		return err
	}
	defer release(l, &err)
	st, err := e.openProfile(ctx, cfg, id)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, st.Close()) }()
	svc, err := settings.New(st)
	if err != nil {
		return err
	}
	chs, err := settings.Load(ctx, svc, settings.DefaultChannels())
	if err != nil {
		return err
	}
	rest := make([]string, 0, len(chs.Additional))
	found := false
	for _, l := range chs.Additional {
		if strings.EqualFold(l, c.Login) {
			found = true
			continue
		}
		rest = append(rest, l)
	}
	if !found {
		return &usageError{err: fmt.Errorf("the channel %q is not listed", c.Login)}
	}
	chs.Additional = rest
	if err := settings.Save(ctx, svc, chs); err != nil {
		return &usageError{err: err}
	}
	_, err = fmt.Fprintf(e.Stdout, "removed channel %s\n", c.Login)
	return err
}

// openProfile opens the database of a profile, with a backup before
// migrations, like the commands that change profiles.
func (e *Env) openProfile(ctx context.Context, cfg *config.Config, id string) (*store.Store, error) {
	st, err := store.Open(ctx, e.profiles(cfg).Path(id), store.WithBeforeMigrate(
		app.PreMigrationBackup(app.BackupDir(cfg.DataDir), buildinfo.Read().Version, discardLogger())))
	return st, err
}

// readOnlySettings adapts a read-only profile database to the settings
// repository; list never writes.
type readOnlySettings struct {
	ro *store.ReadOnly
}

func (r readOnlySettings) Settings(ctx context.Context, section string) ([]byte, bool, error) {
	return r.ro.Settings(ctx, section)
}

func (readOnlySettings) PutSettings(context.Context, string, []byte) error {
	return errors.New("the profile is opened read-only")
}
