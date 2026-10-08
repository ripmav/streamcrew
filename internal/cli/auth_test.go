// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	json "encoding/json/v2"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alecthomas/kong"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/ripmav/streamcrew/internal/app"
	"github.com/ripmav/streamcrew/internal/auth"
	"github.com/ripmav/streamcrew/internal/config"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/profile"
	"github.com/ripmav/streamcrew/internal/store"
)

// testSecretKey is a fixed vault key for the tests (like runCLI in
// cmd/streamcrew), so that the tests never touch the keyring of the
// developer (ADR-0012).
func testSecretKey() string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{42}, 32))
}

// fakeLoginFlow is the login flow of the tests: Start returns a fixed
// device response, Wait completes when the test releases it, and the
// revokes are counted.
type fakeLoginFlow struct {
	mu        sync.Mutex
	release   chan struct{}
	token     *oauth2.Token
	userID    string
	login     string
	revoked   []string
	revokeErr error
}

// newFakeLoginFlow returns a flow that grants the scopes of scopes (space
// separated) on the login.
func newFakeLoginFlow(scopes string) *fakeLoginFlow {
	return &fakeLoginFlow{
		release: make(chan struct{}),
		token: (&oauth2.Token{
			AccessToken:  "at-1",
			RefreshToken: "rt-1",
			TokenType:    "Bearer",
			Expiry:       time.Now().Add(time.Hour).UTC(),
		}).WithExtra(map[string]any{"scope": scopes}),
		userID: "u-1",
		login:  "streamy",
	}
}

// flows is the flows port of the command for the fake.
func (f *fakeLoginFlow) flows() func(platform.Name, string) (auth.Flow, error) {
	return func(platform.Name, string) (auth.Flow, error) { return f, nil }
}

func (f *fakeLoginFlow) Start(context.Context) (*oauth2.DeviceAuthResponse, error) {
	return &oauth2.DeviceAuthResponse{
		DeviceCode:      "dc-1",
		UserCode:        "ABCD-EFGH",
		VerificationURI: "https://www.twitch.tv/activate",
		Interval:        1,
		Expiry:          time.Now().Add(30 * time.Minute),
	}, nil
}

func (f *fakeLoginFlow) Wait(ctx context.Context, da *oauth2.DeviceAuthResponse) (*oauth2.Token, error) {
	if da.DeviceCode != "dc-1" {
		return nil, errors.New("fake login flow: unknown device code")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-f.release:
	}
	return f.token, nil
}

func (f *fakeLoginFlow) Refresh(context.Context, string) (*oauth2.Token, error) {
	return nil, errors.New("fake login flow: no refresh in the tests")
}

func (f *fakeLoginFlow) Revoke(_ context.Context, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked = append(f.revoked, token)
	return f.revokeErr
}

func (f *fakeLoginFlow) User(context.Context, string) (string, string, error) {
	return f.userID, f.login, nil
}

// complete lets the Wait of the login finish.
func (f *fakeLoginFlow) complete() {
	close(f.release)
}

func (f *fakeLoginFlow) setRevokeErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revokeErr = err
}

func (f *fakeLoginFlow) revokedList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.revoked)
}

// authEnv prepares a data directory with the profile "demo" and returns
// the environment the auth commands run in and the data directory.
func authEnv(ctx context.Context, t *testing.T) (*Env, string) {
	t.Helper()
	dataDir := t.TempDir()
	cfg := config.Config{
		DataDir:         dataDir,
		Mode:            config.ModeDaemon,
		Listen:          "127.0.0.1:0",
		ShutdownTimeout: 5 * time.Second,
		Log:             config.LogConfig{Level: "debug", Format: "json", File: true, MaxSize: 1, MaxFiles: 1},
	}
	require.NoError(t, cfg.Resolve())
	mgr := profile.NewManager(dataDir)
	_, err := mgr.Create(ctx, "Demo", "demo")
	require.NoError(t, err)
	cfg.Profile = "demo"
	return &Env{Config: &cfg, SecretKey: testSecretKey()}, dataDir
}

