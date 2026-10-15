// SPDX-License-Identifier: MIT

package user_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/domain/user"
)

// testNow is the fixed time of the title rule tests.
func testNow() time.Time { return time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC) }

// titleUser returns a user with the roles on the mock platform and the
// follow and subscription data of the identity.
func titleUser(roles role.Set, followed, subscribed time.Time) user.User {
	return user.User{
		Identities: []user.Identity{{
			Platform:       platform.Mock,
			PlatformUserID: "1",
			Login:          "ada",
			DisplayName:    "Ada",
			Roles:          roles,
			Data:           user.PlatformData{FollowedAt: followed, SubscribedAt: subscribed},
		}},
	}
}

// TestDefaultTitle covers the matching of the title rules
// (users-and-roles.md, B5).
func TestDefaultTitle(t *testing.T) {
	t.Parallel()
	rules := []user.TitleRule{
		{Name: "New Follower", MinRole: role.Follower, MinMonths: 0},
		{Name: "Fan", MinRole: role.Follower, MinMonths: 3},
		{Name: "Subscriber", MinRole: role.Subscriber, MinMonths: 0},
		{Name: "Devoted Subscriber", MinRole: role.Subscriber, MinMonths: 6},
	}

	tests := []struct {
		name     string
		u        user.User
		rules    []user.TitleRule
		wantName string
		wantOK   bool
	}{
		{
			name:   "no rules",
			u:      titleUser(role.NewSet(role.Follower), testNow().AddDate(0, -1, 0), time.Time{}),
			wantOK: false,
		},
		{
			name:     "the first matching rule, highest role first",
			u:        titleUser(role.NewSet(role.Follower), testNow().AddDate(0, -1, 0), time.Time{}),
			rules:    rules,
			wantName: "New Follower",
			wantOK:   true,
		},
		{
			name:     "a subscriber gets the subscriber rule, not the follower one",
			u:        titleUser(role.NewSet(role.Follower, role.Subscriber), testNow().AddDate(0, -1, 0), testNow().AddDate(0, -1, 0)),
			rules:    rules,
			wantName: "Subscriber",
			wantOK:   true,
		},
		{
			name:     "most months first within the role",
			u:        titleUser(role.NewSet(role.Follower), testNow().AddDate(0, -6, 0), time.Time{}),
			rules:    rules,
			wantName: "Fan",
			wantOK:   true,
		},
		{
			name:     "the months count from the subscription start for subscriber rules",
			u:        titleUser(role.NewSet(role.Subscriber), testNow().AddDate(2, 0, 0), testNow().AddDate(0, -6, 0)),
			rules:    rules,
			wantName: "Devoted Subscriber",
			wantOK:   true,
		},
		{
			name:     "a young subscription misses the months",
			u:        titleUser(role.NewSet(role.Subscriber), testNow().AddDate(2, 0, 0), testNow().AddDate(0, -1, 0)),
			rules:    rules,
			wantName: "Subscriber",
			wantOK:   true,
		},
		{
			name:   "a user without the role misses the rule",
			u:      titleUser(role.NewSet(), testNow().AddDate(0, -1, 0), time.Time{}),
			rules:  rules,
			wantOK: false,
		},
		{
			name:     "a zero date counts as 0 months and matches a 0-month rule",
			u:        titleUser(role.NewSet(role.Follower), time.Time{}, time.Time{}),
			rules:    []user.TitleRule{{Name: "New Follower", MinRole: role.Follower, MinMonths: 0}},
			wantName: "New Follower",
			wantOK:   true,
		},
		{
			name:   "a zero date misses a months rule",
			u:      titleUser(role.NewSet(role.Follower), time.Time{}, time.Time{}),
			rules:  []user.TitleRule{{Name: "Fan", MinRole: role.Follower, MinMonths: 1}},
			wantOK: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := tc.u.DefaultTitle(platform.Mock, testNow(), tc.rules)
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				assert.Equal(t, tc.wantName, got.Name)
			}
		})
	}
}

// TestMonthsBetween covers the whole months from start to now
// (users-and-roles.md, B5).
func TestMonthsBetween(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 0, user.MonthsBetween(time.Time{}, testNow()))
	assert.Equal(t, 0, user.MonthsBetween(testNow().Add(time.Hour), testNow()), "a future start is 0")
	assert.Equal(t, 0, user.MonthsBetween(testNow().AddDate(0, 0, 10), testNow()), "less than a month is 0")
	assert.Equal(t, 0, user.MonthsBetween(testNow().AddDate(0, 0, -10), testNow().AddDate(0, 0, 1)), "the day adjusts the last month down")
	assert.Equal(t, 1, user.MonthsBetween(testNow().AddDate(0, -1, 0), testNow()))
	assert.Equal(t, 13, user.MonthsBetween(testNow().AddDate(-1, -1, 0), testNow()))
	assert.Equal(t, 24, user.MonthsBetween(testNow().AddDate(-2, 0, 0), testNow()))
}
