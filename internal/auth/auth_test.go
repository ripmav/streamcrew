// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/store"
	"github.com/ripmav/streamcrew/internal/vault"
)

// fakeFlow is the flow of the service tests; the calls are recorded where
// they matter (the revoked tokens). The expiries of the tokens come from
// waitExpiry and refreshExpiry, the wall clock when they are zero.
type fakeFlow struct {
	userID        string
	userLogin     string
	waitExpiry    time.Time
	refreshExpiry time.Time
	revoked       []string
}

func (f *fakeFlow) Start(_ context.Context) (*oauth2.DeviceAuthResponse, error) {
	return &oauth2.DeviceAuthResponse{
		DeviceCode:      "dc-1",
		UserCode:        "ABCD-EFGH",
		VerificationURI: "https://login.fake/activate",
		Interval:        5,
		Expiry:          time.Now().UTC().Add(15 * time.Minute),
	}, nil
}

func (f *fakeFlow) Wait(_ context.Context, _ *oauth2.DeviceAuthResponse) (*oauth2.Token, error) {
	expiry := f.waitExpiry
	if expiry.IsZero() {
		expiry = time.Now().UTC().Add(time.Hour)
	}
	tok := &oauth2.Token{
		AccessToken:  "at-1",
		RefreshToken: "rt-1",
		Expiry:       expiry,
	}
	return tok.WithExtra(map[string]any{"scope": "user:read:chat bits:read"}), nil
}

func (f *fakeFlow) Refresh(_ context.Context, _ string) (*oauth2.Token, error) {
	expiry := f.refreshExpiry
	if expiry.IsZero() {
		expiry = time.Now().UTC().Add(time.Hour)
	}
	return &oauth2.Token{
		AccessToken:  "at-2",
		RefreshToken: "rt-2",
		Expiry:       expiry,
	}, nil
}

func (f *fakeFlow) Revoke(_ context.Context, token string) error {
	f.revoked = append(f.revoked, token)
	return nil
}

func (f *fakeFlow) User(_ context.Context, _ string) (string, string, error) {
	return f.userID, f.userLogin, nil
}

// failingFlow is a flow whose calls fail with the given errors.
type failingFlow struct {
	waitErr    error
	refreshErr error
	revokeErr  error
}

func (f *failingFlow) Start(context.Context) (*oauth2.DeviceAuthResponse, error) {
	return &oauth2.DeviceAuthResponse{
		DeviceCode:      "dc-1",
		UserCode:        "ABCD-EFGH",
		VerificationURI: "https://login.fake/activate",
		Interval:        5,
		Expiry:          time.Now().UTC().Add(15 * time.Minute),
	}, nil
}

func (f *failingFlow) Wait(context.Context, *oauth2.DeviceAuthResponse) (*oauth2.Token, error) {
	return nil, f.waitErr
}

func (f *failingFlow) Refresh(context.Context, string) (*oauth2.Token, error) {
	return nil, f.refreshErr
}

func (f *failingFlow) Revoke(context.Context, string) error {
	return f.revokeErr
}

func (f *failingFlow) User(context.Context, string) (string, string, error) {
	return "", "", f.waitErr
}

// recPublisher records the events it takes.
type recPublisher struct {
	mu     sync.Mutex
	events []event.Envelope
}

func (r *recPublisher) Publish(_ context.Context, e event.Envelope) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	return nil
}

func (r *recPublisher) of(t event.Type) []event.Envelope {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []event.Envelope
	for _, e := range r.events {
		if e.Type == t {
			out = append(out, e)
		}
	}
	return out
}

// fakeClock is the clock of the service tests.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// flowCalls records what the Flows port was asked for.
type flowCalls struct {
	mu  sync.Mutex
	all []string
}

func (c *flowCalls) add(p platform.Name, clientID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.all = append(c.all, string(p)+"/"+clientID)
}

func (c *flowCalls) list() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.all)
}

// testAuth is a service over a real store and vault in a temporary
// directory (the pattern of internal/app and internal/vault), with a fake
// flow and a recording publisher. The refresh loop of the service runs on
// a short interval, so that the tests can drive it.
type testAuth struct {
	t       *testing.T
	Service *Service
	Store   *store.Store
	Vault   *vault.Vault
	Flow    *fakeFlow
	Events  *recPublisher
	Clock   *fakeClock
	FlowIDs *flowCalls
}

