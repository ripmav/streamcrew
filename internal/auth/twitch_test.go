// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// tokenReply is the answer of one call of the token endpoint of the fake.
type tokenReply struct {
	status int
	body   string
}

// oauthFake is a fake of the Twitch OAuth and Helix endpoints. The token
// endpoint answers with the entries of tokenScript in order and repeats
// the last one.
type oauthFake struct {
	mu             sync.Mutex
	deviceBody     string
	deviceRequest  url.Values
	tokenScript    []tokenReply
	tokenRequests  []url.Values
	revokeRequests []url.Values
	revokeStatus   int
	userStatus     int
	userData       string
	userAuth       string
	userClientID   string

	srv *httptest.Server
}

const fakeDeviceBody = `{"device_code":"dc-1","user_code":"ABCD-EFGH","verification_uri":"https://login.fake/activate","interval":1,"expires_in":1800}`

const fakeSuccessToken = `{"access_token":"at-1","token_type":"bearer","refresh_token":"rt-1","expires_in":3600,"scope":"user:read:chat user:write:chat"}`

func newOAuthFake(t *testing.T) *oauthFake {
	t.Helper()
	f := &oauthFake{
		deviceBody:   fakeDeviceBody,
		revokeStatus: http.StatusOK,
		userStatus:   http.StatusOK,
		userData:     `{"data":[{"id":"1001","login":"ada"}]}`,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/device", f.handleDevice)
	mux.HandleFunc("/token", f.handleToken)
	mux.HandleFunc("/revoke", f.handleRevoke)
	mux.HandleFunc("/users", f.handleUsers)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *oauthFake) twitchFlow() *twitchFlow {
	return &twitchFlow{
		clientID:  "test-client",
		client:    f.srv.Client(),
		tokenURL:  f.srv.URL + "/token",
		revokeURL: f.srv.URL + "/revoke",
		usersURL:  f.srv.URL + "/users",
		deviceURL: f.srv.URL + "/device",
	}
}

func (f *oauthFake) twitchCodeFlow() *twitchCodeFlow {
	return &twitchCodeFlow{
		clientID:  "test-client",
		client:    f.srv.Client(),
		tokenURL:  f.srv.URL + "/token",
		revokeURL: f.srv.URL + "/revoke",
		usersURL:  f.srv.URL + "/users",
		secret:    "test-secret",
		authorize: "https://id.fake/oauth2/authorize",
		redirect:  RedirectURL,
		window:    codeWindow,
	}
}

// fakeDeviceAuth is the device authorization response of the fake.
func fakeDeviceAuth() *oauth2.DeviceAuthResponse {
	return &oauth2.DeviceAuthResponse{
		DeviceCode:      "dc-1",
		UserCode:        "ABCD-EFGH",
		VerificationURI: "https://login.fake/activate",
		Interval:        1,
		Expiry:          time.Now().UTC().Add(1800 * time.Second),
	}
}

func (f *oauthFake) deviceReq() url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.deviceRequest
}

func (f *oauthFake) tokenReqs() []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.tokenRequests)
}

func (f *oauthFake) revokeReqs() []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.revokeRequests)
}

func (f *oauthFake) userHeaders() (auth, clientID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.userAuth, f.userClientID
}

