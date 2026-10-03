// SPDX-License-Identifier: Apache-2.0

package template

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ripmav/streamcrew/internal/decimal"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/domain/user"
)

// [Interop] The property names in this file follow the original (spec
// template.md, B60) and may be replaced after the legal assessment (roadmap
// Gate O, O.1).

// property reads a value of a user; ok is false if the user has none, e.g.
// no follow date (B4).
type property func(s *Scope, u user.User) (Value, bool)

// userProperties returns the properties of the MVP (B60). Values that depend
// on the platform come from the user's identity on the platform of the run,
// or from the first identity if there is none (spec users-and-roles.md, B2,
// B24).
func userProperties() map[string]property {
	return map[string]property{
		// Identity (users B1, B2).
		"id":   func(_ *Scope, u user.User) (Value, bool) { return TextValue(u.ID.String()), !u.ID.IsZero() },
		"name": identityText(func(i user.Identity) string { return i.Login }),
		"displayname": identityText(func(i user.Identity) string {
			return cmp.Or(i.DisplayName, i.Login)
		}),
		"fulldisplayname": identityText(fullDisplayName),
		"url": identityText(func(i user.Identity) string {
			return i.Platform.ProfileURL(i.Login, i.PlatformUserID)
		}),
		"avatar": identityText(func(i user.Identity) string { return i.AvatarURL }),
		"color":  identityText(func(i user.Identity) string { return i.Color }),

		// Roles and data of the streamer (users B5 to B7, B20 to B22).
		"roles":        roles(roleNames),
		"displayroles": roles(roleNames), // translated with ADR-0022 (roadmap 3.6)
		"primaryrole": roles(func(set role.Set) string {
			return roleName(set.Primary())
		}),
		"title": func(s *Scope, u user.User) (Value, bool) {
			// Until the default titles of phase 5.2, the default title is
			// the primary role (users B5).
			return TextValue(cmp.Or(u.Title, roleName(userRoles(s, u).Primary()))), true
		},
		"notes":               func(_ *Scope, u user.User) (Value, bool) { return TextValue(u.Notes), true },
		"isspecialtyexcluded": func(_ *Scope, u user.User) (Value, bool) { return boolValue(u.Excluded), true },
		"isfollower":          hasRoleValue(followerRoles()...),
		"isregular":           hasRoleValue(role.Regular),
		"issubscriber":        hasRoleValue(subscriberRoles()...),
		"isvip":               hasRoleValue(vipRoles()...),
		"ismod":               hasRoleValue(role.Moderator),

		// Statistics (users B9).
		"time": func(_ *Scope, u user.User) (Value, bool) {
			m := u.Stats.WatchMinutes
			return TextValue(plural(m/60, "Hour", "Hours") + " & " + plural(m%60, "Min", "Mins")), true
		},
		"hours":                 stat(func(st user.Stats) int64 { return st.WatchMinutes / 60 }),
		"mins":                  stat(func(st user.Stats) int64 { return st.WatchMinutes % 60 }),
		"totalstreamswatched":   stat(func(st user.Stats) int64 { return st.StreamsWatched }),
		"totalchatmessagessent": stat(func(st user.Stats) int64 { return st.Messages }),
		"totalcommandsrun":      stat(func(st user.Stats) int64 { return st.CommandsRun }),
		"totaltimestagged":      stat(func(st user.Stats) int64 { return st.Mentions }),
		"moderationstrikes":     stat(func(st user.Stats) int64 { return st.Strikes }),
		"totalamountdonated": func(_ *Scope, u user.User) (Value, bool) {
			return centsValue(u.Stats.DonatedCents), true
		},

		// Spans from data of the platform and the last contact (users B9,
		// B10; B42).
		"accountage":   since(accountCreated, func(sp span) Value { return TextValue(sp.String()) }),
		"accountdays":  since(accountCreated, totalDays),
		"followage":    since(followed, func(sp span) Value { return TextValue(sp.String()) }),
		"followdays":   since(followed, totalDays),
		"followmonths": since(followed, func(sp span) Value { return IntValue(int64(sp.months)) }),
		"followyears":  since(followed, func(sp span) Value { return IntValue(int64(sp.months / 12)) }),
		"subage":       since(subscribed, func(sp span) Value { return TextValue(sp.String()) }),
		"subdays":      since(subscribed, totalDays),
		"submonths":    since(subscribed, func(sp span) Value { return IntValue(int64(sp.months)) }),
		"subtier": identityValue(func(i user.Identity) (Value, bool) {
			if i.Data.SubTier <= 0 {
				return Value{}, false
			}
			return TextValue("Tier " + strconv.Itoa(i.Data.SubTier)), true
		}),
		"lastseenage":  since(lastSeen, func(sp span) Value { return TextValue(sp.String()) }),
		"lastseendays": since(lastSeen, totalDays),
		"lastseendate": func(s *Scope, u user.User) (Value, bool) {
			if u.Stats.LastSeen.IsZero() {
				return Value{}, false
			}
			return TextValue(u.Stats.LastSeen.In(s.location()).Format(layoutDateTime)), true
		},
	}
}

