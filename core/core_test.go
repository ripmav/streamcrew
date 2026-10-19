// SPDX-License-Identifier: MIT

package core_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
	// A test secret key keeps the core away from the system keyring, which
	// is slow or blocked on machines without a working session bus.
	secretKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{42}, 32))
	go func() { errc <- core.Run(ctx, core.Options{DataDir: t.TempDir(), Listen: addr, SecretKey: secretKey}) }()

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
