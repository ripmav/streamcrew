// SPDX-License-Identifier: MIT

// Package twitchmock manages the official twitch-cli mock servers
// (mock API and mock EventSub WebSocket server) for Go tests
// (roadmap 4.5). It is a test helper: production code must not import
// it.
//
// The twitch binary is resolved from the PATH (name "twitch"); New
// returns an error if it is missing, and tests skip with it. CI
// installs the binary (see .github/workflows/ci.yml).
//
// Layout per test run:
//
//   - $HOME is a fresh temporary directory, so the mock API database
//     (eventCache.db) is created from scratch by "mock-api generate";
//   - the mock API runs on a free port (Helix endpoints under
//     "/mock/…", units unauthenticated under "/units/…");
//   - the mock EventSub WebSocket server runs on a free port
//     ("/ws" protocol, GET/POST/DELETE "/eventsub/subscriptions" REST
//     on the same port) plus its JSON-RPC control interface on the
//     fixed port 44747 (see rpc.go);
//   - the Helix router serves one base URL for the adapter's Helix
//     client: "/eventsub/subscriptions" goes to the WebSocket server
//     port, everything else to the mock API with the "/mock" prefix.
//
// The fixed RPC port 44747 allows exactly one WebSocket server per
// machine: close the Client before starting the next one. New takes
// an exclusive lock file (internal/lockfile) before it starts
// anything and holds it until Close, so a sibling package that runs
// in parallel (go test ./...) waits for the other environment
// instead of racing for the port; the operating system releases the
// lock when a test process dies, so there are no stale locks.
package twitchmock

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/ripmav/streamcrew/internal/config"
	"github.com/ripmav/streamcrew/internal/lockfile"
	"github.com/ripmav/streamcrew/internal/twitch"
)

// rpcPort is the fixed JSON-RPC control port of the mock EventSub
// WebSocket server (twitch-cli hardcodes it).
const rpcPort = 44747

// rpcPortWait is how long New waits for the rpc port lock of a
// parallel test package (its mock environment runs the integration
// and chaos tests, a couple of minutes at most).
const rpcPortWait = 4 * time.Minute

// rpcLockName is the exclusive lock file of the fixed rpc port;
// only one mock environment per machine may run at a time.
const rpcLockName = "twitchmock-rpc-44747.lock"

