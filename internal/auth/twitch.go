// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/ripmav/streamcrew/internal/httpclient"
)

const (
	// ClientID is the client ID of the project's Twitch app "StreamCrew"
	// (ADR-0014): a public client without secret. Client IDs are public
	// and may be compiled in; it serves the device code flow fallback
	// (ADR-0023).
	ClientID = "5yjhnihgh11abxo4f2cck1lfwx9ovg"

	// deviceURL is where a device code is requested (ADR-0014).
	deviceURL = "https://id.twitch.tv/oauth2/device"
	// authorizeURL is the authorization endpoint of the authorization
	// code flow (ADR-0023).
	authorizeURL = "https://id.twitch.tv/oauth2/authorize"
	// tokenURL is where device codes, authorization codes and refresh
	// tokens are exchanged (ADR-0014, ADR-0023).
	//nolint:gosec // G101: an endpoint URL, not a credential
	tokenURL = "https://id.twitch.tv/oauth2/token"
	// revokeURL is where a token is revoked (ADR-0014).
	revokeURL = "https://id.twitch.tv/oauth2/revoke"
	// usersURL is the Helix endpoint that looks up the account behind a
	// token (ADR-0014).
	usersURL = "https://api.twitch.tv/helix/users"

	// RedirectURL is the loopback redirect of the authorization code
	// flow (ADR-0023). The user must register exactly this URL in the
	// "OAuth Redirect URLs" of their app; the host is localhost, not the
	// loopback IP, because the Twitch developer console rejects IP
	// addresses (which must be HTTPS), like in the examples of the Twitch
	// docs. The listener answers on both loopback addresses, because a
	// browser may resolve localhost to either.
	RedirectURL = "http://localhost:8741"
	// redirectPort is the port of RedirectURL.
	redirectPort = 8741
	// codeWindow is how long the login window of the authorization code
	// flow stays open (ADR-0023).
	codeWindow = 10 * time.Minute

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

// NewTwitch returns the Twitch flow (ADR-0014, ADR-0023) for the given
// credentials: the authorization code flow for a confidential client (the
// user's own app, BYO), or the device code flow for a public client
// (DeviceFlow). The token calls (exchange, device code polling, refresh,
// revoke) run through the httpclient of the twitch.auth API, and the
// account lookup through the one of the twitch.helix API, which the
// composition root builds with the breakers of the APIs (Code-ADR-0002,
// Code-ADR-0007, Code-ADR-0014). A nil client gets the default timeout;
// in server mode it must honor the outbound allow list (Code-ADR-0019).
func NewTwitch(c Credentials, client *http.Client, twitchAuth, twitchHelix *httpclient.Client) (Flow, error) {
	if !c.DeviceFlow && (c.ID == "" || c.Secret == "") {
		return nil, errors.New("the authorization code flow needs a client ID and a client secret")
	}
	if c.ID == "" {
		c.ID = ClientID
	}
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	if twitchAuth == nil || twitchHelix == nil {
		return nil, errors.New("the twitch flow needs the httpclients of the twitch.auth and the twitch.helix APIs")
	}
	base := twitchBase{
		clientID:  c.ID,
		client:    client,
		auth:      twitchAuth,
		helix:     twitchHelix,
		tokenURL:  tokenURL,
		revokeURL: revokeURL,
		usersURL:  usersURL,
	}
	if c.DeviceFlow {
		return &twitchFlow{twitchBase: base, deviceURL: deviceURL}, nil
	}
	return &twitchCodeFlow{
		twitchBase: base,
		secret:     c.Secret,
		authorize:  authorizeURL,
		redirect:   RedirectURL,
		listenPort: redirectPort,
		window:     codeWindow,
	}, nil
}

// twitchBase is what the device code flow and the authorization code flow
// share: the HTTP plumbing, the account lookup and the token revocation
// (ADR-0023). The token calls run through the httpclient with the breaker
// twitch.auth, and the account lookup through the one with the breaker
// twitch.helix (Code-ADR-0014, Code-ADR-0007); client is only for the
// device code request, which golang.org/x/oauth2 performs itself.
type twitchBase struct {
	clientID  string
	client    *http.Client
	auth      *httpclient.Client
	helix     *httpclient.Client
	tokenURL  string
	revokeURL string
	usersURL  string
}

// post sends v to target through the auth client (breaker twitch.auth)
// and returns the body of the 2xx answer (Code-ADR-0014). A non-2xx
// answer is a *httpclient.StatusError, a transport failure a standard Go
// error, and an open breaker a breaker.ErrUnavailable.
func (b *twitchBase) post(ctx context.Context, target string, v url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(v.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := b.auth.Do(ctx, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

// revoke revokes a token; Twitch answers 400 for a token that is already
// revoked, which counts as success (ADR-0014). A confidential client
// sends its secret (ADR-0023). The call is idempotent, so the httpclient
// may repeat it (Code-ADR-0014).
func (b *twitchBase) revoke(ctx context.Context, token, secret string) error {
	v := url.Values{
		"client_id": {b.clientID},
		"token":     {token},
	}
	if secret != "" {
		v.Set("client_secret", secret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.revokeURL, strings.NewReader(v.Encode()))
	if err != nil {
		return fmt.Errorf("revoke token: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := b.auth.Do(ctx, req)
	if err != nil {
		if se, ok := errors.AsType[*httpclient.StatusError](err); ok && se.StatusCode == http.StatusBadRequest {
			return nil
		}
		return fmt.Errorf("revoke token: %w", err)
	}
	if err := resp.Body.Close(); err != nil {
		return fmt.Errorf("revoke token: %w", err)
	}
	return nil
}

// User looks up the account behind a token with the Helix user endpoint
// (breaker twitch.helix): without arguments it returns the account of the
// token itself.
func (b *twitchBase) User(ctx context.Context, token string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.usersURL, nil)
	if err != nil {
		return "", "", fmt.Errorf("look up account: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	// Helix asks for the client ID in every request (ADR-0014).
	req.Header.Set("Client-Id", b.clientID)
	var resp struct {
		Data []struct {
			ID    string `json:"id"`
			Login string `json:"login"`
		} `json:"data"`
	}
	if err := b.helix.DoJSON(ctx, req, &resp); err != nil {
		return "", "", fmt.Errorf("look up account: %w", err)
	}
	if len(resp.Data) == 0 {
		return "", "", errors.New("the user response has no account")
	}
	return resp.Data[0].ID, resp.Data[0].Login, nil
}

// twitchFlow is the Twitch flow of the public client: device code flow
// (RFC 8628) with golang.org/x/oauth2, without secret (ADR-0014); the
// fallback of ADR-0023.
type twitchFlow struct {
	twitchBase
	deviceURL string
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

// clientContext carries f.client for the device code request, which
// golang.org/x/oauth2 performs itself (not a token call, so it does not
// run through the httpclient); without it the request would use the
// default HTTP client and miss the outbound allow list in server mode
// (Code-ADR-0019).
func (f *twitchFlow) clientContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, f.client)
}

// Start begins a login and returns the handle with the device
// authorization response and the prompt with the code the user must
// enter.
func (f *twitchFlow) Start(ctx context.Context) (*Login, *Prompt, error) {
	da, err := f.config().DeviceAuth(f.clientContext(ctx))
	if err != nil {
		return nil, nil, fmt.Errorf("start login: %w", err)
	}
	p := PromptFrom(da)
	return &Login{device: da}, &p, nil
}

// Wait blocks until the user completes the login, the code expires, or
// ctx ends. It polls the token endpoint at the interval of the device
// authorization response (RFC 8628 §3.2); "authorization_pending" and
// "slow_down" (RFC 8628 §3.5) keep the polling going. The polling is
// done here instead of by golang.org/x/oauth2's DeviceAccessToken,
// because the real endpoint answers these states with the field
// "message" instead of the RFC field "error" (verified against Twitch
// 2026-10-08), which the library does not recognize and aborts on.
func (f *twitchFlow) Wait(ctx context.Context, l *Login) (*oauth2.Token, error) {
	interval := time.Duration(l.device.Interval) * time.Second
	if interval == 0 {
		// "If no value is provided, clients MUST use 5 as the default."
		interval = 5 * time.Second
	}
	if !l.device.Expiry.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, l.device.Expiry)
		defer cancel()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, ErrCodeExpired
			}
			return nil, fmt.Errorf("wait for login: %w", ctx.Err())
		case <-ticker.C:
		}
		body, err := f.post(ctx, f.tokenURL, url.Values{
			"client_id":   {f.clientID},
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
			"device_code": {l.device.DeviceCode},
			"scope":       {strings.Join(Scopes, " ")},
		})
		if err == nil {
			tok, err := tokenFromBody(body, "")
			if err != nil {
				return nil, fmt.Errorf("wait for login: %w", err)
			}
			setExpiry(tok)
			return normalizeScope(tok), nil
		}
		se, ok := errors.AsType[*httpclient.StatusError](err)
		if !ok {
			// A transport failure, or the breaker twitch.auth is open
			// (breaker.ErrUnavailable; the service maps it for the
			// frontends).
			return nil, fmt.Errorf("wait for login: %w", err)
		}
		switch code := devicePollCode([]byte(se.Snippet)); code {
		case "authorization_pending":
			// The user has not authorized yet; keep polling.
		case "slow_down":
			// "the interval MUST be increased by 5 seconds for this
			// and all subsequent requests"
			interval += 5 * time.Second
			ticker.Reset(interval)
		case "expired_token":
			return nil, ErrCodeExpired
		case "access_denied":
			return nil, ErrAccessDenied
		case "":
			return nil, fmt.Errorf("wait for login: %s: %s", http.StatusText(se.StatusCode), se.Snippet)
		default:
			return nil, fmt.Errorf("wait for login: %s: %s", code, se.Snippet)
		}
	}
}

// devicePollCode extracts the polling state of a 4xx answer of the
// device token endpoint: the RFC 8628 field "error" or the field
// "message" the real endpoint uses; an unparseable answer is the empty
// string.
func devicePollCode(body []byte) string {
	var e struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		return ""
	}
	if e.Error != "" {
		return e.Error
	}
	return e.Message
}

// Refresh exchanges a refresh token for a new token. A public client
// sends only the client ID; there is no secret (ADR-0014). An answer that
// says the grant is gone, e.g. the refresh token is invalid, is
// ErrTokenExpired: the login must be started again.
func (f *twitchFlow) Refresh(ctx context.Context, refreshToken string) (*oauth2.Token, error) {
	v := url.Values{
		"client_id":     {f.clientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}
	body, err := f.post(ctx, f.tokenURL, v)
	if err != nil {
		return nil, tokenCallError("refresh token", err)
	}
	tok, err := tokenFromBody(body, refreshToken)
	if err != nil {
		return nil, fmt.Errorf("refresh token: %w", err)
	}
	return tok, nil
}

// Revoke revokes a token; a token that is already revoked is not an
// error.
func (f *twitchFlow) Revoke(ctx context.Context, token string) error {
	return f.revoke(ctx, token, "")
}

// twitchCodeFlow is the Twitch flow of a confidential client:
// authorization code flow (RFC 6749 §4.1) with a loopback redirect
// (ADR-0023). The user authorizes on the Twitch page, the browser
// redirects the code to the local listener, and the flow exchanges it
// with the client secret. Twitch does not support PKCE; the secret and
// the loopback binding carry the security of the code exchange.
type twitchCodeFlow struct {
	twitchBase
	secret     string
	authorize  string
	redirect   string
	listenPort int
	window     time.Duration
}

// Start opens the loopback listeners, builds the authorize URL with a
// fresh state, and returns the handle and the prompt of the login window.
func (f *twitchCodeFlow) Start(ctx context.Context) (*Login, *Prompt, error) {
	state, err := randomState()
	if err != nil {
		return nil, nil, fmt.Errorf("start login: %w", err)
	}
	lns, err := loopbackListeners(ctx, f.listenPort)
	if err != nil {
		return nil, nil, fmt.Errorf("start the loopback listener at %s: %w", f.redirect, err)
	}
	cb := &codeCallback{
		state:  state,
		result: make(chan codeResult, 1),
		lns:    lns,
	}
	cb.server = &http.Server{
		Handler:           http.HandlerFunc(cb.handle),
		ReadHeaderTimeout: 5 * time.Second,
	}
	for _, ln := range lns {
		go func(ln net.Listener) {
			// ErrServerClosed is the expected exit when the login completes.
			_ = cb.server.Serve(ln)
		}(ln)
	}
	p := Prompt{URL: f.authorizeURL(state), Expiry: time.Now().UTC().Add(f.window)}
	return &Login{callback: cb}, &p, nil
}

// Wait blocks until the browser delivers the code, the login window
// closes, or ctx ends. It closes the loopback listener on every path.
func (f *twitchCodeFlow) Wait(ctx context.Context, l *Login) (*oauth2.Token, error) {
	cb := l.callback
	defer cb.close(ctx)
	select {
	case res := <-cb.result:
		if res.err != nil {
			return nil, res.err
		}
		tok, err := f.exchange(ctx, res.code)
		if err != nil {
			return nil, err
		}
		setExpiry(tok)
		return tok, nil
	case <-time.After(f.window):
		return nil, ErrLoginWindow
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Refresh exchanges a refresh token for a new token. A confidential
// client sends its secret (ADR-0023). An answer that says the grant is
// gone, e.g. the refresh token is invalid, is ErrTokenExpired: the login
// must be started again.
func (f *twitchCodeFlow) Refresh(ctx context.Context, refreshToken string) (*oauth2.Token, error) {
	v := url.Values{
		"client_id":     {f.clientID},
		"client_secret": {f.secret},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}
	body, err := f.post(ctx, f.tokenURL, v)
	if err != nil {
		return nil, tokenCallError("refresh token", err)
	}
	tok, err := tokenFromBody(body, refreshToken)
	if err != nil {
		return nil, fmt.Errorf("refresh token: %w", err)
	}
	return tok, nil
}

// Revoke revokes a token with the client secret; a token that is already
// revoked is not an error.
func (f *twitchCodeFlow) Revoke(ctx context.Context, token string) error {
	return f.revoke(ctx, token, f.secret)
}

// authorizeURL builds the URL the user opens to log in and authorize
// (ADR-0023).
func (f *twitchCodeFlow) authorizeURL(state string) string {
	q := url.Values{
		"response_type": {"code"},
		"client_id":     {f.clientID},
		"redirect_uri":  {f.redirect},
		"scope":         {strings.Join(Scopes, " ")},
		"state":         {state},
	}
	return f.authorize + "?" + q.Encode()
}

// exchange trades the authorization code for the tokens of the login
// (ADR-0023). The code is single-use, so the call runs without retry
// (Code-ADR-0014): a repeat would fail anyway.
func (f *twitchCodeFlow) exchange(ctx context.Context, code string) (*oauth2.Token, error) {
	v := url.Values{
		"client_id":     {f.clientID},
		"client_secret": {f.secret},
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {f.redirect},
	}
	body, err := f.post(httpclient.WithoutRetry(ctx), f.tokenURL, v)
	if err != nil {
		return nil, tokenCallError("exchange the code", err)
	}
	tok, err := tokenFromBody(body, "")
	if err != nil {
		return nil, fmt.Errorf("exchange the code: %w", err)
	}
	return tok, nil
}

// codeCallback is the loopback callback of a running authorization code
// login (ADR-0023): it serves the browser redirect on the loopback
// addresses (127.0.0.1 and, when there, ::1), checks the state, and hands
// the code (or the error) to Wait.
type codeCallback struct {
	state  string
	result chan codeResult
	server *http.Server
	lns    []net.Listener

	finishOnce sync.Once
	closeOnce  sync.Once
}

// codeResult is what the callback received: the code or the error.
type codeResult struct {
	code string
	err  error
}

const codeDonePage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>streamcrew</title>
</head>
<body>
<h1>Login complete</h1>
<p>You can close this window and go back to the terminal.</p>
</body>
</html>
`

// handle serves one redirect; every answer ends the login.
func (cb *codeCallback) handle(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if err := q.Get("error"); err != "" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintln(w, "The login could not be completed. You can close this window.")
		cb.finish(codeResult{err: ErrAccessDenied})
		return
	}
	if q.Get("state") != cb.state {
		http.Error(w, "unexpected state", http.StatusBadRequest)
		cb.finish(codeResult{err: errors.New("the redirect state does not match")})
		return
	}
	if code := q.Get("code"); code != "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, codeDonePage)
		cb.finish(codeResult{code: code})
		return
	}
	http.Error(w, "missing code", http.StatusBadRequest)
	cb.finish(codeResult{err: errors.New("the redirect has no code")})
}

// finish records the result once; Wait closes the listener.
func (cb *codeCallback) finish(res codeResult) {
	cb.finishOnce.Do(func() {
		cb.result <- res
	})
}

// close stops the loopback server gracefully, so the browser still
// receives the answer; it is safe to call it more than once.
func (cb *codeCallback) close(ctx context.Context) {
	cb.closeOnce.Do(func() {
		if cb.server == nil {
			return
		}
		sctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		_ = cb.server.Shutdown(sctx)
	})
}

// loopbackListeners listens on the loopback addresses of the redirect
// (ADR-0023): the registered URL is the localhost form, and a browser may
// resolve localhost to 127.0.0.1 or to ::1, so the redirect has to be
// answered on both. A missing IPv6 loopback is not an error, the browser
// then reaches the IPv4 listener. A zero port picks a free port (tests).
func loopbackListeners(ctx context.Context, port int) ([]net.Listener, error) {
	var lc net.ListenConfig
	ln4, err := lc.Listen(ctx, "tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, err
	}
	lns := []net.Listener{ln4}
	if ln6, err := lc.Listen(ctx, "tcp6", fmt.Sprintf("[::1]:%d", port)); err == nil {
		lns = append(lns, ln6)
	}
	return lns, nil
}

// randomState draws a random CSRF state for the authorize URL (ADR-0023).
func randomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// setExpiry sets the token's expiry from expires_in when the response did
// not already do so; a token without expiry is never refreshed.
func setExpiry(tok *oauth2.Token) {
	if tok.Expiry.IsZero() && tok.ExpiresIn > 0 {
		tok.Expiry = time.Now().UTC().Add(time.Duration(tok.ExpiresIn) * time.Second)
	}
}

// tokenCallError maps the error of a token endpoint call (code exchange,
// refresh) to the 4.1 messages: a 401 or 403 means the grant is gone
// (ErrTokenExpired, the login must be started again), the other answers
// keep the error code in the message (tokenError). A transport failure or
// an open breaker is wrapped unchanged; the service maps
// breaker.ErrUnavailable for the frontends.
func tokenCallError(what string, err error) error {
	se, ok := errors.AsType[*httpclient.StatusError](err)
	if !ok {
		return fmt.Errorf("%s: %w", what, err)
	}
	if se.StatusCode == http.StatusUnauthorized || se.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%s: %w: %s", what, ErrTokenExpired, se.Error())
	}
	return fmt.Errorf("%s: %w", what, tokenError([]byte(se.Snippet)))
}

// tokenError maps a Twitch token error response: an answer that says the
// grant is gone is ErrTokenExpired, the other answers keep the error code
// in the message (ADR-0014, ADR-0023).
func tokenError(body []byte) error {
	var e struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &e); err != nil || e.Error == "" {
		return errors.New(snippet(body))
	}
	if e.Error == "expired_token" || e.Error == "invalid_grant" {
		return fmt.Errorf("%w: %s: %s", ErrTokenExpired, e.Error, e.ErrorDescription)
	}
	if e.ErrorDescription == "" {
		return errors.New(e.Error)
	}
	return fmt.Errorf("%s: %s", e.Error, e.ErrorDescription)
}

// tokenFromBody parses a Twitch token response (device code,
// authorization code or refresh): the access token, the refresh token,
// the expiry and the extra fields (which include the granted scopes,
// "scope"). If the response has no refresh token, previous is kept.
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
	return normalizeScope(tok), nil
}

// normalizeScope rewrites the granted scopes of a token. The endpoint
// returns them as a JSON array in the token response (Twitch
// documentation); the rest of the code reads them as one
// space-separated string. A token whose scope is not an array, or has
// no scope at all, is returned unchanged.
func normalizeScope(tok *oauth2.Token) *oauth2.Token {
	arr, ok := tok.Extra("scope").([]any)
	if !ok {
		return tok
	}
	parts := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok {
			parts = append(parts, s)
		}
	}
	// The library keeps the raw response in the token's extra fields,
	// but exposes no setter for one field; rebuild the token with the
	// normalized scope.
	return (&oauth2.Token{
		AccessToken:  tok.AccessToken,
		TokenType:    tok.Type(),
		RefreshToken: tok.RefreshToken,
		Expiry:       tok.Expiry,
	}).WithExtra(map[string]any{"scope": strings.Join(parts, " ")})
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
