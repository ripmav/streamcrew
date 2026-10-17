// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/profile"
	"github.com/ripmav/streamcrew/internal/store"
)

// userCmd manages the users of the profile (roadmap 5.2.4,
// users-and-roles.md, B40).
type userCmd struct {
	List  userListCmd  `cmd:"" help:"List the users of a running core through the API."`
	Show  userShowCmd  `cmd:"" help:"Show one user of a running core through the API."`
	Set   userSetCmd   `cmd:"" help:"Change the role, title, strikes and exclusion of a user of a running core through the API."`
	Merge userMergeCmd `cmd:"" help:"Merge two users: link all of the second user's platform accounts to the first user, add the second user's statistics to the first, and delete the second user. Needs a stopped core."`
}

type userMergeCmd struct {
	KeepPlatform string `arg:"" help:"Platform of the user to keep, e.g. twitch."`
	KeepLogin    string `arg:"" help:"Login of the user to keep."`
	DropPlatform string `arg:"" help:"Platform of the user to merge in."`
	DropLogin    string `arg:"" help:"Login of the user to merge in."`
}

// Run merges the second user into the first (users-and-roles.md, B40): the
// accounts of the second are linked to the first, the statistics are added,
// and the second is deleted.
func (c userMergeCmd) Run(ctx context.Context, e *Env) (err error) {
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

	keepPlatform, dropPlatform := platform.Name(c.KeepPlatform), platform.Name(c.DropPlatform)
	if err := keepPlatform.Validate(); err != nil {
		return &usageError{err: err}
	}
	if err := dropPlatform.Validate(); err != nil {
		return &usageError{err: err}
	}
	keep, err := st.UserByLogin(ctx, keepPlatform, c.KeepLogin)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return &usageError{err: fmt.Errorf("no user with login %q on %s", c.KeepLogin, keepPlatform)}
		}
		return err
	}
	drop, err := st.UserByLogin(ctx, dropPlatform, c.DropLogin)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return &usageError{err: fmt.Errorf("no user with login %q on %s", c.DropLogin, dropPlatform)}
		}
		return err
	}
	if keep.ID == drop.ID {
		return &usageError{err: fmt.Errorf("%s %q and %s %q are the same user", keepPlatform, c.KeepLogin, dropPlatform, c.DropLogin)}
	}
	merged, err := st.MergeUsers(ctx, keep.ID, drop.ID)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(e.Stdout, "merged %s %q into %s %q (%s)\n", dropPlatform, c.DropLogin, keepPlatform, c.KeepLogin, merged.ID)
	return err
}
