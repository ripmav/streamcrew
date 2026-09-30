// SPDX-License-Identifier: Apache-2.0

// Package role is the role model of streamcrew (spec users-and-roles.md,
// B20 to B27): the roles a user has in a channel, their fixed ranking and
// the rule "meets the minimum role".
//
// Platform-specific roles belong to the level they rank at: the platform
// adapters map, for example, a Twitch partner to Creator and a Kick OG to
// VIP (B20).
package role

import (
	json "encoding/json/v2"
	"fmt"
	"strings"
)

// Role is a role of a user in a channel. Its value is a stable lowercase ID
// for the database, the API and commands as code; frontends translate it
// for display (A2).
type Role string

// The roles in ascending rank (B20).
const (
	// Banned is a user who is banned on the platform (B27).
	Banned Role = "banned"
	// User is every user who is not banned (B21).
	User Role = "user"
	// Creator is a Twitch affiliate or partner.
	Creator Role = "creator"
	// Follower follows the channel; on YouTube: a subscriber.
	Follower Role = "follower"
	// Regular is a regular viewer; streamcrew assigns it itself (B26).
	Regular Role = "regular"
	// VIP is a VIP; on Kick also an OG, on VPZone Plus, Founder and
	// Ambassador.
	VIP Role = "vip"
	// Subscriber is a subscriber; on YouTube: a member.
	Subscriber Role = "subscriber"
	// PlatformStaff is a Twitch global moderator or staff member.
	PlatformStaff Role = "platform_staff"
	// Moderator is a moderator of the channel.
	Moderator Role = "moderator"
	// Editor is a channel editor.
	Editor Role = "editor"
	// Streamer owns the account the channel is connected with (B25).
	Streamer Role = "streamer"
)

// All returns the roles in ascending rank.
func All() []Role {
	return []Role{Banned, User, Creator, Follower, Regular, VIP, Subscriber, PlatformStaff, Moderator, Editor, Streamer}
}

// Parse reads a role ID.
func Parse(s string) (Role, error) {
	r := Role(s)
	if r.Rank() == 0 {
		return "", fmt.Errorf("unknown role %q", s)
	}
	return r, nil
}

// Rank returns the position of r in the ranking, from 1 for Banned to 11
// for Streamer, or 0 for an unknown role.
func (r Role) Rank() int {
	switch r {
	case Banned:
		return 1
	case User:
		return 2
	case Creator:
		return 3
	case Follower:
		return 4
	case Regular:
		return 5
	case VIP:
		return 6
	case Subscriber:
		return 7
	case PlatformStaff:
		return 8
	case Moderator:
		return 9
	case Editor:
		return 10
	case Streamer:
		return 11
	default:
		return 0
	}
}

// Valid reports whether r is a known role.
func (r Role) Valid() bool {
	return r.Rank() > 0
}

// Set is a set of roles. The zero value is the empty set.
type Set uint16

// NewSet returns the set of the given roles; unknown roles are left out.
func NewSet(roles ...Role) Set {
	var s Set
	for _, r := range roles {
		s = s.With(r)
	}
	return s
}

func bit(r Role) Set {
	if rank := r.Rank(); rank > 0 {
		return 1 << (rank - 1)
	}
	return 0
}

// Has reports whether r is in the set.
func (s Set) Has(r Role) bool {
	b := bit(r)
	return b != 0 && s&b != 0
}

// With returns the set with r added; an unknown role leaves it unchanged.
func (s Set) With(r Role) Set {
	return s | bit(r)
}

// Without returns the set without r.
func (s Set) Without(r Role) Set {
	return s &^ bit(r)
}

// Union returns the roles that are in s or t (B24).
func (s Set) Union(t Set) Set {
	return s | t
}

// Roles returns the roles of the set in ascending rank.
func (s Set) Roles() []Role {
	var out []Role
	for _, r := range All() {
		if s.Has(r) {
			out = append(out, r)
		}
	}
	return out
}

// Primary returns the primary role: the highest role of the set (B22), or
// User for a set without roles (B21). A banned user's primary role is
// Banned, whatever else the set holds, because a banned user meets no
// minimum role (B27).
func (s Set) Primary() Role {
	if s.Has(Banned) {
		return Banned
	}
	primary := User
	for _, r := range s.Roles() {
		primary = r // ascending, so the last one is the highest
	}
	return primary
}

// Meets reports whether a user with these roles meets the minimum role min:
// the primary role ranks at least as high as min (B23). A banned user meets
// no minimum role, not even User (B27); an unknown minimum role is never
// met.
func (s Set) Meets(minimum Role) bool {
	primary := s.Primary()
	return primary != Banned && minimum.Valid() && primary.Rank() >= minimum.Rank()
}

// String returns the role IDs in ascending rank, separated by commas.
func (s Set) String() string {
	roles := s.Roles()
	ids := make([]string, len(roles))
	for i, r := range roles {
		ids[i] = string(r)
	}
	return strings.Join(ids, ",")
}

// MarshalJSON writes the set as an array of role IDs in ascending rank.
func (s Set) MarshalJSON() ([]byte, error) {
	roles := s.Roles()
	if roles == nil {
		roles = []Role{}
	}
	return json.Marshal(roles, json.Deterministic(true))
}

// UnmarshalJSON reads an array of role IDs; an unknown ID is an error.
func (s *Set) UnmarshalJSON(data []byte) error {
	var ids []string
	if err := json.Unmarshal(data, &ids); err != nil {
		return fmt.Errorf("roles: %w", err)
	}
	var set Set
	for _, v := range ids {
		r, err := Parse(v)
		if err != nil {
			return err
		}
		set = set.With(r)
	}
	*s = set
	return nil
}
