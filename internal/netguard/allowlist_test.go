// SPDX-License-Identifier: Apache-2.0

package netguard_test

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/netguard"
)

// TestParseAllowlist covers Code-ADR-0019, point 4: addresses, networks and
// host names, in their normal form.
func TestParseAllowlist(t *testing.T) {
	t.Parallel()
	a, err := netguard.ParseAllowlist([]string{
		"192.168.1.10", "10.0.0.0/8", "10.1.2.3/8", "fd00::/8", "::ffff:192.168.2.0/120",
		"HomeAssistant", "nas.local.", "my_service", "twitch.tv", "192.168.1.10",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"192.168.1.10", "10.0.0.0/8", "fd00::/8", "192.168.2.0/24",
		"homeassistant", "nas.local", "my_service", "twitch.tv",
	}, a.Entries(), "normal form, without duplicates")

	for addr, want := range map[string]bool{
		"192.168.1.10":        true,
		"192.168.1.11":        false,
		"10.200.0.1":          true,
		"11.0.0.1":            false,
		"fd12::1":             true,
		"fe80::1":             false,
		"::ffff:10.0.0.1":     true,
		"192.168.2.200":       true,
		"::ffff:192.168.1.10": true,
	} {
		assert.Equal(t, want, a.Contains(netip.MustParseAddr(addr)), addr)
	}
	for host, want := range map[string]bool{
		"homeassistant":  true,
		"HOMEASSISTANT.": true,
		"nas.local":      true,
		"x.nas.local":    false,
		"twitch.tv":      true,
		"192.168.1.10":   false,
	} {
		assert.Equal(t, want, a.HasHost(host), host)
	}
}

func TestParseAllowlistRejects(t *testing.T) {
	t.Parallel()
	for _, entry := range []string{
		"", "10.0.0.0/33", "10.0.0/8", "192.168.1.10:8080", "fe80::1%eth0", "*.local",
		"-bad.example", "bad-.example", "a..b", "http://nas.local", "nas.local/x",
		strings.Repeat("a", 64) + ".example", strings.Repeat("a.", 127) + "a",
	} {
		_, err := netguard.ParseAllowlist([]string{"nas.local", entry})
		require.ErrorIs(t, err, netguard.ErrInvalidEntry, entry)
	}

	_, err := netguard.ParseAllowlist([]string{"*", "10.0.0.0/99"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"*"`, "every invalid entry is named")
	assert.Contains(t, err.Error(), `"10.0.0.0/99"`)
}

func TestAllowlistZeroAndEqual(t *testing.T) {
	t.Parallel()
	var zero netguard.Allowlist
	assert.False(t, zero.Contains(netip.MustParseAddr("10.0.0.1")))
	assert.False(t, zero.HasHost("nas"))
	assert.Empty(t, zero.Entries())

	a, err := netguard.ParseAllowlist([]string{"nas", "10.0.0.0/8"})
	require.NoError(t, err)
	b, err := netguard.ParseAllowlist([]string{"NAS", "10.0.0.1/8"})
	require.NoError(t, err)
	c, err := netguard.ParseAllowlist([]string{"10.0.0.0/8", "nas"})
	require.NoError(t, err)
	assert.True(t, a.Equal(b))
	assert.False(t, a.Equal(c), "the order is part of the list")
	assert.False(t, a.Equal(zero))

	empty, err := netguard.ParseAllowlist(nil)
	require.NoError(t, err)
	assert.True(t, empty.Equal(zero))
}
