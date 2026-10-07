// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

const (
	// ClientID is the client ID of the project's Twitch app "StreamCrew"
	// (ADR-0014): a public client without secret. Client IDs are public
	// and may be compiled in; a user-provided client ID overrides it (BYO).
	ClientID = "5yjhnihgh11abxo4f2cck1lfwx9ovg"

	// deviceURL is where a device code is requested (ADR-0014).
	deviceURL = "https://id.twitch.tv/oauth2/device"
	// tokenURL is where device codes and refresh tokens are exchanged
	// (ADR-0014).
	//nolint:gosec // G101: an endpoint URL, not a credential
	tokenURL = "https://id.twitch.tv/oauth2/token"
	// revokeURL is where a token is revoked (ADR-0014).
	revokeURL = "https://id.twitch.tv/oauth2/revoke"
	// usersURL is the Helix endpoint that looks up the account behind a
	// token (ADR-0014).
	usersURL = "https://api.twitch.tv/helix/users"

	// timeout is the deadline of one request of the flow.
	timeout = 30 * time.Second
)

// Scopes are the scopes the app requests on a Twitch login (ADR-0014,
// table). Twitch asks apps to request only the scopes they use; the list
// grows with new features, and a missing scope marks the login "required"
// until the user logs in again.
//
//nolint:gochecknoglobals // the required scope list of ADR-0014, never mutated
var Scopes = []string{
	"user:read:chat",
	"user:write:chat",
	"moderator:read:chat_settings",
	"moderator:manage:chat_settings",
	"moderator:read:chat_messages",
	"moderator:manage:chat_messages",
	"moderator:read:banned_users",
	"moderator:manage:banned_users",
	"moderator:read:moderators",
	"channel:manage:moderators",
	"moderator:read:warnings",
	"moderator:manage:warnings",
	"moderator:read:chatters",
	"moderator:read:shoutouts",
	"moderator:manage:shoutouts",
	"moderator:read:shield_mode",
	"moderator:manage:shield_mode",
	"moderator:read:unban_requests",
	"moderator:manage:unban_requests",
	"moderator:read:suspicious_users",
	"moderator:read:vips",
	"channel:read:vips",
	"channel:manage:vips",
	"moderator:read:followers",
	"user:manage:whispers",
	"channel:read:subscriptions",
	"channel:read:charity",
	"channel:read:goals",
	"channel:read:hype_train",
	"channel:read:ads",
	"channel:edit:commercial",
	"channel:read:polls",
	"channel:manage:polls",
	"channel:read:predictions",
	"channel:manage:predictions",
	"channel:read:redemptions",
	"channel:manage:redemptions",
	"channel:manage:broadcast",
	"channel:manage:raids",
	"clips:edit",
	"bits:read",
}

// NewTwitch returns the Twitch flow (ADR-0014) with the project's client
// ID or a user-provided one (BYO), and the given HTTP client; in server
// mode it must honor the outbound allow list (Code-ADR-0019). A nil
// client gets the default timeout.
func NewTwitch(clientID string, client *http.Client) Flow {
	if clientID == "" {
		clientID = ClientID
	}
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	return &twitchFlow{
		clientID:  clientID,
		client:    client,
		deviceURL: deviceURL,
		tokenURL:  tokenURL,
		revokeURL: revokeURL,
		usersURL:  usersURL,
	}
}

// twitchFlow is the Twitch flow: device code flow (RFC 8628) with
// golang.org/x/oauth2 for a public client without secret (ADR-0014).
type twitchFlow struct {
	clientID  string
	client    *http.Client
	deviceURL string
	tokenURL  string
	revokeURL string
	usersURL  string
}

func (f *twitchFlow) config() *oauth2.Config {
	return &oauth2.Config{
		ClientID: f.clientID,
		Scopes:   Scopes,
		Endpoint: oauth2.Endpoint{
			DeviceAuthURL: f.deviceURL,
			TokenURL:      f.tokenURL,
			// A public client sends the client ID in the body, not in a
			// Basic auth header (ADR-0014).
			AuthStyle: oauth2.AuthStyleInParams,
		},
	}
}

// clientContext carries f.client for the oauth2 calls; without it they
// would use the default HTTP client and miss the outbound allow list in
// server mode (Code-ADR-0019).
func (f *twitchFlow) clientContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, f.client)
}

// Start begins a login and returns the device authorization response with
// the code the user must enter.
func (f *twitchFlow) Start(ctx context.Context) (*oauth2.DeviceAuthResponse, error) {
	da, err := f.config().DeviceAuth(f.clientContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("start login: %w", err)
	}
	return da, nil
}