// acquireRPCLock takes the exclusive lock of the fixed rpc port;
// while a parallel test package holds it, it waits (up to
// rpcPortWait) and retries.
func acquireRPCLock(ctx context.Context) (*lockfile.Lock, error) {
	path := filepath.Join(os.TempDir(), rpcLockName)
	var locked *lockfile.LockedError
	deadline := time.Now().Add(rpcPortWait)
	for {
		l, err := lockfile.Acquire(path)
		if err == nil {
			return l, nil
		}
		if !errors.As(err, &locked) {
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("the other mock environment does not release the lock (%w)", locked)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

// Client is a started mock environment: both twitch-cli servers, the
// generated account data, and the Helix router. Close tears everything
// down (tests register it with t.Cleanup).
type Client struct {
	// ClientID is the application client ID generated for the run;
	// it goes into the Helix "Client-ID" header and is accepted by
	// the mock API.
	ClientID string
	// Secret is the application client secret of the generated run.
	Secret string
	// BroadcasterID is the user ID of the generated partner user
	// ("with all applicable units"); it is the broadcaster of the
	// conditions and the streamer account of the adapter.
	BroadcasterID string
	// BroadcasterLogin is the login of the partner user.
	BroadcasterLogin string
	// Token is the pre-created user token for the partner user; the
	// mock API accepts it with the ClientID.
	Token string

	// HelixBase is the base URL of the Helix router: it serves
	// "/eventsub/subscriptions" from the WebSocket server and
	// everything else from the mock API ("/mock" prefix).
	HelixBase string
	// WSBase is the EventSub WebSocket base of the mock server
	// (the value for the Base of the adapter's EventSub client).
	WSBase string
	// MockAPIBase is the base of the mock API with the "/mock"
	// prefix (direct access without the router).
	MockAPIBase string

	bin    string           // the twitch binary
	cmds   []*exec.Cmd      // the started processes (mock API, WebSocket server)
	server *httptest.Server // the Helix router
	home   string           // the temporary $HOME of the run
	lock   *lockfile.Lock   // the exclusive lock of the fixed rpc port
}

var (
	reClientID = regexp.MustCompile(`Client-ID:\s+(\S+)`)
	reSecret   = regexp.MustCompile(`Secret:\s+(\S+)`)
	reUserID   = regexp.MustCompile(`User ID (\d+) has all applicable units`)
	reToken    = regexp.MustCompile(`Created authorization with token (\S+)`)
)

// FindBinary resolves the twitch binary from the PATH.
func FindBinary() (string, error) {
	return exec.LookPath("twitch")
}

// New starts the mock environment. The context bounds the startup
// (generation and the health checks) and stays bound to the servers
// afterwards: a canceled context stops them, like Close. New takes
// the exclusive lock of the fixed rpc port (held until Close), so
// parallel test packages wait for each other.
func New(ctx context.Context) (*Client, error) {
	bin, err := FindBinary()
	if err != nil {
		return nil, fmt.Errorf("twitchmock: the twitch binary is unavailable (install twitch-cli to the PATH): %w", err)
	}
	lock, err := acquireRPCLock(ctx)
	if err != nil {
		return nil, fmt.Errorf("twitchmock: the fixed rpc port %d is not available (another mock environment is running): %w", rpcPort, err)
	}
	home, err := os.MkdirTemp("", "twitchmock-home")
	if err != nil {
		_ = lock.Release()
		return nil, err
	}
	c := &Client{home: home, bin: bin, lock: lock}
	defer func() {
		if err != nil {
			c.Close()
		}
	}()

	// Generate the database (partner user, client, pre-created
	// token) into the fresh $HOME.
	out, err := c.run(ctx, "mock-api", "generate", "-c", "2")
	if err != nil {
		return nil, fmt.Errorf("twitchmock: generate failed: %w\n%s", err, out)
	}
	m := reClientID.FindStringSubmatch(out)
	if m == nil {
		return nil, fmt.Errorf("twitchmock: no Client-ID in the generate output:\n%s", out)
	}
	c.ClientID = m[1]
	m = reSecret.FindStringSubmatch(out)
	if m == nil {
		return nil, fmt.Errorf("twitchmock: no Secret in the generate output:\n%s", out)
	}
	c.Secret = m[1]
	m = reUserID.FindStringSubmatch(out)
	if m == nil {
		return nil, fmt.Errorf("twitchmock: no partner user in the generate output:\n%s", out)
	}
	c.BroadcasterID = m[1]
	m = reToken.FindStringSubmatch(out)
	if m == nil {
		return nil, fmt.Errorf("twitchmock: no token in the generate output:\n%s", out)
	}
	c.Token = m[1]
	apiPort, err := freePort(ctx)
	if err != nil {
		return nil, err
	}
	wsPort, err := freePort(ctx)
	if err != nil {
		return nil, err
	}
	// The WebSocket server's RPC port is fixed (44747) and the
	// server logs it out silently when it is taken (stray twitch-cli
	// process from a crashed test run): check it up front, because a
	// second server would then accept no events at all. The lock
	// guarantees no other mock environment of the test run is up.
	l, err := (&net.ListenConfig{}).Listen(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", rpcPort))
	if err != nil {
		return nil, fmt.Errorf("twitchmock: the fixed rpc port %d is occupied (kill the stray twitch-cli process): %w", rpcPort, err)
	}
	_ = l.Close()
	c.MockAPIBase = fmt.Sprintf("http://127.0.0.1:%d/mock", apiPort)
	c.WSBase = fmt.Sprintf("ws://127.0.0.1:%d/ws", wsPort)

	if err := c.start(ctx, "mock-api", "start", "-p", strconv.Itoa(apiPort)); err != nil {
		return nil, err
	}
	if err := c.waitHTTP(ctx, fmt.Sprintf("http://127.0.0.1:%d/units/users", apiPort)); err != nil {
		return nil, fmt.Errorf("twitchmock: the mock api is not ready: %w", err)
	}
	if err := c.start(ctx, "event", "websocket", "start-server", "-p", strconv.Itoa(wsPort)); err != nil {
		return nil, err
	}
	if err := c.waitHTTP(ctx, fmt.Sprintf("http://127.0.0.1:%d/eventsub/subscriptions", wsPort)); err != nil {
		return nil, fmt.Errorf("twitchmock: the eventsub websocket server is not ready: %w", err)
	}
	if err := c.broadcasterLogin(ctx); err != nil {
		return nil, err
	}

	// The Helix router: "/eventsub/subscriptions" to the WebSocket
	// server, everything else to the mock API.
	proxyAPI := httputil.NewSingleHostReverseProxy(mustURL(c.MockAPIBase))
	proxyWS := httputil.NewSingleHostReverseProxy(mustURL(fmt.Sprintf("http://127.0.0.1:%d", wsPort)))
	c.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/eventsub/subscriptions" {
			proxyWS.ServeHTTP(w, r)
			return
		}
		r.URL.Path = "/mock" + r.URL.Path
		r.URL.RawPath = ""
		proxyAPI.ServeHTTP(w, r)
	}))
	c.HelixBase = c.server.URL
	return c, nil
}

