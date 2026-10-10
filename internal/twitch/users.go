// SPDX-License-Identifier: MIT

package twitch

import (
	"context"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/helix"
)

// users is the connector.Users of the twitch platform (roadmap 4.4):
// it looks accounts up through Helix.
type users struct{ p *Platform }

var _ connector.Users = users{}

// UserByLogin implements connector.Users.
func (u users) UserByLogin(ctx context.Context, login string) (user.Identity, error) {
	return u.lookup(ctx, nil, []string{login})
}

// UserByID implements connector.Users.
func (u users) UserByID(ctx context.Context, platformUserID string) (user.Identity, error) {
	return u.lookup(ctx, []string{platformUserID}, nil)
}

// lookup asks Helix for the account and reports connector.ErrUnknownUser
// when it is none.
func (u users) lookup(ctx context.Context, ids, logins []string) (user.Identity, error) {
	if _, err := u.p.account(ctx); err != nil {
		return user.Identity{}, err
	}
	hc, err := u.p.ops(ctx)
	if err != nil {
		return user.Identity{}, err
	}
	found, err := hc.GetUsers(ctx, ids, logins)
	if err != nil {
		return user.Identity{}, err
	}
	if len(found) == 0 {
		return user.Identity{}, connector.ErrUnknownUser
	}
	return userIdentity(found[0]), nil
}

// userIdentity is the user.Identity of a Helix user on twitch.
func userIdentity(u helix.User) user.Identity {
	return user.Identity{
		Platform:       platform.Twitch,
		PlatformUserID: u.ID,
		Login:          u.Login,
		DisplayName:    u.DisplayName,
	}
}
