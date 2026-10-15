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

// AddStats adds each delta to the statistics of its user in one
// transaction (users-and-roles.md, B9): the counters are incremented, and
// the seen times follow the Seen of the delta.
func (s *Store) AddStats(ctx context.Context, deltas []user.StatsDelta) error {
	if len(deltas) == 0 {
		return nil
	}
	err := s.Write(ctx, func(q *sqlcgen.Queries) error {
		for _, d := range deltas {
			if _, err := q.AddUserStats(ctx, sqlcgen.AddUserStatsParams{
				WatchMinutes:   d.WatchMinutes,
				Messages:       d.Messages,
				CommandsRun:    d.CommandsRun,
				Mentions:       d.Mentions,
				StreamsWatched: d.StreamsWatched,
				FirstSeen:      nullMillis(d.Seen),
				LastSeen:       nullMillis(d.Seen),
				UserID:         d.UserID.String(),
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("add statistics: %w", err)
	}
	return nil
}

// ListUsersRegular returns the users and their watch minutes for the sweep
// of the regular role (users-and-roles.md, B26), ordered by ID.
func (s *Store) ListUsersRegular(ctx context.Context) ([]user.RegularSweep, error) {
	var out []user.RegularSweep
	err := s.readTx(ctx, func(q *sqlcgen.Queries) error {
		rows, err := q.ListUsersRegular(ctx)
		if err != nil {
			return err
		}
		out = make([]user.RegularSweep, 0, len(rows))
		for _, r := range rows {
			uid, err := id.Parse(r.ID)
			if err != nil {
				return err
			}
			out = append(out, user.RegularSweep{ID: uid, Regular: r.Regular != 0, WatchMinutes: r.WatchMinutes})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list users for the regular role: %w", err)
	}
	return out, nil
}

// TopUsers returns up to n users ranked by stat, from the highest value down
// (roadmap 5.2.5): excluded users are never ranked (users-and-roles.md, B7),
// neither are users whose value of the statistic is 0, and ties are ordered
// by user ID.
func (s *Store) TopUsers(ctx context.Context, stat user.Stat, n int) ([]user.Ranked, error) {
	if !stat.Valid() {
		return nil, fmt.Errorf("top users: %w: %s", user.ErrInvalid, stat)
	}
	if n < 1 {
		return nil, fmt.Errorf("top users: %w: n = %d", user.ErrInvalid, n)
	}
	var out []user.Ranked
	err := s.readTx(ctx, func(q *sqlcgen.Queries) error {
		var (
			rs  any
			err error
		)
		switch stat {
		case user.StatWatchMinutes:
			rs, err = q.TopUsersWatchMinutes(ctx, int64(n))
		case user.StatMessages:
			rs, err = q.TopUsersMessages(ctx, int64(n))
		case user.StatCommandsRun:
			rs, err = q.TopUsersCommandsRun(ctx, int64(n))
		case user.StatMentions:
			rs, err = q.TopUsersMentions(ctx, int64(n))
		case user.StatStreamsWatched:
			rs, err = q.TopUsersStreamsWatched(ctx, int64(n))
		}
		if err != nil {
			return err
		}
		out, err = s.ranked(ctx, q, rs)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("top users: %w", err)
	}
	return out, nil
}

// ranked loads the users of a top list and pairs them with their value; the
// rows are the generated rows of one of the TopUsers* queries.
func (s *Store) ranked(ctx context.Context, q *sqlcgen.Queries, rows any) ([]user.Ranked, error) {
	var ids []string
	var vals []int64
	switch rs := rows.(type) {
	case []sqlcgen.TopUsersWatchMinutesRow:
		for _, r := range rs {
			ids, vals = append(ids, r.ID), append(vals, r.Val)
		}
	case []sqlcgen.TopUsersMessagesRow:
		for _, r := range rs {
			ids, vals = append(ids, r.ID), append(vals, r.Val)
		}
	case []sqlcgen.TopUsersCommandsRunRow:
		for _, r := range rs {
			ids, vals = append(ids, r.ID), append(vals, r.Val)
		}
	case []sqlcgen.TopUsersMentionsRow:
		for _, r := range rs {
			ids, vals = append(ids, r.ID), append(vals, r.Val)
		}
	case []sqlcgen.TopUsersStreamsWatchedRow:
		for _, r := range rs {
			ids, vals = append(ids, r.ID), append(vals, r.Val)
		}
	default:
		return nil, fmt.Errorf("ranked: %w: unknown row type", user.ErrInvalid)
	}
	out := make([]user.Ranked, 0, len(ids))
	for i, idStr := range ids {
		u, err := loadUser(ctx, q, idStr)
		if err != nil {
			return nil, err
		}
		out = append(out, user.Ranked{User: u, Value: vals[i]})
	}
	return out, nil
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

// MergeUsers folds drop into keep (roadmap 5.2.4, users-and-roles.md
// B40): every platform identity of drop is linked to keep, the
// statistics are added, and drop is deleted. The two users must not share
// a platform account (B3); that is a conflict. keep's own data is kept;
// its statistics gain drop's, its seen times span both. One transaction.
func (s *Store) MergeUsers(ctx context.Context, keep, drop id.ID) (user.User, error) {
	if keep == drop {
		return user.User{}, errors.New("merge users: keep and drop are the same user")
	}
	var merged user.User
	err := s.Atomically(ctx, func(tx *Store) error {
		k, err := tx.User(ctx, keep)
		if err != nil {
			return err
		}
		d, err := tx.User(ctx, drop)
		if err != nil {
			return err
		}
		if err := mergeConflict(k, d); err != nil {
			return err
		}
		err = tx.Write(ctx, func(q *sqlcgen.Queries) error {
			_, err := q.MoveIdentitiesToUser(ctx, sqlcgen.MoveIdentitiesToUserParams{
				UserID:    keep.String(),
				UpdatedAt: now().UnixMilli(),
				UserID_2:  drop.String(),
			})
			return err
		})
		if err != nil {
			return fmt.Errorf("link identities: %w", err)
		}
		merged, err = tx.UpdateUser(ctx, keep, func(u *user.User) error {
			u.Stats = mergeStats(k.Stats, d.Stats)
			return nil
		})
		if err != nil {
			return err
		}
		return tx.DeleteUser(ctx, drop)
	})
	if err != nil {
		return user.User{}, fmt.Errorf("merge %s into %s: %w", drop, keep, err)
	}
	return merged, nil
}

// mergeConflict reports a platform account that belongs to both users (B3):
// the account is already linked, so the merge is refused.
func mergeConflict(k, d user.User) error {
	for _, ki := range k.Identities {
		for _, di := range d.Identities {
			if ki.Platform == di.Platform && ki.PlatformUserID == di.PlatformUserID {
				return fmt.Errorf("%s account %q already belongs to both users", ki.Platform, ki.PlatformUserID)
			}
		}
	}
	return nil
}

// mergeStats adds b to a; the seen times span both (first is the earlier,
// last the later), with a zero time meaning "never seen".
func mergeStats(a, b user.Stats) user.Stats {
	s := a
	s.WatchMinutes += b.WatchMinutes
	s.Messages += b.Messages
	s.CommandsRun += b.CommandsRun
	s.Mentions += b.Mentions
	s.StreamsWatched += b.StreamsWatched
	s.DonatedCents += b.DonatedCents
	s.Strikes += b.Strikes
	s.FirstSeen = earliest(a.FirstSeen, b.FirstSeen)
	s.LastSeen = latest(a.LastSeen, b.LastSeen)
	return s
}

// earliest returns the earlier of two seen times; a zero time means
// "never seen" and loses to a real time, two zero times stay zero.
func earliest(a, b time.Time) time.Time {
	switch {
	case a.IsZero():
		return b
	case b.IsZero():
		return a
	case b.Before(a):
		return b
	default:
		return a
	}
}

// latest returns the later of two seen times; a zero time means "never
// seen" and loses to a real time, two zero times stay zero.
func latest(a, b time.Time) time.Time {
	switch {
	case a.IsZero():
		return b
	case b.IsZero():
		return a
	case b.After(a):
		return b
	default:
		return a
	}
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
