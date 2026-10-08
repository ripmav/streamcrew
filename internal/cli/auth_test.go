// SPDX-License-Identifier: MIT

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
// prompt (a code when the flow is a device code login), Wait completes when
// the test releases it, the revokes are counted, and the credentials of the
// flows are remembered.
type fakeLoginFlow struct {
	mu        sync.Mutex
	release   chan struct{}
	token     *oauth2.Token
	userID    string
	login     string
	code      string
	revoked   []string
	revokeErr error
	creds     []auth.Credentials
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

// flows is the flows port of the command for the fake; it remembers the
// credentials of every flow it makes, so that the tests can check which app
// a call was made with.
func (f *fakeLoginFlow) flows() func(platform.Name, auth.Credentials) (auth.Flow, error) {
	return func(_ platform.Name, c auth.Credentials) (auth.Flow, error) {
		f.mu.Lock()
		f.creds = append(f.creds, c)
		f.mu.Unlock()
		return f, nil
	}
}

func (f *fakeLoginFlow) Start(context.Context) (*auth.Login, *auth.Prompt, error) {
	return &auth.Login{}, &auth.Prompt{
		URL:    "https://id.twitch.tv/oauth2/authorize",
		Code:   f.code,
		Expiry: time.Now().Add(10 * time.Minute),
	}, nil
}

func (f *fakeLoginFlow) Wait(ctx context.Context, _ *auth.Login) (*oauth2.Token, error) {
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

func (f *fakeLoginFlow) credsList() []auth.Credentials {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.creds)
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
// of the command; bot selects the role, creds the flags of the command.
func login(ctx context.Context, t *testing.T, env *Env, flow *fakeLoginFlow, bot bool, creds auth.Credentials) (*syncedLog, error) {
	t.Helper()
	stdout, stderr := &syncedLog{}, &syncedLog{}
	env.Stdout, env.Stderr = stdout, stderr
	cmd := authLoginCmd{Platform: "twitch", Bot: bot, DeviceFlow: creds.DeviceFlow, ClientID: creds.ID, ClientSecret: creds.Secret, flows: flow.flows()}
	done := make(chan error, 1)
	go func() { done <- cmd.Run(ctx, env) }()
	require.Eventually(t, func() bool {
		return stdout.Contains("authorize the app") || stdout.Contains("enter the code")
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
	kctx, err := parser.Parse([]string{"auth", "login", "twitch", "--bot", "--device-flow", "--client-id", "byo", "--client-secret", "s"})
	require.NoError(t, err)
	assert.Equal(t, "auth login <platform>", kctx.Command())
	assert.True(t, root.Auth.Login.Bot)
	assert.True(t, root.Auth.Login.DeviceFlow)
	assert.Equal(t, "byo", root.Auth.Login.ClientID, "the flag is derived from ClientID")
	assert.Equal(t, "s", root.Auth.Login.ClientSecret, "the flag is derived from ClientSecret")

	kctx, err = parser.Parse([]string{"auth", "status", "-o", "json"})
	require.NoError(t, err)
	assert.Equal(t, "auth status", kctx.Command())
	assert.Equal(t, "json", root.Auth.Status.Output)

	kctx, err = parser.Parse([]string{"auth", "logout", "twitch", "--bot"})
	require.NoError(t, err)
	assert.Equal(t, "auth logout <platform>", kctx.Command())
	assert.True(t, root.Auth.Logout.Bot)

	bad, err := kong.New(&Root{}, append(opts, kong.Exit(func(int) {}))...)
	require.NoError(t, err)
	_, err = bad.Parse([]string{"auth", "login", "youtube"})
	assert.Error(t, err, "an unknown platform is a usage error")
}

// TestAuthLogin covers the login end to end: the prompt, the result line,
// and the account and the token in the store and the vault. The subtests
// run in order: the later ones build on the app stored by the first.
func TestAuthLogin(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	env, dataDir := authEnv(ctx, t)
	allScopes := strings.Join(auth.Scopes, " ")
	app := auth.Credentials{ID: "app-1", Secret: "secret-1"}

	readAccount := func(t *testing.T, role string) store.Account {
		t.Helper()
		ro, err := store.OpenReadOnly(ctx, profile.NewManager(dataDir).Path("demo"))
		require.NoError(t, err)
		defer ro.Close()
		list, err := ro.Accounts(ctx)
		require.NoError(t, err)
		for _, a := range list {
			if a.Role == role {
				return a
			}
		}
		t.Fatalf("no account with role %q", role)
		return store.Account{}
	}

	t.Run("streamer by the authorization code flow", func(t *testing.T) {
		flow := newFakeLoginFlow(allScopes)
		stdout, err := login(ctx, t, env, flow, false, app)
		require.NoError(t, err)
		// The prompt as text for the user.
		assert.Contains(t, stdout.String(),
			"twitch: open https://id.twitch.tv/oauth2/authorize and authorize the app\n(the login expires in 10m)\n")
		assert.Contains(t, stdout.String(), "twitch: logged in as streamy (streamer)\n")
		// The login flow is made with the BYO app, start and wait alike.
		require.Equal(t, []auth.Credentials{app, app}, flow.credsList())

		acc := readAccount(t, "streamer")
		assert.Equal(t, "streamy", acc.Login)
		assert.Equal(t, "u-1", acc.UserID)
		assert.Equal(t, allScopes, acc.Scopes)
		assert.Equal(t, "app-1", acc.ClientID, "the BYO client ID is stored with the account")
		assert.Equal(t, auth.FlowAuthorizationCode, acc.Flow, "the flow column records the code flow")

		ro, err := store.OpenReadOnly(ctx, profile.NewManager(dataDir).Path("demo"))
		require.NoError(t, err)
		defer ro.Close()
		_, found, err := ro.GetSecret(ctx, "auth/twitch/streamer")
		require.NoError(t, err)
		assert.True(t, found, "the token is stored in the vault")
		_, found, err = ro.GetSecret(ctx, "auth/twitch/client")
		require.NoError(t, err)
		assert.True(t, found, "the app credentials are stored in the vault")
	})

	t.Run("without flags the app of the last login is reused", func(t *testing.T) {
		flow := newFakeLoginFlow(allScopes)
		flow.login = "reuse"
		stdout, err := login(ctx, t, env, flow, false, auth.Credentials{})
		require.NoError(t, err)
		assert.Contains(t, stdout.String(), "twitch: logged in as reuse (streamer)\n")
		// The service resolved the stored app for the revoke of the
		// previous login, the start, and the wait alike.
		require.Equal(t, []auth.Credentials{app, app, app}, flow.credsList())
	})

	t.Run("bot", func(t *testing.T) {
		flow := newFakeLoginFlow(allScopes)
		stdout, err := login(ctx, t, env, flow, true, auth.Credentials{})
		require.NoError(t, err)
		assert.Contains(t, stdout.String(), "twitch: logged in as streamy (bot)\n")
		ro, err := store.OpenReadOnly(ctx, profile.NewManager(dataDir).Path("demo"))
		require.NoError(t, err)
		defer ro.Close()
		list, err := ro.Accounts(ctx)
		require.NoError(t, err)
		assert.Len(t, list, 2, "the bot joins the streamer account")
	})

	t.Run("re-login replaces the account", func(t *testing.T) {
		flow := newFakeLoginFlow(allScopes)
		flow.login = "other"
		other := auth.Credentials{ID: "app-2", Secret: "secret-2"}
		stdout, err := login(ctx, t, env, flow, false, other)
		require.NoError(t, err)
		assert.Contains(t, stdout.String(), "twitch: logged in as other (streamer)\n")
		assert.Equal(t, []string{"rt-1"}, flow.revokedList(), "the previous login is revoked")
		assert.Equal(t, []auth.Credentials{other, other, app}, flow.credsList(),
			"the start and the wait use the given app, the revoke the app of the previous login")
		acc := readAccount(t, "streamer")
		assert.Equal(t, "other", acc.Login, "the re-login updates the account")
		assert.Equal(t, "app-2", acc.ClientID, "the re-login stores its app")
	})
}

// TestAuthLoginErrors covers the command line errors of the login.
func TestAuthLoginErrors(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	t.Run("the authorization code flow without an app fails", func(t *testing.T) {
		t.Parallel()
		env, _ := authEnv(ctx, t)
		flow := newFakeLoginFlow(strings.Join(auth.Scopes, " "))
		env.Stdout, env.Stderr = &syncedLog{}, &syncedLog{}
		err := authLoginCmd{Platform: "twitch", flows: flow.flows()}.Run(ctx, env)
		assert.ErrorContains(t, err, "no app for the login")
	})

	t.Run("the client ID without the secret fails", func(t *testing.T) {
		t.Parallel()
		env, _ := authEnv(ctx, t)
		flow := newFakeLoginFlow(strings.Join(auth.Scopes, " "))
		env.Stdout, env.Stderr = &syncedLog{}, &syncedLog{}
		err := authLoginCmd{Platform: "twitch", ClientID: "app-x", flows: flow.flows()}.Run(ctx, env)
		assert.ErrorContains(t, err, "given together")
	})
}

// TestAuthLoginDeviceFlow covers the fallback of the device code flow
// (ADR-0023): the prompt shows the code, the project app is used, and no
// app credentials are stored.
func TestAuthLoginDeviceFlow(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	env, dataDir := authEnv(ctx, t)

	flow := newFakeLoginFlow(strings.Join(auth.Scopes, " "))
	flow.code = "ABCD-EFGH"
	stdout, err := login(ctx, t, env, flow, true, auth.Credentials{DeviceFlow: true})
	require.NoError(t, err)
	assert.Contains(t, stdout.String(),
		"twitch: open https://id.twitch.tv/oauth2/authorize and enter the code ABCD-EFGH\n(the code expires in 10m)\n")
	assert.Contains(t, stdout.String(), "twitch: logged in as streamy (bot)\n")

	ro, err := store.OpenReadOnly(ctx, profile.NewManager(dataDir).Path("demo"))
	require.NoError(t, err)
	defer ro.Close()
	list, err := ro.Accounts(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, auth.FlowDeviceCode, list[0].Flow, "the flow column records the device code flow")
	assert.Equal(t, auth.ClientID, list[0].ClientID, "the project app is used by default")
	_, found, err := ro.GetSecret(ctx, "auth/twitch/client")
	require.NoError(t, err)
	assert.False(t, found, "a device code login stores no app credentials")
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
	cmd := authLoginCmd{Platform: "twitch", ClientID: "app-1", ClientSecret: "secret-1", flows: flow.flows()}
	done := make(chan error, 1)
	go func() { done <- cmd.Run(abortCtx, env) }()
	require.Eventually(t, func() bool {
		return stdout.Contains("authorize the app")
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
	byo := auth.Credentials{ID: "app-1", Secret: "secret-1"}

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
	_, err := login(ctx, t, env, flow, false, byo)
	require.NoError(t, err)
	botFlow := newFakeLoginFlow("chat:read user:read:chat")
	_, err = login(ctx, t, env, botFlow, true, auth.Credentials{})
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
		Scopes: strings.Join(auth.Scopes, " "), ClientID: auth.ClientID, Flow: auth.FlowDeviceCode,
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

// TestAuthLogout covers the logout: the refresh token is revoked with the
// client of the account row, the account and the token are removed, the app
// credentials stay, and a failed revoke keeps them.
func TestAuthLogout(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	env, dataDir := authEnv(ctx, t)
	app := auth.Credentials{ID: "app-1", Secret: "secret-1"}

	t.Run("no account", func(t *testing.T) {
		env.Stdout, env.Stderr = &bytes.Buffer{}, &bytes.Buffer{}
		err := authLogoutCmd{Platform: "twitch"}.Run(ctx, env)
		assert.ErrorContains(t, err, "no account")
	})

	flow := newFakeLoginFlow(strings.Join(auth.Scopes, " "))
	_, err := login(ctx, t, env, flow, false, app)
	require.NoError(t, err)

	t.Run("removes the account, keeps the app credentials", func(t *testing.T) {
		stdout := &bytes.Buffer{}
		env.Stdout, env.Stderr = stdout, &bytes.Buffer{}
		err := authLogoutCmd{Platform: "twitch", flows: flow.flows()}.Run(ctx, env)
		require.NoError(t, err)
		assert.Equal(t, "twitch: removed streamy (streamer)\n", stdout.String())
		assert.Equal(t, []string{"rt-1"}, flow.revokedList(), "the refresh token is revoked")
		// The revoke is made with the stored app (ADR-0023).
		creds := flow.credsList()
		require.NotEmpty(t, creds)
		assert.Equal(t, app, creds[len(creds)-1], "the stored client ID and secret are used")

		ro, err := store.OpenReadOnly(ctx, profile.NewManager(dataDir).Path("demo"))
		require.NoError(t, err)
		defer ro.Close()
		list, err := ro.Accounts(ctx)
		require.NoError(t, err)
		assert.Empty(t, list)
		_, found, err := ro.GetSecret(ctx, "auth/twitch/streamer")
		require.NoError(t, err)
		assert.False(t, found)
		_, found, err = ro.GetSecret(ctx, "auth/twitch/client")
		require.NoError(t, err)
		assert.True(t, found, "the app credentials stay for the next login")
	})

	t.Run("revoke error keeps the account", func(t *testing.T) {
		env2, dataDir2 := authEnv(ctx, t)
		flow2 := newFakeLoginFlow(strings.Join(auth.Scopes, " "))
		flow2.setRevokeErr(errors.New("revoke broken"))
		_, err := login(ctx, t, env2, flow2, false, app)
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
