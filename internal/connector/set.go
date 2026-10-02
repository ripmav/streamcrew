// SPDX-License-Identifier: Apache-2.0

package connector

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"github.com/ripmav/streamcrew/internal/domain/platform"
)

// Set holds the platforms of a profile, at most one per name, in a fixed
// order. It does not change after NewSet and is safe for concurrent use.
// The zero value is a set without platforms.
type Set struct {
	list []Platform
}

// NewSet returns the set of ps, in this order. It rejects nil, an invalid
// name and two platforms with the same name.
func NewSet(ps ...Platform) (*Set, error) {
	s := &Set{}
	for _, p := range ps {
		if p == nil {
			return nil, errors.New("platform set: nil platform")
		}
		name := p.Name()
		if err := name.Validate(); err != nil {
			return nil, fmt.Errorf("platform set: %w", err)
		}
		if _, dup := s.Platform(name); dup {
			return nil, fmt.Errorf("platform set: %s twice", name)
		}
		s.list = append(s.list, p)
	}
	return s, nil
}

// Platform returns the platform name; ok is false if the set has none.
func (s *Set) Platform(name platform.Name) (p Platform, ok bool) {
	for _, p := range s.list {
		if p.Name() == name {
			return p, true
		}
	}
	return nil, false
}

// Connected returns the platforms that are connected now (actions.md
// B62), in the order of the set; the list is empty if none is.
func (s *Set) Connected() []Platform {
	var out []Platform
	for _, p := range s.list {
		if p.Status().Connected() {
			out = append(out, p)
		}
	}
	return out
}

// DefaultFirst returns ps with the default platform first and the others
// in their order (platform.Default, actions.md B82); ps stays as it is.
func DefaultFirst(ps []Platform) []Platform {
	out := slices.Clone(ps)
	slices.SortStableFunc(out, func(a, b Platform) int {
		return cmp.Compare(rank(a), rank(b))
	})
	return out
}

// rank orders the default platform before the others.
func rank(p Platform) int {
	if p.Name() == platform.Default {
		return 0
	}
	return 1
}
