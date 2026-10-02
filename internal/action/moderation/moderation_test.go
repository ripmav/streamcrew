// SPDX-License-Identifier: Apache-2.0

package moderation_test

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/actiontest"
	"github.com/ripmav/streamcrew/internal/action/moderation"
	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/connector/connectortest"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/template"
)

// update reports whether golden files are written first (Code-ADR-0006).
func update() bool {
	return os.Getenv("STREAMCREW_UPDATE_GOLDEN") != ""
}

// logs is a log that tests read.
type logs struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logs) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// store is a fake of the users the core knows, with their strikes.
type store struct {
	mu    sync.Mutex
	users []user.User
	err   error
}

func (s *store) UserByName(_ context.Context, p platform.Name, name string) (user.User, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return user.User{}, false, s.err
	}
	for _, u := range s.users {
		if slices.ContainsFunc(u.Identities, func(i user.Identity) bool {
			return i.Platform == p && strings.EqualFold(i.Login, name)
		}) {
			return u, true, nil
		}
	}
	return user.User{}, false, nil
}

func (s *store) UpsertIdentity(_ context.Context, ident user.Identity) (user.User, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.users {
		for _, i := range u.Identities {
			if i.Platform == ident.Platform && i.PlatformUserID == ident.PlatformUserID {
				return u, false, nil
			}
		}
	}
	u := user.User{ID: id.New(), Identities: []user.Identity{ident}}
	s.users = append(s.users, u)
	return u, true, nil
}

func (s *store) UpdateUser(_ context.Context, userID id.ID, fn func(*user.User) error) (user.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.users, func(u user.User) bool { return u.ID == userID })
	if i < 0 {
		return user.User{}, fmt.Errorf("user %s: not found", userID)
	}
	u := s.users[i]
	if err := fn(&u); err != nil {
		return user.User{}, err
	}
	s.users[i] = u
	return u, nil
}

func (s *store) ResetStrikes(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.users {
		s.users[i].Stats.Strikes = 0
	}
	return nil
}

// add adds a user with the accounts and strikes.
func (s *store) add(strikes int64, accounts ...user.Identity) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users = append(s.users, user.User{ID: id.New(), Identities: accounts, Stats: user.Stats{Strikes: strikes}})
}

// strikes returns the strikes of the user with the account login on p; -1
// if there is none.
func (s *store) strikes(p platform.Name, login string) int64 {
	u, ok, _ := s.UserByName(context.Background(), p, login)
	if !ok {
		return -1
	}
	return u.Stats.Strikes
}

// count returns the number of users.
func (s *store) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.users)
}

// mute is a fake of the muted chat.
type mute struct {
	mu    sync.Mutex
	calls []string
}

func (m *mute) Mute(context.Context)   { m.add("mute") }
func (m *mute) Unmute(context.Context) { m.add("unmute") }

func (m *mute) add(call string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, call)
}

func (m *mute) get() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.calls)
}

// fixture is a running engine with the moderation action and three
// platforms: Twitch, YouTube and Kick, all connected.
type fixture struct {
	t       *testing.T
	reg     *action.Registry
	harness *actiontest.Harness
	twitch  *connectortest.Platform
	youtube *connectortest.Platform
	kick    *connectortest.Platform
	store   *store
	mute    *mute
	logs    *logs
}

// newFixture returns a fixture. Create it inside synctest.Test.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		t:       t,
		twitch:  connectortest.New(platform.Twitch, connectortest.Features{}),
		youtube: connectortest.New(platform.YouTube, connectortest.Features{}),
		kick:    connectortest.New(platform.Kick, connectortest.Features{}),
		store:   &store{},
		mute:    &mute{},
		logs:    &logs{},
	}
	set, err := connector.NewSet(f.twitch, f.youtube, f.kick)
	require.NoError(t, err)
	f.reg = registry(t, moderation.Ports{Platforms: set, Users: f.store, Strikes: f.store, Mute: f.mute},
		slog.New(slog.NewTextHandler(f.logs, nil)))
	f.harness = actiontest.NewHarness(t, f.reg)
	return f
}