func newTestAuth(t *testing.T, flow Flow) *testAuth {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(t.Context(), filepath.Join(dir, "profile.db"),
		store.WithLogger(slog.New(slog.DiscardHandler)))
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	key := make([]byte, 32)
	_, err = rand.Read(key)
	require.NoError(t, err)
	ks, err := vault.NewKeys(dir, base64.StdEncoding.EncodeToString(key), nil, nil).Load(t.Context())
	require.NoError(t, err)
	vaultOf := func(repo vault.Repository) *vault.Vault { return vault.New(repo, ks) }

	pub := &recPublisher{}
	clk := &fakeClock{now: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)}
	calls := &flowCalls{}
	s, err := New(Ports{
		Store: st,
		Vault: vaultOf,
		Flows: func(p platform.Name, clientID string) (Flow, error) {
			calls.add(p, clientID)
			return flow, nil
		},
		Publisher: pub,
		Logger:    slog.New(slog.DiscardHandler),
		Clock:     clk.Now,
	}, WithTick(5*time.Millisecond))
	require.NoError(t, err)
	f, _ := flow.(*fakeFlow)
	return &testAuth{t: t, Service: s, Store: st, Vault: vault.New(st, ks), Flow: f, Events: pub, Clock: clk, FlowIDs: calls}
}

func newTestFlow(t *testing.T) *fakeFlow {
	t.Helper()
	return &fakeFlow{
		userID:    "1001",
		userLogin: "ada",
	}
}

// login stores an account and its token directly, the way a finished login
// does.
func (a *testAuth) login(acc Account, tok Token) {
	a.t.Helper()
	require.NoError(a.t, a.Service.save(a.t.Context(), acc, tok))
}

// storedToken reads the token record of the twitch streamer from the vault.
func (a *testAuth) storedToken() (Token, bool) {
	a.t.Helper()
	data, err := a.Vault.Get(a.t.Context(), authName(platform.Twitch, connector.AccountStreamer))
	if errors.Is(err, vault.ErrNotFound) {
		return Token{}, false
	}
	require.NoError(a.t, err)
	var tok Token
	require.NoError(a.t, json.Unmarshal([]byte(data.Reveal()), &tok))
	return tok, true
}

// streamerAccount is a streamer account for the tests.
func streamerAccount(scopes ...string) Account {
	return Account{
		Platform: platform.Twitch,
		Role:     connector.AccountStreamer,
		Login:    "ada",
		UserID:   "1001",
		Scopes:   scopes,
		ClientID: "test-client",
	}
}

// TestStartLogin covers the prompt and its event.
func TestStartLogin(t *testing.T) {
	t.Parallel()
	a := newTestAuth(t, newTestFlow(t))
	ctx := t.Context()

	pr, err := a.Service.StartLogin(ctx, platform.Twitch, connector.AccountStreamer, "")
	require.NoError(t, err)
	assert.Equal(t, "https://login.fake/activate", pr.URL)
	assert.Equal(t, "ABCD-EFGH", pr.Code)
	assert.False(t, pr.Expiry.IsZero())
	// The default client ID of the platform is used.
	assert.Equal(t, []string{string(platform.Twitch) + "/" + ClientID}, a.FlowIDs.list())

	events := a.Events.of(eventtype.AuthActionRequired)
	require.Len(t, events, 1)
	assert.Equal(t, event.Source{Kind: event.SourceSystem, Name: "auth"}, events[0].Source)
	payload, ok := event.Payload[ActionRequired](events[0])
	require.True(t, ok)
	assert.Equal(t, platform.Twitch, payload.Platform)
	assert.Equal(t, connector.AccountStreamer, payload.Role)
	assert.Equal(t, "https://login.fake/activate", payload.URL)
	assert.Equal(t, "ABCD-EFGH", payload.Code)
	assert.False(t, payload.Expires.IsZero())
}

