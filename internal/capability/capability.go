// SPDX-License-Identifier: MIT

// Package capability names what a function of the core may do beyond the
// core (ADR-0013): files, programs, input, sound on the host, requests to
// other hosts and scripts. Action types name the capabilities they need in
// their descriptor (Code-ADR-0013); the operating mode and the start
// configuration decide which ones the core has.
package capability

import (
	"errors"
	"fmt"
	"slices"
)

// Capability is a named right beyond the core (ADR-0013).
type Capability string

// The capabilities of ADR-0013.
const (
	// HostFS reads and writes files under released roots.
	HostFS Capability = "host:fs"
	// HostProcess starts programs.
	HostProcess Capability = "host:process"
	// HostInput sends keyboard and mouse input and listens to hotkeys.
	HostInput Capability = "host:input"
	// HostAudio plays sound on the host.
	HostAudio Capability = "host:audio"
	// NetOutbound sends requests to other hosts.
	NetOutbound Capability = "net:outbound"
	// Script runs scripts in a sandbox.
	Script Capability = "script"
)

// ErrUnknown is returned for a capability that is not one of All.
var ErrUnknown = errors.New("unknown capability")

// All returns every capability, in a fixed order.
func All() []Capability {
	return []Capability{HostFS, HostProcess, HostInput, HostAudio, NetOutbound, Script}
}

// Valid reports whether c is a known capability.
func (c Capability) Valid() bool {
	return slices.Contains(All(), c)
}

// Set is a set of capabilities, such as the ones the core has. Its zero
// value is the empty set.
type Set struct {
	caps map[Capability]struct{}
}

// NewSet returns the set of caps; an unknown capability is an error
// wrapping ErrUnknown.
func NewSet(caps ...Capability) (Set, error) {
	s := Set{caps: make(map[Capability]struct{}, len(caps))}
	for _, c := range caps {
		if !c.Valid() {
			return Set{}, fmt.Errorf("%w: %q", ErrUnknown, c)
		}
		s.caps[c] = struct{}{}
	}
	return s, nil
}

// Has reports whether s contains c.
func (s Set) Has(c Capability) bool {
	_, ok := s.caps[c]
	return ok
}

// Missing returns the capabilities of need that s does not contain, in the
// order of need; the list is empty if none is missing.
func (s Set) Missing(need []Capability) []Capability {
	missing := []Capability{}
	for _, c := range need {
		if !s.Has(c) && !slices.Contains(missing, c) {
			missing = append(missing, c)
		}
	}
	return missing
}

// List returns the capabilities in s in the order of All.
func (s Set) List() []Capability {
	list := []Capability{}
	for _, c := range All() {
		if s.Has(c) {
			list = append(list, c)
		}
	}
	return list
}
