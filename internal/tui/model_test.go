// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/app"
	"github.com/ripmav/streamcrew/internal/config"
	"github.com/ripmav/streamcrew/internal/profile"
)

// startCore starts an in-process core on a free port and returns its base URL.
func startCore(t *testing.T) string {
	t.Helper()
	ctx := t.Context()
	dir := t.TempDir()
	cfg := config.Config{
		DataDir:         dir,
		Mode:            config.ModeDaemon,
		Listen:          "127.0.0.1:0",
		ShutdownTimeout: 5 * time.Second,
		Stream:          config.StreamConfig{Buffer: 1024, CatchUp: 256},
		Log:             config.LogConfig{Level: "info", Format: "text", MaxSize: 1, MaxFiles: 1},
	}
	require.NoError(t, cfg.Resolve())
	mgr := profile.NewManager(dir)
	_, err := mgr.Create(ctx, "Demo", "demo")
	require.NoError(t, err)
	cfg.Profile = "demo"

	a, err := app.New(ctx, cfg, app.WithConsole(io.Discard), app.WithKeyring(nil))
	require.NoError(t, err)
	runCtx, cancel := context.WithCancel(ctx)
	errc := make(chan error, 1)
	go func() { errc <- a.Run(runCtx) }()
	require.Eventually(t, a.Ready, 10*time.Second, 10*time.Millisecond, "the core to be ready")
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-errc)
	})
	return "http://" + mustServerAddr(t, a)
}

// sendKey sends a key press to the model and returns the result.
func sendKey(t *testing.T, m model, s string) (model, tea.Cmd) {
	t.Helper()
	msg := &keyMsg{code: rune(s[0]), text: s}
	next, cmd := m.Update(msg)
	return next.(model), cmd
}

// sendTab sends a tab press (no printable text).
func sendTab(t *testing.T, m model) (model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(&keyMsg{code: tea.KeyTab})
	return next.(model), cmd
}

// keyMsg is a test KeyMsg.
type keyMsg struct {
	code rune
	text string
}

func (k *keyMsg) String() string { return k.text }
func (k *keyMsg) Key() tea.Key   { return tea.Key{Code: k.code, Text: k.text} }

// TestKeyBindings covers the framework keys of the model.
func TestKeyBindings(t *testing.T) {
	t.Parallel()
	m := newModel(Options{Server: "http://127.0.0.1:1"})

	// theme toggle
	m, _ = sendKey(t, m, "t")
	assert.False(t, m.dark)
	m, _ = sendKey(t, m, "t")
	assert.True(t, m.dark)

	// help toggles and eats keys while open
	m, _ = sendKey(t, m, "?")
	assert.True(t, m.help)
	m, _ = sendKey(t, m, "1")
	assert.Equal(t, 0, m.view, "keys are eaten while the help is open")
	m, _ = sendKey(t, m, "q")
	assert.False(t, m.help, "q closes the help")

	// view switching
	m, _ = sendKey(t, m, "3")
	assert.Equal(t, 2, m.view)
	m, _ = sendKey(t, m, strconv.Itoa(len(views)))
	assert.Equal(t, len(views)-1, m.view)
	m, _ = sendTab(t, m)
	assert.Equal(t, 0, m.view, "tab wraps to the first view")
}

// TestQuit returns the quit command.
func TestQuit(t *testing.T) {
	t.Parallel()
	m := newModel(Options{Server: "http://127.0.0.1:1"})
	_, cmd := sendKey(t, m, "q")
	require.NotNil(t, cmd)
	assert.Equal(t, tea.QuitMsg{}, cmd())
}

