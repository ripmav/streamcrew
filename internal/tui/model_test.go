// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	teatest "github.com/charmbracelet/x/exp/teatest/v2"
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
		BootstrapToken:  testToken,
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

// TestKeyBindings covers the framework keys of the model, driven through
// the real Bubble Tea program loop of the teatest harness.
func TestKeyBindings(t *testing.T) {
	t.Parallel()
	t.Run("theme", func(t *testing.T) {
		t.Parallel()
		tm := newTestModel(t, Options{Server: "http://127.0.0.1:1"})
		tm.Type("t")
		m := quitModel(t, tm)
		assert.False(t, m.dark, "t switches from the dark start to light")
	})
	t.Run("help", func(t *testing.T) {
		t.Parallel()
		tm := newTestModel(t, Options{Server: "http://127.0.0.1:1"})
		tm.Type("?")
		tm.Type("1")
		tm.Type("q")
		m := quitModel(t, tm)
		assert.False(t, m.help, "q closes the help")
		assert.Equal(t, 0, m.view, "keys are eaten while the help is open")
	})
	t.Run("views", func(t *testing.T) {
		t.Parallel()
		tm := newTestModel(t, Options{Server: "http://127.0.0.1:1"})
		tm.Type("3")
		tm.Type(strconv.Itoa(len(views)))
		tm.Send(tea.KeyPressMsg{Code: tea.KeyTab})
		m := quitModel(t, tm)
		assert.Equal(t, 0, m.view, "tab wraps from the last view to the first")
	})
}

// TestQuit quits the whole program through the real loop: the q key
// returns the quit command and the program ends.
func TestQuit(t *testing.T) {
	t.Parallel()
	tm := newTestModel(t, Options{Server: "http://127.0.0.1:1"})
	tm.Type("q")
	tm.WaitFinished(t, teatest.WithFinalTimeout(10*time.Second))
	_, ok := tm.FinalModel(t).(model)
	require.True(t, ok, "the program returned its final model")
}

// TestModelFetchStatus fetches the status of a running core and renders it.
func TestModelFetchStatus(t *testing.T) {
	t.Parallel()
	base := startCore(t)
	tm := newTestModel(t, Options{Server: base, Token: testToken})
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return strings.Contains(string(b), "Demo")
	}, teatest.WithDuration(10*time.Second))
	m := quitModel(t, tm)
	require.NotNil(t, m.status)
	assert.NoError(t, m.err)
	assert.Contains(t, m.View().Content, "streamcrew")
	assert.Contains(t, m.View().Content, base)
	assert.Contains(t, m.View().Content, "Demo")
}

// TestModelConnectionError shows the error state of an unreachable core.
func TestModelConnectionError(t *testing.T) {
	t.Parallel()
	tm := newTestModel(t, Options{Server: "http://127.0.0.1:1"})
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return strings.Contains(string(b), "connection refused")
	}, teatest.WithDuration(10*time.Second))
	m := quitModel(t, tm)
	require.ErrorContains(t, m.err, "connection refused")
	assert.Contains(t, m.View().Content, "connection refused")
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
	t.Run("initial", func(t *testing.T) {
		t.Parallel()
		m := newModel(t.Context(), Options{Server: "http://127.0.0.1:1"})
		m.width, m.height = 100, 30

		view := m.View()
		assert.Contains(t, view.Content, "streamcrew")
		assert.Contains(t, view.Content, "connecting …")
		assert.Contains(t, view.Content, "q quit · ? help · t theme")
		assert.True(t, view.AltScreen)
	})
	t.Run("rendered", func(t *testing.T) {
		t.Parallel()
		// the program renders through the real renderer; the final output
		// contains the header and the key row
		tm := newTestModel(t, Options{Server: "http://127.0.0.1:1"})
		_ = tm.Quit()
		tm.WaitFinished(t, teatest.WithFinalTimeout(10*time.Second))
		out, err := io.ReadAll(tm.FinalOutput(t))
		require.NoError(t, err)
		assert.Contains(t, string(out), "streamcrew")
		assert.Contains(t, string(out), "q quit · ? help · t theme")
	})
	t.Run("help", func(t *testing.T) {
		t.Parallel()
		tm := newTestModel(t, Options{Server: "http://127.0.0.1:1"})
		tm.Type("?")
		teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
			return strings.Contains(string(b), "toggle theme")
		}, teatest.WithDuration(10*time.Second))
		m := quitModel(t, tm)
		assert.True(t, m.help)
		assert.Contains(t, m.View().Content, "Help")
		assert.Contains(t, m.View().Content, "toggle theme")
	})
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
	m := newModel(t.Context(), Options{Server: "http://127.0.0.1:1", Theme: "dark"})
	dark := m.View()
	m.dark = false
	light := m.View()
	assert.NotEqual(t, dark.Content, light.Content)
}