func (f *oauthFake) handleDevice(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	v, err := url.ParseQuery(string(body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deviceRequest = v
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, f.deviceBody)
}

func (f *oauthFake) handleToken(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	v, err := url.ParseQuery(string(body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.tokenRequests = append(f.tokenRequests, v)
	call := len(f.tokenRequests) - 1
	reply := f.tokenScript[len(f.tokenScript)-1]
	if call < len(f.tokenScript) {
		reply = f.tokenScript[call]
	}
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(reply.status)
	fmt.Fprint(w, reply.body)
}

func (f *oauthFake) handleRevoke(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	v, err := url.ParseQuery(string(body))
	if err == nil {
		f.mu.Lock()
		f.revokeRequests = append(f.revokeRequests, v)
		f.mu.Unlock()
	}
	w.WriteHeader(f.revokeStatus)
}

func (f *oauthFake) handleUsers(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	status, data := f.userStatus, f.userData
	f.userAuth = r.Header.Get("Authorization")
	f.userClientID = r.Header.Get("Client-Id")
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprint(w, data)
}

func TestNewTwitch(t *testing.T) {
	t.Parallel()

	t.Run("the device code flow gets the project client ID", func(t *testing.T) {
		t.Parallel()
		flow, err := NewTwitch(Credentials{DeviceFlow: true}, nil)
		require.NoError(t, err)
		f, ok := flow.(*twitchFlow)
		require.True(t, ok)
		assert.Equal(t, ClientID, f.clientID)
		assert.Equal(t, timeout, f.client.Timeout)
		assert.Equal(t, deviceURL, f.deviceURL)
		assert.Equal(t, tokenURL, f.tokenURL)
		assert.Equal(t, revokeURL, f.revokeURL)
		assert.Equal(t, usersURL, f.usersURL)
	})

	t.Run("a user device client ID overrides the project one", func(t *testing.T) {
		t.Parallel()
		flow, err := NewTwitch(Credentials{ID: "other", DeviceFlow: true}, nil)
		require.NoError(t, err)
		f, ok := flow.(*twitchFlow)
		require.True(t, ok)
		assert.Equal(t, "other", f.clientID)
	})

	t.Run("the authorization code flow needs credentials", func(t *testing.T) {
		t.Parallel()
		_, err := NewTwitch(Credentials{}, nil)
		require.Error(t, err)
		_, err = NewTwitch(Credentials{ID: "only-id"}, nil)
		require.Error(t, err, "without a secret there is no confidential client")
	})

	t.Run("the authorization code flow", func(t *testing.T) {
		t.Parallel()
		flow, err := NewTwitch(Credentials{ID: "other", Secret: "s3cret"}, nil)
		require.NoError(t, err)
		f, ok := flow.(*twitchCodeFlow)
		require.True(t, ok)
		assert.Equal(t, "other", f.clientID)
		assert.Equal(t, "s3cret", f.secret)
		assert.Equal(t, authorizeURL, f.authorize)
		assert.Equal(t, RedirectURL, f.redirect)
		assert.Equal(t, tokenURL, f.tokenURL)
		assert.Equal(t, revokeURL, f.revokeURL)
		assert.Equal(t, usersURL, f.usersURL)
		assert.Equal(t, timeout, f.client.Timeout)
	})
}

func TestStart(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newOAuthFake(t)
	flow := f.twitchFlow()

	l, p, err := flow.Start(ctx)
	require.NoError(t, err)
	require.NotNil(t, l.device)
	assert.Equal(t, "dc-1", l.device.DeviceCode)
	assert.Equal(t, "ABCD-EFGH", l.device.UserCode)
	assert.Equal(t, "https://login.fake/activate", l.device.VerificationURI)
	assert.Equal(t, int64(1), l.device.Interval)
	assert.WithinDuration(t, time.Now().Add(1800*time.Second), l.device.Expiry, 5*time.Second)

	assert.Equal(t, PromptFrom(l.device), *p)

	v := f.deviceReq()
	assert.Equal(t, "test-client", v.Get("client_id"))
	assert.Equal(t, strings.Join(Scopes, " "), v.Get("scope"), "the scopes are space separated in the body")
}

func TestWait(t *testing.T) {
	t.Parallel()

	t.Run("pending then success", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		f := newOAuthFake(t)
		flow := f.twitchFlow()
		f.tokenScript = []tokenReply{
			{http.StatusBadRequest, `{"error":"authorization_pending"}`},
			{http.StatusOK, fakeSuccessToken},
		}
		l, _, err := flow.Start(ctx)
		require.NoError(t, err)
		tok, err := flow.Wait(ctx, l)
		require.NoError(t, err)
		assert.Equal(t, "at-1", tok.AccessToken)
		assert.Equal(t, "rt-1", tok.RefreshToken)
		assert.WithinDuration(t, time.Now().Add(3600*time.Second), tok.Expiry, 5*time.Second)
		assert.Equal(t, "user:read:chat user:write:chat", tok.Extra("scope"))

		reqs := f.tokenReqs()
		require.Len(t, reqs, 2, "it polls until the login is complete")
		assert.Equal(t, "test-client", reqs[0].Get("client_id"))
		assert.Equal(t, "urn:ietf:params:oauth:grant-type:device_code", reqs[0].Get("grant_type"))
		assert.Equal(t, "dc-1", reqs[0].Get("device_code"))
		assert.Empty(t, reqs[0].Get("client_secret"), "a public client never sends a secret")
	})

	t.Run("slow_down lengthens the interval", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		f := newOAuthFake(t)
		flow := f.twitchFlow()
		f.tokenScript = []tokenReply{
			{http.StatusBadRequest, `{"error":"slow_down"}`},
			{http.StatusOK, fakeSuccessToken},
		}
		l, _, err := flow.Start(ctx)
		require.NoError(t, err)
		start := time.Now()
		tok, err := flow.Wait(ctx, l)
		require.NoError(t, err)
		require.NotNil(t, tok)
		// The interval is 1 s; slow_down adds 5 s, so the second poll
		// comes no earlier than 6 s after the first.
		assert.GreaterOrEqual(t, time.Since(start), 6*time.Second)
		assert.Len(t, f.tokenReqs(), 2)
	})

	t.Run("access_denied", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		f := newOAuthFake(t)
		flow := f.twitchFlow()
		f.tokenScript = []tokenReply{{http.StatusBadRequest, `{"error":"access_denied"}`}}
		_, err := flow.Wait(ctx, &Login{device: fakeDeviceAuth()})
		require.ErrorIs(t, err, ErrAccessDenied)
	})

	t.Run("expired_token", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		f := newOAuthFake(t)
		flow := f.twitchFlow()
		f.tokenScript = []tokenReply{{http.StatusBadRequest, `{"error":"expired_token"}`}}
		_, err := flow.Wait(ctx, &Login{device: fakeDeviceAuth()})
		require.ErrorIs(t, err, ErrCodeExpired)
	})

	t.Run("the code expires", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		f := newOAuthFake(t)
		flow := f.twitchFlow()
		f.tokenScript = []tokenReply{{http.StatusBadRequest, `{"error":"authorization_pending"}`}}
		da := fakeDeviceAuth()
		da.Expiry = time.Now().Add(1500 * time.Millisecond)
		_, err := flow.Wait(ctx, &Login{device: da})
		require.ErrorIs(t, err, ErrCodeExpired)
	})

	t.Run("canceled", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		f := newOAuthFake(t)
		flow := f.twitchFlow()
		f.tokenScript = []tokenReply{{http.StatusBadRequest, `{"error":"authorization_pending"}`}}
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		_, err := flow.Wait(cctx, &Login{device: fakeDeviceAuth()})
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestCodeStart(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newOAuthFake(t)
	flow := f.twitchCodeFlow()

	l, p, err := flow.Start(ctx)
	require.NoError(t, err)
	require.NotNil(t, l.callback)
	t.Cleanup(func() { l.callback.close(context.Background()) })

	u, err := url.Parse(p.URL)
	require.NoError(t, err)
	assert.Equal(t, "https", u.Scheme)
	assert.Equal(t, "id.fake", u.Host)
	assert.Equal(t, "/oauth2/authorize", u.Path)
	q := u.Query()
	assert.Equal(t, "code", q.Get("response_type"))
	assert.Equal(t, "test-client", q.Get("client_id"))
	assert.Equal(t, RedirectURL, q.Get("redirect_uri"))
	assert.Equal(t, strings.Join(Scopes, " "), q.Get("scope"))
	assert.NotEmpty(t, q.Get("state"))
	assert.Empty(t, p.Code, "the user only authorizes, there is no code to enter")
	assert.WithinDuration(t, time.Now().Add(codeWindow), p.Expiry, 5*time.Second)
}

// redirect gets the loopback listener of the login to answer, like the
// browser would after the authorization; it returns the status line and
// the body.
func redirect(ctx context.Context, t *testing.T, l *Login, q url.Values) string {
	t.Helper()
	addr, ok := l.callback.lns[0].Addr().(*net.TCPAddr)
	require.True(t, ok)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("http://127.0.0.1:%d/?%s", addr.Port, q.Encode()), nil)
	require.NoError(t, err)
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return res.Status + " " + string(body)
}

