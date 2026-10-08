// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ripmav/streamcrew/internal/store/sqlcgen"
)

// Account is the metadata of a platform login (ADR-0014, ADR-0023); the
// tokens of the login are encrypted in the vault under the name
// "auth/<platform>/<role>". Platform and Role are the raw IDs of the
// platform and the account, mapped to their typed values by the caller.
type Account struct {
	// Platform is the ID of the platform, e.g. "twitch".
	Platform string
	// Role is the account of the platform: "streamer" or "bot".
	Role string
	// Login is the display name of the account, e.g. the channel name.
	Login string
	// UserID is the ID of the user on the platform.
	UserID string
	// Scopes are the granted scopes of the login, space separated.
	Scopes string
	// ClientID is the client ID of the app the login was made with.
	ClientID string
	// Flow is how the login was made: "authorization_code" or
	// "device_code" (ADR-0023); refresh and revoke must use the same flow.
	Flow string
	// UpdatedAt is the time the account was last changed.
	UpdatedAt time.Time
}

// UpsertAccount saves the metadata of a platform login; a new login
// replaces the account of the platform and role and sets the updated time.
func (s *Store) UpsertAccount(ctx context.Context, a Account) error {
	return s.Write(ctx, func(q *sqlcgen.Queries) error {
		return q.UpsertAccount(ctx, sqlcgen.UpsertAccountParams{
			Platform:  a.Platform,
			Role:      a.Role,
			Login:     a.Login,
			UserID:    a.UserID,
			Scopes:    a.Scopes,
			ClientID:  a.ClientID,
			Flow:      a.Flow,
			UpdatedAt: time.Now().UnixMilli(),
		})
	})
}

// Account returns the metadata of a platform login; found is false if the
// platform has no account of the role.
func (s *Store) Account(ctx context.Context, platform, role string) (Account, bool, error) {
	row, err := s.reader().GetAccount(ctx, sqlcgen.GetAccountParams{Platform: platform, Role: role})
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, false, nil
	}
	if err != nil {
		return Account{}, false, fmt.Errorf("account %s %s: %w", platform, role, translate(err))
	}
	return accountFrom(row), true, nil
}

// Accounts returns the metadata of all platform logins, by platform and role.
func (s *Store) Accounts(ctx context.Context) ([]Account, error) {
	rows, err := s.reader().ListAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", translate(err))
	}
	out := make([]Account, 0, len(rows))
	for _, r := range rows {
		out = append(out, accountFrom(r))
	}
	return out, nil
}

// DeleteAccount removes the metadata of a platform login; deleted is false
// if there was no such account.
func (s *Store) DeleteAccount(ctx context.Context, platform, role string) (bool, error) {
	var n int64
	err := s.Write(ctx, func(q *sqlcgen.Queries) error {
		var err error
		n, err = q.DeleteAccount(ctx, sqlcgen.DeleteAccountParams{Platform: platform, Role: role})
		return err
	})
	return n > 0, err
}

func accountFrom(r sqlcgen.Account) Account {
	return Account{
		Platform:  r.Platform,
		Role:      r.Role,
		Login:     r.Login,
		UserID:    r.UserID,
		Scopes:    r.Scopes,
		ClientID:  r.ClientID,
		Flow:      r.Flow,
		UpdatedAt: time.UnixMilli(r.UpdatedAt).UTC(),
	}
}
