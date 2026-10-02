// SPDX-License-Identifier: Apache-2.0

package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"syscall"
	"time"
)

// ErrBlocked is wrapped by the error of a connection to an internal network
// that the allowlist does not open (ADR-0013, point 4).
var ErrBlocked = errors.New("target in an internal network")

// dialTimeout limits the connection itself; the web request has its own
// limit for the whole request.
const dialTimeout = 10 * time.Second

// blockedNetworks returns the networks that are internal besides loopback,
// private, link-local, multicast and unspecified addresses
// (Code-ADR-0019, point 4): "this network" and the shared address space of
// carrier-grade NAT.
func blockedNetworks() []netip.Prefix {
	return []netip.Prefix{netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10")}
}

// Internal reports whether addr is in an internal network (Code-ADR-0019,
// point 4): loopback, private networks (RFC 1918, fc00::/7), link-local,
// including the metadata address of cloud providers 169.254.169.254,
// multicast, the unspecified address, 0.0.0.0/8 and 100.64.0.0/10. IPv4
// addresses in IPv6 count as IPv4.
func Internal(addr netip.Addr) bool {
	addr = addr.Unmap()
	return addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() || addr.IsInterfaceLocalMulticast() || addr.IsMulticast() ||
		addr.IsUnspecified() ||
		slices.ContainsFunc(blockedNetworks(), func(p netip.Prefix) bool { return p.Contains(addr) })
}

// Dialer connects to the targets of web requests (ADR-0013, point 4;
// Code-ADR-0019, point 4). With Protect, the server mode, it refuses
// addresses in internal networks unless the allowlist opens them by
// address, network or the requested host name. It checks the address it
// actually connects to, after the name was resolved, so that DNS rebinding
// does not help; this holds for every redirect, because each one connects
// anew. Without Protect it connects anywhere.
type Dialer struct {
	// Protect turns the protection on; the composition root sets it in
	// server mode.
	Protect bool
	// Allowlist returns the allowlist that applies now, e.g.
	// config.Live.Outbound; it is read at every connection, so that a
	// change of the configuration applies at once. It must not be nil with
	// Protect.
	Allowlist func() Allowlist
}

// DialContext connects to address on network, e.g. for http.Transport.
func (d Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	nd := net.Dialer{Timeout: dialTimeout}
	if d.Protect {
		nd.Control = func(_, addr string, _ syscall.RawConn) error { return d.check(host, addr) }
	}
	return nd.DialContext(ctx, network, address)
}

// check decides whether a connection for the requested host to the
// resolved address addr may be made.
func (d Dialer) check(host, addr string) error {
	ap, err := netip.ParseAddrPort(addr)
	if err != nil {
		return fmt.Errorf("%w: unknown address %q", ErrBlocked, addr)
	}
	ip := ap.Addr().Unmap().WithZone("")
	if !Internal(ip) {
		return nil
	}
	allow := d.Allowlist()
	if allow.Contains(ip) || allow.HasHost(host) {
		return nil
	}
	return fmt.Errorf("%w: %s (%s); the allowlist (--outbound-allow) can open it", ErrBlocked, host, ip)
}