// registry returns the moderation action with p and a template engine that
// knows the arguments, the values of the run and the users.
func registry(t *testing.T, p moderation.Ports, logger *slog.Logger) *action.Registry {
	t.Helper()
	identifiers, err := template.NewRegistry(template.ArgumentFamily(), template.RunFamily(), template.UserFamily(nil))
	require.NoError(t, err)
	p.Templates, p.Logger = template.New(identifiers), logger
	ds, err := moderation.Descriptors(p)
	require.NoError(t, err)
	reg, err := action.NewRegistry(capability.Set{}, ds...)
	require.NoError(t, err)
	return reg
}

// action returns a moderation action of the document doc, without "type".
func (f *fixture) action(doc string) moderation.Moderation {
	f.t.Helper()
	d, ok := f.reg.Descriptor(moderation.TypeModeration)
	require.True(f.t, ok)
	a, err := d.Decode([]byte(doc), json.DefaultOptionsV2())
	require.NoError(f.t, err, doc)
	require.NoError(f.t, a.Validate(), doc)
	m, ok := a.(moderation.Moderation)
	require.True(f.t, ok)
	return m
}

// on returns a moderation action of kind k on the user.
func (f *fixture) on(k moderation.Kind, usr string) moderation.Moderation {
	return f.action(`{"kind":"` + string(k) + `","user":"` + usr + `"}`)
}

// start runs a command with the actions and the parameters p.
func (f *fixture) start(p engine.Params, actions ...command.Action) engine.Instance {
	return f.harness.Start(f.harness.Command("x", actions...), p)
}

// all returns the platforms of the fixture.
func (f *fixture) all() []*connectortest.Platform {
	return []*connectortest.Platform{f.twitch, f.youtube, f.kick}
}

// onTwitch returns the parameters of a run triggered on Twitch with the
// arguments.
func onTwitch(args ...string) engine.Params {
	return engine.Params{Platform: platform.Twitch, Args: args, ArgsText: strings.Join(args, " ")}
}

// timer returns the parameters of a run without a platform, e.g. of a
// timer.
func timer() engine.Params {
	return engine.Params{}
}

