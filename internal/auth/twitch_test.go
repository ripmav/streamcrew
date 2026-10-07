// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"fmt"
	"io"
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

func newTwitchFake(t *testing.T) (*oauthFake, *twitchFlow) {
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
	flow := &twitchFlow{
		clientID:  "test-client",
		client:    f.srv.Client(),
		deviceURL: f.srv.URL + "/device",
		tokenURL:  f.srv.URL + "/token",
		revokeURL: f.srv.URL + "/revoke",
		usersURL:  f.srv.URL + "/users",
	}
	return f, flow
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
	flow, ok := NewTwitch("", nil).(*twitchFlow)
	require.True(t, ok)
	assert.Equal(t, ClientID, flow.clientID)
	assert.Equal(t, timeout, flow.client.Timeout)
	assert.Equal(t, deviceURL, flow.deviceURL)
	assert.Equal(t, tokenURL, flow.tokenURL)
	assert.Equal(t, revokeURL, flow.revokeURL)
	assert.Equal(t, usersURL, flow.usersURL)
	flow, ok = NewTwitch("other", nil).(*twitchFlow)
	require.True(t, ok)
	assert.Equal(t, "other", flow.clientID)
}

func TestStart(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f, flow := newTwitchFake(t)

	da, err := flow.Start(ctx)
	require.NoError(t, err)
	assert.Equal(t, "dc-1", da.DeviceCode)
	assert.Equal(t, "ABCD-EFGH", da.UserCode)
	assert.Equal(t, "https://login.fake/activate", da.VerificationURI)
	assert.Equal(t, int64(1), da.Interval)
	assert.WithinDuration(t, time.Now().Add(1800*time.Second), da.Expiry, 5*time.Second)

	p := PromptFrom(da)
	assert.Equal(t, Prompt{URL: "https://login.fake/activate", Code: "ABCD-EFGH", Expiry: da.Expiry}, p)

	v := f.deviceReq()
	assert.Equal(t, "test-client", v.Get("client_id"))
	assert.Equal(t, strings.Join(Scopes, " "), v.Get("scope"), "the scopes are space separated in the body")
}

func TestWait(t *testing.T) {
	t.Parallel()

	t.Run("pending then success", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		f, flow := newTwitchFake(t)
		f.tokenScript = []tokenReply{
			{http.StatusBadRequest, `{"error":"authorization_pending"}`},
			{http.StatusOK, fakeSuccessToken},
		}
		da, err := flow.Start(ctx)
		require.NoError(t, err)
		tok, err := flow.Wait(ctx, da)
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
		f, flow := newTwitchFake(t)
		f.tokenScript = []tokenReply{
			{http.StatusBadRequest, `{"error":"slow_down"}`},
			{http.StatusOK, fakeSuccessToken},
		}
		da, err := flow.Start(ctx)
		require.NoError(t, err)
		start := time.Now()
		tok, err := flow.Wait(ctx, da)
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
		f, flow := newTwitchFake(t)
		f.tokenScript = []tokenReply{{http.StatusBadRequest, `{"error":"access_denied"}`}}
		_, err := flow.Wait(ctx, fakeDeviceAuth())
		require.ErrorIs(t, err, ErrAccessDenied)
	})

	t.Run("expired_token", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		f, flow := newTwitchFake(t)
		f.tokenScript = []tokenReply{{http.StatusBadRequest, `{"error":"expired_token"}`}}
		_, err := flow.Wait(ctx, fakeDeviceAuth())
		require.ErrorIs(t, err, ErrCodeExpired)
	})

	t.Run("the code expires", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		f, flow := newTwitchFake(t)
		f.tokenScript = []tokenReply{{http.StatusBadRequest, `{"error":"authorization_pending"}`}}
		da := fakeDeviceAuth()
		da.Expiry = time.Now().Add(1500 * time.Millisecond)
		_, err := flow.Wait(ctx, da)
		require.ErrorIs(t, err, ErrCodeExpired)
	})

	t.Run("canceled", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		f, flow := newTwitchFake(t)
		f.tokenScript = []tokenReply{{http.StatusBadRequest, `{"error":"authorization_pending"}`}}
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		_, err := flow.Wait(cctx, fakeDeviceAuth())
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestRefresh(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		f, flow := newTwitchFake(t)
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
		f, flow := newTwitchFake(t)
		f.tokenScript = []tokenReply{{http.StatusOK, `{"access_token":"at-3","token_type":"bearer","expires_in":3600}`}}
		tok, err := flow.Refresh(ctx, "rt-9")
		require.NoError(t, err)
		assert.Equal(t, "at-3", tok.AccessToken)
		assert.Equal(t, "rt-9", tok.RefreshToken)
	})

	t.Run("a revoked refresh token", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		f, flow := newTwitchFake(t)
		f.tokenScript = []tokenReply{{http.StatusBadRequest, `{"error":"invalid_grant","error_description":"The refresh token is invalid"}`}}
		_, err := flow.Refresh(ctx, "rt-1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid_grant")
	})

	t.Run("a server error", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		f, flow := newTwitchFake(t)
		f.tokenScript = []tokenReply{{http.StatusInternalServerError, `{"error":"internal"}`}}
		_, err := flow.Refresh(ctx, "rt-1")
		require.Error(t, err)
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
			f, flow := newTwitchFake(t)
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
		})
	}
}

func TestUser(t *testing.T) {
	t.Parallel()

	t.Run("the account of the token", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		f, flow := newTwitchFake(t)
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
		f, flow := newTwitchFake(t)
		f.userData = `{"data":[]}`
		_, _, err := flow.User(ctx, "at-1")
		require.Error(t, err)
	})

	t.Run("unauthorized", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		f, flow := newTwitchFake(t)
		f.userStatus = http.StatusUnauthorized
		_, _, err := flow.User(ctx, "at-1")
		require.Error(t, err)
	})
}