// identity returns the user's identity on the platform of the run, or the
// first identity.
func identity(s *Scope, u user.User) (user.Identity, bool) {
	if i, ok := u.Identity(s.Platform); ok {
		return i, true
	}
	if len(u.Identities) > 0 {
		return u.Identities[0], true
	}
	return user.Identity{}, false
}

// identityValue returns a property from the identity.
func identityValue(value func(user.Identity) (Value, bool)) property {
	return func(s *Scope, u user.User) (Value, bool) {
		i, ok := identity(s, u)
		if !ok {
			return Value{}, false
		}
		return value(i)
	}
}

// identityText returns a text property from the identity; an empty text has
// no value because the platform did not report it.
func identityText(text func(user.Identity) string) property {
	return identityValue(func(i user.Identity) (Value, bool) {
		v := text(i)
		return TextValue(v), v != ""
	})
}

// fullDisplayName returns the display name followed by the login in
// parentheses if the two differ other than in case, e.g. "Ålice (alice_99)".
func fullDisplayName(i user.Identity) string {
	if i.DisplayName == "" || strings.EqualFold(i.DisplayName, i.Login) {
		return cmp.Or(i.DisplayName, i.Login)
	}
	return i.DisplayName + " (" + i.Login + ")"
}

// userRoles returns the user's roles on the platform of their identity
// (users B24).
func userRoles(s *Scope, u user.User) role.Set {
	i, _ := identity(s, u)
	return u.Roles(i.Platform)
}

// roles returns a property from the user's roles.
func roles(text func(role.Set) string) property {
	return func(s *Scope, u user.User) (Value, bool) {
		return TextValue(text(userRoles(s, u))), true
	}
}

// hasRoleValue returns a property that says whether the user has one of
// roles.
func hasRoleValue(roles ...role.Role) property {
	return func(s *Scope, u user.User) (Value, bool) {
		return boolValue(userRoles(s, u).HasAny(roles...)), true
	}
}

// The kinds of roles that identifiers like $userisvip ask for, each with
// the levels of all platforms (users B20): whoever has one of them.

// followerRoles are the followers, on YouTube the subscribers.
func followerRoles() []role.Role {
	return []role.Role{role.Follower, role.YouTubeSubscriber}
}

// subscriberRoles are the subscribers, on YouTube the members.
func subscriberRoles() []role.Role {
	return []role.Role{role.Subscriber, role.YouTubeMember}
}

// vipRoles are the VIPs and their counterparts on the other platforms.
func vipRoles() []role.Role {
	return []role.Role{
		role.TwitchVIP, role.KickVIP, role.KickOG, role.VeloraVIP,
		role.VPZonePlus, role.VPZoneFounder, role.VPZoneAmbassador,
	}
}

