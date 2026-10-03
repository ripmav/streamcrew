// SPDX-License-Identifier: Apache-2.0

package requirement_test

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/action/actiontest"
	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/connector/connectortest"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/i18n"
	"github.com/ripmav/streamcrew/internal/polydoc"
	"github.com/ripmav/streamcrew/internal/requirement"
)

// language is a fake of requirement.Language.
type language struct {
	lang i18n.Language
	err  error
}

func (l language) Language(context.Context) (i18n.Language, error) { return l.lang, l.err }

// cooldowns is a fake of requirement.Cooldowns.
type cooldowns struct {
	mu     sync.Mutex
	groups []command.CooldownGroup
	ends   map[command.CooldownKey]time.Time
	// err fails every method, putErr only PutCooldown.
	err    error
	putErr error
}

func newCooldowns(groups ...command.CooldownGroup) *cooldowns {
	return &cooldowns{groups: groups, ends: make(map[command.CooldownKey]time.Time)}
}

func (c *cooldowns) CooldownGroups(context.Context) ([]command.CooldownGroup, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.groups), c.err
}

func (c *cooldowns) CooldownEnd(_ context.Context, key command.CooldownKey) (time.Time, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return time.Time{}, false, c.err
	}
	ends, ok := c.ends[key]
	return ends, ok, nil
}

func (c *cooldowns) PutCooldown(_ context.Context, key command.CooldownKey, ends, now time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := cmp.Or(c.err, c.putErr); err != nil {
		return err
	}
	maps.DeleteFunc(c.ends, func(_ command.CooldownKey, e time.Time) bool { return !e.After(now) })
	c.ends[key] = ends
	return nil
}

func (c *cooldowns) DeleteCooldown(_ context.Context, key command.CooldownKey, ends time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return c.err
	}
	if c.ends[key].Equal(ends) {
		delete(c.ends, key)
	}
	return nil
}

// running returns the running cooldowns.
func (c *cooldowns) running() map[command.CooldownKey]time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return maps.Clone(c.ends)
}

// left returns the time left of each running cooldown at now.
func (c *cooldowns) left(now time.Time) map[command.CooldownKey]time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[command.CooldownKey]time.Duration, len(c.ends))
	for k, ends := range c.ends {
		out[k] = ends.Sub(now)
	}
	return out
}

// fail sets the error of every method.
func (c *cooldowns) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.err = err
}

// users is a fake of engine.Users: users by platform and lowercase login
// name.
type users struct {
	mu     sync.Mutex
	byName map[platform.Name]map[string]user.User
	err    error
}

func newUsers(people ...*user.User) *users {
	u := &users{byName: make(map[platform.Name]map[string]user.User)}
	for _, p := range people {
		for _, ident := range p.Identities {
			if u.byName[ident.Platform] == nil {
				u.byName[ident.Platform] = make(map[string]user.User)
			}
			u.byName[ident.Platform][strings.ToLower(ident.Login)] = *p
		}
	}
	return u
}

func (u *users) UserByName(_ context.Context, p platform.Name, name string) (user.User, bool, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.err != nil {
		return user.User{}, false, u.err
	}
	found, ok := u.byName[p][strings.ToLower(name)]
	return found, ok, nil
}

// streamer is a fake of requirement.Streamer.
type streamer map[platform.Name]id.ID

func (s streamer) StreamerUser(_ context.Context, p platform.Name) (id.ID, error) {
	userID, ok := s[p]
	if !ok {
		return id.ID{}, fmt.Errorf("no streamer on %s", p)
	}
	return userID, nil
}

// fixture is a service with Twitch, which replies, and Kick, which does
// not.
type fixture struct {
	service   *requirement.Service
	twitch    *connectortest.Platform
	kick      *connectortest.Platform
	logs      *bytes.Buffer
	cooldowns *cooldowns
	// streamer has the user of the streamer on Twitch and Kick.
	streamer streamer
	// users knows bob on Twitch and kim on Kick.
	users *users
}

