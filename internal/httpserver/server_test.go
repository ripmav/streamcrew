// SPDX-License-Identifier: MIT

package httpserver_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/httpserver"
	"github.com/ripmav/streamcrew/internal/supervisor"
)

func get(t *testing.T, h http.Handler, path string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
	return rec.Code, rec.Body.String()
}

func TestHealthEndpoints(t *testing.T) {
	t.Parallel()
	var ready atomic.Bool
	srv := httpserver.New(httpserver.Config{}, nil, ready.Load)

	code, body := get(t, srv.Handler(), "/healthz")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "ok\n", body)

	code, body = get(t, srv.Handler(), "/readyz")
	assert.Equal(t, http.StatusServiceUnavailable, code)
	assert.Equal(t, "not ready\n", body)

	ready.Store(true)
	code, body = get(t, srv.Handler(), "/readyz")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "ready\n", body)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/healthz", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestPprofOnlyInDevMode(t *testing.T) {
	t.Parallel()
	code, _ := get(t, httpserver.New(httpserver.Config{}, nil, nil).Handler(), "/debug/pprof/")
	assert.Equal(t, http.StatusNotFound, code)

	code, body := get(t, httpserver.New(httpserver.Config{Dev: true}, nil, nil).Handler(), "/debug/pprof/")
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "goroutine")
}

func TestRunServesAndShutsDown(t *testing.T) {
	t.Parallel()
	srv := httpserver.New(httpserver.Config{Addr: "127.0.0.1:0", ShutdownTimeout: 5 * time.Second}, nil, nil)
	ctx, cancel := context.WithCancel(t.Context())
	errc := make(chan error, 1)
	go func() { errc <- srv.Run(ctx) }()

	select {
	case <-srv.Listening():
	case err := <-errc:
		t.Fatalf("Run returned early: %v", err)
	}
	addr, ok := srv.Addr()
	require.True(t, ok)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr.String()+"/healthz", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "ok\n", string(body))

	cancel()
	require.NoError(t, <-errc)
}

func TestRunListenErrorIsPermanent(t *testing.T) {
	t.Parallel()
	var lc net.ListenConfig
	busy, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer busy.Close()

	srv := httpserver.New(httpserver.Config{Addr: busy.Addr().String(), ShutdownTimeout: time.Second}, nil, nil)
	err = srv.Run(t.Context())
	require.Error(t, err)
	assert.True(t, supervisor.IsPermanent(err))
	assert.ErrorContains(t, err, "listen on")
	_, ok := srv.Addr()
	assert.False(t, ok)
}

// TestRunClosesIdleNewConnections is a regression test: a connection on
// which the client never sends a request (like a browser preconnect or a
// connection the HTTP client dialed in reserve) kept net/http's graceful
// shutdown waiting for 5 s, longer than the shutdown timeout of the core.
func TestRunClosesIdleNewConnections(t *testing.T) {
	t.Parallel()
	srv := httpserver.New(httpserver.Config{Addr: "127.0.0.1:0", ShutdownTimeout: 200 * time.Millisecond}, nil, nil)
	ctx, cancel := context.WithCancel(t.Context())
	errc := make(chan error, 1)
	go func() { errc <- srv.Run(ctx) }()
	<-srv.Listening()
	addr, ok := srv.Addr()
	require.True(t, ok)

	var d net.Dialer
	conn, err := d.DialContext(t.Context(), "tcp", addr.String())
	require.NoError(t, err)
	defer conn.Close()

	begin := time.Now()
	cancel()
	require.NoError(t, <-errc)
	assert.Less(t, time.Since(begin), 2*time.Second, "stops after the shutdown timeout, not after net/http's 5 s")
}