// TestWaitLogin covers the end of a login: the account and the token are
// stored, and the result is published.
func TestWaitLogin(t *testing.T) {
	t.Parallel()
	flow := newTestFlow(t)
	a := newTestAuth(t, flow)
	ctx := t.Context()
	da, err := flow.Start(ctx)
	require.NoError(t, err)

	// A failed login publishes login_failed and stores nothing.
	a.Service.ports.Flows = func(platform.Name, string) (Flow, error) {
		return &failingFlow{waitErr: ErrAccessDenied}, nil
	}
	_, err = a.Service.WaitLogin(ctx, da, platform.Twitch, connector.AccountStreamer, "")
	assert.Error(t, err)
	require.Len(t, a.Events.of(eventtype.AuthLoginFailed), 1)
	failed, _ := event.Payload[LoginFailed](a.Events.of(eventtype.AuthLoginFailed)[0])
	assert.Equal(t, "the user denied the login", failed.Reason)
	_, found, err := a.Store.Account(ctx, "twitch", "streamer")
	require.NoError(t, err)
	assert.False(t, found)

	// An aborted login is a failed one, too.
	a.Service.ports.Flows = func(platform.Name, string) (Flow, error) {
		return &failingFlow{waitErr: context.Canceled}, nil
	}
	_, err = a.Service.WaitLogin(ctx, da, platform.Twitch, connector.AccountStreamer, "")
	assert.Error(t, err)
	require.Len(t, a.Events.of(eventtype.AuthLoginFailed), 2)
	failed, _ = event.Payload[LoginFailed](a.Events.of(eventtype.AuthLoginFailed)[1])
	assert.Equal(t, "the login was aborted", failed.Reason)

	// A finished login stores the account and the token, and publishes
	// login_completed.
	a.Service.ports.Flows = func(platform.Name, string) (Flow, error) { return flow, nil }
	acc, err := a.Service.WaitLogin(ctx, da, platform.Twitch, connector.AccountStreamer, "")
	require.NoError(t, err)
	assert.Equal(t, "ada", acc.Login)
	assert.Equal(t, "1001", acc.UserID)
	assert.Equal(t, ClientID, acc.ClientID, "the default client ID is stored")
	assert.Equal(t, []string{"user:read:chat", "bits:read"}, acc.Scopes)

	row, found, err := a.Store.Account(ctx, "twitch", "streamer")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "ada", row.Login)
	assert.Equal(t, "user:read:chat bits:read", row.Scopes)
	assert.Equal(t, ClientID, row.ClientID)

	tok, found := a.storedToken()
	require.True(t, found)
	assert.Equal(t, "at-1", tok.AccessToken)
	assert.Equal(t, "rt-1", tok.RefreshToken)
	assert.False(t, tok.ExpiresAt.IsZero())

	events := a.Events.of(eventtype.AuthLoginCompleted)
	require.Len(t, events, 1)
	completed, _ := event.Payload[LoginCompleted](events[0])
	assert.Equal(t, platform.Twitch, completed.Platform)
	assert.Equal(t, connector.AccountStreamer, completed.Role)
	assert.Equal(t, "ada", completed.Login)
}

// TestWaitLoginStoresClientID covers the client ID a login is made with.
func TestWaitLoginStoresClientID(t *testing.T) {
	t.Parallel()
	flow := newTestFlow(t)
	a := newTestAuth(t, flow)
	ctx := t.Context()
	da, err := flow.Start(ctx)
	require.NoError(t, err)

	_, err = a.Service.WaitLogin(ctx, da, platform.Twitch, connector.AccountBot, "byo-client")
	require.NoError(t, err)
	row, found, err := a.Store.Account(ctx, "twitch", "bot")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "byo-client", row.ClientID, "the given client ID is stored")
}

// TestSaveIsAtomic covers Code-ADR-0008: a save that fails stores neither
// the account nor the token.
func TestSaveIsAtomic(t *testing.T) {
	t.Parallel()
	a := newTestAuth(t, newTestFlow(t))
	ctx := t.Context()
	tok := Token{AccessToken: "at-1", RefreshToken: "rt-1", ExpiresAt: a.Clock.Now().Add(time.Hour)}

	// The role "admin" violates the check of the accounts table, so the
	// transaction rolls back.
	acc := streamerAccount(Scopes...)
	acc.Role = connector.Account("admin")
	assert.Error(t, a.Service.save(ctx, acc, tok))

	_, found, err := a.Store.Account(ctx, "twitch", "streamer")
	require.NoError(t, err)
	assert.False(t, found, "the account is rolled back")
	_, found = a.storedToken()
	assert.False(t, found, "the vault entry is rolled back")
}