// Wait blocks until the user completes the login, the code expires, or
// ctx ends.
func (f *twitchFlow) Wait(ctx context.Context, da *oauth2.DeviceAuthResponse) (*oauth2.Token, error) {
	tok, err := f.config().DeviceAccessToken(f.clientContext(ctx), da)
	if err != nil {
		var re *oauth2.RetrieveError
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, ErrCodeExpired
		case errors.As(err, &re):
			switch re.ErrorCode {
			case "access_denied":
				return nil, ErrAccessDenied
			case "expired_token":
				return nil, ErrCodeExpired
			}
			return nil, fmt.Errorf("wait for login: %w", err)
		default:
			return nil, fmt.Errorf("wait for login: %w", err)
		}
	}
	setExpiry(tok)
	return tok, nil
}

// Refresh exchanges a refresh token for a new token. A public client
// sends only the client ID; there is no secret (ADR-0014).
func (f *twitchFlow) Refresh(ctx context.Context, refreshToken string) (*oauth2.Token, error) {
	v := url.Values{
		"client_id":     {f.clientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}
	body, err := f.post(ctx, f.tokenURL, v)
	if err != nil {
		return nil, fmt.Errorf("refresh token: %w", err)
	}
	tok, err := tokenFromBody(body, refreshToken)
	if err != nil {
		return nil, fmt.Errorf("refresh token: %w", err)
	}
	return tok, nil
}

// Revoke revokes a token; Twitch answers 400 for a token that is already
// revoked, which counts as success (ADR-0014).
func (f *twitchFlow) Revoke(ctx context.Context, token string) error {
	v := url.Values{
		"client_id": {f.clientID},
		"token":     {token},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.revokeURL, strings.NewReader(v.Encode()))
	if err != nil {
		return fmt.Errorf("revoke token: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r, err := f.client.Do(req)
	if err != nil {
		return fmt.Errorf("revoke token: %w", err)
	}
	defer r.Body.Close()
	if _, err := io.Copy(io.Discard, io.LimitReader(r.Body, 1<<20)); err != nil {
		return fmt.Errorf("revoke token: %w", err)
	}
	switch {
	case r.StatusCode >= 200 && r.StatusCode < 300:
		return nil
	case r.StatusCode == http.StatusBadRequest:
		return nil
	default:
		return fmt.Errorf("revoke token: %s", r.Status)
	}
}

// User looks up the account behind a token with the Helix user endpoint:
// without arguments it returns the account of the token itself.
func (f *twitchFlow) User(ctx context.Context, token string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.usersURL, nil)
	if err != nil {
		return "", "", fmt.Errorf("look up account: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	// Helix asks for the client ID in every request (ADR-0014).
	req.Header.Set("Client-Id", f.clientID)
	body, err := f.do(req)
	if err != nil {
		return "", "", fmt.Errorf("look up account: %w", err)
	}
	var resp struct {
		Data []struct {
			ID    string `json:"id"`
			Login string `json:"login"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", "", fmt.Errorf("look up account: %w", err)
	}
	if len(resp.Data) == 0 {
		return "", "", errors.New("the user response has no account")
	}
	return resp.Data[0].ID, resp.Data[0].Login, nil
}

// post sends v to url and returns the body of a 2xx answer.
func (f *twitchFlow) post(ctx context.Context, target string, v url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(v.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return f.do(req)
}

// do sends req and returns the body of a 2xx answer.
func (f *twitchFlow) do(req *http.Request) ([]byte, error) {
	r, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if r.StatusCode < 200 || r.StatusCode > 299 {
		return nil, fmt.Errorf("%s: %s", r.Status, snippet(body))
	}
	return body, nil
}

// setExpiry sets the token's expiry from expires_in when the response did
// not already do so; a token without expiry is never refreshed.
func setExpiry(tok *oauth2.Token) {
	if tok.Expiry.IsZero() && tok.ExpiresIn > 0 {
		tok.Expiry = time.Now().UTC().Add(time.Duration(tok.ExpiresIn) * time.Second)
	}
}

// tokenFromBody parses a Twitch token response (device code or refresh):
// the access token, the refresh token, the expiry and the extra fields
// (which include the granted scopes, "scope"). If the response has no
// refresh token, previous is kept.
func tokenFromBody(body []byte, previous string) (*oauth2.Token, error) {
	var tj struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tj); err != nil {
		return nil, err
	}
	if tj.AccessToken == "" {
		return nil, errors.New("the token response has no access_token")
	}
	tok := &oauth2.Token{
		AccessToken: tj.AccessToken,
		// A public client keeps the refresh token if the response omits
		// it, like golang.org/x/oauth2 does.
		RefreshToken: tj.RefreshToken,
	}
	if tok.RefreshToken == "" {
		tok.RefreshToken = previous
	}
	if tj.ExpiresIn > 0 {
		tok.Expiry = time.Now().UTC().Add(time.Duration(tj.ExpiresIn) * time.Second)
	}
	extra := map[string]any{}
	if err := json.Unmarshal(body, &extra); err == nil {
		tok = tok.WithExtra(extra)
	}
	return tok, nil
}

// snippet shortens a response body for error messages.
func snippet(body []byte) string {
	s := strings.TrimSpace(string(body))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}
