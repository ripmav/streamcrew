// SPDX-License-Identifier: Apache-2.0

// Package netguard guards the connections the core opens for commands
// (ADR-0013, point 4; Code-ADR-0019, point 4): in server mode, web requests
// must not reach internal networks unless the allowlist opens them. Targets
// with fixed addresses, such as the platform adapters, do not go through
// it.
package netguard

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

// Allowlist holds the internal targets that web requests may reach in
// server mode (Code-ADR-0019, point 4): addresses, networks and host names.
// Public targets need no entry. The zero value opens nothing. An Allowlist
// does not change; the configuration swaps the whole list.
type Allowlist struct {
	// entries are the entries in their normal form, in the order written,
	// without duplicates.
	entries  []string
	prefixes []netip.Prefix
	hosts    []string
}

// ErrInvalidEntry is wrapped by the errors of ParseAllowlist.
var ErrInvalidEntry = errors.New("invalid allowlist entry")

// maxHostLen is the longest host name DNS allows.
const maxHostLen = 253

// ParseAllowlist returns the allowlist of entries: an IP address
// ("192.168.1.10"), a network in CIDR notation ("10.0.0.0/8") or a host name
// ("homeassistant", "nas.local"). Host names match exactly, regardless of
// case and of a trailing dot, without wildcards; ports are not part of an
// entry. The error names every invalid entry.
func ParseAllowlist(entries []string) (Allowlist, error) {
	var a Allowlist
	var errs []error
	for _, e := range entries {
		norm, err := a.add(e)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !slices.Contains(a.entries, norm) {
			a.entries = append(a.entries, norm)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return Allowlist{}, err
	}
	return a, nil
}

// add parses the entry e into a and returns its normal form.
func (a *Allowlist) add(e string) (string, error) {
	switch {
	case e == "":
		return "", fmt.Errorf("%w: empty entry", ErrInvalidEntry)
	case strings.Contains(e, "/"):
		p, err := netip.ParsePrefix(e)
		if err != nil {
			return "", fmt.Errorf("%w %q: not a network in CIDR notation", ErrInvalidEntry, e)
		}
		p = netip.PrefixFrom(p.Addr().Unmap(), unmappedBits(p)).Masked()
		a.prefixes = append(a.prefixes, p)
		return p.String(), nil
	}
	if addr, err := netip.ParseAddr(e); err == nil {
		if addr.Zone() != "" {
			return "", fmt.Errorf("%w %q: an address with a zone", ErrInvalidEntry, e)
		}
		addr = addr.Unmap()
		a.prefixes = append(a.prefixes, netip.PrefixFrom(addr, addr.BitLen()))
		return addr.String(), nil
	}
	host := strings.TrimSuffix(strings.ToLower(e), ".")
	if !validHost(host) {
		return "", fmt.Errorf("%w %q: not an address, a network or a host name", ErrInvalidEntry, e)
	}
	a.hosts = append(a.hosts, host)
	return host, nil
}

// unmappedBits returns the prefix length of p for its unmapped address: an
// IPv4 network written in IPv6 ("::ffff:10.0.0.0/104") counts as IPv4.
func unmappedBits(p netip.Prefix) int {
	if p.Addr().Is4In6() {
		return max(p.Bits()-96, 0)
	}
	return p.Bits()
}

// validHost reports whether host, in lowercase, is a host name: labels of
// letters, digits, "-" and "_", separated by dots. Underscores occur in the
// service names of container networks.
func validHost(host string) bool {
	if host == "" || len(host) > maxHostLen {
		return false
	}
	for label := range strings.SplitSeq(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '_' {
				return false
			}
		}
	}
	return true
}

// Entries returns the entries in their normal form, in the order written.
func (a Allowlist) Entries() []string {
	return slices.Clone(a.entries)
}

// Equal reports whether a and b have the same entries.
func (a Allowlist) Equal(b Allowlist) bool {
	return slices.Equal(a.entries, b.entries)
}

// Contains reports whether addr is one of the addresses or in one of the
// networks of a. IPv4 addresses in IPv6 count as IPv4.
func (a Allowlist) Contains(addr netip.Addr) bool {
	addr = addr.Unmap().WithZone("")
	return slices.ContainsFunc(a.prefixes, func(p netip.Prefix) bool { return p.Contains(addr) })
}

// HasHost reports whether the host name is in a, regardless of case and of
// a trailing dot.
func (a Allowlist) HasHost(host string) bool {
	return slices.Contains(a.hosts, strings.TrimSuffix(strings.ToLower(host), "."))
}