// TestStatus covers the state of the accounts.
func TestStatus(t *testing.T) {
	t.Parallel()
	a := newTestAuth(t, newTestFlow(t))
	ctx := t.Context()
	now := a.Clock.Now()

	// Without accounts, nothing.
	out, err := a.Service.Status(ctx)
	require.NoError(t, err)
	assert.Empty(t, out)

	// A complete account is ok.
	a.login(streamerAccount(Scopes...), Token{AccessToken: "at-1", RefreshToken: "rt-1", ExpiresAt: now.Add(time.Hour)})
	out, err = a.Service.Status(ctx)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, StateOK, out[0].State)
	assert.Equal(t, Scopes, out[0].Scopes)
	assert.Empty(t, out[0].Missing)
	require.NotNil(t, out[0].Expires)
	assert.WithinDuration(t, now.Add(time.Hour), *out[0].Expires, time.Second)

	// A bot login without a required scope is login_required.
	bot := streamerAccount("user:read:chat")
	bot.Role = connector.AccountBot
	bot.Login = "botbot"
	a.login(bot, Token{AccessToken: "at-2", RefreshToken: "rt-2", ExpiresAt: now.Add(time.Hour)})
	out, err = a.Service.Status(ctx)
	require.NoError(t, err)
	require.Len(t, out, 2)
	for _, st := range out {
		if st.Role != connector.AccountBot {
			continue
		}
		assert.Equal(t, StateLoginRequired, st.State)
		assert.NotEmpty(t, st.Missing, "the missing scopes are named")
		assert.NotContains(t, st.Missing, "user:read:chat", "a granted scope is not missing")
	}

	// An account without a token record is login_required.
	require.NoError(t, a.Vault.Delete(ctx, authName(platform.Twitch, connector.AccountBot)))
	out, err = a.Service.Status(ctx)
	require.NoError(t, err)
	for _, st := range out {
		if st.Role != connector.AccountBot {
			continue
		}
		assert.Equal(t, StateLoginRequired, st.State)
		assert.Nil(t, st.Expires)
	}

	// A token without a refresh token is login_required.
	noRefresh := streamerAccount(Scopes...)
	noRefresh.Role = connector.AccountBot
	noRefresh.Login = "nor"
	a.login(noRefresh, Token{AccessToken: "at-3", RefreshToken: "", ExpiresAt: now.Add(time.Hour)})
	out, err = a.Service.Status(ctx)
	require.NoError(t, err)
	for _, st := range out {
		if st.Login != "nor" {
			continue
		}
		assert.Equal(t, StateLoginRequired, st.State)
	}
}

