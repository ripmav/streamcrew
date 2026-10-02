// SPDX-License-Identifier: MIT

package users_test

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
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
	"github.com/ripmav/streamcrew/internal/action/users"
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

// store is a fake of the users the core knows.
type store struct {
	mu        sync.Mutex
	users     []user.User
	err       error
	upsertErr error
	upserted  []user.Identity
}

func (s *store) UserByName(_ context.Context, p platform.Name, name string) (user.User, bool, error) {
	return s.find(func(i user.Identity) bool { return i.Platform == p && strings.EqualFold(i.Login, name) })
}

func (s *store) UserByPlatformID(_ context.Context, p platform.Name, platformUserID string) (user.User, bool, error) {
	return s.find(func(i user.Identity) bool { return i.Platform == p && i.PlatformUserID == platformUserID })
}

func (s *store) find(match func(user.Identity) bool) (user.User, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return user.User{}, false, s.err
	}
	for _, u := range s.users {
		if slices.ContainsFunc(u.Identities, match) {
			return u, true, nil
		}
	}
	return user.User{}, false, nil
}

func (s *store) UpsertIdentity(_ context.Context, ident user.Identity) (user.User, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.upsertErr != nil {
		return user.User{}, false, s.upsertErr
	}
	s.upserted = append(s.upserted, ident)
	u := user.User{ID: id.New(), Identities: []user.Identity{ident}}
	s.users = append(s.users, u)
	return u, true, nil
}

// add adds a user with the accounts.
func (s *store) add(accounts ...user.Identity) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users = append(s.users, user.User{ID: id.New(), Identities: accounts})
}

// stored returns the accounts stored after a lookup over a platform.
func (s *store) stored() []user.Identity {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.upserted)
}

// fixture is a running engine with the user lookup, Twitch and YouTube.
type fixture struct {
	t         *testing.T
	reg       *action.Registry
	harness   *actiontest.Harness
	templates *template.Engine
	twitch    *connectortest.Platform
	youtube   *connectortest.Platform
	store     *store
	logs      *logs
	lines     *lines
}

// newFixture returns a fixture. Create it inside synctest.Test.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		t:       t,
		twitch:  connectortest.New(platform.Twitch, connectortest.Features{}),
		youtube: connectortest.New(platform.YouTube, connectortest.Features{}),
		store:   &store{},
		logs:    &logs{},
		lines:   &lines{},
	}
	set, err := connector.NewSet(f.twitch, f.youtube)
	require.NoError(t, err)
	f.reg, f.templates = registry(t, set, f.store, slog.New(slog.NewTextHandler(f.logs, nil)))
	f.harness = actiontest.NewHarness(t, f.reg)
	return f
}

// registry returns the user lookup with a template engine that knows the
// arguments and the values of the run.
func registry(t *testing.T, platforms users.Platforms, us users.Users, logger *slog.Logger) (*action.Registry, *template.Engine) {
	t.Helper()
	identifiers, err := template.NewRegistry(template.ArgumentFamily(), template.RunFamily())
	require.NoError(t, err)
	templates := template.New(identifiers)
	ds, err := users.Descriptors(users.Ports{Templates: templates, Platforms: platforms, Users: us, Logger: logger})
	require.NoError(t, err)
	reg, err := action.NewRegistry(capability.Set{}, ds...)
	require.NoError(t, err)
	return reg, templates
}

// lookup returns a user lookup of the user on the platform.
func (f *fixture) lookup(p platform.Name, usr string) users.UserLookup {
	f.t.Helper()
	d, ok := f.reg.Descriptor(users.TypeUserLookup)
	require.True(f.t, ok)
	a, err := d.Decode([]byte(`{"platform":"`+string(p)+`","user":"`+usr+`"}`), json.DefaultOptionsV2())
	require.NoError(f.t, err)
	l, ok := a.(users.UserLookup)
	require.True(f.t, ok)
	return l
}

// results is a template of all result values.
const results = "$lookupusername|$lookupdisplayname|$lookupid|$lookupavatarurl|$lookupsuccess"

// show returns an action that renders the result values and records them.
func (f *fixture) show() command.Action {
	return probe{fn: func(ctx context.Context, run *engine.Run) error {
		out, err := f.templates.Render(ctx, template.Parse(results), run.Scope(), template.Text)
		f.lines.add(out)
		return err
	}}
}

// start runs a command with the actions and the arguments.
func (f *fixture) start(args []string, actions ...command.Action) engine.Instance {
	return f.harness.Start(f.harness.Command("x", actions...),
		engine.Params{Platform: platform.Twitch, Args: args, ArgsText: strings.Join(args, " ")})
}

// lines records what show renders.
type lines struct {
	mu   sync.Mutex
	list []string
}

func (l *lines) add(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.list = append(l.list, line)
}

func (l *lines) get() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.list)
}