func newFixture(t *testing.T, lang language) *fixture {
	t.Helper()
	catalog, err := i18n.Load()
	require.NoError(t, err)
	f := &fixture{
		twitch:    connectortest.New(platform.Twitch, connectortest.Features{Replies: true}),
		kick:      connectortest.New(platform.Kick, connectortest.Features{}),
		logs:      &bytes.Buffer{},
		cooldowns: newCooldowns(),
		streamer:  streamer{platform.Twitch: id.New(), platform.Kick: id.New()},
		users:     newUsers(person("bob", platform.Twitch), person("kim", platform.Kick)),
	}
	set, err := connector.NewSet(f.twitch, f.kick)
	require.NoError(t, err)
	f.service, err = requirement.New(requirement.Ports{
		Catalog: catalog, Language: lang, Platforms: set,
		Cooldowns: f.cooldowns, Streamer: f.streamer, Users: f.users,
		Logger: slog.New(slog.NewTextHandler(f.logs, nil)),
	})
	require.NoError(t, err)
	return f
}

// person returns a user with the login name and roles on platform p.
func person(login string, p platform.Name, roles ...role.Role) *user.User {
	return &user.User{ID: id.New(), Identities: []user.Identity{{
		Platform: p, PlatformUserID: "id-" + login, Login: login, DisplayName: login, Roles: role.NewSet(roles...),
	}}}
}

// cmd returns a command with the requirements.
func cmd(reqs ...command.Requirement) command.Command {
	c := command.Command{Requirements: reqs}
	c.ID, c.Name, c.Kind, c.Enabled = id.New(), "hug", command.KindChat, true
	return c
}

// chat returns the parameters of a chat message of u on Twitch.
func chat(u *user.User) engine.Params {
	return engine.Params{Platform: platform.Twitch, User: u, Message: "!hug", MessageID: "m1"}
}

// rejection returns the rejection of a minimum role.
func rejection(minimum role.Role, tell bool) engine.Decision {
	return engine.Rejected(engine.Rejection{
		Requirement: command.TypeRole,
		Reason:      i18n.Message{Key: i18n.KeyRequirementRole, Args: map[string]i18n.Value{"role": i18n.Text(string(minimum))}},
		Tell:        tell,
	})
}

// TestRole covers requirements.md B4 and B10 to B12, B104, B110: the
// primary role on the platform of the run must reach the minimum role;
// without a role requirement only banned users are rejected; runs without a
// user are checked as the streamer and meet every role; banned users and
// runs without a chat message are not told.
func TestRole(t *testing.T) {
	t.Parallel()
	f := newFixture(t, language{lang: i18n.English})
	mods := command.RoleRequirement{Role: role.Moderator}
	ada := person("ada", platform.Twitch)
	mod := person("mo", platform.Twitch, role.Moderator)
	kickMod := person("ko", platform.Kick, role.Moderator)
	sub := person("sue", platform.Twitch, role.Subscriber, role.Follower)
	banned := person("bob", platform.Twitch, role.Banned, role.Moderator)
	regular := person("reg", platform.Twitch)
	regular.Regular = true
	event := engine.Params{Platform: platform.Twitch, User: sub}

	for name, tc := range map[string]struct {
		cmd  command.Command
		p    engine.Params
		want engine.Decision
	}{
		"no requirement":                  {cmd(), chat(ada), engine.Met(chat(ada))},
		"no requirement, banned":          {cmd(), chat(banned), rejection(role.User, false)},
		"no requirement, no user":         {cmd(), engine.Params{}, engine.Met(engine.Params{})},
		"moderator":                       {cmd(mods), chat(mod), engine.Met(chat(mod))},
		"moderator on another platform":   {cmd(mods), chat(kickMod), rejection(role.Moderator, true)},
		"streamer above moderator":        {cmd(mods), chat(person("st", platform.Twitch, role.Streamer)), engine.Met(chat(person("st", platform.Twitch, role.Streamer)))},
		"subscriber below moderator":      {cmd(mods), chat(sub), rejection(role.Moderator, true)},
		"no chat message":                 {cmd(mods), event, rejection(role.Moderator, false)},
		"no user":                         {cmd(mods), engine.Params{}, engine.Met(engine.Params{})},
		"streamer, no user":               {cmd(command.RoleRequirement{Role: role.Streamer}), engine.Params{}, engine.Met(engine.Params{})},
		"banned moderator":                {cmd(mods), chat(banned), rejection(role.Moderator, false)},
		"user, no user":                   {cmd(command.RoleRequirement{Role: role.User}), engine.Params{}, engine.Met(engine.Params{})},
		"user, banned":                    {cmd(command.RoleRequirement{Role: role.User}), chat(banned), rejection(role.User, false)},
		"regular":                         {cmd(command.RoleRequirement{Role: role.Regular}), chat(regular), engine.Met(chat(regular))},
		"settings check nothing":          {cmd(command.SettingsRequirement{ShowInChatMenu: true}), chat(ada), engine.Met(chat(ada))},
		"settings and role":               {cmd(command.SettingsRequirement{}, mods), chat(ada), rejection(role.Moderator, true)},
		"role before unsupported":         {cmd(command.CooldownRequirement{Scope: command.CooldownStandard, Duration: 1}, mods), chat(ada), rejection(role.Moderator, true)},
		"role before unsupported, banned": {cmd(command.ArgumentsRequirement{}), chat(banned), rejection(role.User, false)},
	} {
		got, err := f.service.Apply(t.Context(), tc.cmd, tc.p)
		require.NoError(t, err, name)
		if tc.want.Verdict == engine.VerdictMet {
			require.Len(t, got.Runs, 1, name)
			tc.want.Runs[0].User = got.Runs[0].User
		}
		assert.Equal(t, tc.want, got, name)
	}
}