func TestConformance(t *testing.T) {
	t.Parallel()
	reg := registry(t, moderation.Ports{Platforms: &connector.Set{}, Users: &store{}, Strikes: &store{}, Mute: &mute{}},
		slog.New(slog.DiscardHandler))
	d, ok := reg.Descriptor(moderation.TypeModeration)
	require.True(t, ok)
	examples := []actiontest.Example{
		{Name: "timeout", Doc: `{"type":"moderation","kind":"timeout","user":"$targetusername","seconds":600,"reason":"Spam by $username"}`, Valid: true},
		{Name: "timeout with an expression", Doc: `{"type":"moderation","kind":"timeout","user":"@bob","seconds":"$arg2text * 60"}`, Valid: true},
		{Name: "longest timeout", Doc: `{"type":"moderation","kind":"timeout","user":"bob","seconds":1209600}`, Valid: true},
		{Name: "ban with a reason", Doc: `{"type":"moderation","kind":"ban","user":"bob","reason":"Spam"}`, Valid: true},
		{Name: "disabled", Doc: `{"type":"moderation","enabled":false,"kind":"ban","user":"bob"}`, Valid: true},
		{Name: "timeout without user", Doc: `{"type":"moderation","kind":"timeout","seconds":600}`},
		{Name: "timeout without seconds", Doc: `{"type":"moderation","kind":"timeout","user":"bob"}`},
		{Name: "no seconds", Doc: `{"type":"moderation","kind":"timeout","user":"bob","seconds":0}`},
		{Name: "too long", Doc: `{"type":"moderation","kind":"timeout","user":"bob","seconds":1209601}`},
		{Name: "fraction", Doc: `{"type":"moderation","kind":"timeout","user":"bob","seconds":1.5}`},
		{Name: "empty expression", Doc: `{"type":"moderation","kind":"timeout","user":"bob","seconds":""}`},
		{Name: "empty user", Doc: `{"type":"moderation","kind":"ban","user":""}`},
		{Name: "empty reason", Doc: `{"type":"moderation","kind":"ban","user":"bob","reason":""}`},
		{Name: "purge with seconds", Doc: `{"type":"moderation","kind":"purge","user":"bob","seconds":60}`},
		{Name: "purge with a reason", Doc: `{"type":"moderation","kind":"purge","user":"bob","reason":"x"}`},
		{Name: "mod with an empty reason", Doc: `{"type":"moderation","kind":"mod","user":"bob","reason":""}`},
		{Name: "clear chat with a user", Doc: `{"type":"moderation","kind":"clear_chat","user":"bob"}`},
		{Name: "reset strikes with an empty user", Doc: `{"type":"moderation","kind":"reset_strikes","user":""}`},
		{Name: "kind missing", Doc: `{"type":"moderation","user":"bob"}`},
		{Name: "unknown kind", Doc: `{"type":"moderation","kind":"vip","user":"bob"}`},
	}
	for _, k := range moderation.Kinds() {
		doc := `{"type":"moderation","kind":"` + string(k) + `"}`
		switch k {
		case moderation.KindTimeout:
			continue
		case moderation.KindPurge, moderation.KindBan, moderation.KindUnban, moderation.KindMod, moderation.KindUnmod,
			moderation.KindAddStrike, moderation.KindRemoveStrike:
			examples = append(examples, actiontest.Example{Name: string(k) + " without user", Doc: doc})
			doc = `{"type":"moderation","kind":"` + string(k) + `","user":"bob"}`
		case moderation.KindClearChat, moderation.KindResetStrikes, moderation.KindDisableChat, moderation.KindEnableChat:
		}
		examples = append(examples, actiontest.Example{Name: string(k), Doc: doc, Valid: true})
	}
	actiontest.Suite{Descriptor: d, Update: update(), Examples: examples}.Run(t)
}

// TestOnPlatform covers actions.md B80, B81 and B83: each kind acts on
// the platform of the run; the user is found among the known users, or
// else over the platform, with or without "@".
func TestOnPlatform(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		f.store.add(0, f.twitch.Account("bob"))
		f.twitch.AddAccounts(f.twitch.Account("carol"))
		in := f.start(onTwitch("@Bob", "5"),
			f.action(`{"kind":"timeout","user":"$arg1text","seconds":"$arg2text * 60","reason":"spam ($arg2text)"}`),
			f.action(`{"kind":"timeout","user":"carol","seconds":1}`),
			f.on(moderation.KindPurge, "bob"),
			f.action(`{"kind":"clear_chat"}`),
			f.action(`{"kind":"ban","user":"bob","reason":"  "}`),
			f.on(moderation.KindUnban, "bob"),
			f.on(moderation.KindMod, "carol"),
			f.on(moderation.KindUnmod, "@carol"),
		)
		assert.Empty(t, in.Errors)
		assert.Equal(t, []connectortest.Call{
			{Op: connectortest.OpTimeout, Target: "bob", Duration: 5 * time.Minute, Reason: "spam (5)"},
			{Op: connectortest.OpUserByLogin, Target: "carol"},
			{Op: connectortest.OpTimeout, Target: "carol", Duration: time.Second},
			{Op: connectortest.OpPurge, Target: "bob"},
			{Op: connectortest.OpClearChat},
			{Op: connectortest.OpBan, Target: "bob"},
			{Op: connectortest.OpUnban, Target: "bob"},
			{Op: connectortest.OpUserByLogin, Target: "carol"},
			{Op: connectortest.OpMod, Target: "carol"},
			{Op: connectortest.OpUserByLogin, Target: "carol"},
			{Op: connectortest.OpUnmod, Target: "carol"},
		}, f.twitch.Calls())
		assert.Empty(t, f.youtube.Calls())
		assert.Empty(t, f.kick.Calls())
		assert.Equal(t, 1, f.store.count(), "users found over the platform are not stored for these kinds")
	})
}

