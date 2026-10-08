// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"time"

	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/store/sqlcgen"
)

var _ user.Repository = (*Store)(nil)

// User implements user.Repository.
func (s *Store) User(ctx context.Context, userID id.ID) (user.User, error) {
	var u user.User
	err := s.readTx(ctx, func(q *sqlcgen.Queries) error {
		var err error
		u, err = loadUser(ctx, q, userID.String())
		return err
	})
	if err != nil {
		return user.User{}, fmt.Errorf("user %s: %w", userID, err)
	}
	return u, nil
}

// UserByIdentity implements user.Repository.
func (s *Store) UserByIdentity(ctx context.Context, p platform.Name, platformUserID string) (user.User, error) {
	var u user.User
	err := s.readTx(ctx, func(q *sqlcgen.Queries) error {
		ident, err := q.GetIdentity(ctx, sqlcgen.GetIdentityParams{Platform: string(p), PlatformUserID: platformUserID})
		if err != nil {
			return err
		}
		u, err = loadUser(ctx, q, ident.UserID)
		return err
	})
	if err != nil {
		return user.User{}, fmt.Errorf("user of %s account %q: %w", p, platformUserID, err)
	}
	return u, nil
}

// UserByLogin returns the user of the account with the login name on
// platform p, regardless of case for ASCII letters, as login names of the
// platforms are; of several accounts with the name, the one changed last.
// The error wraps ErrNotFound if there is none.
func (s *Store) UserByLogin(ctx context.Context, p platform.Name, login string) (user.User, error) {
	var u user.User
	err := s.readTx(ctx, func(q *sqlcgen.Queries) error {
		ident, err := q.GetIdentityByLogin(ctx, sqlcgen.GetIdentityByLoginParams{Platform: string(p), Login: login})
		if err != nil {
			return err
		}
		u, err = loadUser(ctx, q, ident.UserID)
		return err
	})
	if err != nil {
		return user.User{}, fmt.Errorf("user of %s login %q: %w", p, login, err)
	}
	return u, nil
}

// ResetStrikes sets the strikes of every user to 0 (actions.md, B84).
func (s *Store) ResetStrikes(ctx context.Context) error {
	if err := s.Write(ctx, func(q *sqlcgen.Queries) error { return q.ResetStrikes(ctx) }); err != nil {
		return fmt.Errorf("reset strikes: %w", err)
	}
	return nil
}

// UpsertIdentity implements user.Repository.
func (s *Store) UpsertIdentity(ctx context.Context, ident user.Identity) (u user.User, created bool, err error) {
	if err := ident.Validate(); err != nil {
		return user.User{}, false, err
	}
	err = s.Write(ctx, func(q *sqlcgen.Queries) error {
		t := now()
		row, err := q.GetIdentity(ctx, sqlcgen.GetIdentityParams{Platform: string(ident.Platform), PlatformUserID: ident.PlatformUserID})
		switch {
		case err == nil:
			err = q.UpdateIdentityNames(ctx, sqlcgen.UpdateIdentityNamesParams{
				Login:          ident.Login,
				DisplayName:    ident.DisplayName,
				Color:          ident.Color,
				AvatarUrl:      ident.AvatarURL,
				UpdatedAt:      t.UnixMilli(),
				Platform:       row.Platform,
				PlatformUserID: row.PlatformUserID,
			})
			if err != nil {
				return err
			}
			u, err = loadUser(ctx, q, row.UserID)
			return err
		case errors.Is(err, sql.ErrNoRows):
			created = true
			userID := id.New().String()
			if err := insertUser(ctx, q, userID, t); err != nil {
				return err
			}
			if err := insertIdentity(ctx, q, userID, ident, t); err != nil {
				return err
			}
			u, err = loadUser(ctx, q, userID)
			return err
		default:
			return err
		}
	})
	if err != nil {
		return user.User{}, false, fmt.Errorf("upsert %s account %q: %w", ident.Platform, ident.PlatformUserID, err)
	}
	return u, created, nil
}

// AddIdentity implements user.Repository.
func (s *Store) AddIdentity(ctx context.Context, userID id.ID, ident user.Identity) error {
	if err := ident.Validate(); err != nil {
		return err
	}
	err := s.Write(ctx, func(q *sqlcgen.Queries) error {
		if _, err := q.GetUser(ctx, userID.String()); err != nil {
			return err
		}
		return insertIdentity(ctx, q, userID.String(), ident, now())
	})
	if err != nil {
		return fmt.Errorf("add %s account %q to user %s: %w", ident.Platform, ident.PlatformUserID, userID, err)
	}
	return nil
}