func TestCodeWait(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		f := newOAuthFake(t)
		flow := f.twitchCodeFlow()
		f.tokenScript = []tokenReply{{http.StatusOK, fakeSuccessToken}}

		l, p, err := flow.Start(ctx)
		require.NoError(t, err)

		page := redirect(ctx, t, l, url.Values{"code": {"auth-code-1"}, "state": {queryState(t, p.URL)}})
		assert.Contains(t, page, "200")
		assert.Contains(t, page, "Login complete")

		tok, err := flow.Wait(ctx, l)
		require.NoError(t, err)
		assert.Equal(t, "at-1", tok.AccessToken)
		assert.Equal(t, "rt-1", tok.RefreshToken)
		assert.WithinDuration(t, time.Now().Add(3600*time.Second), tok.Expiry, 5*time.Second)
		assert.Equal(t, "user:read:chat user:write:chat", tok.Extra("scope"))

		reqs := f.tokenReqs()
		require.Len(t, reqs, 1)
		assert.Equal(t, "test-client", reqs[0].Get("client_id"))
		assert.Equal(t, "test-secret", reqs[0].Get("client_secret"))
		assert.Equal(t, "authorization_code", reqs[0].Get("grant_type"))
		assert.Equal(t, "auth-code-1", reqs[0].Get("code"))
		assert.Equal(t, RedirectURL, reqs[0].Get("redirect_uri"))

		_, err = l.callback.lns[0].Accept()
		require.Error(t, err, "the loopback listener is closed after the login")
	})

	t.Run("the IPv6 loopback", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		f := newOAuthFake(t)
		flow := f.twitchCodeFlow()
		f.tokenScript = []tokenReply{{http.StatusOK, fakeSuccessToken}}

		l, p, err := flow.Start(ctx)
		require.NoError(t, err)
		t.Cleanup(func() { l.callback.close(context.Background()) })
		if len(l.callback.lns) < 2 {
			t.Skip("no IPv6 loopback on this host")
		}
		addr, ok := l.callback.lns[1].Addr().(*net.TCPAddr)
		require.True(t, ok)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			fmt.Sprintf("http://[::1]:%d/?%s", addr.Port,
				url.Values{"code": {"auth-code-1"}, "state": {queryState(t, p.URL)}}.Encode()), nil)
		require.NoError(t, err)
		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		assert.Contains(t, res.Status, "200")
		assert.Contains(t, string(body), "Login complete")

		tok, err := flow.Wait(ctx, l)
		require.NoError(t, err)
		assert.Equal(t, "at-1", tok.AccessToken)
	})

	t.Run("the user denies", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		f := newOAuthFake(t)
		flow := f.twitchCodeFlow()
		l, p, err := flow.Start(ctx)
		require.NoError(t, err)
		state := queryState(t, p.URL)
		redirect(ctx, t, l, url.Values{"error": {"access_denied"}, "error_description": {"denied"}, "state": {state}})
		_, err = flow.Wait(ctx, l)
		require.ErrorIs(t, err, ErrAccessDenied)
	})

	t.Run("a state mismatch", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		f := newOAuthFake(t)
		flow := f.twitchCodeFlow()
		l, _, err := flow.Start(ctx)
		require.NoError(t, err)
		redirect(ctx, t, l, url.Values{"code": {"auth-code-1"}, "state": {"wrong"}})
		_, err = flow.Wait(ctx, l)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "state")
	})

	t.Run("a redirect without code", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		f := newOAuthFake(t)
		flow := f.twitchCodeFlow()
		l, p, err := flow.Start(ctx)
		require.NoError(t, err)
		state := queryState(t, p.URL)
		redirect(ctx, t, l, url.Values{"state": {state}})
		_, err = flow.Wait(ctx, l)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no code")
	})

	t.Run("the login window expires", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		f := newOAuthFake(t)
		flow := f.twitchCodeFlow()
		flow.window = 100 * time.Millisecond
		l, _, err := flow.Start(ctx)
		require.NoError(t, err)
		_, err = flow.Wait(ctx, l)
		require.ErrorIs(t, err, ErrLoginWindow)
	})

	t.Run("canceled", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		f := newOAuthFake(t)
		flow := f.twitchCodeFlow()
		l, _, err := flow.Start(ctx)
		require.NoError(t, err)
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		_, err = flow.Wait(cctx, l)
		require.ErrorIs(t, err, context.Canceled)
		_, err = l.callback.lns[0].Accept()
		require.Error(t, err, "the loopback listener is closed when canceled")
	})
}

