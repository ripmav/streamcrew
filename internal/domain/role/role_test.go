// SPDX-License-Identifier: MIT

package role_test

import (
	json "encoding/json/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/role"
)

// TestRanking covers B20: the order of the roles is fixed.
func TestRanking(t *testing.T) {
	t.Parallel()
	all := role.All()
	require.Len(t, all, 21)
	for i, r := range all {
		assert.Equal(t, i+1, r.Rank(), r)
		parsed, err := role.Parse(string(r))
		require.NoError(t, err)
		assert.Equal(t, r, parsed)
	}
	ids := make([]string, len(all))
	for i, r := range all {
		ids[i] = string(r)
	}
	assert.Equal(t, []string{
		"banned", "user", "twitch_affiliate", "twitch_partner", "follower", "youtube_subscriber", "regular",
		"twitch_vip", "kick_vip", "kick_og", "velora_vip", "vpzone_plus", "vpzone_founder", "vpzone_ambassador",
		"subscriber", "youtube_member", "twitch_global_mod", "twitch_staff", "moderator", "editor", "streamer",
	}, ids, "the IDs are stable")

	_, err := role.Parse("admin")
	require.Error(t, err)
	for _, old := range []string{"creator", "vip", "platform_staff"} {
		_, err := role.Parse(old)
		require.Error(t, err, "%s is gone since the fine levels", old)
	}
	assert.Zero(t, role.Role("admin").Rank())
	assert.False(t, role.Role("").Valid())
}

// TestMeetsAllPairs covers B20, B23 and B27 for every pair of a user's
// single role and a minimum role.
func TestMeetsAllPairs(t *testing.T) {
	t.Parallel()
	for _, has := range role.All() {
		for _, minimum := range role.All() {
			want := has != role.Banned && has.Rank() >= minimum.Rank()
			assert.Equal(t, want, role.NewSet(has).Meets(minimum), "role %s, minimum %s", has, minimum)
		}
	}
}

func TestMeets(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		roles   role.Set
		minimum role.Role
		want    bool
	}{
		{"B43: a user without roles meets user", role.Set(0), role.User, true},
		{"a user without roles does not meet follower", role.Set(0), role.Follower, false},
		{"B23: a higher role meets a lower minimum", role.NewSet(role.Follower, role.Subscriber), role.TwitchVIP, true},
		{"B20: a Kick VIP does not meet kick_og", role.NewSet(role.KickVIP), role.KickOG, false},
		{"B20: an affiliate does not meet twitch_partner", role.NewSet(role.TwitchAffiliate), role.TwitchPartner, false},
		{"B20: a Kick OG meets twitch_vip", role.NewSet(role.KickOG), role.TwitchVIP, true},
		{"B20: a YouTube member meets subscriber", role.NewSet(role.YouTubeMember), role.Subscriber, true},
		{"B27: banned meets nothing, not even user", role.NewSet(role.Banned), role.User, false},
		{"B27: banned wins over other roles", role.NewSet(role.Banned, role.Subscriber), role.Follower, false},
		{"an unknown minimum is never met", role.NewSet(role.Streamer), role.Role("admin"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.roles.Meets(tt.minimum))
		})
	}
}

// TestPrimary covers B21, B22 and B27.
func TestPrimary(t *testing.T) {
	t.Parallel()
	assert.Equal(t, role.User, role.Set(0).Primary(), "B21: every user has at least user")
	assert.Equal(t, role.Moderator, role.NewSet(role.Follower, role.Moderator, role.Subscriber).Primary(), "B22")
	assert.Equal(t, role.Banned, role.NewSet(role.Banned, role.Follower).Primary(), "B27")
}

func TestSetOperations(t *testing.T) {
	t.Parallel()
	s := role.NewSet(role.Follower, role.Role("admin"))
	assert.Equal(t, []role.Role{role.Follower}, s.Roles(), "unknown roles are left out")
	assert.True(t, s.Has(role.Follower))
	assert.False(t, s.Has(role.Role("admin")))

	s = s.With(role.KickVIP).Union(role.NewSet(role.Moderator))
	assert.Equal(t, "follower,kick_vip,moderator", s.String())
	assert.True(t, s.HasAny(role.TwitchVIP, role.KickVIP))
	assert.False(t, s.HasAny(role.TwitchVIP, role.KickOG))
	assert.False(t, s.HasAny())
	s = s.Without(role.KickVIP)
	assert.Equal(t, "follower,moderator", s.String())
	assert.Empty(t, role.Set(0).Roles())
	every := role.NewSet(role.All()...)
	assert.Equal(t, role.All(), every.Roles(), "the set holds all 21 roles")
	assert.Equal(t, role.Streamer, every.Without(role.Banned).Primary())
}

func TestJSON(t *testing.T) {
	t.Parallel()
	s := role.NewSet(role.Moderator, role.Follower)
	data, err := json.Marshal(s)
	require.NoError(t, err)
	assert.JSONEq(t, `["follower","moderator"]`, string(data), "ascending rank")

	var back role.Set
	require.NoError(t, json.Unmarshal(data, &back))
	assert.Equal(t, s, back)

	data, err = json.Marshal(role.Set(0))
	require.NoError(t, err)
	assert.JSONEq(t, `[]`, string(data))

	require.Error(t, json.Unmarshal([]byte(`["admin"]`), &back))
	require.Error(t, json.Unmarshal([]byte(`"follower"`), &back))
}