// login runs "auth login twitch" on the profile of env with the fake flow,
// completing the login as soon as the prompt is up, and returns the output
// of the command.
func login(ctx context.Context, t *testing.T, env *Env, flow *fakeLoginFlow, bot bool) (*syncedLog, error) {
	t.Helper()
	stdout, stderr := &syncedLog{}, &syncedLog{}
	env.Stdout, env.Stderr = stdout, stderr
	cmd := authLoginCmd{Platform: "twitch", Bot: bot, flows: flow.flows()}
	done := make(chan error, 1)
	go func() { done <- cmd.Run(ctx, env) }()
	require.Eventually(t, func() bool {
		return stdout.Contains("enter the code ABCD-EFGH")
	}, 10*time.Second, 10*time.Millisecond, "the prompt of the login did not appear")
	flow.complete()
	select {
	case err := <-done:
		if err != nil {
			t.Logf("stderr of the login: %s", stderr.String())
		}
		return stdout, err
	case <-time.After(10 * time.Second):
		t.Fatal("the login did not finish")
		return nil, nil
	}
}

// TestAuthKong covers the kong structure of the auth commands: the command
// line parses, and an unknown platform is a usage error.
func TestAuthKong(t *testing.T) {
	t.Parallel()
	// Only the variables of the config, not its resolvers: the test checks
	// the structure of the command line, not the sources of the flags.
	opts := []kong.Option{
		kong.Name("streamcrew"),
		kong.Writers(io.Discard, io.Discard),
		kong.UsageOnError(),
		kong.Vars{"default_data_dir": t.TempDir()},
	}

	var root Root
	parser, err := kong.New(&root, append(opts, kong.Exit(func(int) { t.Error("kong exited on a valid command line") }))...)
	require.NoError(t, err, "the auth commands are a valid kong structure")
	kctx, err := parser.Parse([]string{"auth", "login", "twitch", "--bot"})
	require.NoError(t, err)
	assert.Equal(t, "auth login <platform>", kctx.Command())

	bad, err := kong.New(&Root{}, append(opts, kong.Exit(func(int) {}))...)
	require.NoError(t, err)
	_, err = bad.Parse([]string{"auth", "login", "youtube"})
	assert.Error(t, err, "an unknown platform is a usage error")
}

// TestAuthLogin covers the login end to end: the prompt, the result line,
// and the account and the token in the store and the vault.
func TestAuthLogin(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	env, dataDir := authEnv(ctx, t)
	allScopes := strings.Join(auth.Scopes, " ")

	t.Run("streamer", func(t *testing.T) {
		flow := newFakeLoginFlow(allScopes)
		stdout, err := login(ctx, t, env, flow, false)
		require.NoError(t, err)
		// The prompt as text for the user.
		assert.Contains(t, stdout.String(),
			"twitch: open https://www.twitch.tv/activate and enter the code ABCD-EFGH\n(the code expires in 30m)\n")
		assert.Contains(t, stdout.String(), "twitch: logged in as streamy (streamer)\n")

		// The account and the token are stored under the account of the
		// login.
		ro, err := store.OpenReadOnly(ctx, profile.NewManager(dataDir).Path("demo"))
		require.NoError(t, err)
		defer ro.Close()
		list, err := ro.Accounts(ctx)
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, "twitch", list[0].Platform)
		assert.Equal(t, "streamer", list[0].Role)
		assert.Equal(t, "streamy", list[0].Login)
		assert.Equal(t, "u-1", list[0].UserID)
		assert.Equal(t, allScopes, list[0].Scopes)
		assert.Equal(t, auth.ClientID, list[0].ClientID, "the default client ID of the platform is stored")
		_, found, err := ro.GetSecret(ctx, "auth/twitch/streamer")
		require.NoError(t, err)
		assert.True(t, found, "the token is stored in the vault")
	})

	t.Run("bot", func(t *testing.T) {
		flow := newFakeLoginFlow(allScopes)
		stdout, err := login(ctx, t, env, flow, true)
		require.NoError(t, err)
		assert.Contains(t, stdout.String(), "twitch: logged in as streamy (bot)\n")
		ro, err := store.OpenReadOnly(ctx, profile.NewManager(dataDir).Path("demo"))
		require.NoError(t, err)
		defer ro.Close()
		list, err := ro.Accounts(ctx)
		require.NoError(t, err)
		assert.Len(t, list, 2, "the bot joins the streamer account")
	})
}