// TestModelFetchStatus fetches the status of a running core.
func TestModelFetchStatus(t *testing.T) {
	t.Parallel()
	base := startCore(t)
	m := newModel(Options{Server: base})

	fetched := m.fetchStatus()
	require.IsType(t, statusMsg{}, fetched)
	next, cmd := m.Update(fetched)
	m = next.(model)
	require.NotNil(t, cmd, "the next poll is scheduled")

	view := m.View()
	assert.Contains(t, view.Content, "streamcrew")
	assert.Contains(t, view.Content, base)
	assert.Contains(t, view.Content, "Demo")
}

// TestModelConnectionError shows the error state and schedules a retry.
func TestModelConnectionError(t *testing.T) {
	t.Parallel()
	m := newModel(Options{Server: "http://127.0.0.1:1"})

	fetched := m.fetchStatus()
	require.IsType(t, statusErr{}, fetched)
	next, cmd := m.Update(fetched)
	m = next.(model)
	require.NotNil(t, cmd, "a retry is scheduled")

	view := m.View()
	assert.Contains(t, view.Content, "connection refused")
}

// TestServerResolution resolves the runtime file and the default.
func TestServerResolution(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// no runtime file: the default
	assert.Equal(t, defaultServer, server(dir, ""))

	// a runtime file with a TCP address
	require.NoError(t, os.WriteFile(filepath.Join(dir, "runtime.json"),
		[]byte(`{"address":"127.0.0.1:9999"}`), 0o600))
	assert.Equal(t, "http://127.0.0.1:9999", server(dir, ""))

	// a runtime file with a unix socket
	require.NoError(t, os.WriteFile(filepath.Join(dir, "runtime.json"),
		[]byte(`{"address":"unix:`+filepath.Join(dir, "core.sock")+`"}`), 0o600))
	assert.Equal(t, "unix:"+filepath.Join(dir, "core.sock"), server(dir, ""))

	// an explicit server wins
	assert.Equal(t, "http://example.org:1", server(dir, "http://example.org:1"))
}

// TestViewRendering renders the dashboard and the footer.
func TestViewRendering(t *testing.T) {
	t.Parallel()
	m := newModel(Options{Server: "http://127.0.0.1:1"})
	m.width, m.height = 100, 30

	view := m.View()
	assert.Contains(t, view.Content, "streamcrew")
	assert.Contains(t, view.Content, "connecting …")
	assert.Contains(t, view.Content, "q quit · ? help · t theme")
	assert.True(t, view.AltScreen)

	// the help renders the keys
	m.help = true
	help := m.View()
	assert.Contains(t, help.Content, "Help")
	assert.Contains(t, help.Content, "toggle theme")
}

// TestClientsBuildsAUnixClient returns the unix dialer for unix sockets.
func TestClientsBuildsAUnixClient(t *testing.T) {
	t.Parallel()
	client, base := httpClientFor("unix:/tmp/does-not-matter.sock")
	require.NotNil(t, client)
	assert.Equal(t, "http://core", base)
	client, base = httpClientFor("http://127.0.0.1:8740")
	require.NotNil(t, client)
	assert.Equal(t, "http://127.0.0.1:8740", base)
}

// TestThemeToggles renders dark and light.
func TestThemeToggles(t *testing.T) {
	t.Parallel()
	m := newModel(Options{Server: "http://127.0.0.1:1", Theme: "dark"})
	dark := m.View()
	m.dark = false
	light := m.View()
	assert.NotEqual(t, dark.Content, light.Content)
}

// TestRunQuits runs the whole program with piped input: a q key quits.
func TestRunQuits(t *testing.T) {
	t.Parallel()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	errc := make(chan error, 1)
	go func() {
		errc <- run(Options{Server: "http://127.0.0.1:1"}, []tea.ProgramOption{
			tea.WithInput(r),
			tea.WithOutput(io.Discard),
			tea.WithoutRenderer(),
		})
	}()
	// let the program start, then feed the quit key
	time.Sleep(200 * time.Millisecond)
	if _, err := w.Write([]byte("q")); err != nil {
		t.Log(err)
	}
	require.NoError(t, w.Close())
	require.NoError(t, <-errc)
}