// Close tears down the environment: both servers, the router, the
// temporary $HOME, and the rpc port lock. It is safe to call more
// than once.
func (c *Client) Close() {
	if c == nil {
		return
	}
	for _, cmd := range c.cmds {
		// The servers run until they are killed (no clean stop
		// command).
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}
	c.cmds = nil
	if c.server != nil {
		c.server.Close()
		c.server = nil
	}
	if c.home != "" {
		_ = os.RemoveAll(c.home)
		c.home = ""
	}
	if c.lock != nil {
		_ = c.lock.Release()
		c.lock = nil
	}
}

// Auth is the static twitch.Auth of the mock environment: the partner
// user as the streamer account, with the pre-created token and the
// generated client ID (roadmap 4.5, task 3).
type Auth struct {
	AccountID   string
	Login       string
	AccessToken string
	AppClientID string
}

// Streamer implements twitch.Auth.
func (a *Auth) Streamer(_ context.Context) (twitch.AccountState, error) {
	return twitch.AccountState{AccountID: a.AccountID, Login: a.Login, Ready: true}, nil
}

// Token implements twitch.Auth.
func (a *Auth) Token(_ context.Context) (string, error) { return a.AccessToken, nil }

// ClientID implements twitch.Auth.
func (a *Auth) ClientID(_ context.Context) (string, error) { return a.AppClientID, nil }

// Bot implements twitch.Auth: the test server has no bot account, so the
// additional channels stay out (twitch-multi-channel.md, B2).
func (a *Auth) Bot(_ context.Context) (twitch.AccountState, error) {
	return twitch.AccountState{}, nil
}

// BotToken implements twitch.Auth.
func (a *Auth) BotToken(_ context.Context) (string, error) {
	return "", errors.New("no bot account")
}

// start launches one CLI server process; it is bound to the context,
// so a canceled context also stops the servers.
func (c *Client) start(ctx context.Context, args ...string) error {
	cmd := c.command(ctx, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("twitchmock: start %v: %w", cmd.Args, err)
	}
	c.cmds = append(c.cmds, cmd)
	return nil
}

// command builds a CLI process with the temporary $HOME; the output
// goes to the null device, because the servers log to the console.
func (c *Client) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, c.bin, args...) //nolint:gosec // fixed subcommands; the ports come from freePort
	cmd.Env = append(config.ProgramEnv(), "HOME="+c.home)
	if devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0); err == nil {
		cmd.Stdout = devNull
		cmd.Stderr = devNull
	}
	return cmd
}

// run runs a CLI process and returns its combined output.
func (c *Client) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, c.bin, args...) //nolint:gosec // fixed subcommands; the ports come from freePort
	cmd.Env = append(config.ProgramEnv(), "HOME="+c.home)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// broadcasterLogin resolves the login of the partner user from the
// units endpoint (unauthenticated).
func (c *Client) broadcasterLogin(ctx context.Context) error {
	u, err := url.Parse(c.MockAPIBase)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		u.Scheme+"://"+u.Host+"/units/users", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var body struct {
		Data []struct {
			ID    string `json:"id"`
			Login string `json:"login"`
		} `json:"data"`
	}
	if err := jsonDecode(resp.Body, &body); err != nil {
		return err
	}
	for _, u := range body.Data {
		if u.ID == c.BroadcasterID {
			c.BroadcasterLogin = u.Login
			return nil
		}
	}
	return fmt.Errorf("twitchmock: the partner user %s is not in the units", c.BroadcasterID)
}

// waitHTTP polls the URL until it answers (any status) or the
// context ends.
func (c *Client) waitHTTP(ctx context.Context, target string) error {
	deadline := time.Now().Add(30 * time.Second)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			return nil
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// mustURL parses a URL that is built from known-valid parts.
func mustURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

// freePort reserves a free TCP port on 127.0.0.1.
func freePort(ctx context.Context) (int, error) {
	l, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port, nil
}