// TestFaulty covers requirements.md B7, B8 and B40: unknown requirements
// and, until phase 8, currencies, ranks and items reject the command
// before anything else, without telling the user, with a warning in the
// log.
func TestFaulty(t *testing.T) {
	t.Parallel()
	mods := command.RoleRequirement{Role: role.Moderator}
	for name, tc := range map[string]struct {
		req command.Requirement
		key i18n.Key
	}{
		"currency":  {command.CurrencyRequirement{Currency: id.New(), Mode: command.CurrencyRequired, Amount: 5}, i18n.KeyRequirementFaulty},
		"rank":      {command.RankRequirement{Rank: id.New(), Match: command.RankAtLeast}, i18n.KeyRequirementFaulty},
		"inventory": {command.InventoryRequirement{Item: id.New(), Amount: 1}, i18n.KeyRequirementFaulty},
		"unknown":   {command.UnknownRequirement{Type: "karma"}, i18n.KeyRequirementUnknown},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, language{lang: i18n.English})
			got, err := f.service.Apply(t.Context(), cmd(mods, tc.req), chat(person("ada", platform.Twitch)))
			require.NoError(t, err)
			assert.Equal(t, engine.Rejected(engine.Rejection{Requirement: tc.req.DocType(), Reason: i18n.Message{Key: tc.key}}), got,
				"before the role, and nobody is told")
			assert.Contains(t, f.logs.String(), "command not run: faulty requirement")
			assert.Contains(t, f.logs.String(), "requirement="+tc.req.DocType())
		})
	}
}

// TestNotSupported: thresholds cannot be decided yet; the service says so
// instead of guessing (B1), after the arguments, and starts no cooldown.
func TestNotSupported(t *testing.T) {
	t.Parallel()
	f := newFixture(t, language{lang: i18n.English})
	minute := command.CooldownRequirement{Scope: command.CooldownStandard, Duration: polydoc.Duration(time.Minute)}
	threshold := command.ThresholdRequirement{Users: 2, Within: 1}
	_, err := f.service.Apply(t.Context(), cmd(threshold, minute), chat(person("ada", platform.Twitch)))
	require.ErrorIs(t, err, requirement.ErrNotSupported)
	assert.Empty(t, f.cooldowns.running())

	needed := command.ArgumentsRequirement{Arguments: []command.Argument{{Name: "a", Type: command.ArgumentText, Required: true}}}
	d, err := f.service.Apply(t.Context(), cmd(threshold, needed), chat(person("ada", platform.Twitch)))
	require.NoError(t, err)
	assert.Equal(t, command.TypeArguments, d.Rejection.Requirement, "the arguments come before the threshold")
}

