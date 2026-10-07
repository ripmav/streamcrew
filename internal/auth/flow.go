// SPDX-License-Identifier: Apache-2.0

// Package auth keeps the platform accounts of a profile (ADR-0014): it
// starts and follows logins, stores the tokens encrypted in the vault,
// refreshes them before they expire, and reports which accounts need a new
// login.
package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

var (
	// ErrCodeExpired is the device code expired before the user
	// completed the login; the login must be started again.
	ErrCodeExpired = errors.New("the device code expired")
	// ErrAccessDenied is the user denied the login at the prompt.
	ErrAccessDenied = errors.New("the user denied the login")
)

// Flow is the OAuth flow of a platform: start a login, wait for the user
// to finish it, refresh and revoke tokens, and look up the account behind
// a token. Each platform has one implementation (twitchFlow for Twitch,
// ADR-0014); tests provide fakes.
type Flow interface {
	// Start begins a login and returns the device authorization response
	// with the code the user must enter (PromptFrom).
	Start(ctx context.Context) (*oauth2.DeviceAuthResponse, error)
	// Wait blocks until the user completes the login, the code expires,
	// or ctx ends (context.Canceled).
	Wait(ctx context.Context, da *oauth2.DeviceAuthResponse) (*oauth2.Token, error)
	// Refresh exchanges a refresh token for a new token; the granted
	// scopes stay in the token's extra fields ("scope").
	Refresh(ctx context.Context, refreshToken string) (*oauth2.Token, error)
	// Revoke revokes a token; a token that is already revoked is not an
	// error.
	Revoke(ctx context.Context, token string) error
	// User looks up the account behind a token: its platform ID and login
	// name.
	User(ctx context.Context, token string) (id, login string, err error)
}

// Prompt is what the user must do to complete a login: enter the code at
// the URL. Frontends render it as they like (text, browser, link).
type Prompt struct {
	// URL is where the user enters the code.
	URL string
	// Code is the code the user enters at the URL.
	Code string
	// Expiry is when the code expires.
	Expiry time.Time
}

// PromptFrom derives the prompt of a device authorization response.
func PromptFrom(da *oauth2.DeviceAuthResponse) Prompt {
	return Prompt{URL: da.VerificationURI, Code: da.UserCode, Expiry: da.Expiry}
}

// Missing reports the scopes from want that are not among the granted
// scopes have (space separated, from the token response). The order of
// want is kept; the casing and the whitespace of have do not matter.
func Missing(have string, want []string) []string {
	granted := make(map[string]bool)
	for s := range strings.FieldsSeq(have) {
		granted[strings.ToLower(s)] = true
	}
	var out []string
	for _, s := range want {
		if !granted[strings.ToLower(s)] {
			out = append(out, s)
		}
	}
	return out
}
