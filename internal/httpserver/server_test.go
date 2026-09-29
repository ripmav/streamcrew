// SPDX-License-Identifier: Apache-2.0

package httpserver_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
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
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+srv.Addr().String()+"/healthz", nil)
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
	assert.Nil(t, srv.Addr())
}