// TestRunServesTLS covers the TLS mode (roadmap 6.2.3): with a certificate
// pair the server speaks TLS and answers over https.
func TestRunServesTLS(t *testing.T) {
	t.Parallel()
	certFile, keyFile := selfSignedCert(t)
	srv := httpserver.New(httpserver.Config{
		Addr:            "127.0.0.1:0",
		ShutdownTimeout: 5 * time.Second,
		TLSCert:         certFile,
		TLSKey:          keyFile,
	}, nil, nil)
	ctx, cancel := context.WithCancel(t.Context())
	errc := make(chan error, 1)
	go func() { errc <- srv.Run(ctx) }()

	select {
	case <-srv.Listening():
	case err := <-errc:
		t.Fatalf("Run returned early: %v", err)
	}
	addr, ok := srv.Addr()
	require.True(t, ok)
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // self-signed test certificate
	}}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://"+addr.String()+"/healthz", nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "ok\n", string(body))

	cancel()
	require.NoError(t, <-errc)
}

// TestRunRejectsABrokenKeyPair covers that an unreadable or mismatching key
// pair stops the server before it serves.
func TestRunRejectsABrokenKeyPair(t *testing.T) {
	t.Parallel()
	certFile, _ := selfSignedCert(t)
	srv := httpserver.New(httpserver.Config{
		Addr:            "127.0.0.1:0",
		ShutdownTimeout: time.Second,
		TLSCert:         certFile,
		TLSKey:          certFile, // not the key
	}, nil, nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- srv.Run(ctx) }()
	err := <-errc
	require.Error(t, err)
	assert.True(t, supervisor.IsPermanent(err))
}

// TestRunServesOnAUnixSocket covers the desktop default (ADR-0005): the
// server listens on a Unix socket, replaces a stale socket file and answers
// the health endpoint.
func TestRunServesOnAUnixSocket(t *testing.T) {
	t.Parallel()
	sock := filepath.Join(t.TempDir(), "core.sock")
	var lc net.ListenConfig
	stale, err := lc.Listen(t.Context(), "unix", sock)
	require.NoError(t, err)
	require.NoError(t, stale.Close())

	srv := httpserver.New(httpserver.Config{
		Addr:            "unix:" + sock,
		ShutdownTimeout: 5 * time.Second,
	}, nil, nil)
	ctx, cancel := context.WithCancel(t.Context())
	errc := make(chan error, 1)
	go func() { errc <- srv.Run(ctx) }()

	select {
	case <-srv.Listening():
	case err := <-errc:
		t.Fatalf("Run returned early: %v", err)
	}
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
	}}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://core/healthz", nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "ok\n", string(body))

	cancel()
	require.NoError(t, <-errc)
}

// TestRunRejectsAFileAtTheUnixSocketPath covers that a regular file at the
// socket path stops the server instead of being deleted.
func TestRunRejectsAFileAtTheUnixSocketPath(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "core.sock")
	require.NoError(t, os.WriteFile(path, []byte("not a socket"), 0o600))
	srv := httpserver.New(httpserver.Config{
		Addr:            "unix:" + path,
		ShutdownTimeout: time.Second,
	}, nil, nil)
	err := srv.Run(t.Context())
	require.Error(t, err)
	assert.ErrorContains(t, err, "exists and is not a socket")
}

// selfSignedCert writes a self-signed certificate for 127.0.0.1 to the test
// directory and returns the file paths.
func selfSignedCert(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "streamcrew test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	dir := t.TempDir()
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")
	require.NoError(t, os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600))
	return certFile, keyFile
}

// TestAddrReportsOnceListening covers the contract that Addr reports the
// address only once the server listens: the core is ready while the first
// listener is still coming up, and callers read the address right after
// readiness. While no listener is up it reports ok=false instead of
// blocking, so a failed listen cannot hang the caller.
func TestAddrReportsOnceListening(t *testing.T) {
	t.Parallel()
	srv := httpserver.New(httpserver.Config{Addr: "127.0.0.1:0", ShutdownTimeout: time.Second}, nil, nil)

	_, ok := srv.Addr()
	assert.False(t, ok)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err := make(chan error, 1)
	go func() { err <- srv.Run(ctx) }()
	require.Eventually(t, func() bool { _, ok := srv.Addr(); return ok }, 5*time.Second, 10*time.Millisecond)
	addr, _ := srv.Addr()
	assert.NotNil(t, addr)
	select {
	case e := <-err:
		t.Fatalf("Run returned early: %v", e)
	case <-time.After(50 * time.Millisecond):
	}
}