// TestNotify covers requirements.md B70 to B72: the reason in the language
// of the profile, as a reply where the platform can, otherwise after "@"
// and the login name, from the bot if it is connected.
func TestNotify(t *testing.T) {
	t.Parallel()
	mods := command.RoleRequirement{Role: role.Moderator}
	sue := person("sue", platform.Twitch, role.Subscriber)
	sue.Identities = append(sue.Identities, user.Identity{Platform: platform.Kick, PlatformUserID: "k-sue", Login: "suek"})

	f := newFixture(t, language{lang: i18n.English})
	d, err := f.service.Apply(t.Context(), cmd(mods), chat(sue))
	require.NoError(t, err)
	require.NoError(t, f.service.Notify(t.Context(), cmd(mods), chat(sue), d.Rejection))
	assert.Equal(t, []connectortest.Call{{Op: connectortest.OpReply, From: connector.AccountStreamer, MessageID: "m1",
		Text: "This command needs the role moderator or a higher one."}}, f.twitch.Calls())

	de := newFixture(t, language{lang: i18n.German})
	de.kick.SetStatus(connector.Status{Streamer: true, Bot: true})
	onKick := engine.Params{Platform: platform.Kick, User: sue, Message: "!hug", MessageID: "k1"}
	require.NoError(t, de.service.Notify(t.Context(), cmd(mods), onKick, d.Rejection))
	assert.Equal(t, []connectortest.Call{{Op: connectortest.OpSend, From: connector.AccountBot,
		Text: "@suek Dieser Command braucht die Rolle Moderator oder eine höhere."}}, de.kick.Calls(),
		"no replies on this platform: the name first, from the bot")

	noID := engine.Params{Platform: platform.Twitch, User: sue, Message: "!hug"}
	require.NoError(t, de.service.Notify(t.Context(), cmd(mods), noID, d.Rejection))
	assert.Equal(t, connectortest.OpSend, de.twitch.Calls()[0].Op, "a reply needs the ID of the message")
	assert.Equal(t, "@sue Dieser Command braucht die Rolle Moderator oder eine höhere.", de.twitch.Calls()[0].Text)

	stranger := engine.Params{Platform: platform.Kick, User: person("x", platform.Twitch), Message: "!hug"}
	require.NoError(t, de.service.Notify(t.Context(), cmd(mods), stranger, d.Rejection))
	assert.Equal(t, "Dieser Command braucht die Rolle Moderator oder eine höhere.", de.kick.Calls()[1].Text,
		"without an account on the platform, no name")
}

// TestNotifyFails: a message that cannot be written or sent is an error.
func TestNotifyFails(t *testing.T) {
	t.Parallel()
	reason := engine.Rejection{Requirement: command.TypeRole, Reason: i18n.Message{Key: i18n.KeyRequirementFaulty}, Tell: true}
	sue := chat(person("sue", platform.Twitch))

	broken := newFixture(t, language{err: errors.New("settings unreadable")})
	require.ErrorContains(t, broken.service.Notify(t.Context(), cmd(), sue, reason), "settings unreadable")

	f := newFixture(t, language{lang: i18n.English})
	missing := engine.Rejection{Requirement: command.TypeRole, Reason: i18n.Message{Key: i18n.KeyRequirementRole}, Tell: true}
	require.ErrorIs(t, f.service.Notify(t.Context(), cmd(), sue, missing), i18n.ErrMissingValue)

	f.twitch.SetStatus(connector.Status{Bot: true})
	require.ErrorIs(t, f.service.Notify(t.Context(), cmd(), sue, reason), connector.ErrNotConnected)
	velora := engine.Params{Platform: "velora", User: sue.User, Message: "!hug"}
	require.ErrorIs(t, f.service.Notify(t.Context(), cmd(), velora, reason), connector.ErrNotConnected)
	assert.Empty(t, f.twitch.Calls())

	ok := newFixture(t, language{lang: i18n.English})
	ok.twitch.Fail(connectortest.OpReply, errors.New("rate limited"))
	require.ErrorContains(t, ok.service.Notify(t.Context(), cmd(), sue, reason), "rate limited")
}