// TestAuthLoginAbort covers that a canceled context stops the login with
// the abort message and no stored account.
func TestAuthLoginAbort(t *testing.T) {
	t.Parallel()
	env, dataDir := authEnv(t.Context(), t)
	// abortCtx is the context of the login; the checks after the abort run
	// on the context of the test.
	abortCtx, cancel := context.WithCancel(t.Context())
	defer cancel()

	flow := newFakeLoginFlow(strings.Join(auth.Scopes, " "))
	stdout, stderr := &syncedLog{}, &syncedLog{}
	env.Stdout, env.Stderr = stdout, stderr
	cmd := authLoginCmd{Platform: "twitch", flows: flow.flows()}
	done := make(chan error, 1)
	go func() { done <- cmd.Run(abortCtx, env) }()
	require.Eventually(t, func() bool {
		return stdout.Contains("enter the code ABCD-EFGH")
	}, 10*time.Second, 10*time.Millisecond, "the prompt of the login did not appear")
	cancel()
	select {
	case err := <-done:
		assert.EqualError(t, err, "twitch: login aborted")
	case <-time.After(10 * time.Second):
		t.Fatal("the login did not stop after the abort")
	}
	assert.Empty(t, stderr.String())

	ro, err := store.OpenReadOnly(t.Context(), profile.NewManager(dataDir).Path("demo"))
	require.NoError(t, err)
	defer ro.Close()
	list, err := ro.Accounts(t.Context())
	require.NoError(t, err)
	assert.Empty(t, list, "an aborted login stores nothing")
}

// TestAuthStatus covers the status output: the empty profile, the accounts
// as text and JSON, and that it works while the core holds the lock.
func TestAuthStatus(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	env, dataDir := authEnv(ctx, t)

	runStatus := func(output string) (string, error) {
		stdout := &bytes.Buffer{}
		env.Stdout, env.Stderr = stdout, &bytes.Buffer{}
		err := authStatusCmd{Output: output}.Run(ctx, env)
		return stdout.String(), err
	}

	t.Run("empty", func(t *testing.T) {
		out, err := runStatus("text")
		require.NoError(t, err)
		assert.Equal(t, "no accounts\n", out)
		out, err = runStatus("json")
		require.NoError(t, err)
		assert.Equal(t, "[]\n", out)
	})

	flow := newFakeLoginFlow(strings.Join(auth.Scopes, " "))
	_, err := login(ctx, t, env, flow, false)
	require.NoError(t, err)
	botFlow := newFakeLoginFlow("chat:read user:read:chat")
	_, err = login(ctx, t, env, botFlow, true)
	require.NoError(t, err)

	// parseRow splits a table row: the first four columns are fixed, the
	// last one is the expiry with a space (date and time), and in between
	// stands the possibly empty list of the missing scopes.
	parseRow := func(line string) (prefix, missing []string, expiry string) {
		fields := strings.Fields(line)
		prefix = fields[:4]
		missing = fields[4 : len(fields)-2]
		expiry = fields[len(fields)-2] + " " + fields[len(fields)-1]
		return prefix, missing, expiry
	}

	t.Run("text", func(t *testing.T) {
		type row struct {
			prefix, missing []string
			expiry          string
		}
		out, err := runStatus("text")
		require.NoError(t, err)
		lines := strings.Split(strings.TrimSpace(out), "\n")
		require.Len(t, lines, 3, out)
		assert.Equal(t, []string{"PLATFORM", "ROLE", "LOGIN", "STATE", "MISSING", "EXPIRES"}, strings.Fields(lines[0]))

		rows := map[string]row{}
		for _, line := range lines[1:] {
			prefix, missing, expiry := parseRow(line)
			rows[prefix[1]] = row{prefix, missing, expiry}
		}
		assert.Equal(t, []string{"twitch", "streamer", "streamy", "ok"}, rows["streamer"].prefix)
		assert.Empty(t, rows["streamer"].missing)
		assert.Equal(t, formatTime(flow.token.Expiry), rows["streamer"].expiry)

		assert.Equal(t, []string{"twitch", "bot", "streamy", "login_required"}, rows["bot"].prefix)
		assert.Equal(t, auth.Missing("chat:read user:read:chat", auth.Scopes), rows["bot"].missing)
		assert.Equal(t, formatTime(botFlow.token.Expiry), rows["bot"].expiry)
	})

	t.Run("json", func(t *testing.T) {
		out, err := runStatus("json")
		require.NoError(t, err)
		var list []auth.Status
		require.NoError(t, json.Unmarshal([]byte(out), &list))
		require.Len(t, list, 2)
		var st *auth.Status
		for i := range list {
			if list[i].Role == "streamer" {
				st = &list[i]
			}
		}
		require.NotNil(t, st)
		assert.Equal(t, platform.Twitch, st.Platform)
		assert.Equal(t, "streamy", st.Login)
		assert.Equal(t, auth.StateOK, st.State)
		assert.Empty(t, st.Missing)
		require.NotNil(t, st.Expires)
		assert.WithinDuration(t, flow.token.Expiry, *st.Expires, time.Second)
	})

	t.Run("missing profile", func(t *testing.T) {
		env.Config.Profile = "nope"
		env.Stdout, env.Stderr = &bytes.Buffer{}, &bytes.Buffer{}
		err := authStatusCmd{Output: "text"}.Run(ctx, env)
		env.Config.Profile = "demo"
		assert.ErrorContains(t, err, "nope")
	})

	t.Run("works while the lock is held", func(t *testing.T) {
		l, err := app.LockDataDir(dataDir)
		require.NoError(t, err)
		defer func() {
			if err := l.Release(); err != nil {
				t.Errorf("release the lock: %v", err)
			}
		}()
		out, err := runStatus("text")
		require.NoError(t, err, "the status reads the profile read-only, without the lock")
		assert.Contains(t, out, "streamy")
	})
}

