// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ripmav/streamcrew/internal/api"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/domain/user"
)

// Default limits of the user API when the request does not set one.
const (
	apiUserListLimit    = 50
	apiUserRankingLimit = 10
)

// apiUsers adapts the store to the api.Users port (roadmap 6.2.1, API).
type apiUsers struct{ a *App }

// toView converts a domain user to its API view.
func (u apiUsers) toView(x user.User) api.User {
	v := api.User{
		ID:           x.ID.String(),
		Role:         string(primaryRole(x)),
		Title:        x.Title,
		Excluded:     x.Excluded,
		Strikes:      toStrikes(x.Stats.Strikes),
		WatchMinutes: x.Stats.WatchMinutes,
		Messages:     x.Stats.Messages,
		CommandsRun:  x.Stats.CommandsRun,
		Mentions:     x.Stats.Mentions,
		FirstSeen:    x.Stats.FirstSeen,
		LastSeen:     x.Stats.LastSeen,
	}
	for _, i := range x.Identities {
		v.Identities = append(v.Identities, api.UserIdentity{
			Platform:       string(i.Platform),
			PlatformUserID: i.PlatformUserID,
			Login:          i.Login,
			DisplayName:    i.DisplayName,
			FollowedAt:     i.Data.FollowedAt,
		})
	}
	return v
}

// primaryRole is the user's highest role across all identities, plus the
// regular role if the user is a regular (B22, B26).
func primaryRole(x user.User) role.Role {
	var set role.Set
	for _, i := range x.Identities {
		set = set.Union(i.Roles)
	}
	if x.Regular {
		set = set.With(role.Regular)
	}
	return set.Primary()
}

// parsePlatformLogin splits a "platform/login" reference.
func parsePlatformLogin(s string) (platform.Name, string, error) {
	p, login, ok := strings.Cut(s, "/")
	if !ok || p == "" || login == "" {
		return "", "", fmt.Errorf("platform login %q: want \"platform/login\"", s)
	}
	return platform.Name(p), login, nil
}

// List returns the users; stat ranks by a statistic, else prefix filters.
func (u apiUsers) List(ctx context.Context, prefix, stat string, limit int) ([]api.User, error) {
	if limit <= 0 {
		limit = apiUserListLimit
	}
	if stat != "" {
		s, err := user.ParseStat(stat)
		if err != nil {
			return nil, err
		}
		ranked, err := u.a.store.TopUsers(ctx, s, limit)
		if err != nil {
			return nil, err
		}
		out := make([]api.User, len(ranked))
		for i, r := range ranked {
			out[i] = u.toView(r.User)
		}
		return out, nil
	}
	users, err := u.a.store.ListUsers(ctx, prefix, limit)
	if err != nil {
		return nil, err
	}
	out := make([]api.User, len(users))
	for i, x := range users {
		out[i] = u.toView(x)
	}
	return out, nil
}

// Get returns one user by ID and/or platform login.
func (u apiUsers) Get(ctx context.Context, idStr, platformLogin string) (api.User, error) {
	switch {
	case idStr != "":
		uid, err := id.Parse(idStr)
		if err != nil {
			return api.User{}, err
		}
		x, err := u.a.store.User(ctx, uid)
		if err != nil {
			return api.User{}, err
		}
		return u.toView(x), nil
	case platformLogin != "":
		p, login, err := parsePlatformLogin(platformLogin)
		if err != nil {
			return api.User{}, err
		}
		x, err := u.a.store.UserByLogin(ctx, p, login)
		if err != nil {
			return api.User{}, err
		}
		return u.toView(x), nil
	default:
		return api.User{}, errors.New("get user: set id or platform_login")
	}
}

// Update edits the user: title when set (nil keeps it, an empty value
// clears it), role (added to every identity), excluded and strikes when
// set.
func (u apiUsers) Update(ctx context.Context, idStr, roleStr string, title *string, excluded *bool, strikes *uint32) (api.User, error) {
	uid, err := id.Parse(idStr)
	if err != nil {
		return api.User{}, err
	}
	if roleStr != "" {
		r, err := role.Parse(roleStr)
		if err != nil {
			return api.User{}, err
		}
		stored, err := u.a.store.User(ctx, uid)
		if err != nil {
			return api.User{}, err
		}
		for _, i := range stored.Identities {
			if err := u.a.store.SetRoles(ctx, i.Platform, i.PlatformUserID, i.Roles.With(r)); err != nil {
				return api.User{}, err
			}
		}
	}
	updated, err := u.a.store.UpdateUser(ctx, uid, func(x *user.User) error {
		if title != nil {
			x.Title = *title
		}
		if excluded != nil {
			x.Excluded = *excluded
		}
		if strikes != nil {
			x.Stats.Strikes = int64(*strikes)
		}
		return nil
	})
	if err != nil {
		return api.User{}, err
	}
	return u.toView(updated), nil
}

// Merge folds the merge account into the keep account.
func (u apiUsers) Merge(ctx context.Context, keepPlatformLogin, mergePlatformLogin string) (string, error) {
	keepP, keepLogin, err := parsePlatformLogin(keepPlatformLogin)
	if err != nil {
		return "", err
	}
	mergeP, mergeLogin, err := parsePlatformLogin(mergePlatformLogin)
	if err != nil {
		return "", err
	}
	keep, err := u.a.store.UserByLogin(ctx, keepP, keepLogin)
	if err != nil {
		return "", err
	}
	merge, err := u.a.store.UserByLogin(ctx, mergeP, mergeLogin)
	if err != nil {
		return "", err
	}
	out, err := u.a.store.MergeUsers(ctx, keep.ID, merge.ID)
	if err != nil {
		return "", err
	}
	return out.ID.String(), nil
}

// Rankings returns the top users per statistic.
func (u apiUsers) Rankings(ctx context.Context, limit int) ([]api.Ranking, error) {
	if limit <= 0 {
		limit = apiUserRankingLimit
	}
	stats := []user.Stat{user.StatWatchMinutes, user.StatMessages, user.StatCommandsRun, user.StatMentions}
	out := make([]api.Ranking, 0, len(stats))
	for _, s := range stats {
		ranked, err := u.a.store.TopUsers(ctx, s, limit)
		if err != nil {
			return nil, err
		}
		r := api.Ranking{Stat: string(s)}
		for _, x := range ranked {
			r.Users = append(r.Users, api.RankedUser{ID: x.User.ID.String(), Login: primaryLogin(x.User), Value: x.Value})
		}
		out = append(out, r)
	}
	return out, nil
}

// primaryLogin is the login of the user's first identity, for rankings.
func primaryLogin(x user.User) string {
	if len(x.Identities) == 0 {
		return ""
	}
	return x.Identities[0].Login
}

// toStrikes converts the stored strike count to its uint32 view, clamped to
// the range (gosec G115): a negative count is 0, a count above the maximum
// is the maximum.
func toStrikes(n int64) uint32 {
	const maxStrikes = uint64(1<<32 - 1)
	switch {
	case n < 0:
		return 0
	case uint64(n) > maxStrikes:
		return uint32(maxStrikes)
	}
	return uint32(n)
}
