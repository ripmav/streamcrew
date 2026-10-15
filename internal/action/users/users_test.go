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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/actiontest"
	"github.com/ripmav/streamcrew/internal/action/users"
	"github.com/ripmav/streamcrew/internal/capability"
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

// store is a fake of the users the core knows, as the run's lookup sees
// them (engine.Users); it matches by login name before by platform ID, as
// the user service does (actions.md, B91).
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
		if slices.ContainsFunc(u.Identities, func(i user.Identity) bool { return i.Platform == p && strings.EqualFold(i.Login, name) }) {
			return u, true, nil
		}
	}
	for _, u := range s.users {
		if slices.ContainsFunc(u.Identities, func(i user.Identity) bool { return i.Platform == p && i.PlatformUserID == name }) {
			return u, true, nil
		}
	}
	return user.User{}, false, nil
}

// add adds a user with the accounts.
func (s *store) add(accounts ...user.Identity) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users = append(s.users, user.User{ID: id.New(), Identities: accounts})
}

// fixture is a running engine with the user lookup.
type fixture struct {
	t         *testing.T
	reg       *action.Registry
	harness   *actiontest.Harness
	templates *template.Engine
	store     *store
	logs      *logs
	lines     *lines
}

// newFixture returns a fixture. Create it inside synctest.Test.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, store: &store{}, logs: &logs{}, lines: &lines{}}
	f.reg, f.templates = registry(t, slog.New(slog.NewTextHandler(f.logs, nil)))
	// The run finds known users through the engine (spec command-engine.md,
	// B17).
	f.harness = actiontest.NewHarnessWith(t, f.reg, actiontest.NewCommands(), engine.WithUsers(f.store))
	return f
}

// registry returns the user lookup with a template engine that knows the
// arguments and the values of the run.
func registry(t *testing.T, logger *slog.Logger) (*action.Registry, *template.Engine) {
	t.Helper()
	identifiers, err := template.NewRegistry(template.ArgumentFamily(), template.RunFamily())
	require.NoError(t, err)
	templates := template.New(identifiers)
	ds, err := users.Descriptors(users.Ports{Templates: templates, Logger: logger})
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

// TestConformance covers the golden files of the user lookup.
func TestConformance(t *testing.T) {
	t.Parallel()
	reg, _ := registry(t, slog.New(slog.DiscardHandler))
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

// TestKnown covers actions.md B91 and B93: the action takes the user the
// lookup of the run found, by login name, regardless of case and with or
// without "@", or by platform ID, and sets the result values of the
// account on its platform.
func TestKnown(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		f.store.add(bob(), user.Identity{Platform: platform.YouTube, PlatformUserID: "id-bob", Login: "bob", DisplayName: "BOB"})
		f.store.add(user.Identity{Platform: platform.Twitch, PlatformUserID: "bob", Login: "robert", DisplayName: "Robert"})
		in := f.start([]string{"@BOB"},
			f.lookup(platform.Twitch, "$arg1text"), f.show(),
			f.lookup(platform.Twitch, "1001"), f.show(),
			f.lookup(platform.YouTube, " bob "), f.show(),
		)
		assert.Empty(t, in.Errors)
		assert.Equal(t, []string{bobResults, bobResults, "bob|BOB|id-bob||True"}, f.lines.get(),
			"the name wins over the ID of another user")
	})
}

// TestNoHit covers actions.md B93: without a hit the result values are
// empty and $lookupsuccess is False; they replace those of an earlier
// lookup, and the action does not fail. An empty name is not looked up.
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
		assert.Contains(t, f.logs.String(), "user lookup without result: empty name")
	})
}

// TestFails covers actions.md B6: an error of the lookup of the run lets
// the action fail without result values.
func TestFails(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		f.store.err = boom
		in := f.start(nil, f.lookup(platform.Twitch, "bob"), f.show())
		require.Len(t, in.Errors, 1)
		assert.Equal(t, `user: look up user "bob" on twitch after 3 attempts: boom`, in.Errors[0].Message)
		assert.Equal(t, []string{results}, f.lines.get(), "no result values")
	})
}

func TestPorts(t *testing.T) {
	t.Parallel()
	full := users.Ports{
		Templates: template.New(nil),
		Logger:    slog.New(slog.DiscardHandler),
	}
	_, err := users.Descriptors(full)
	require.NoError(t, err)
	for name, edit := range map[string]func(*users.Ports){
		"templates": func(p *users.Ports) { p.Templates = nil },
		"logger":    func(p *users.Ports) { p.Logger = nil },
	} {
		p := full
		edit(&p)
		_, err := users.Descriptors(p)
		require.Error(t, err, name)
	}
}
