// SPDX-License-Identifier: MIT

package moderation

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/engine"
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
	// "hi!!!" is 3 over 2 = 150%.
	assert.True(t, punctuationViolates("hi!!!", 50, true))
	// "hello!" is 1 over 5 = 20%.
	assert.False(t, punctuationViolates("hello!", 50, true))
}

func TestEmoteViolates(t *testing.T) {
	t.Parallel()
	assert.False(t, emoteViolates("hello world", nil, 1, false))
	assert.True(t, emoteViolates("hello world", []string{"Kappa", "LUL"}, 2, false))
	// 2 emotes over 2 words = 100%.
	assert.True(t, emoteViolates("a b", []string{"x", "y"}, 50, true))
	// 1 over 4 = 25%.
	assert.False(t, emoteViolates("a b c d", []string{"x"}, 50, true))
}

func TestStrikeLevel(t *testing.T) {
	t.Parallel()
	levels := []settings.StrikeLevel{{Level: 3, Command: "a"}, {Level: 5, Command: "b"}}
	l, ok := strikeLevel(levels, 3)
	assert.True(t, ok)
	assert.Equal(t, "a", l.Command)
	l, ok = strikeLevel(levels, 5)
	assert.True(t, ok)
	assert.Equal(t, "b", l.Command)
	// above the highest set level: the highest (B23)
	l, ok = strikeLevel(levels, 9)
	assert.True(t, ok)
	assert.Equal(t, "b", l.Command)
	// between the set levels: none (B25)
	_, ok = strikeLevel(levels, 1)
	assert.False(t, ok)
	_, ok = strikeLevel(levels, 4)
	assert.False(t, ok)
	// no levels at all: none
	_, ok = strikeLevel(nil, 1)
	assert.False(t, ok)
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

// memStrikes implements Strikes in memory, keeping the count per user.
type memStrikes struct {
	mu      sync.Mutex
	strikes map[id.ID]int64
}

func newMemStrikes() *memStrikes { return &memStrikes{strikes: map[id.ID]int64{}} }

func (m *memStrikes) UpdateUser(_ context.Context, u id.ID, fn func(*user.User) error) (user.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	uu := user.User{ID: u, Stats: user.Stats{Strikes: m.strikes[u]}}
	if err := fn(&uu); err != nil {
		return user.User{}, err
	}
	m.strikes[u] = uu.Stats.Strikes
	return uu, nil
}

func (m *memStrikes) count(u id.ID) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.strikes[u]
}

func (m *memStrikes) seed(u id.ID, n int64) {
	m.mu.Lock()
	m.strikes[u] = n
	m.mu.Unlock()
}

// fakeCommands finds the follow-up commands by name.
type fakeCommands struct {
	cmds map[string]command.Command
}

func (f *fakeCommands) CommandByName(_ context.Context, name string) (command.Command, bool, error) {
	c, ok := f.cmds[name]
	return c, ok, nil
}

// fakeEngine records the submitted requests.
type fakeEngine struct {
	mu   sync.Mutex
	reqs []engine.Request
}

func (f *fakeEngine) Submit(_ context.Context, req engine.Request, done func(engine.Result, error)) error {
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	f.mu.Unlock()
	if done != nil {
		done(engine.Result{Outcome: engine.OutcomeQueued}, nil)
	}
	return nil
}

func (f *fakeEngine) requests() []engine.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]engine.Request(nil), f.reqs...)
}

// fx is a service wired with in-memory ports.
type fx struct {
	svc      *Service
	strikes  *memStrikes
	commands *fakeCommands
	engine   *fakeEngine
}

func newFX(t *testing.T, cfg settings.Moderation, cmds map[string]command.Command) *fx {
	t.Helper()
	f := &fx{
		strikes:  newMemStrikes(),
		commands: &fakeCommands{cmds: cmds},
		engine:   &fakeEngine{},
	}
	s, err := New(Ports{
		Settings: func(context.Context) (settings.Moderation, error) { return cfg, nil },
		Strikes:  f.strikes,
		Commands: f.commands,
		Engine:   f.engine,
	})
	require.NoError(t, err)
	f.svc = s
	return f
}

func TestCheckAllowsClean(t *testing.T) {
	t.Parallel()
	f := newFX(t, settings.Moderation{Filters: []settings.Filter{{Kind: settings.FilterWord, Word: "badword"}}}, nil)
	u := userWithRoles(role.Set(0))
	d, err := f.svc.Check(t.Context(), Message{Platform: platform.Twitch, Text: "hello", User: u})
	require.NoError(t, err)
	assert.True(t, d.Allow)
	assert.Equal(t, int64(0), f.strikes.count(u.ID))
}