// queryState reads the state parameter of an authorize URL.
func queryState(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	state := u.Query().Get("state")
	require.NotEmpty(t, state)
	return state
}

func TestRefresh(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		f := newOAuthFake(t)
		flow := f.twitchFlow()
		f.tokenScript = []tokenReply{{http.StatusOK,
			`{"access_token":"at-2","token_type":"bearer","refresh_token":"rt-2","expires_in":3600,"scope":"user:read:chat user:write:chat"}`}}
		tok, err := flow.Refresh(ctx, "rt-1")
		require.NoError(t, err)
		assert.Equal(t, "at-2", tok.AccessToken)
		assert.Equal(t, "rt-2", tok.RefreshToken)
		assert.WithinDuration(t, time.Now().Add(3600*time.Second), tok.Expiry, 5*time.Second)
		assert.Equal(t, "user:read:chat user:write:chat", tok.Extra("scope"))

		v := f.tokenReqs()[0]
		assert.Equal(t, "test-client", v.Get("client_id"))
		assert.Equal(t, "refresh_token", v.Get("grant_type"))
		assert.Equal(t, "rt-1", v.Get("refresh_token"))
		assert.Empty(t, v.Get("client_secret"), "a public client never sends a secret")
	})

	t.Run("keeps the refresh token if the response omits it", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		f := newOAuthFake(t)
		flow := f.twitchFlow()
		f.tokenScript = []tokenReply{{http.StatusOK, `{"access_token":"at-3","token_type":"bearer","expires_in":3600}`}}
		tok, err := flow.Refresh(ctx, "rt-9")
		require.NoError(t, err)
		assert.Equal(t, "at-3", tok.AccessToken)
		assert.Equal(t, "rt-9", tok.RefreshToken)
	})

	t.Run("a revoked refresh token", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		f := newOAuthFake(t)
		flow := f.twitchFlow()
		f.tokenScript = []tokenReply{{http.StatusBadRequest, `{"error":"invalid_grant","error_description":"The refresh token is invalid"}`}}
		_, err := flow.Refresh(ctx, "rt-1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid_grant")
	})

	t.Run("a server error", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		f := newOAuthFake(t)
		flow := f.twitchFlow()
		f.tokenScript = []tokenReply{{http.StatusInternalServerError, `{"error":"internal"}`}}
		_, err := flow.Refresh(ctx, "rt-1")
		require.Error(t, err)
	})

	t.Run("the code flow sends the secret", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		f := newOAuthFake(t)
		flow := f.twitchCodeFlow()
		f.tokenScript = []tokenReply{{http.StatusOK,
			`{"access_token":"at-2","token_type":"bearer","refresh_token":"rt-2","expires_in":3600,"scope":"user:read:chat user:write:chat"}`}}
		tok, err := flow.Refresh(ctx, "rt-1")
		require.NoError(t, err)
		assert.Equal(t, "at-2", tok.AccessToken)
		assert.Equal(t, "rt-2", tok.RefreshToken)

		v := f.tokenReqs()[0]
		assert.Equal(t, "test-client", v.Get("client_id"))
		assert.Equal(t, "test-secret", v.Get("client_secret"))
		assert.Equal(t, "refresh_token", v.Get("grant_type"))
		assert.Equal(t, "rt-1", v.Get("refresh_token"))
	})
}