// TestNew: every port is required.
func TestNew(t *testing.T) {
	t.Parallel()
	catalog, err := i18n.Load()
	require.NoError(t, err)
	set, err := connector.NewSet()
	require.NoError(t, err)
	full := requirement.Ports{
		Catalog: catalog, Language: language{}, Platforms: set,
		Cooldowns: newCooldowns(), Streamer: streamer{}, Users: newUsers(), Logger: slog.New(slog.DiscardHandler),
	}
	for name, change := range map[string]func(*requirement.Ports){
		"catalog":   func(p *requirement.Ports) { p.Catalog = nil },
		"language":  func(p *requirement.Ports) { p.Language = nil },
		"platforms": func(p *requirement.Ports) { p.Platforms = nil },
		"cooldowns": func(p *requirement.Ports) { p.Cooldowns = nil },
		"streamer":  func(p *requirement.Ports) { p.Streamer = nil },
		"users":     func(p *requirement.Ports) { p.Users = nil },
		"logger":    func(p *requirement.Ports) { p.Logger = nil },
	} {
		p := full
		change(&p)
		_, err := requirement.New(p)
		assert.Error(t, err, name)
	}
	_, err = requirement.New(full)
	require.NoError(t, err)
}

// TestWithEngine: the decisions fit the engine (B1), which tells the user
// through the service (command-engine.md, B11).
func TestWithEngine(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, language{lang: i18n.German})
		h := actiontest.NewHarnessWith(t, noTypes{}, actiontest.NewCommands(), engine.WithRequirements(f.service))
		mods := h.Command("mods only")
		mods.Requirements = []command.Requirement{command.RoleRequirement{Role: role.Moderator}}
		h.Put(mods)

		res, err := h.Engine().Trigger(t.Context(), engine.Request{Command: mods, Source: engine.SourceChat, Params: chat(person("ada", platform.Twitch))})
		require.NoError(t, err)
		assert.Equal(t, engine.OutcomeRejected, res.Outcome)
		synctest.Wait()
		require.Len(t, f.twitch.Calls(), 1)
		assert.Equal(t, "Dieser Command braucht die Rolle Moderator oder eine höhere.", f.twitch.Calls()[0].Text)

		mo := person("mo", platform.Twitch, role.Moderator)
		res, err = h.Engine().Trigger(t.Context(), engine.Request{Command: mods, Source: engine.SourceChat, Params: chat(mo)})
		require.NoError(t, err)
		assert.Equal(t, engine.OutcomeQueued, res.Outcome)

		daily := h.Command("daily")
		daily.Requirements = []command.Requirement{command.CooldownRequirement{Scope: command.CooldownPerUser, Duration: polydoc.Duration(time.Minute)}}
		h.Put(daily)
		res, err = h.Engine().Trigger(t.Context(), engine.Request{Command: daily, Source: engine.SourceChat, Params: chat(mo)})
		require.NoError(t, err)
		assert.Equal(t, engine.OutcomeQueued, res.Outcome)
		time.Sleep(time.Second)
		res, err = h.Engine().Trigger(t.Context(), engine.Request{Command: daily, Source: engine.SourceChat, Params: chat(mo)})
		require.NoError(t, err)
		assert.Equal(t, engine.OutcomeRejected, res.Outcome)
		synctest.Wait()
		require.Len(t, f.twitch.Calls(), 2)
		assert.Equal(t, "Du kannst diesen Command in 59 Sekunden wieder nutzen.", f.twitch.Calls()[1].Text)
	})
}

// noTypes knows no action types.
type noTypes struct{}

func (noTypes) VisualAudio(string) bool { return false }
func (noTypes) Missing(string) []capability.Capability {
	return []capability.Capability{}
}