// probe is an action that runs fn.
type probe struct {
	fn func(ctx context.Context, run *engine.Run) error
}

func (probe) DocType() string                                      { return "probe" }
func (probe) Validate() error                                      { return nil }
func (probe) Enabled() bool                                        { return true }
func (p probe) Perform(ctx context.Context, run *engine.Run) error { return p.fn(ctx, run) }

// bob is an account on Twitch with all values.
func bob() user.Identity {
	return user.Identity{
		Platform: platform.Twitch, PlatformUserID: "1001", Login: "bob", DisplayName: "Bob",
		AvatarURL: "https://example.invalid/bob.png",
	}
}

// bobResults are the result values of bob.
const bobResults = "bob|Bob|1001|https://example.invalid/bob.png|True"

// noResults are the result values without a hit.
const noResults = "||||False"

func TestConformance(t *testing.T) {
	t.Parallel()
	reg, _ := registry(t, &connector.Set{}, &store{}, slog.New(slog.DiscardHandler))
	d, ok := reg.Descriptor(users.TypeUserLookup)
	require.True(t, ok)
	assert.Equal(t, users.Results(), d.Results)
	actiontest.Suite{Descriptor: d, Update: update(), Examples: []actiontest.Example{
		{Name: "lookup", Doc: `{"type":"user_lookup","platform":"twitch","user":"$arg1text"}`, Valid: true},
		{Name: "disabled", Doc: `{"type":"user_lookup","enabled":false,"platform":"youtube","user":"@bob"}`, Valid: true},
		{Name: "platform without adapter", Doc: `{"type":"user_lookup","platform":"velora","user":"bob"}`, Valid: true},
		{Name: "platform missing", Doc: `{"type":"user_lookup","user":"bob"}`},
		{Name: "platform in uppercase", Doc: `{"type":"user_lookup","platform":"Twitch","user":"bob"}`},
		{Name: "user missing", Doc: `{"type":"user_lookup","platform":"twitch"}`},
		{Name: "empty user", Doc: `{"type":"user_lookup","platform":"twitch","user":""}`},
		{Name: "result name", Doc: `{"type":"user_lookup","platform":"twitch","user":"bob","result":"x"}`},
	}}.Run(t)

	for _, name := range users.Results() {
		fixed, ok := reg.Reserved(strings.ToUpper(name))
		assert.True(t, ok, name)
		assert.Equal(t, name, fixed)
	}
}

// TestKnown covers actions.md B91 and B93: a known user is found by login
// name, regardless of case and with or without "@", or by platform ID,
// without asking the platform.
func TestKnown(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		f.store.add(bob(), f.youtube.Account("bob"))
		f.store.add(user.Identity{Platform: platform.Twitch, PlatformUserID: "bob", Login: "robert", DisplayName: "Robert"})
		in := f.start([]string{"@BOB"},
			f.lookup(platform.Twitch, "$arg1text"), f.show(),
			f.lookup(platform.Twitch, "1001"), f.show(),
			f.lookup(platform.YouTube, " bob "), f.show(),
		)
		assert.Empty(t, in.Errors)
		assert.Equal(t, []string{bobResults, bobResults, "bob|BOB|id-bob||True"}, f.lines.get(),
			"the name wins over the ID of another user")
		assert.Empty(t, f.twitch.Calls())
		assert.Empty(t, f.youtube.Calls())
		assert.Empty(t, f.store.stored())
	})
}

// TestOverPlatform covers actions.md B91: a user the core does not know is
// looked up over the platform by name, then by ID, and stored.
func TestOverPlatform(t *testing.T) {
	t.Parallel()
	t.Run("by name", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			f := newFixture(t)
			f.twitch.AddAccounts(bob())
			in := f.start(nil, f.lookup(platform.Twitch, "@Bob"), f.show())
			assert.Empty(t, in.Errors)
			assert.Equal(t, []string{bobResults}, f.lines.get())
			assert.Equal(t, []connectortest.Call{{Op: connectortest.OpUserByLogin, Target: "Bob"}}, f.twitch.Calls())
			assert.Equal(t, []user.Identity{bob()}, f.store.stored())
		})
	})
	t.Run("by ID", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			f := newFixture(t)
			f.twitch.AddAccounts(bob())
			in := f.start(nil, f.lookup(platform.Twitch, "1001"), f.show())
			assert.Empty(t, in.Errors)
			assert.Equal(t, []string{bobResults}, f.lines.get())
			assert.Equal(t, []connectortest.Op{connectortest.OpUserByLogin, connectortest.OpUserByID}, f.twitch.Ops())
			assert.Equal(t, []user.Identity{bob()}, f.store.stored())
		})
	})
}

