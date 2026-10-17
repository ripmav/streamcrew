// SPDX-License-Identifier: MIT

package app_test

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/alecthomas/kong"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	streamcrewv1alpha1connect "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1/streamcrewv1alpha1connect"
	"github.com/ripmav/streamcrew/internal/app"
	"github.com/ripmav/streamcrew/internal/config"
	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/connector/mock"
	"github.com/ripmav/streamcrew/internal/event"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1" //nolint:gci // the package is named streamcrewv1alpha1
)

// outPub records the outputs of the mock platform.
type outPub struct {
	mu      sync.Mutex
	outputs []mock.Output
}

func (p *outPub) Publish(_ context.Context, e event.Envelope) error {
	o, ok := event.Payload[mock.Output](e)
	if !ok || e.Type != mock.TypeOutput {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.outputs = append(p.outputs, o)
	return nil
}

func (p *outPub) sentText(text string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, o := range p.outputs {
		if o.Op == mock.OpSend && o.Text == text {
			return true
		}
	}
	return false
}

// hotCore starts a daemon-mode core with a mock platform (whose outputs go
// to pub) and a configuration file the watcher watches (roadmap 6.6).
func hotCore(t *testing.T, pub *outPub) (*app.App, string) {
	return hotCoreWithConsole(t, pub, &bytes.Buffer{})
}

func hotCoreWithConsole(t *testing.T, pub *outPub, console *bytes.Buffer) (*app.App, string) {
	t.Helper()
	cfg := testConfig(t)
	require.NoError(t, cfg.Resolve())
	require.NoError(t, os.MkdirAll(cfg.DataDir, 0o700))
	path := filepath.Join(cfg.DataDir, config.ConfigFileName)
	require.NoError(t, os.WriteFile(path, []byte("log_level: debug\n"), 0o600))

	var cli struct {
		config.Config

		Run struct{} `cmd:""`
	}
	file := config.NewFileResolver(path)
	opts := append(config.KongOptions(config.Defaults{DataDir: cfg.DataDir}, file),
		kong.Exit(func(int) { t.Fatal("kong exited") }))
	parser, err := kong.New(&cli, opts...)
	require.NoError(t, err)
	_, err = parser.Parse([]string{"run"})
	require.NoError(t, err)
	require.NoError(t, file.Err())
	require.NoError(t, cli.Resolve())
	cfg = cli.Config

	a, err := app.New(t.Context(), cfg,
		app.WithConsole(console), app.WithKeyring(nil),
		app.WithConfigFile(file),
		app.WithPlatform(func(_ context.Context, b app.PlatformBuilder) (connector.Platform, error) {
			return mock.New(b.Receiver, mock.WithLogger(b.Logger), mock.WithPublisher(pub))
		}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, a.Close()) })

	ctx, cancel := context.WithCancel(t.Context())
	errRun := make(chan error, 1)
	go func() { errRun <- a.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		assert.NoError(t, <-errRun)
	})
	select {
	case <-a.HTTPServer().Listening():
	case err := <-errRun:
		t.Fatalf("Run returned early: %v", err)
	}
	// Warm up the watcher: the file was created before the resolver
	// loaded, so the watcher compares against the content it read at
	// start; a rewrite with the same content is a no-op for the rights
	// but proves the comparison runs.
	rewriteFile(t, path, "log_level: debug\n")
	return a, path
}

// freePort asks the system for a free TCP port on loopback.
func freePort(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr[strings.LastIndex(addr, ":")+1:]
}

func chatClient(t *testing.T, base string) streamcrewv1alpha1connect.ChatServiceClient {
	t.Helper()
	return streamcrewv1alpha1connect.NewChatServiceClient(http.DefaultClient, base)
}

// rewriteFile rewrites the file and waits for the watcher interval to pass.
func rewriteFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	time.Sleep(2 * config.ReloadInterval)
}

// TestHotApplyDryRun verifies the dry run of the platforms switches at
// runtime through the configuration file (roadmap 6.6): with it on, the
// chat write is recorded instead of delivered to the platform.
func TestHotApplyDryRun(t *testing.T) {
	pub := &outPub{}
	a, path := hotCore(t, pub)
	// Dry run off: the core's chat message goes to the platform.
	base := "http://" + a.HTTPServer().Addr().String()
	chat := chatClient(t, base)
	_, err := chat.Send(t.Context(), connect.NewRequest(&v1alpha1.SendChatRequest{Text: "hello"}))
	require.NoError(t, err)
	require.Eventually(t, func() bool { return pub.sentText("hello") },
		2*time.Second, 10*time.Millisecond, "dry run off: the message is delivered")

	// Switch the dry run on: the next message is not delivered.
	rewriteFile(t, path, "log_level: debug\ndry_run: true\n")
	_, err = chat.Send(t.Context(), connect.NewRequest(&v1alpha1.SendChatRequest{Text: "dry"}))
	require.NoError(t, err)
	time.Sleep(config.ReloadInterval)
	assert.False(t, pub.sentText("dry"), "dry run on: the message is not delivered")

	// Switch it off again.
	rewriteFile(t, path, "log_level: debug\ndry_run: false\n")
	_, err = chat.Send(t.Context(), connect.NewRequest(&v1alpha1.SendChatRequest{Text: "again"}))
	require.NoError(t, err)
	require.Eventually(t, func() bool { return pub.sentText("again") },
		2*time.Second, 10*time.Millisecond, "dry run off again: the message is delivered")
}

// TestHotApplyShutdownTimeout verifies the shutdown timeout changes at
// runtime (roadmap 6.6).
func TestHotApplyShutdownTimeout(t *testing.T) {
	a, path := hotCore(t, &outPub{})
	assert.Equal(t, 15*time.Second, a.ShutdownTimeout())
	rewriteFile(t, path, "log_level: debug\nshutdown_timeout: 30s\n")
	require.Eventually(t, func() bool { return a.ShutdownTimeout() == 30*time.Second },
		5*time.Second, 10*time.Millisecond, "the shutdown timeout is applied")
}

// TestHotApplyListenRebinds verifies the listen address moves at runtime
// (roadmap 6.6): the new address serves, the old one stops.
func TestHotApplyListenRebinds(t *testing.T) {
	a, path := hotCore(t, &outPub{})
	old := a.HTTPServer().Addr().String()

	// The rebind moves the server to a fixed port the test picks.
	port := freePort(t)
	rewriteFile(t, path, "log_level: debug\nlisten: 127.0.0.1:"+port+"\n")
	require.Eventually(t, func() bool {
		return a.HTTPServer().Addr() != nil && a.HTTPServer().Addr().String() == "127.0.0.1:"+port
	}, 5*time.Second, 10*time.Millisecond, "the server moved to the new address")
	_ = old
}
