// SPDX-License-Identifier: MIT

package user

import (
	"slices"
	"time"

	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/role"
)

// TitleRule is one of the streamer's title rules (users-and-roles.md, B5):
// a user on a platform whose highest role meets MinRole and whose follow
// or subscription is at least MinMonths old gets the title Name.
type TitleRule struct {
	// Name is the title, e.g. "Super Fan"; it is not empty.
	Name string `json:"name"`
	// MinRole is the minimum role, e.g. role.Follower.
	MinRole role.Role `json:"minRole"`
	// MinMonths is the whole months the follow or the subscription must be
	// old; 0 for none.
	MinMonths int `json:"minMonths"`
}

// DefaultTitle returns the first title rule matching the user on platform
// p at now (users-and-roles.md, B5), in the order of the highest minimum
// role first, then of the most minimum months, and false if none matches.
// A subscriber rule (minimum role subscriber or youtube_member) counts
// from the identity's subscription start, every other rule from its follow
// (decision, plan phase 5.2.3); a zero date counts as 0 months.
func (u User) DefaultTitle(p platform.Name, now time.Time, rules []TitleRule) (TitleRule, bool) {
	roles := u.Roles(p)
	var data PlatformData
	if i, ok := u.Identity(p); ok {
		data = i.Data
	}
	sorted := slices.Clone(rules)
	slices.SortFunc(sorted, func(a, b TitleRule) int {
		if r := b.MinRole.Rank() - a.MinRole.Rank(); r != 0 {
			return r
		}
		return b.MinMonths - a.MinMonths
	})
	for _, r := range sorted {
		if !roles.Meets(r.MinRole) {
			continue
		}
		start := data.FollowedAt
		if isSubscriberRole(r.MinRole) {
			start = data.SubscribedAt
		}
		if MonthsBetween(start, now) < r.MinMonths {
			continue
		}
		return r, true
	}
	return TitleRule{}, false
}

// isSubscriberRole reports whether r is one of the roles whose titles
// count from the subscription start (users-and-roles.md, B5).
func isSubscriberRole(r role.Role) bool {
	return r == role.Subscriber || r == role.YouTubeMember
}

// MonthsBetween returns the whole months from start to now: full calendar
// months, with the day adjusting the last one; 0 when start is zero or
// after now.
func MonthsBetween(start, now time.Time) int {
	if start.IsZero() || now.Before(start) {
		return 0
	}
	m := (now.Year()-start.Year())*12 + int(now.Month()-start.Month())
	if now.Day() < start.Day() {
		m--
	}
	if m < 0 {
		return 0
	}
	return m
}