// SetRoles implements user.Repository.
func (s *Store) SetRoles(ctx context.Context, p platform.Name, platformUserID string, roles role.Set) error {
	if roles.Has(role.Regular) {
		return fmt.Errorf("%w: regular is granted by streamcrew, not by a platform", user.ErrInvalid)
	}
	doc, err := json.Marshal(roles, json.Deterministic(true))
	if err != nil {
		return err
	}
	return s.Write(ctx, func(q *sqlcgen.Queries) error {
		n, err := q.UpdateIdentityRoles(ctx, sqlcgen.UpdateIdentityRolesParams{
			Roles:          string(doc),
			UpdatedAt:      now().UnixMilli(),
			Platform:       string(p),
			PlatformUserID: platformUserID,
		})
		if err != nil {
			return err
		}
		return notFound(n, fmt.Sprintf("%s account %q", p, platformUserID))
	})
}

// SetPlatformData implements user.Repository.
func (s *Store) SetPlatformData(ctx context.Context, p platform.Name, platformUserID string, data user.PlatformData) error {
	if data.SubTier < 0 {
		return fmt.Errorf("%w: negative subscription tier", user.ErrInvalid)
	}
	return s.Write(ctx, func(q *sqlcgen.Queries) error {
		n, err := q.UpdateIdentityData(ctx, sqlcgen.UpdateIdentityDataParams{
			FollowedAt:       nullMillis(data.FollowedAt),
			SubscribedAt:     nullMillis(data.SubscribedAt),
			SubTier:          int64(data.SubTier),
			AccountCreatedAt: nullMillis(data.AccountCreatedAt),
			DataUpdatedAt:    nullMillis(data.UpdatedAt),
			UpdatedAt:        now().UnixMilli(),
			Platform:         string(p),
			PlatformUserID:   platformUserID,
		})
		if err != nil {
			return err
		}
		return notFound(n, fmt.Sprintf("%s account %q", p, platformUserID))
	})
}

// UpdateUser implements user.Repository. Changes to ID, identities and
// timestamps made by fn are ignored.
func (s *Store) UpdateUser(ctx context.Context, userID id.ID, fn func(*user.User) error) (user.User, error) {
	var u user.User
	err := s.Write(ctx, func(q *sqlcgen.Queries) error {
		stored, err := loadUser(ctx, q, userID.String())
		if err != nil {
			return err
		}
		u = stored
		if err := fn(&u); err != nil {
			return err
		}
		err = q.UpdateUser(ctx, sqlcgen.UpdateUserParams{
			Title:             u.Title,
			Notes:             u.Notes,
			Excluded:          flag(u.Excluded),
			Regular:           flag(u.Regular),
			EntranceCommandID: nullID(u.EntranceCommand),
			UpdatedAt:         now().UnixMilli(),
			ID:                stored.ID.String(),
		})
		if err != nil {
			return err
		}
		if err := putStats(ctx, q, stored.ID.String(), u.Stats); err != nil {
			return err
		}
		u, err = loadUser(ctx, q, stored.ID.String())
		return err
	})
	if err != nil {
		return user.User{}, fmt.Errorf("update user %s: %w", userID, err)
	}
	return u, nil
}

// DeleteUser implements user.Repository; identities and statistics go with
// the user.
func (s *Store) DeleteUser(ctx context.Context, userID id.ID) error {
	return s.Write(ctx, func(q *sqlcgen.Queries) error {
		n, err := q.DeleteUser(ctx, userID.String())
		if err != nil {
			return err
		}
		return notFound(n, "user "+userID.String())
	})
}

func insertUser(ctx context.Context, q *sqlcgen.Queries, userID string, t time.Time) error {
	err := q.InsertUser(ctx, sqlcgen.InsertUserParams{ID: userID, CreatedAt: t.UnixMilli(), UpdatedAt: t.UnixMilli()})
	if err != nil {
		return err
	}
	return putStats(ctx, q, userID, user.Stats{})
}

