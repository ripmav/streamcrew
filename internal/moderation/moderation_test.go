// SPDX-License-Identifier: MIT

package moderation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/settings"
)

func TestWordViolates(t *testing.T) {
	t.Parallel()
	cases := []struct {
		text, pattern string
		want          bool
	}{
		{"hello badword there", "badword", true},
		{"Hello BADWORD there", "badword", true}, // case-insensitive
		{"the badwording", "badword", false},     // whole word only
		{"badwording", "*badword*", true},        // wildcard both
		{"badge", "bad*", true},                  // wildcard start
		{"cubad", "*bad", true},                  // wildcard end
		{"cubad", "bad", false},                  // no wildcard: no
		{"clean message", "badword", false},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, wordViolates(c.text, c.pattern), "%q vs %q", c.text, c.pattern)
	}
}

func TestLinkViolates(t *testing.T) {
	t.Parallel()
	assert.True(t, linkViolates("see https://example.com/x"))
	assert.True(t, linkViolates("go to http://example.com"))
	assert.True(t, linkViolates("visit example.com today"))
	assert.True(t, linkViolates("my site is sub.example.co.uk"))
	assert.False(t, linkViolates("hello world"))
	assert.False(t, linkViolates("version 3.14 is out"))
	assert.False(t, linkViolates("e.g. this is fine"))
}

func TestUppercaseViolates(t *testing.T) {
	t.Parallel()
	// Count mode: fully uppercase words of at least two letters.
	assert.False(t, uppercaseViolates("hello", 1, false))
	assert.True(t, uppercaseViolates("HELLO world", 1, false))
	assert.True(t, uppercaseViolates("HELLO WORLD", 2, false))
	assert.False(t, uppercaseViolates("HELLO WORLD", 3, false))
	assert.False(t, uppercaseViolates("HELLO WORLD", 0, false), "threshold 0 is off")
	// Percentage mode: uppercase letters over all letters.
	assert.True(t, uppercaseViolates("HELLO world", 50, true), "5/10 = 50%")
	assert.False(t, uppercaseViolates("hELLO world", 50, true), "3/10 = 30%")
	assert.False(t, uppercaseViolates("!!!", 50, true), "no letters")
}

func TestPunctuationViolates(t *testing.T) {
	t.Parallel()
	assert.False(t, punctuationViolates("hello", 1, false))
	assert.True(t, punctuationViolates("hello!!!", 3, false))
	assert.False(t, punctuationViolates("hello!!!", 4, false))
	// Percentage: punctuation over letters. "hi!!!" is 3 over 2 = 150 -> 100.
	assert.True(t, punctuationViolates("hi!!!", 50, true))
	// "hello!" is 1 over 5 = 20%.
	assert.False(t, punctuationViolates("hello!", 50, true))
}

func TestEmoteViolates(t *testing.T) {
	t.Parallel()
	assert.False(t, emoteViolates("hello world", nil, 1, false))
	assert.True(t, emoteViolates("hello world", []string{"Kappa", "LUL"}, 2, false))
	// Percentage: emotes over words. 2 emotes over 2 words = 100%.
	assert.True(t, emoteViolates("a b", []string{"x", "y"}, 50, true))
	assert.False(t, emoteViolates("a b c d", []string{"x"}, 50, true), "1 over 4 = 25%")
}

func userWithRoles(roles role.Set) user.User {
	return user.User{
		ID: id.New(),
		Identities: []user.Identity{{
			Platform: platform.Twitch,
			Login:    "alice",
			Roles:    roles,
		}},
	}
}

func newForTest(cfg settings.Moderation) *Service {
	s, err := New(Ports{Settings: func(context.Context) (settings.Moderation, error) {
		return cfg, nil
	}})
	if err != nil {
		panic(err)
	}
	return s
}

func TestCheck(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	// No filters: everything runs through.
	s := newForTest(settings.Moderation{})
	d, err := s.Check(ctx, Message{Platform: platform.Twitch, Text: "anything at all", User: userWithRoles(role.Set(0))})
	require.NoError(t, err)
	assert.True(t, d.Allow)

	// A banned word is discarded.
	s = newForTest(settings.Moderation{Filters: []settings.Filter{{Kind: settings.FilterWord, Word: "badword"}}})
	d, err = s.Check(ctx, Message{Platform: platform.Twitch, Text: "hey badword", User: userWithRoles(role.Set(0))})
	require.NoError(t, err)
	assert.False(t, d.Allow, "the banned word is a violation")
	d, _ = s.Check(ctx, Message{Platform: platform.Twitch, Text: "hello", User: userWithRoles(role.Set(0))})
	assert.True(t, d.Allow)

	// The streamer runs through (B2).
	d, _ = s.Check(ctx, Message{Platform: platform.Twitch, Text: "hey badword", User: userWithRoles(role.NewSet(role.Streamer))})
	assert.True(t, d.Allow, "the streamer is exempt")

	// An exempt role runs through (B22).
	s = newForTest(settings.Moderation{Filters: []settings.Filter{{Kind: settings.FilterWord, Word: "badword", Exempt: []role.Role{role.Moderator}}}})
	d, _ = s.Check(ctx, Message{Platform: platform.Twitch, Text: "hey badword", User: userWithRoles(role.NewSet(role.Moderator))})
	assert.True(t, d.Allow, "the moderator is exempt")
	d, _ = s.Check(ctx, Message{Platform: platform.Twitch, Text: "hey badword", User: userWithRoles(role.NewSet(role.User))})
	assert.False(t, d.Allow, "a plain user is not")
}
