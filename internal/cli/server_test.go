// SPDX-License-Identifier: MIT

package cli

import (
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
	streamcrewv1alpha1connect "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1/streamcrewv1alpha1connect"
	"github.com/ripmav/streamcrew/internal/config"
)

// TestServerResolution covers the code review finding S6: without --server
// the API commands resolve the endpoint from the runtime file of the data
// directory (ADR-0010 §4), including the unix socket, and fall back to the
// local default without a runtime file.
func TestServerResolution(t *testing.T) {
	t.Parallel()
	base, dir := startMockCore(t)

	// A running core: the runtime file's address is the endpoint.
	env := &Env{Config: &config.Config{DataDir: dir}}
	assert.Equal(t, base, env.server())

	// --server wins over the runtime file.
	env = &Env{Config: &config.Config{DataDir: dir}, Server: "http://127.0.0.1:1"}
	assert.Equal(t, "http://127.0.0.1:1", env.server())

	// No runtime file: the local default.
	env = &Env{Config: &config.Config{DataDir: t.TempDir()}}
	assert.Equal(t, "http://127.0.0.1:8740", env.server())

	// The endpoint works end to end without --server (token from the local
	// token file, address from the runtime file).
	env = &Env{Config: &config.Config{DataDir: dir}}
	cl := streamcrewv1alpha1connect.NewCounterServiceClient(env.connectClient())
	_, err := cl.List(t.Context(), &v1alpha1.ListCountersRequest{})
	require.NoError(t, err)
}

// TestUnixClientDialsTheSocket covers the unix socket of the runtime file:
// a unix: address gets a unix dialer and a loopback-free base URL.
func TestUnixClientDialsTheSocket(t *testing.T) {
	t.Parallel()
	sock := filepath.Join(t.TempDir(), "core.sock")
	var lc net.ListenConfig
	l, err := lc.Listen(t.Context(), "unix", sock)
	require.NoError(t, err)
	srv := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})}
	t.Cleanup(func() { _ = srv.Close() })
	go func() { _ = srv.Serve(l) }()

	client, base := httpClientFor("unix:" + sock)
	assert.Equal(t, "http://core", base)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/ping", nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	t.Cleanup(func() { _ = resp.Body.Close() })
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "ok", string(body))

	// A plain address keeps the default client.
	plain, base := httpClientFor("http://127.0.0.1:8740")
	assert.Equal(t, http.DefaultClient, plain)
	assert.Equal(t, "http://127.0.0.1:8740", base)
}
