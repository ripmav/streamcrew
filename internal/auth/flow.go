// SPDX-License-Identifier: MIT

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
	// ErrLoginWindow is the login window of the authorization code flow
	// closed before the user completed the login; the login must be
	// started again (ADR-0023).
	ErrLoginWindow = errors.New("the login window expired")
	// ErrAccessDenied is the user denied the login at the prompt.
	ErrAccessDenied = errors.New("the user denied the login")
	// ErrTokenExpired is the refresh token is no longer valid: the login
	// must be started again (ADR-0014).
	ErrTokenExpired = errors.New("the token expired")
)

const (
	// FlowAuthorizationCode is the flow column of an account that was
	// logged in by the authorization code flow (ADR-0023).
	FlowAuthorizationCode = "authorization_code"
	// FlowDeviceCode is the flow column of an account that was logged in
	// by the device code flow (ADR-0023).
	FlowDeviceCode = "device_code"
)

// Flow is the OAuth flow of a platform: start a login, wait for the user
// to finish it, refresh and revoke tokens, and look up the account behind
// a token. Each platform has one implementation (twitchFlow and
// twitchCodeFlow for Twitch, ADR-0023); tests provide fakes.
type Flow interface {
	// Start begins a login and returns the handle to follow it with Wait
	// and the prompt of what the user must do.
	Start(ctx context.Context) (*Login, *Prompt, error)
	// Wait blocks until the user completes the login, the login expires,
	// or ctx ends (context.Canceled).
	Wait(ctx context.Context, l *Login) (*oauth2.Token, error)
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

// Login is a running login of the flow: the in-memory handle Wait follows
// up on. The device code flow holds the device authorization response, the
// authorization code flow the loopback callback that receives the browser
// redirect (ADR-0023). It is never persisted.
type Login struct {
	// device is the device authorization response of the device code
	// flow.
	device *oauth2.DeviceAuthResponse
	// callback is the loopback callback of the authorization code flow.
	callback *codeCallback
}

// Credentials are the app credentials of a login (ADR-0023): for the
// authorization code flow the client ID and secret of the user's own
// confidential app (BYO), for the device code flow the client ID of a
// public app.
type Credentials struct {
	// ID is the client ID of the app; empty selects the project app for
	// a device code login.
	ID string
	// Secret is the client secret of a confidential app; it is empty for
	// a public client (device code flow).
	Secret string
	// DeviceFlow selects the device code flow for a public client
	// instead of the authorization code flow.
	DeviceFlow bool
}

// Prompt is what the user must do to complete a login: open the URL and,
// for the device code flow, enter the code. Frontends render it as they
// like (text, browser, link).
type Prompt struct {
	// URL is where the user completes the login.
	URL string
	// Code is the code the user enters at the URL; it is empty for the
	// authorization code flow, where the user only authorizes (ADR-0023).
	Code string
	// Expiry is when the login expires and must be started again.
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