func TestRevoke(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		status  int
		wantErr bool
	}{
		{"ok", http.StatusOK, false},
		{"already revoked", http.StatusBadRequest, false},
		{"server error", http.StatusInternalServerError, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			f := newOAuthFake(t)
			flow := f.twitchFlow()
			f.revokeStatus = tc.status
			err := flow.Revoke(ctx, "at-1")
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			v := f.revokeReqs()[0]
			assert.Equal(t, "test-client", v.Get("client_id"))
			assert.Equal(t, "at-1", v.Get("token"))
			assert.Empty(t, v.Get("client_secret"), "a public client never sends a secret")
		})
	}

	t.Run("the code flow sends the secret", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		f := newOAuthFake(t)
		flow := f.twitchCodeFlow()
		require.NoError(t, flow.Revoke(ctx, "at-1"))
		v := f.revokeReqs()[0]
		assert.Equal(t, "test-client", v.Get("client_id"))
		assert.Equal(t, "test-secret", v.Get("client_secret"))
		assert.Equal(t, "at-1", v.Get("token"))
	})
}

func TestUser(t *testing.T) {
	t.Parallel()

	t.Run("the account of the token", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		f := newOAuthFake(t)
		flow := f.twitchFlow()
		id, login, err := flow.User(ctx, "at-1")
		require.NoError(t, err)
		assert.Equal(t, "1001", id)
		assert.Equal(t, "ada", login)
		auth, clientID := f.userHeaders()
		assert.Equal(t, "Bearer at-1", auth)
		assert.Equal(t, "test-client", clientID)
	})

	t.Run("the code flow looks up with the same headers", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		f := newOAuthFake(t)
		flow := f.twitchCodeFlow()
		id, login, err := flow.User(ctx, "at-1")
		require.NoError(t, err)
		assert.Equal(t, "1001", id)
		assert.Equal(t, "ada", login)
		auth, clientID := f.userHeaders()
		assert.Equal(t, "Bearer at-1", auth)
		assert.Equal(t, "test-client", clientID)
	})

	t.Run("no account", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		f := newOAuthFake(t)
		flow := f.twitchFlow()
		f.userData = `{"data":[]}`
		_, _, err := flow.User(ctx, "at-1")
		require.Error(t, err)
	})

	t.Run("unauthorized", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		f := newOAuthFake(t)
		flow := f.twitchFlow()
		f.userStatus = http.StatusUnauthorized
		_, _, err := flow.User(ctx, "at-1")
		require.Error(t, err)
	})
}
