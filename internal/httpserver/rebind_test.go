// SPDX-License-Identifier: MIT

package httpserver_test

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/httpserver"
)

func getURL(t *testing.T, url string) (int, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	if _, err := io.ReadAll(resp.Body); err != nil {
		_ = resp.Body.Close()
		return 0, err
	}
	return resp.StatusCode, resp.Body.Close()
}

func startServer(t *testing.T, cfg httpserver.Config) *httpserver.Server {
	t.Helper()
	srv := httpserver.New(cfg, nil, nil)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	errc := make(chan error, 1)
	go func() { errc <- srv.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-errc)
	})
	<-srv.Listening()
	return srv
}

// TestRebindMovesToANewPort verifies the server moves to a new address and
// the old binding is shut down (roadmap 6.5).
func TestRebindMovesToANewPort(t *testing.T) {
	t.Parallel()
	srv := startServer(t, httpserver.Config{Addr: "127.0.0.1:0"})
	oldAddr := srv.Addr().String()

	err := srv.Rebind(t.Context(), httpserver.Config{Addr: "127.0.0.1:0"})
	require.NoError(t, err)
	newAddr := srv.Addr().String()
	assert.NotEqual(t, oldAddr, newAddr)

	code, err := getURL(t, "http://"+newAddr+"/healthz")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)

	// The old binding does not serve any more.
	_, err = getURL(t, "http://"+oldAddr+"/healthz")
	assert.Error(t, err)
}

// TestRebindKeepsTheOldBindingOnError verifies a failed rebind leaves the
// old binding active (roadmap 6.5).
func TestRebindKeepsTheOldBindingOnError(t *testing.T) {
	t.Parallel()
	srv := startServer(t, httpserver.Config{Addr: "127.0.0.1:0"})
	addr := srv.Addr().String()

	// Rebinding to the address already in use fails: the old binding must
	// still serve.
	err := srv.Rebind(t.Context(), httpserver.Config{Addr: addr})
	require.Error(t, err)

	code, err := getURL(t, "http://"+addr+"/healthz")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
}

// TestRebindKeepsTheOldBindingOnABrokenKeyPair verifies a rebind with a
// broken TLS key pair leaves the old binding active (roadmap 6.5).
func TestRebindKeepsTheOldBindingOnABrokenKeyPair(t *testing.T) {
	t.Parallel()
	srv := startServer(t, httpserver.Config{Addr: "127.0.0.1:0"})
	addr := srv.Addr().String()

	err := srv.Rebind(t.Context(), httpserver.Config{
		Addr: "127.0.0.1:0", TLSCert: "no-such-cert", TLSKey: "no-such-key",
	})
	require.Error(t, err)

	code, err := getURL(t, "http://"+addr+"/healthz")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
}

// TestSetDevTogglesThePprofRoutes verifies the developer mode can be
// switched at runtime (roadmap 6.5).
func TestSetDevTogglesThePprofRoutes(t *testing.T) {
	t.Parallel()
	srv := startServer(t, httpserver.Config{Addr: "127.0.0.1:0"})

	get := func() int {
		code, err := getURL(t, "http://"+srv.Addr().String()+"/debug/pprof/")
		if err != nil {
			t.Fatal(err)
		}
		return code
	}

	srv.SetDev(true)
	assert.Equal(t, http.StatusOK, get())
	srv.SetDev(false)
	assert.Equal(t, http.StatusNotFound, get())
}

// TestSetCorsAllowSwapsTheAllowlist verifies the CORS allowlist can be
// changed at runtime (roadmap 6.5).
func TestSetCorsAllowSwapsTheAllowlist(t *testing.T) {
	t.Parallel()
	srv := startServer(t, httpserver.Config{Addr: "127.0.0.1:0"})
	addr := "http://" + srv.Addr().String() + "/healthz"

	cross := func(origin string) int {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, addr, nil)
		require.NoError(t, err)
		req.Header.Set("Origin", origin)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_, err = io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		return resp.StatusCode
	}

	assert.Equal(t, http.StatusForbidden, cross("http://example.com"))
	srv.SetCorsAllow([]string{"http://example.com"})
	assert.Equal(t, http.StatusOK, cross("http://example.com"))
	srv.SetCorsAllow(nil)
	assert.Equal(t, http.StatusForbidden, cross("http://example.com"))
}

// TestRebindServesTLS switches a plain server to TLS with a rebind
// (roadmap 6.5): after the rebind the new address speaks TLS.
func TestRebindServesTLS(t *testing.T) {
	t.Parallel()
	certFile, keyFile := selfSignedCert(t)
	srv := startServer(t, httpserver.Config{Addr: "127.0.0.1:0"})
	oldAddr := srv.Addr().String()

	require.NoError(t, srv.Rebind(t.Context(), httpserver.Config{
		Addr: "127.0.0.1:0", TLSCert: certFile, TLSKey: keyFile,
	}))
	newAddr := srv.Addr().String()

	// The TLS client gets the health endpoint.
	// The certificate is the self-signed one of the test.
	//nolint:gosec // self-signed test certificate
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://"+newAddr+"/healthz", nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	_, err = io.ReadAll(resp.Body)
	require.NoError(t, resp.Body.Close())
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// The old plain binding is gone.
	_, err = getURL(t, "http://"+oldAddr+"/healthz")
	assert.Error(t, err)
}

var _ = net.Conn(nil)
var _ = time.Second
