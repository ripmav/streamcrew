// SPDX-License-Identifier: MIT

package connector

import (
	"context"
	"fmt"
	"strings"

	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
)

// Known finds the users the core has seen; the user service implements it
// (roadmap 5.2).
type Known interface {
	// UserByName finds the user with the login name on platform p,
	// regardless of case; ok is false if there is none.
	UserByName(ctx context.Context, p platform.Name, name string) (u user.User, ok bool, err error)
}

// Login returns name as a login name: without surrounding white space and
// without a leading "@", so that a user can be named with or without it
// (actions.md B63, B81, B91).
func Login(name string) string {
	return strings.TrimPrefix(strings.TrimSpace(name), "@")
}

// FindAccount returns the account with the login name on p, regardless of
// case and with or without "@": the one the core knows, and otherwise the
// one the platform reports (actions.md B63, B81). An error wraps
// ErrUnknownUser if neither has one, also for an empty name.
func FindAccount(ctx context.Context, known Known, p Platform, name string) (user.Identity, error) {
	login := Login(name)
	if login == "" {
		return user.Identity{}, fmt.Errorf("find account: %w: empty name", ErrUnknownUser)
	}
	u, ok, err := known.UserByName(ctx, p.Name(), login)
	if err != nil {
		return user.Identity{}, fmt.Errorf("find account %q on %s: %w", login, p.Name(), err)
	}
	if ok {
		// The user may have more than one account on the platform.
		for _, ident := range u.Identities {
			if ident.Platform == p.Name() && strings.EqualFold(ident.Login, login) {
				return ident, nil
			}
		}
	}
	ident, err := p.Users().UserByLogin(ctx, login)
	if err != nil {
		return user.Identity{}, fmt.Errorf("find account %q on %s: %w", login, p.Name(), err)
	}
	return ident, nil
}