// TestToken covers the token port of the platform adapters.
func TestToken(t *testing.T) {
	t.Parallel()
	flow := newTestFlow(t)
	a := newTestAuth(t, flow)
	ctx := t.Context()
	now := a.Clock.Now()

	// Without an account, no token.
	_, err := a.Service.Token(ctx, platform.Twitch, connector.AccountStreamer)
	assert.ErrorIs(t, err, ErrNoAccount)

	// A valid token is returned without a refresh.
	flow.waitExpiry = now.Add(time.Hour)
	a.login(streamerAccount(Scopes...), Token{AccessToken: "at-1", RefreshToken: "rt-1", ExpiresAt: now.Add(time.Hour)})
	tok, err := a.Service.Token(ctx, platform.Twitch, connector.AccountStreamer)
	require.NoError(t, err)
	assert.Equal(t, "at-1", tok)

	// An expired token is refreshed, and the new one is stored.
	flow.refreshExpiry = now.Add(3 * time.Hour)
	a.Clock.Set(now.Add(2 * time.Hour))
	tok, err = a.Service.Token(ctx, platform.Twitch, connector.AccountStreamer)
	require.NoError(t, err)
	assert.Equal(t, "at-2", tok)
	stored, found := a.storedToken()
	require.True(t, found)
	assert.Equal(t, "at-2", stored.AccessToken)
	assert.Equal(t, "rt-2", stored.RefreshToken)

	// An expired refresh token is a login_required.
	a.Clock.Set(now.Add(4 * time.Hour))
	a.Service.ports.Flows = func(platform.Name, string) (Flow, error) {
		return &failingFlow{refreshErr: fmt.Errorf("%w: invalid_grant: expired", ErrTokenExpired)}, nil
	}
	_, err = a.Service.Token(ctx, platform.Twitch, connector.AccountStreamer)
	assert.ErrorIs(t, err, ErrLoginRequired)

	// Another refresh error stays an error.
	a.Service.ports.Flows = func(platform.Name, string) (Flow, error) {
		return &failingFlow{refreshErr: errors.New("the server is unreachable")}, nil
	}
	_, err = a.Service.Token(ctx, platform.Twitch, connector.AccountStreamer)
	assert.Error(t, err)
	assert.NotErrorIs(t, err, ErrLoginRequired)

	// An account without a token record is a login_required.
	noTok := streamerAccount(Scopes...)
	noTok.Role = connector.AccountBot
	require.NoError(t, a.Store.UpsertAccount(ctx, noTok.ToStore()))
	_, err = a.Service.Token(ctx, platform.Twitch, connector.AccountBot)
	assert.ErrorIs(t, err, ErrLoginRequired)
}

// TestLogout covers the revoke and the deletion.
func TestLogout(t *testing.T) {
	t.Parallel()
	a := newTestAuth(t, newTestFlow(t))
	ctx := t.Context()
	now := a.Clock.Now()
	a.login(streamerAccount(Scopes...), Token{AccessToken: "at-1", RefreshToken: "rt-1", ExpiresAt: now.Add(time.Hour)})

	// Without an account, no logout.
	err := a.Service.Logout(ctx, platform.Twitch, connector.AccountBot, "")
	assert.ErrorIs(t, err, ErrNoAccount)

	// A failed revoke keeps the account and the token.
	a.Service.ports.Flows = func(platform.Name, string) (Flow, error) {
		return &failingFlow{revokeErr: errors.New("the server is unreachable")}, nil
	}
	assert.Error(t, a.Service.Logout(ctx, platform.Twitch, connector.AccountStreamer, ""))
	_, found, err := a.Store.Account(ctx, "twitch", "streamer")
	require.NoError(t, err)
	assert.True(t, found)
	_, found = a.storedToken()
	assert.True(t, found)

	// A successful logout revokes the refresh token and removes both.
	a.Service.ports.Flows = func(platform.Name, string) (Flow, error) { return a.Flow, nil }
	require.NoError(t, a.Service.Logout(ctx, platform.Twitch, connector.AccountStreamer, ""))
	_, found, err = a.Store.Account(ctx, "twitch", "streamer")
	require.NoError(t, err)
	assert.False(t, found)
	_, found = a.storedToken()
	assert.False(t, found)
	assert.Equal(t, []string{"rt-1"}, a.Flow.revoked, "the refresh token is revoked")
}