func insertIdentity(ctx context.Context, q *sqlcgen.Queries, userID string, ident user.Identity, t time.Time) error {
	roles, err := json.Marshal(ident.Roles, json.Deterministic(true))
	if err != nil {
		return err
	}
	return q.InsertIdentity(ctx, sqlcgen.InsertIdentityParams{
		Platform:         string(ident.Platform),
		PlatformUserID:   ident.PlatformUserID,
		UserID:           userID,
		Login:            ident.Login,
		DisplayName:      ident.DisplayName,
		Color:            ident.Color,
		AvatarUrl:        ident.AvatarURL,
		Roles:            string(roles),
		FollowedAt:       nullMillis(ident.Data.FollowedAt),
		SubscribedAt:     nullMillis(ident.Data.SubscribedAt),
		SubTier:          int64(ident.Data.SubTier),
		AccountCreatedAt: nullMillis(ident.Data.AccountCreatedAt),
		DataUpdatedAt:    nullMillis(ident.Data.UpdatedAt),
		CreatedAt:        t.UnixMilli(),
		UpdatedAt:        t.UnixMilli(),
	})
}

func putStats(ctx context.Context, q *sqlcgen.Queries, userID string, st user.Stats) error {
	return q.PutUserStats(ctx, sqlcgen.PutUserStatsParams{
		UserID:         userID,
		WatchMinutes:   st.WatchMinutes,
		Messages:       st.Messages,
		CommandsRun:    st.CommandsRun,
		Mentions:       st.Mentions,
		StreamsWatched: st.StreamsWatched,
		FirstSeen:      nullMillis(st.FirstSeen),
		LastSeen:       nullMillis(st.LastSeen),
		DonatedCents:   st.DonatedCents,
		Strikes:        st.Strikes,
	})
}

// loadUser reads a user with statistics and identities.
func loadUser(ctx context.Context, q *sqlcgen.Queries, userID string) (user.User, error) {
	row, err := q.GetUser(ctx, userID)
	if err != nil {
		return user.User{}, err
	}
	st, err := q.GetUserStats(ctx, userID)
	if err != nil {
		return user.User{}, err
	}
	idents, err := q.ListIdentities(ctx, userID)
	if err != nil {
		return user.User{}, err
	}
	uid, err := id.Parse(row.ID)
	if err != nil {
		return user.User{}, err
	}
	entrance, err := parseNullID(row.EntranceCommandID)
	if err != nil {
		return user.User{}, err
	}
	u := user.User{
		ID:              uid,
		Title:           row.Title,
		Notes:           row.Notes,
		Excluded:        row.Excluded != 0,
		Regular:         row.Regular != 0,
		EntranceCommand: entrance,
		Stats: user.Stats{
			WatchMinutes:   st.WatchMinutes,
			Messages:       st.Messages,
			CommandsRun:    st.CommandsRun,
			Mentions:       st.Mentions,
			StreamsWatched: st.StreamsWatched,
			FirstSeen:      fromNullMillis(st.FirstSeen),
			LastSeen:       fromNullMillis(st.LastSeen),
			DonatedCents:   st.DonatedCents,
			Strikes:        st.Strikes,
		},
		Identities: make([]user.Identity, 0, len(idents)),
		CreatedAt:  fromMillis(row.CreatedAt),
		UpdatedAt:  fromMillis(row.UpdatedAt),
	}
	for _, i := range idents {
		var roles role.Set
		if err := json.Unmarshal([]byte(i.Roles), &roles); err != nil {
			return user.User{}, fmt.Errorf("%s account %q: %w", i.Platform, i.PlatformUserID, err)
		}
		u.Identities = append(u.Identities, user.Identity{
			Platform:       platform.Name(i.Platform),
			PlatformUserID: i.PlatformUserID,
			Login:          i.Login,
			DisplayName:    i.DisplayName,
			Color:          i.Color,
			AvatarURL:      i.AvatarUrl,
			Roles:          roles,
			Data: user.PlatformData{
				FollowedAt:       fromNullMillis(i.FollowedAt),
				SubscribedAt:     fromNullMillis(i.SubscribedAt),
				SubTier:          int(i.SubTier),
				AccountCreatedAt: fromNullMillis(i.AccountCreatedAt),
				UpdatedAt:        fromNullMillis(i.DataUpdatedAt),
			},
		})
	}
	return u, nil
}