// TestAuthStatusNoToken covers an account without a token: it is
// login_required without an expiry.
func TestAuthStatusNoToken(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	env, dataDir := authEnv(ctx, t)

	w, err := store.Open(ctx, profile.NewManager(dataDir).Path("demo"))
	require.NoError(t, err)
	err = w.UpsertAccount(ctx, store.Account{
		Platform: "twitch", Role: "streamer", Login: "ghost", UserID: "u-9",
		Scopes: strings.Join(auth.Scopes, " "), ClientID: auth.ClientID,
	})
	require.NoError(t, err)
	require.NoError(t, w.Close())

	stdout := &bytes.Buffer{}
	env.Stdout, env.Stderr = stdout, &bytes.Buffer{}
	err = authStatusCmd{Output: "text"}.Run(ctx, env)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	require.Len(t, lines, 2)
	assert.Equal(t, []string{"twitch", "streamer", "ghost", "login_required", "-"}, strings.Fields(lines[1]))
}

// TestAuthLogout covers the logout: the refresh token is revoked, the
// account and the token are removed, and a failed revoke keeps them.
func TestAuthLogout(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	env, dataDir := authEnv(ctx, t)

	t.Run("no account", func(t *testing.T) {
		env.Stdout, env.Stderr = &bytes.Buffer{}, &bytes.Buffer{}
		err := authLogoutCmd{Platform: "twitch"}.Run(ctx, env)
		assert.ErrorContains(t, err, "no account")
	})

	flow := newFakeLoginFlow(strings.Join(auth.Scopes, " "))
	_, err := login(ctx, t, env, flow, false)
	require.NoError(t, err)

	t.Run("removes the account", func(t *testing.T) {
		stdout := &bytes.Buffer{}
		env.Stdout, env.Stderr = stdout, &bytes.Buffer{}
		err := authLogoutCmd{Platform: "twitch", flows: flow.flows()}.Run(ctx, env)
		require.NoError(t, err)
		assert.Equal(t, "twitch: removed streamy (streamer)\n", stdout.String())
		assert.Equal(t, []string{"rt-1"}, flow.revokedList(), "the refresh token is revoked")

		ro, err := store.OpenReadOnly(ctx, profile.NewManager(dataDir).Path("demo"))
		require.NoError(t, err)
		defer ro.Close()
		list, err := ro.Accounts(ctx)
		require.NoError(t, err)
		assert.Empty(t, list)
		_, found, err := ro.GetSecret(ctx, "auth/twitch/streamer")
		require.NoError(t, err)
		assert.False(t, found)
	})

	t.Run("revoke error keeps the account", func(t *testing.T) {
		env2, dataDir2 := authEnv(ctx, t)
		flow2 := newFakeLoginFlow(strings.Join(auth.Scopes, " "))
		flow2.setRevokeErr(errors.New("revoke broken"))
		_, err := login(ctx, t, env2, flow2, false)
		require.NoError(t, err)

		stdout := &bytes.Buffer{}
		env2.Stdout, env2.Stderr = stdout, &bytes.Buffer{}
		err = authLogoutCmd{Platform: "twitch", flows: flow2.flows()}.Run(ctx, env2)
		assert.ErrorContains(t, err, "revoke broken")

		ro, err := store.OpenReadOnly(ctx, profile.NewManager(dataDir2).Path("demo"))
		require.NoError(t, err)
		defer ro.Close()
		list, err := ro.Accounts(ctx)
		require.NoError(t, err)
		assert.Len(t, list, 1, "the account stays, so that the logout can be retried")
	})
}