// TestUnknownUser covers actions.md B81: a user who is neither known nor
// on the platform lets the action fail.
func TestUnknownUser(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		in := f.start(onTwitch(), f.on(moderation.KindBan, "@dave"), f.on(moderation.KindBan, "$arg1text "),
			f.on(moderation.KindBan, "@"))
		require.Len(t, in.Errors, 3)
		assert.Equal(t, `user: unknown user "dave" on twitch`, in.Errors[0].Message)
		assert.Equal(t, `user: unknown user "$arg1text" on twitch`, in.Errors[1].Message)
		assert.Equal(t, `user: unknown user "" on twitch`, in.Errors[2].Message)
		assert.Equal(t, []connectortest.Op{connectortest.OpUserByLogin, connectortest.OpUserByLogin}, f.twitch.Ops())
	})
}

// TestWithoutPlatform covers actions.md B82: without a platform of the run
// the kinds without a user act on every connected platform; those with a
// user look for it on the connected platforms in their order and act only
// on the first that has it. A lookup that fails lets the action fail.
func TestWithoutPlatform(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		f.youtube.SetStatus(connector.Status{})
		f.store.add(0, f.twitch.Account("bob"))
		f.kick.AddAccounts(f.kick.Account("bob"), f.kick.Account("dave"))
		in := f.start(timer(), f.on(moderation.KindPurge, "bob"), f.action(`{"kind":"clear_chat"}`))
		assert.Empty(t, in.Errors)
		assert.Equal(t, []connectortest.Op{connectortest.OpPurge, connectortest.OpClearChat}, f.twitch.Ops())
		assert.Equal(t, []connectortest.Op{connectortest.OpClearChat}, f.kick.Ops(), "the namesake on kick is left alone")
		assert.Empty(t, f.youtube.Calls(), "not connected")

		f.twitch.ForgetCalls()
		f.kick.ForgetCalls()
		in = f.start(timer(), f.on(moderation.KindBan, "dave"))
		assert.Empty(t, in.Errors)
		assert.Equal(t, []connectortest.Op{connectortest.OpUserByLogin}, f.twitch.Ops())
		assert.Equal(t, []connectortest.Op{connectortest.OpUserByLogin, connectortest.OpBan}, f.kick.Ops())

		in = f.start(timer(), f.on(moderation.KindPurge, "carol"))
		require.Len(t, in.Errors, 1)
		assert.Equal(t, `user: unknown user "carol" on any connected platform`, in.Errors[0].Message)

		f.kick.ForgetCalls()
		f.twitch.Fail(connectortest.OpUserByLogin, errors.New("rate limited"))
		in = f.start(timer(), f.on(moderation.KindBan, "dave"))
		require.Len(t, in.Errors, 1)
		assert.Contains(t, in.Errors[0].Message, "user: failed on twitch: ")
		assert.Contains(t, in.Errors[0].Message, "rate limited")
		assert.Empty(t, f.kick.Calls(), "the first match may lie on twitch")
	})
}

// TestNotConnected: on a platform of the run that is not connected the
// action fails; without a platform of the run and without a connected
// platform, the kinds without a user do nothing, and the core logs it.
func TestNotConnected(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		for _, p := range f.all() {
			p.SetStatus(connector.Status{Bot: true})
		}
		f.store.add(0, f.twitch.Account("bob"))
		in := f.start(onTwitch(), f.on(moderation.KindPurge, "bob"))
		require.Len(t, in.Errors, 1)
		assert.Equal(t, "twitch: account not connected", in.Errors[0].Message)

		in = f.start(timer(), f.action(`{"kind":"clear_chat"}`), f.on(moderation.KindPurge, "bob"))
		require.Len(t, in.Errors, 1)
		assert.Equal(t, []int{2}, in.Errors[0].Path)
		assert.Contains(t, f.logs.String(), "moderation skipped: no platform connected")
		for _, p := range f.all() {
			assert.Empty(t, p.Calls(), p.Name())
		}

		in = f.start(engine.Params{Platform: "velora"}, f.action(`{"kind":"clear_chat"}`))
		require.Len(t, in.Errors, 1)
		assert.Equal(t, "velora: account not connected", in.Errors[0].Message, "a platform the profile does not have")
	})
}

