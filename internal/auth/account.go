// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"strings"
	"time"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/store"
)

// Account is the state of a platform login (ADR-0014, ADR-0023): which
// account belongs to which role of the platform, with which scopes, with
// which app and which flow it was made. The token of the login is stored
// encrypted in the vault under the name of authName, next to the account
// row.
type Account struct {
	// Platform is the platform the login belongs to.
	Platform platform.Name
	// Role is the account of the platform: streamer or bot.
	Role connector.Account
	// Login is the display name of the account, e.g. the channel name.
	Login string
	// UserID is the ID of the user on the platform.
	UserID string
	// Scopes are the scopes granted on the login.
	Scopes []string
	// ClientID is the client ID of the app the login was made with; a
	// refresh must use the same client ID.
	ClientID string
	// Flow is how the login was made (ADR-0023); a refresh and a revoke
	// must use the same flow and client.
	Flow string
	// UpdatedAt is the time the account was last changed.
	UpdatedAt time.Time
}

// FromStore maps the store row to the domain form.
func FromStore(a store.Account) Account {
	return Account{
		Platform:  platform.Name(a.Platform),
		Role:      connector.Account(a.Role),
		Login:     a.Login,
		UserID:    a.UserID,
		Scopes:    splitScopes(a.Scopes),
		ClientID:  a.ClientID,
		Flow:      a.Flow,
		UpdatedAt: a.UpdatedAt,
	}
}

// ToStore maps the domain form to the store row.
func (a Account) ToStore() store.Account {
	return store.Account{
		Platform:  string(a.Platform),
		Role:      string(a.Role),
		Login:     a.Login,
		UserID:    a.UserID,
		Scopes:    joinScopes(a.Scopes),
		ClientID:  a.ClientID,
		Flow:      a.Flow,
		UpdatedAt: a.UpdatedAt,
	}
}

// Token is what the vault keeps for a login (ADR-0014): the token pair and
// the expiry of the access token. The vault stores it as JSON; the time is
// UTC.
type Token struct {
	// AccessToken authorizes the requests to the platform.
	AccessToken string `json:"access_token"`
	// RefreshToken is exchanged for a new access token.
	RefreshToken string `json:"refresh_token"`
	// ExpiresAt is when the access token expires; zero if the platform
	// did not say, which the service takes as "it never expires".
	ExpiresAt time.Time `json:"expires_at"`
}

// expired reports whether the access token is at or past its expiry.
func (t Token) expired(now time.Time) bool {
	return !t.ExpiresAt.IsZero() && !t.ExpiresAt.After(now)
}

// authName is the vault entry of a login (ADR-0014).
func authName(p platform.Name, r connector.Account) string {
	return "auth/" + string(p) + "/" + string(r)
}

// ClientRecord is what the vault keeps for the app of a platform
// (ADR-0023): the client ID and secret of the confidential app the last
// login used. The vault stores it as JSON, so the secret never leaves the
// vault in the clear.
type ClientRecord struct {
	// ID is the client ID of the app.
	ID string `json:"id"`
	// Secret is the client secret of the app.
	Secret string `json:"secret"`
}

// clientName is the vault entry of the app credentials of a platform
// (ADR-0023); one entry per platform, shared by the streamer and the bot
// account.
func clientName(p platform.Name) string {
	return "auth/" + string(p) + "/client"
}

func splitScopes(s string) []string {
	out := strings.Fields(s)
	if out == nil {
		return []string{}
	}
	return out
}

func joinScopes(s []string) string {
	return strings.Join(s, " ")
}
