// SPDX-License-Identifier: MIT

package netguard_test

import (
	"net"
	"net/netip"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/netguard"
)

// TestInternal covers Code-ADR-0019, point 4: the networks that are
// blocked in server mode.
func TestInternal(t *testing.T) {
	t.Parallel()
	for addr, want := range map[string]bool{
		"127.0.0.1":       true,
		"::1":             true,
		"10.1.2.3":        true,
		"172.16.0.1":      true,
		"192.168.1.10":    true,
		"fd12::1":         true,
		"169.254.169.254": true,
		"fe80::1":         true,
		"224.0.0.1":       true,
		"ff02::1":         true,
		"0.0.0.0":         true,
		"::":              true,
		"0.1.2.3":         true,
		"100.64.0.1":      true,
		"100.127.255.255": true,
		"::ffff:10.0.0.1": true,
		"::ffff:8.8.8.8":  false,
		"8.8.8.8":         false,
		"100.128.0.1":     false,
		"2606:4700::1111": false,
		"172.32.0.1":      false,
	} {
		assert.Equal(t, want, netguard.Internal(netip.MustParseAddr(addr)), addr)
	}
}

// listen returns the address of a listener on loopback that accepts and
// closes connections until the test ends.
func listen(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	return l.Addr().String()
}

// TestDialer covers ADR-0013, point 4 and Code-ADR-0019, point 4: with
// Protect, connections to internal networks fail unless the allowlist
// opens them; without Protect, they go through. The allowlist is read at
// every connection.
func TestDialer(t *testing.T) {
	t.Parallel()
	addr := listen(t)
	_, port, err := net.SplitHostPort(addr)
	require.NoError(t, err)

	var current atomic.Pointer[netguard.Allowlist]
	set := func(entries ...string) {
		a, err := netguard.ParseAllowlist(entries)
		require.NoError(t, err)
		current.Store(&a)
	}
	set()
	d := netguard.Dialer{Protect: true, Allowlist: func() netguard.Allowlist { return *current.Load() }}

	_, err = d.DialContext(t.Context(), "tcp", addr)
	require.ErrorIs(t, err, netguard.ErrBlocked)
	assert.Contains(t, err.Error(), "--outbound-allow")

	for _, entry := range []string{"127.0.0.1", "127.0.0.0/8", "localhost"} {
		set(entry)
		target := addr
		if entry == "localhost" {
			target = net.JoinHostPort("localhost", port)
		}
		c, err := d.DialContext(t.Context(), "tcp4", target)
		require.NoError(t, err, entry)
		require.NoError(t, c.Close())
	}

	set("nas")
	_, err = d.DialContext(t.Context(), "tcp4", net.JoinHostPort("localhost", port))
	require.ErrorIs(t, err, netguard.ErrBlocked, "a name resolving to loopback stays blocked")

	open := netguard.Dialer{}
	c, err := open.DialContext(t.Context(), "tcp", addr)
	require.NoError(t, err, "without protection, as in desktop and daemon mode")
	require.NoError(t, c.Close())

	_, err = d.DialContext(t.Context(), "tcp", "no-port")
	require.Error(t, err)
}