// TestRefused covers actions.md B86 and B215: if the platform refuses, the
// action fails with the reason of the platform; on several platforms the
// others keep the change.
func TestRefused(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		f.twitch.AddAccounts(f.twitch.Account("streamer"))
		f.twitch.Fail(connectortest.OpTimeout, fmt.Errorf("%w: the broadcaster may not be timed out", connector.ErrRefused))
		in := f.start(onTwitch(), f.action(`{"kind":"timeout","user":"streamer","seconds":60}`))
		require.Len(t, in.Errors, 1)
		assert.Equal(t, "failed on twitch: twitch: refused by the platform: the broadcaster may not be timed out", in.Errors[0].Message)

		f.kick.Fail(connectortest.OpClearChat, errors.New("no rights"))
		in = f.start(timer(), f.action(`{"kind":"clear_chat"}`))
		require.Len(t, in.Errors, 1)
		assert.Equal(t, "failed on kick: kick: no rights", in.Errors[0].Message)
		assert.Equal(t, connectortest.OpClearChat, f.twitch.Calls()[len(f.twitch.Calls())-1].Op)
		assert.Equal(t, []connectortest.Op{connectortest.OpClearChat}, f.youtube.Ops())
	})
}

// TestLookupFails: an error of a lookup that is not "unknown user" lets
// the action fail.
func TestLookupFails(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		f.store.err = errors.New("database locked")
		in := f.start(onTwitch(), f.on(moderation.KindBan, "bob"), f.on(moderation.KindAddStrike, "bob"))
		require.Len(t, in.Errors, 2)
		assert.Equal(t, `user: failed on twitch: twitch: find account "bob" on twitch: database locked`, in.Errors[0].Message)
		assert.Equal(t, "user: database locked", in.Errors[1].Message)
		assert.Empty(t, f.twitch.Calls())
	})
}

// TestTimeoutRange covers actions.md B4 and B80: a duration from a template
// outside the range lets the action fail before the platform is asked.
func TestTimeoutRange(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		f.store.add(0, f.twitch.Account("bob"))
		timeout := f.action(`{"kind":"timeout","user":"bob","seconds":"$arg1text"}`)
		for _, arg := range []string{"0", "1209601", "1.5", "abc"} {
			in := f.start(onTwitch(arg), timeout)
			require.Len(t, in.Errors, 1, arg)
			assert.True(t, strings.HasPrefix(in.Errors[0].Message, "seconds: invalid action: "), in.Errors[0].Message)
		}
		assert.Empty(t, f.twitch.Calls())
	})
}

// TestStrikes covers actions.md B84: streamcrew counts strikes itself; a
// strike is removed down to 0, and reset_strikes resets every user.
func TestStrikes(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		f.store.add(2, f.twitch.Account("bob"))
		f.store.add(5, f.twitch.Account("carol"))
		add, remove := f.on(moderation.KindAddStrike, "$arg1text"), f.on(moderation.KindRemoveStrike, "$arg1text")

		in := f.start(onTwitch("@Bob"), add, add)
		assert.Empty(t, in.Errors)
		assert.Equal(t, int64(4), f.store.strikes(platform.Twitch, "bob"))
		in = f.start(onTwitch("bob"), remove, remove, remove, remove, remove)
		assert.Empty(t, in.Errors)
		assert.Equal(t, int64(0), f.store.strikes(platform.Twitch, "bob"), "not below 0")
		assert.Equal(t, int64(5), f.store.strikes(platform.Twitch, "carol"))

		in = f.start(onTwitch(), f.action(`{"kind":"reset_strikes"}`))
		assert.Empty(t, in.Errors)
		assert.Equal(t, int64(0), f.store.strikes(platform.Twitch, "carol"))
		assert.Empty(t, f.twitch.Calls(), "strikes are not the platform's")
	})
}