func TestViolationRaisesStrikeAndRunsCommand(t *testing.T) {
	t.Parallel()
	cmds := map[string]command.Command{"timeouthug": {Header: command.Header{Name: "timeouthug"}}}
	f := newFX(t, settings.Moderation{
		Filters:      []settings.Filter{{Kind: settings.FilterWord, Word: "badword"}},
		StrikeLevels: []settings.StrikeLevel{{Level: 1, Command: "timeouthug"}},
	}, cmds)
	u := userWithRoles(role.Set(0))
	d, err := f.svc.Check(t.Context(), Message{Platform: platform.Twitch, Channel: "main", Text: "hey badword", Emotes: []string{"K"}, MessageID: "m1", User: u})
	require.NoError(t, err)
	assert.False(t, d.Allow)
	assert.Equal(t, int64(1), d.Strikes, "B9")
	assert.Equal(t, "timeouthug", d.Command)
	assert.Equal(t, int64(1), f.strikes.count(u.ID), "B9")

	// the command ran with the parameters of the message (B10)
	reqs := f.engine.requests()
	require.Len(t, reqs, 1)
	assert.Equal(t, "timeouthug", reqs[0].Command.Name)
	assert.Equal(t, engine.SourceChat, reqs[0].Source)
	p := reqs[0].Params
	assert.Equal(t, platform.Twitch, p.Platform)
	assert.Equal(t, "main", p.Channel)
	require.NotNil(t, p.User)
	assert.Equal(t, u.ID, p.User.ID)
	assert.Equal(t, "hey badword", p.Message)
	assert.Equal(t, "m1", p.MessageID)
	assert.Equal(t, []string{"K"}, p.Emotes)
}

func TestViolationNoLevelOnlyCounter(t *testing.T) {
	t.Parallel()
	// No strike levels: only the counter rises (B25).
	f := newFX(t, settings.Moderation{Filters: []settings.Filter{{Kind: settings.FilterWord, Word: "badword"}}}, nil)
	u := userWithRoles(role.Set(0))
	d, err := f.svc.Check(t.Context(), Message{Platform: platform.Twitch, Text: "badword", User: u})
	require.NoError(t, err)
	assert.False(t, d.Allow)
	assert.Equal(t, int64(1), d.Strikes)
	assert.Empty(t, d.Command)
	assert.Empty(t, f.engine.requests(), "no command runs")
}

func TestViolationAboveHighestLevel(t *testing.T) {
	t.Parallel()
	cmds := map[string]command.Command{"a": {Header: command.Header{Name: "a"}}, "b": {Header: command.Header{Name: "b"}}}
	f := newFX(t, settings.Moderation{
		Filters:      []settings.Filter{{Kind: settings.FilterWord, Word: "badword"}},
		StrikeLevels: []settings.StrikeLevel{{Level: 3, Command: "a"}, {Level: 5, Command: "b"}},
	}, cmds)
	u := userWithRoles(role.Set(0))
	// the user is already at strike 5; the next violation reaches 6 > 5,
	// so the highest level (5) runs (B23).
	f.strikes.seed(u.ID, 5)
	d, err := f.svc.Check(t.Context(), Message{Platform: platform.Twitch, Text: "badword", User: u})
	require.NoError(t, err)
	assert.False(t, d.Allow)
	assert.Equal(t, int64(6), d.Strikes)
	assert.Equal(t, "b", d.Command, "the highest set level")
}

func TestStreamerRunsThrough(t *testing.T) {
	t.Parallel()
	f := newFX(t, settings.Moderation{Filters: []settings.Filter{{Kind: settings.FilterWord, Word: "badword"}}}, nil)
	u := userWithRoles(role.NewSet(role.Streamer))
	d, err := f.svc.Check(t.Context(), Message{Platform: platform.Twitch, Text: "hey badword", User: u})
	require.NoError(t, err)
	assert.True(t, d.Allow, "B2")
	assert.Equal(t, int64(0), f.strikes.count(u.ID))
}

func TestExemptRoleRunsThrough(t *testing.T) {
	t.Parallel()
	f := newFX(t, settings.Moderation{
		Filters: []settings.Filter{{Kind: settings.FilterWord, Word: "badword", Exempt: []role.Role{role.Moderator}}},
	}, nil)
	mod := userWithRoles(role.NewSet(role.Moderator))
	d, err := f.svc.Check(t.Context(), Message{Platform: platform.Twitch, Text: "hey badword", User: mod})
	require.NoError(t, err)
	assert.True(t, d.Allow, "B22")
	assert.Equal(t, int64(0), f.strikes.count(mod.ID))

	plain := userWithRoles(role.NewSet(role.User))
	d, err = f.svc.Check(t.Context(), Message{Platform: platform.Twitch, Text: "hey badword", User: plain})
	require.NoError(t, err)
	assert.False(t, d.Allow, "a plain user is not exempt")
	assert.Equal(t, int64(1), f.strikes.count(plain.ID))
}
