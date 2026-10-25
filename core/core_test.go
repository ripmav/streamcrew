// SPDX-License-Identifier: MIT

package core_test

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
	streamcrewv1alpha1connect "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1/streamcrewv1alpha1connect"
	"github.com/ripmav/streamcrew/core"
)

// TestRunStartsAndStopsTheCore covers the public start API (ADR-0006):
// Run starts the core, which answers the health endpoint, and stops the
// core cleanly when the context ends.
func TestRunStartsAndStopsTheCore(t *testing.T) {
	t.Parallel()
	port := freePort(t)
	addr := "127.0.0.1:" + strconv.Itoa(port)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- core.Run(ctx, core.Options{DataDir: t.TempDir(), Listen: addr}) }()

	// The core comes up and answers the health endpoint.
	deadline := time.Now().Add(30 * time.Second)
	for {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+"/healthz", nil)
		require.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.NoError(t, resp.Body.Close())
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the core did not come up")
		}
		select {
		case err := <-errc:
			t.Fatalf("Run returned early: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
	}

	// Canceling the context stops the core cleanly.
	cancel()
	select {
	case err := <-errc:
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

// TestRunRejectsInvalidOptions covers that invalid options fail before the
// core starts.
func TestRunRejectsInvalidOptions(t *testing.T) {
	t.Parallel()
	err := core.Run(t.Context(), core.Options{DataDir: t.TempDir(), TLSCert: "/no/cert.pem"})
	require.Error(t, err)
}

// TestRunWithConfigFileWiresTheWatcher covers the C14 of the code review
// 2026-10-24: core.Run loads the configuration file, so the watcher
// watches it and ConfigService/Update accepts it; log-file is reported as
// restart-only (Code-ADR-0021, Nachtrag 2026-10-24, point 6).
func TestRunWithConfigFileWiresTheWatcher(t *testing.T) {
	t.Parallel()
	port := freePort(t)
	addr := "127.0.0.1:" + strconv.Itoa(port)
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(file, []byte("log_level: info\n"), 0o600))

	const token = "core-test-token"
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errc := make(chan error, 1)
	go func() {
		errc <- core.Run(ctx, core.Options{
			DataDir:        dir,
			Mode:           "server",
			Listen:         addr,
			BootstrapToken: token,
			ConfigFile:     file,
		})
	}()

	// The core comes up and answers the health endpoint.
	deadline := time.Now().Add(30 * time.Second)
	for {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+"/healthz", nil)
		require.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.NoError(t, resp.Body.Close())
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the core did not come up")
		}
		select {
		case err := <-errc:
			t.Fatalf("Run returned early: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
	}

	// Get reports the live document with log-file in the restart list.
	client := streamcrewv1alpha1connect.NewConfigServiceClient(connect.NewClient(
		connecthttp.NewTransport(http.DefaultClient, "http://"+addr),
		withBearer(token)))
	g, err := client.Get(t.Context(), &v1alpha1.GetConfigRequest{})
	require.NoError(t, err)
	assert.True(t, g.GetLive())
	assert.Contains(t, g.GetRestart(), "log-file")

	// Update accepts the document; the watcher applies it on its next
	// comparison.
	u, err := client.Update(t.Context(), &v1alpha1.UpdateConfigRequest{Document: "log_level: debug\n"})
	require.NoError(t, err)
	assert.Contains(t, u.GetApplied(), "log")

	// A document that extends the current configuration is denied.
	_, err = client.Update(t.Context(), &v1alpha1.UpdateConfigRequest{Document: "grant: [host:fs]\n"})
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}

// withBearer returns the client interceptor that sends the bearer token of
// value with every call.
func withBearer(value string) connect.ClientInterceptor {
	return func(next connect.ClientFunc) connect.ClientFunc {
		return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
			if info, ok := connect.CallInfoForClientContext(ctx); ok {
				info.RequestHeader().Set("Authorization", "Bearer "+value)
			}
			return next(ctx, spec)
		}
	}
}

// freePort returns a free TCP port on loopback.
func freePort(t *testing.T) int {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())
	return port
}