// TestNoHit covers actions.md B93: without a hit the result values are
// empty and $lookupsuccess is False; they replace those of an earlier
// lookup, and the action does not fail.
func TestNoHit(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		f.store.add(bob())
		in := f.start(nil,
			f.lookup(platform.Twitch, "bob"), f.show(),
			f.lookup(platform.Twitch, "carol"), f.show(),
			f.lookup(platform.Twitch, "@"), f.show(),
		)
		assert.Empty(t, in.Errors)
		assert.Equal(t, []string{bobResults, noResults, noResults}, f.lines.get())
		assert.Equal(t, []connectortest.Op{connectortest.OpUserByLogin, connectortest.OpUserByID}, f.twitch.Ops(),
			"an empty name is not looked up")
		assert.Contains(t, f.logs.String(), "user lookup without result: empty name")
	})
}

// TestLimit covers actions.md B92 and B216: over the platforms the core
// looks up once per minute, across all user lookups; a lookup in between
// is without result, and the core logs why. Known users are not limited.
func TestLimit(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		f.store.add(bob())
		f.youtube.AddAccounts(f.youtube.Account("carol"))
		in := f.start(nil,
			f.lookup(platform.Twitch, "dave"), f.show(),
			f.lookup(platform.YouTube, "carol"), f.show(),
			f.lookup(platform.Twitch, "bob"), f.show(),
		)
		assert.Empty(t, in.Errors)
		assert.Equal(t, []string{noResults, noResults, bobResults}, f.lines.get())
		assert.Empty(t, f.youtube.Calls(), "the limit is across platforms")
		assert.Contains(t, f.logs.String(), "user lookup without result: one lookup over a platform per minute")

		time.Sleep(users.LookupInterval - time.Second)
		f.start(nil, f.lookup(platform.YouTube, "carol"), f.show())
		assert.Empty(t, f.youtube.Calls(), "still within the minute")

		time.Sleep(time.Second)
		f.start(nil, f.lookup(platform.YouTube, "carol"), f.show())
		assert.Equal(t, []connectortest.Op{connectortest.OpUserByLogin}, f.youtube.Ops())
		assert.Equal(t, "carol|CAROL|id-carol||True", f.lines.get()[4])
	})
}

// TestNotConnected covers actions.md B92: on a platform that is not
// connected, or that the profile does not have, the lookup is without
// result and the core logs why; it does not use up the minute.
func TestNotConnected(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		f.youtube.SetStatus(connector.Status{Bot: true})
		f.twitch.AddAccounts(bob())
		in := f.start(nil,
			f.lookup(platform.YouTube, "bob"), f.show(),
			f.lookup("velora", "bob"), f.show(),
			f.lookup(platform.Twitch, "bob"), f.show(),
		)
		assert.Empty(t, in.Errors)
		assert.Equal(t, []string{noResults, noResults, bobResults}, f.lines.get())
		assert.Equal(t, 2, strings.Count(f.logs.String(), "user lookup without result: platform not connected"))
		assert.Empty(t, f.youtube.Calls())
	})
}

// TestFails covers actions.md B6: errors of the known users, of the
// platform and of storing let the action fail without result values.
func TestFails(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	for _, tc := range []struct {
		name  string
		setup func(f *fixture)
		want  string
	}{
		{"known users", func(f *fixture) { f.store.err = boom }, "user: boom"},
		{"platform", func(f *fixture) { f.twitch.Fail(connectortest.OpUserByLogin, boom) }, `user: look up "bob" on twitch: boom`},
		{"platform by ID", func(f *fixture) { f.twitch.Fail(connectortest.OpUserByID, boom) }, `user: look up "bob" on twitch: boom`},
		{"storing", func(f *fixture) {
			f.twitch.AddAccounts(bob())
			f.store.upsertErr = boom
		}, `user: store "bob" of twitch: boom`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				f := newFixture(t)
				tc.setup(f)
				in := f.start(nil, f.lookup(platform.Twitch, "bob"), f.show())
				require.Len(t, in.Errors, 1)
				assert.Equal(t, tc.want, in.Errors[0].Message)
				assert.Equal(t, []string{results}, f.lines.get(), "no result values")
			})
		})
	}
}

func TestPorts(t *testing.T) {
	t.Parallel()
	full := users.Ports{
		Templates: template.New(nil),
		Platforms: &connector.Set{},
		Users:     &store{},
		Logger:    slog.New(slog.DiscardHandler),
	}
	_, err := users.Descriptors(full)
	require.NoError(t, err)
	for name, edit := range map[string]func(*users.Ports){
		"templates": func(p *users.Ports) { p.Templates = nil },
		"platforms": func(p *users.Ports) { p.Platforms = nil },
		"users":     func(p *users.Ports) { p.Users = nil },
		"logger":    func(p *users.Ports) { p.Logger = nil },
	} {
		p := full
		edit(&p)
		_, err := users.Descriptors(p)
		require.Error(t, err, name)
	}
}