// TestRun covers the refresh loop.
func TestRun(t *testing.T) {
	t.Parallel()

	t.Run("refreshes within the window", func(t *testing.T) {
		t.Parallel()
		flow := newTestFlow(t)
		a := newTestAuth(t, flow)
		now := a.Clock.Now()
		flow.refreshExpiry = now.Add(time.Hour)
		a.login(streamerAccount(Scopes...), Token{AccessToken: "at-1", RefreshToken: "rt-1", ExpiresAt: now.Add(10 * time.Minute)})

		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)
		done := make(chan error, 1)
		go func() { done <- a.Service.Run(ctx) }()

		require.Eventually(t, func() bool {
			stored, _ := a.storedToken()
			return stored.AccessToken == "at-2"
		}, 2*time.Second, time.Millisecond)
		cancel()
		assert.NoError(t, <-done)
	})

	t.Run("does not refresh outside the window", func(t *testing.T) {
		t.Parallel()
		a := newTestAuth(t, newTestFlow(t))
		now := a.Clock.Now()
		a.login(streamerAccount(Scopes...), Token{AccessToken: "at-1", RefreshToken: "rt-1", ExpiresAt: now.Add(time.Hour)})
		a.Service.tick(t.Context())
		stored, _ := a.storedToken()
		assert.Equal(t, "at-1", stored.AccessToken, "the token is untouched")
	})

	t.Run("an expired refresh token publishes login_failed", func(t *testing.T) {
		t.Parallel()
		a := newTestAuth(t, newTestFlow(t))
		now := a.Clock.Now()
		a.login(streamerAccount(Scopes...), Token{AccessToken: "at-1", RefreshToken: "rt-1", ExpiresAt: now.Add(10 * time.Minute)})
		a.Service.ports.Flows = func(platform.Name, string) (Flow, error) {
			return &failingFlow{refreshErr: fmt.Errorf("%w: invalid_grant: expired", ErrTokenExpired)}, nil
		}

		a.Service.tick(t.Context())

		events := a.Events.of(eventtype.AuthLoginFailed)
		require.Len(t, events, 1)
		failed, _ := event.Payload[LoginFailed](events[0])
		assert.Equal(t, "token expired", failed.Reason)
	})

	t.Run("an account without a token is skipped", func(t *testing.T) {
		t.Parallel()
		a := newTestAuth(t, newTestFlow(t))
		acc := streamerAccount(Scopes...)
		require.NoError(t, a.Store.UpsertAccount(t.Context(), acc.ToStore()))
		a.Service.tick(t.Context())
		assert.Empty(t, a.Events.of(eventtype.AuthLoginFailed))
	})
}

// TestTokenJSON covers the vault form of the token (RFC3339, UTC).
func TestTokenJSON(t *testing.T) {
	t.Parallel()
	tok := Token{
		AccessToken:  "at-1",
		RefreshToken: "rt-1",
		ExpiresAt:    time.Date(2026, 10, 7, 13, 0, 0, 0, time.UTC),
	}
	data, err := json.Marshal(tok)
	require.NoError(t, err)
	assert.JSONEq(t, `{"access_token":"at-1","refresh_token":"rt-1","expires_at":"2026-10-07T13:00:00Z"}`, string(data))

	var back Token
	require.NoError(t, json.Unmarshal(data, &back))
	assert.Equal(t, tok, back)
}

// TestAccountMapping covers the mapping between the store row and the
// domain form.
func TestAccountMapping(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "auth/twitch/streamer", authName(platform.Twitch, connector.AccountStreamer))
	assert.Equal(t, "auth/twitch/bot", authName(platform.Twitch, connector.AccountBot))

	row := store.Account{
		Platform:  "twitch",
		Role:      "streamer",
		Login:     "ada",
		UserID:    "1001",
		Scopes:    "user:read:chat bits:read",
		ClientID:  "test-client",
		UpdatedAt: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC),
	}
	acc := FromStore(row)
	assert.Equal(t, platform.Twitch, acc.Platform)
	assert.Equal(t, connector.AccountStreamer, acc.Role)
	assert.Equal(t, []string{"user:read:chat", "bits:read"}, acc.Scopes)
	assert.Equal(t, row, acc.ToStore())

	// An empty scope list stays a list, not null.
	empty := FromStore(store.Account{Platform: "twitch", Role: "streamer"})
	assert.Empty(t, empty.Scopes)
	assert.NotNil(t, empty.Scopes)
}

// TestRegisterEvents covers the catalog of the auth events.
func TestRegisterEvents(t *testing.T) {
	t.Parallel()
	c := event.NewCatalog()
	require.NoError(t, RegisterEvents(c))
	types := c.Types()
	assert.Contains(t, types, eventtype.AuthActionRequired)
	assert.Contains(t, types, eventtype.AuthLoginCompleted)
	assert.Contains(t, types, eventtype.AuthLoginFailed)

	// A payload of another Go type does not match the catalog.
	src := event.Source{Kind: event.SourceSystem, Name: "auth"}
	assert.Error(t, c.Check(event.New(src, eventtype.AuthActionRequired, "not a payload")))
	assert.NoError(t, c.Check(event.New(src, eventtype.AuthActionRequired, ActionRequired{Code: "x"})))
}