// TestStrikeLookup covers actions.md B81, B82 and B84: a user found only
// over the platform is stored for the strike; without a platform of the
// run only the first match counts; known users get strikes even if the
// platform of the run is not connected.
func TestStrikeLookup(t *testing.T) {
	t.Parallel()
	t.Run("over the platform", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			f := newFixture(t)
			f.twitch.AddAccounts(f.twitch.Account("dave"))
			in := f.start(onTwitch(), f.on(moderation.KindAddStrike, "dave"), f.on(moderation.KindAddStrike, "dave"))
			assert.Empty(t, in.Errors)
			assert.Equal(t, int64(2), f.store.strikes(platform.Twitch, "dave"))
			assert.Equal(t, 1, f.store.count())
			assert.Equal(t, []connectortest.Op{connectortest.OpUserByLogin}, f.twitch.Ops(), "the second time the core knows dave")
		})
	})
	t.Run("without a platform of the run", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			f := newFixture(t)
			f.store.add(0, f.twitch.Account("bob"), f.kick.Account("bob"))
			f.store.add(0, f.youtube.Account("bob"))
			in := f.start(timer(), f.on(moderation.KindAddStrike, "bob"))
			assert.Empty(t, in.Errors)
			assert.Equal(t, int64(1), f.store.strikes(platform.Twitch, "bob"), "the first match")
			assert.Equal(t, int64(0), f.store.strikes(platform.YouTube, "bob"), "another user of that name")
		})
	})
	t.Run("the platform of the run is not connected", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			f := newFixture(t)
			f.twitch.SetStatus(connector.Status{})
			f.twitch.AddAccounts(f.twitch.Account("dave"))
			f.store.add(1, f.twitch.Account("bob"))
			in := f.start(onTwitch(), f.on(moderation.KindAddStrike, "bob"), f.on(moderation.KindAddStrike, "dave"))
			require.Len(t, in.Errors, 1)
			assert.Equal(t, int64(2), f.store.strikes(platform.Twitch, "bob"))
			assert.Equal(t, `user: unknown user "dave" on twitch`, in.Errors[0].Message, "the platform is not asked")
			assert.Empty(t, f.twitch.Calls())
		})
	})
}

// TestMute covers actions.md B85: disable_chat and enable_chat switch the
// muted chat of the core.
func TestMute(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		in := f.start(timer(), f.action(`{"kind":"disable_chat"}`), f.action(`{"kind":"enable_chat"}`),
			f.action(`{"kind":"disable_chat"}`))
		assert.Empty(t, in.Errors)
		assert.Equal(t, []string{"mute", "unmute", "mute"}, f.mute.get())
		for _, p := range f.all() {
			assert.Empty(t, p.Calls(), p.Name())
		}
	})
}

func TestPorts(t *testing.T) {
	t.Parallel()
	full := moderation.Ports{
		Templates: template.New(nil),
		Platforms: &connector.Set{},
		Users:     &store{},
		Strikes:   &store{},
		Mute:      &mute{},
		Logger:    slog.New(slog.DiscardHandler),
	}
	_, err := moderation.Descriptors(full)
	require.NoError(t, err)
	for name, edit := range map[string]func(*moderation.Ports){
		"templates": func(p *moderation.Ports) { p.Templates = nil },
		"platforms": func(p *moderation.Ports) { p.Platforms = nil },
		"users":     func(p *moderation.Ports) { p.Users = nil },
		"strikes":   func(p *moderation.Ports) { p.Strikes = nil },
		"mute":      func(p *moderation.Ports) { p.Mute = nil },
		"logger":    func(p *moderation.Ports) { p.Logger = nil },
	} {
		p := full
		edit(&p)
		_, err := moderation.Descriptors(p)
		require.Error(t, err, name)
	}
}