// roleNames returns the names of the roles from the highest to the lowest,
// e.g. "Moderator, Subscriber, Follower"; User only if there is no other
// role.
func roleNames(set role.Set) string {
	rs := set.Without(role.User).Roles()
	if len(rs) == 0 {
		return roleName(role.User)
	}
	slices.Reverse(rs)
	names := make([]string, len(rs))
	for i, r := range rs {
		names[i] = roleName(r)
	}
	return strings.Join(names, ", ")
}

// roleName returns the English name of a role until the texts are translated
// (ADR-0022, roadmap 3.6; users A2).
func roleName(r role.Role) string {
	switch r {
	case role.Banned:
		return "Banned"
	case role.User:
		return "User"
	case role.TwitchAffiliate:
		return "Twitch Affiliate"
	case role.TwitchPartner:
		return "Twitch Partner"
	case role.Follower:
		return "Follower"
	case role.YouTubeSubscriber:
		return "YouTube Subscriber"
	case role.Regular:
		return "Regular"
	case role.TwitchVIP:
		return "Twitch VIP"
	case role.KickVIP:
		return "Kick VIP"
	case role.KickOG:
		return "Kick OG"
	case role.VeloraVIP:
		return "Velora VIP"
	case role.VPZonePlus:
		return "VPZone Plus"
	case role.VPZoneFounder:
		return "VPZone Founder"
	case role.VPZoneAmbassador:
		return "VPZone Ambassador"
	case role.Subscriber:
		return "Subscriber"
	case role.YouTubeMember:
		return "YouTube Member"
	case role.TwitchGlobalMod:
		return "Twitch Global Moderator"
	case role.TwitchStaff:
		return "Twitch Staff"
	case role.Moderator:
		return "Moderator"
	case role.Editor:
		return "Editor"
	case role.Streamer:
		return "Streamer"
	default:
		return string(r)
	}
}

// stat returns a statistic as a number.
func stat(value func(user.Stats) int64) property {
	return func(_ *Scope, u user.User) (Value, bool) {
		return IntValue(value(u.Stats)), true
	}
}

// centsValue returns hundredths as a number with two decimals, e.g. "12.34"
// for 1234. The currency is up to the donation integrations (roadmap phase
// 10).
func centsValue(cents int64) Value {
	digits, negative := strings.CutPrefix(strconv.FormatInt(cents, 10), "-")
	digits = strings.Repeat("0", max(3-len(digits), 0)) + digits
	text := digits[:len(digits)-2] + "." + digits[len(digits)-2:]
	if negative {
		text = "-" + text
	}
	number, _ := decimal.Parse(text) // the hundredths of an int64 always fit (Code-ADR-0020)
	return Value{Text: text, Number: number, IsNumber: true}
}

// boolValue returns "true" or "false".
func boolValue(b bool) Value {
	return TextValue(strconv.FormatBool(b))
}

// The dates a span starts at; zero if unknown.
func accountCreated(s *Scope, u user.User) time.Time {
	i, _ := identity(s, u)
	return i.Data.AccountCreatedAt
}

func followed(s *Scope, u user.User) time.Time {
	i, _ := identity(s, u)
	return i.Data.FollowedAt
}

func subscribed(s *Scope, u user.User) time.Time {
	i, _ := identity(s, u)
	return i.Data.SubscribedAt
}

func lastSeen(_ *Scope, u user.User) time.Time {
	return u.Stats.LastSeen
}

// since returns a property of the span from a date until now; without the
// date the property has no value.
func since(from func(*Scope, user.User) time.Time, value func(span) Value) property {
	return func(s *Scope, u user.User) (Value, bool) {
		t := from(s, u)
		if t.IsZero() {
			return Value{}, false
		}
		return value(spanBetween(t, s.now(), s.location())), true
	}
}

// totalDays returns all days of a span.
func totalDays(sp span) Value {
	return IntValue(int64(sp.totalDays))
}
