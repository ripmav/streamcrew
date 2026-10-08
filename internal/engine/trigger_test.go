// SPDX-License-Identifier: MIT

package engine_test

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/polydoc"
	"github.com/ripmav/streamcrew/internal/settings"
)

// requirements is a fake of engine.Requirements.
type requirements struct {
	mu sync.Mutex
	// decisions has the decision per command name; without one, the
	// requirements are met for the run itself.
	decisions map[string]engine.Decision
	// runs, if set, returns the runs of met requirements.
	runs func(p engine.Params) []engine.Params
	// gate, if set, holds Apply back until it is closed.
	gate      chan struct{}
	err       error
	notifyErr error
	applied   []string
	notified  []string
}

func (r *requirements) Apply(_ context.Context, cmd command.Command, p engine.Params) (engine.Decision, error) {
	r.mu.Lock()
	gate := r.gate
	r.mu.Unlock()
	if gate != nil {
		<-gate
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.applied = append(r.applied, cmd.Name)
	if r.err != nil {
		return engine.Decision{}, r.err
	}
	if d, ok := r.decisions[cmd.Name]; ok {
		return d, nil
	}
	if r.runs != nil {
		return engine.Met(r.runs(p)...), nil
	}
	return engine.Met(p), nil
}

func (r *requirements) Notify(_ context.Context, cmd command.Command, _ engine.Params, rej engine.Rejection) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notified = append(r.notified, cmd.Name+" "+rej.Requirement)
	return r.notifyErr
}

// decide sets the decision for the command name; met removes it.
func (r *requirements) decide(name string, d engine.Decision, met bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if met {
		delete(r.decisions, name)
		return
	}
	r.decisions[name] = d
}

func (r *requirements) appliedTo() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.applied)
}

// messages returns the notifications since the last call.
func (r *requirements) messages() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.notified
	r.notified = nil
	return m
}

func newRequirements() *requirements {
	return &requirements{decisions: make(map[string]engine.Decision)}
}

// rejected returns a rejection of requirement that tells the user.
func rejected(requirement string) engine.Decision {
	return engine.Rejected(engine.Rejection{Requirement: requirement, Reason: "not now", Tell: true})
}

// trigger triggers cmd from the chat and waits until the engine has nothing
// more to do.
func (f *fixture) trigger(cmd command.Command, p engine.Params) (engine.Result, error) {
	f.t.Helper()
	res, err := f.engine.Trigger(f.t.Context(), engine.Request{Command: cmd, Source: engine.SourceChat, Params: p})
	synctest.Wait()
	return res, err
}

// TestTrigger covers B10: a command whose requirements are met is queued
// after they were applied.
func TestTrigger(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		reqs := newRequirements()
		f := newFixture(t, settings.LockPerCommandType, engine.WithRequirements(reqs))
		defer f.stop()

		res, err := f.trigger(f.command("hug", command.KindChat, f.journal.note("hug")), engine.Params{})
		require.NoError(t, err)
		assert.Equal(t, engine.OutcomeQueued, res.Outcome)
		require.Len(t, res.Instances, 1)
		assert.Zero(t, res.Dropped)
		in, _ := f.engine.Instance(res.Instances[0])
		assert.Equal(t, engine.SourceChat, in.Source)
		assert.Equal(t, engine.StateCompleted, in.State)
		assert.Equal(t, []string{"hug"}, reqs.appliedTo())
		assert.Equal(t, []string{"hug"}, f.journal.get())

		_, err = f.engine.Trigger(t.Context(), engine.Request{Command: f.command("x", command.KindChat), Source: engine.SourceReplay})
		require.ErrorIs(t, err, engine.ErrInvalidSource)
		invalid := f.command("x", command.KindChat)
		invalid.ErrorPolicy = ""
		_, err = f.trigger(invalid, engine.Params{})
		require.ErrorIs(t, err, engine.ErrInvalidCommand)
	})
}

// TestTriggerDisabled covers B14: a disabled command is never triggered
// automatically, but it can be started by hand without its requirements.
func TestTriggerDisabled(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		reqs := newRequirements()
		f := newFixture(t, settings.LockPerCommandType, engine.WithRequirements(reqs))
		defer f.stop()

		cmd := f.command("off", command.KindChat, f.journal.note("off"))
		cmd.Enabled = false
		res, err := f.trigger(cmd, engine.Params{})
		require.NoError(t, err)
		assert.Equal(t, engine.Result{Outcome: engine.OutcomeDisabled}, res)

		reqs.decide("off", rejected("role"), false)
		instanceID := f.start(cmd, engine.Params{})
		assert.Equal(t, engine.StateCompleted, f.state(instanceID))
		assert.Empty(t, reqs.appliedTo(), "no requirements for a start by hand")
	})
}

// TestRequirementNotMet covers B11: no instance; the user is told why if
// the rejection says so. Waiting is not a rejection.
func TestRequirementNotMet(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		reqs := newRequirements()
		f := newFixture(t, settings.LockPerCommandType, engine.WithRequirements(reqs))
		defer f.stop()

		told := engine.Rejection{Requirement: "cooldown", Reason: "wait 5 s", Tell: true}
		reqs.decide("hug", engine.Rejected(told), false)
		res, err := f.trigger(f.command("hug", command.KindChat, f.journal.note("hug")), engine.Params{})
		require.NoError(t, err)
		assert.Equal(t, engine.Result{Outcome: engine.OutcomeRejected, Rejection: told}, res)
		assert.Equal(t, []string{"hug cooldown"}, reqs.messages())

		silent := engine.Rejection{Requirement: "role", Reason: "mods only"}
		reqs.decide("silent", engine.Rejected(silent), false)
		res, err = f.trigger(f.command("silent", command.KindTimer), engine.Params{})
		require.NoError(t, err)
		assert.Equal(t, engine.Result{Outcome: engine.OutcomeRejected, Rejection: silent}, res)
		assert.Empty(t, reqs.messages(), "a rejection without Tell tells nobody")

		reqs.decide("raid", engine.Waiting(), false)
		res, err = f.trigger(f.command("raid", command.KindChat), engine.Params{})
		require.NoError(t, err)
		assert.Equal(t, engine.Result{Outcome: engine.OutcomeWaiting}, res)
		assert.Empty(t, reqs.messages())

		assert.Empty(t, f.engine.History())
		assert.Empty(t, f.journal.get())

		reqs.err = errors.New("database locked")
		_, err = f.trigger(f.command("broken", command.KindChat), engine.Params{})
		require.ErrorIs(t, err, reqs.err)
	})
}

// TestInvalidDecision: a decision that contradicts itself is an error, not
// a guess.
func TestInvalidDecision(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		reqs := newRequirements()
		f := newFixture(t, settings.LockPerCommandType, engine.WithRequirements(reqs))
		defer f.stop()

		rejection := engine.Rejection{Requirement: "role", Reason: "no", Tell: true}
		for name, d := range map[string]engine.Decision{
			"met without a run":      engine.Met(),
			"met with a rejection":   {Verdict: engine.VerdictMet, Runs: []engine.Params{{}}, Rejection: rejection},
			"met with invalid run":   engine.Met(engine.Params{Args: []string{"a"}}),
			"waiting with runs":      {Verdict: engine.VerdictWaiting, Runs: []engine.Params{{}}},
			"waiting with rejection": {Verdict: engine.VerdictWaiting, Rejection: rejection},
			"rejected without one":   {Verdict: engine.VerdictRejected},
			"rejected without text":  engine.Rejected(engine.Rejection{Requirement: "role", Tell: true}),
			"rejected with runs":     {Verdict: engine.VerdictRejected, Runs: []engine.Params{{}}, Rejection: rejection},
			"no verdict":             {},
			"unknown verdict":        {Verdict: "maybe"},
		} {
			reqs.decide("x", d, false)
			_, err := f.trigger(f.command("x", command.KindChat), engine.Params{})
			require.ErrorIs(t, err, engine.ErrInvalidDecision, name)
		}
		assert.Empty(t, reqs.messages())
		assert.Empty(t, f.engine.History())
	})
}

// TestErrorCooldown covers B12 and B13 in all three modes.
func TestErrorCooldown(t *testing.T) {
	t.Parallel()
	type step struct {
		wait    time.Duration
		command string
		unmet   string // empty: the requirements are met
		message bool
	}
	for _, tc := range []struct {
		mode  settings.ErrorCooldown
		steps []step
	}{
		{settings.ErrorCooldownPerCommand, []step{
			{command: "a", unmet: "cooldown", message: true},
			{command: "a", unmet: "cooldown"},
			{command: "a", unmet: "role", message: true},
			{command: "b", unmet: "cooldown", message: true},
			{wait: 9 * time.Second, command: "a", unmet: "cooldown"},
			{wait: time.Second, command: "a", unmet: "cooldown", message: true},
			{command: "b", unmet: "cooldown", message: true},
			{command: "b"}, // B13: met and queued
			{command: "b", unmet: "cooldown", message: true},
			{command: "a", unmet: "cooldown"}, // the reset of b leaves a as it is
		}},
		{settings.ErrorCooldownGlobal, []step{
			{command: "a", unmet: "cooldown", message: true},
			{command: "b", unmet: "role"},
			{command: "a"},
			{command: "a", unmet: "cooldown"},
			{wait: 10 * time.Second, command: "b", unmet: "role", message: true},
		}},
		{settings.ErrorCooldownOff, []step{
			{command: "a", unmet: "cooldown", message: true},
			{command: "a", unmet: "cooldown", message: true},
		}},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				reqs := newRequirements()
				f := newFixture(t, settings.LockNone, engine.WithRequirements(reqs))
				defer f.stop()
				f.configs.mu.Lock()
				f.configs.cfg.Commands.ErrorCooldown = tc.mode
				f.configs.cfg.Commands.ErrorCooldownDuration = polydoc.Duration(10 * time.Second)
				f.configs.mu.Unlock()

				cmds := map[string]command.Command{
					"a": f.command("a", command.KindChat),
					"b": f.command("b", command.KindChat),
				}
				for i, s := range tc.steps {
					time.Sleep(s.wait)
					reqs.decide(s.command, rejected(s.unmet), s.unmet == "")
					res, err := f.trigger(cmds[s.command], engine.Params{})
					require.NoError(t, err, "step %d", i)
					want := engine.OutcomeQueued
					if s.unmet != "" {
						want = engine.OutcomeRejected
					}
					assert.Equal(t, want, res.Outcome, "step %d", i)
					var messages []string
					if s.message {
						messages = []string{s.command + " " + s.unmet}
					}
					assert.Equal(t, messages, reqs.messages(), "step %d", i)
				}
			})
		})
	}
}

// TestNotifyFails: a message that cannot be sent is logged.
func TestNotifyFails(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		reqs := newRequirements()
		reqs.notifyErr = errors.New("chat offline")
		logs := &records{}
		f := newFixture(t, settings.LockPerCommandType, engine.WithRequirements(reqs), engine.WithLogger(slog.New(logs)))
		defer f.stop()

		reqs.decide("x", rejected("role"), false)
		res, err := f.trigger(f.command("x", command.KindChat), engine.Params{})
		require.NoError(t, err)
		assert.Equal(t, engine.OutcomeRejected, res.Outcome)
		assert.Contains(t, logs.messages(), "telling the user about an unmet requirement failed")
	})
}

// TestQueueFullBeforeRequirements covers B15 and B108: a full queue drops a
// triggered command before its requirements are checked.
func TestQueueFullBeforeRequirements(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		reqs := newRequirements()
		f := newFixture(t, settings.LockSingular, engine.WithRequirements(reqs))
		defer f.stop()

		release := make(chan struct{})
		defer close(release)
		f.start(f.command("holder", command.KindChat, f.journal.hold("x", "holder", release)), engine.Params{})
		waiting := f.command("waiting", command.KindChat, f.journal.note("waiting"))
		for range engine.MaxPending - 1 {
			_, err := f.engine.Start(t.Context(), waiting, engine.Params{})
			require.NoError(t, err)
		}

		// The last place goes to a trigger whose requirements are checked.
		reqs.gate = make(chan struct{})
		result := make(chan error, 1)
		go func() {
			_, err := f.engine.Trigger(t.Context(), engine.Request{Command: waiting, Source: engine.SourceChat})
			result <- err
		}()
		synctest.Wait()
		_, err := f.engine.Start(t.Context(), waiting, engine.Params{})
		require.ErrorIs(t, err, engine.ErrQueueFull, "the place is taken")
		_, err = f.trigger(f.command("costly", command.KindChat), engine.Params{})
		require.ErrorIs(t, err, engine.ErrQueueFull)

		close(reqs.gate)
		require.NoError(t, <-result)
		assert.Equal(t, []string{"waiting"}, reqs.appliedTo(), "costly was dropped before its requirements")
	})
}

// TestRunnerParams covers B82: a threshold that runs the command for each
// user gives one instance per user; runs that no longer fit are dropped.
func TestRunnerParams(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		reqs := newRequirements()
		f := newFixture(t, settings.LockSingular, engine.WithRequirements(reqs))
		defer f.stop()

		users := []*user.User{{ID: id.New()}, {ID: id.New()}, {ID: id.New()}}
		reqs.runs = func(p engine.Params) []engine.Params {
			var runs []engine.Params
			for _, u := range users {
				p.User = u
				runs = append(runs, p)
			}
			return runs
		}
		raid := f.command("raid", command.KindChat, f.journal.note("raid"))
		res, err := f.trigger(raid, engine.Params{Args: []string{"go"}, ArgsText: "go"})
		require.NoError(t, err)
		require.Len(t, res.Instances, 3)
		for i, instanceID := range res.Instances {
			in, _ := f.engine.Instance(instanceID)
			assert.Equal(t, users[i].ID, in.UserID)
			assert.Equal(t, []string{"go"}, in.Args)
		}

		release := make(chan struct{})
		defer close(release)
		f.start(f.command("holder", command.KindChat, f.journal.hold("x", "holder", release)), engine.Params{})
		for range engine.MaxPending - 2 {
			_, err := f.engine.Start(t.Context(), raid, engine.Params{})
			require.NoError(t, err)
		}
		res, err = f.trigger(raid, engine.Params{})
		require.NoError(t, err)
		assert.Equal(t, engine.OutcomeQueued, res.Outcome)
		assert.Len(t, res.Instances, 2)
		assert.Equal(t, 1, res.Dropped)
	})
}

// TestEntrancePause covers B41: while entrance commands are paused, they are
// not queued; other commands are.
func TestEntrancePause(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerCommandType)
		defer f.stop()
		ctx := t.Context()

		welcome := f.command("welcome", command.KindChat, f.journal.note("welcome"))
		entrance := engine.Request{Command: welcome, Source: engine.SourceChat, Entrance: true}
		require.NoError(t, f.engine.Pause(ctx, engine.PauseEntrance))
		assert.True(t, paused(t, f.engine, engine.PauseEntrance))
		assert.False(t, paused(t, f.engine, engine.PauseAll))

		res, err := f.engine.Trigger(ctx, entrance)
		require.NoError(t, err)
		assert.Equal(t, engine.Result{Outcome: engine.OutcomeEntrancePaused}, res)
		res, err = f.trigger(welcome, engine.Params{})
		require.NoError(t, err)
		assert.Equal(t, engine.OutcomeQueued, res.Outcome, "not as an entrance command")

		require.NoError(t, f.engine.Resume(ctx, engine.PauseEntrance))
		res, err = f.engine.Trigger(ctx, entrance)
		require.NoError(t, err)
		assert.Equal(t, engine.OutcomeQueued, res.Outcome)
		synctest.Wait()
		assert.Equal(t, []string{"welcome", "welcome"}, f.journal.get())
		events := f.events()
		assert.Equal(t, "paused entrance", events[0])
		assert.Contains(t, events, "resumed entrance")
	})
}

// users is a fake of engine.Users.
type users struct {
	byName map[string]user.User
	err    error
}

func (u users) UserByName(_ context.Context, p platform.Name, name string) (user.User, bool, error) {
	if u.err != nil {
		return user.User{}, false, u.err
	}
	found, ok := u.byName[string(p)+"/"+strings.ToLower(name)]
	return found, ok, nil
}

// TestTarget covers B81: the target is the one the caller set, else the
// user the first argument names, else the triggering user.
func TestTarget(t *testing.T) {
	t.Parallel()
	identity := func(login string) *user.User {
		return &user.User{ID: id.New(), Identities: []user.Identity{{Platform: platform.Twitch, Login: login}}}
	}
	ada, bob, eve := identity("ada"), identity("bob"), identity("eve")
	known := users{byName: map[string]user.User{"twitch/bob": *bob}}
	args := func(words ...string) engine.Params {
		return engine.Params{Platform: platform.Twitch, User: ada, Args: words, ArgsText: strings.Join(words, " ")}
	}
	for _, tc := range []struct {
		name  string
		users engine.Users
		p     engine.Params
		want  string
	}{
		{"named with @", known, args("@Bob"), "bob"},
		{"named without @", known, args("bob", "hi"), "bob"},
		{"unknown", known, args("carl"), "ada"},
		{"only @", known, args("@"), "ada"},
		{"other platform", known, engine.Params{Platform: platform.YouTube, User: ada, Args: []string{"bob"}, ArgsText: "bob"}, "ada"},
		{"no platform", known, engine.Params{User: ada, Args: []string{"bob"}, ArgsText: "bob"}, "ada"},
		{"no arguments", known, args(), "ada"},
		{"set by the caller", known, engine.Params{Platform: platform.Twitch, User: ada, Target: eve, Args: []string{"bob"}, ArgsText: "bob"}, "eve"},
		{"no lookup", nil, args("bob"), "ada"},
		{"lookup fails", users{err: errors.New("offline")}, args("bob"), "ada"},
		{"no user at all", known, engine.Params{Platform: platform.Twitch}, "none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				var opts []engine.Option
				if tc.users != nil {
					opts = append(opts, engine.WithUsers(tc.users))
				}
				f := newFixture(t, settings.LockNone, opts...)
				defer f.stop()

				target := action{typ: "target", fn: func(_ context.Context, run *engine.Run) error {
					login := "none"
					if tu := run.Scope().Target; tu != nil {
						login = tu.Identities[0].Login
					}
					f.journal.add(login)
					return nil
				}}
				cmd := f.command("x", command.KindChat, target)
				f.start(cmd, tc.p)
				_, err := f.trigger(cmd, tc.p)
				require.NoError(t, err)
				assert.Equal(t, []string{tc.want, tc.want}, f.journal.get(), "start and trigger")
			})
		})
	}
	_, err := engine.New(&commandStore{}, &actionTypes{}, engine.WithUsers(nil))
	require.ErrorIs(t, err, engine.ErrInvalidOption)
	_, err = engine.New(&commandStore{}, &actionTypes{}, engine.WithRequirements(nil))
	require.ErrorIs(t, err, engine.ErrInvalidOption)
}

// TestAppStopping covers B55: while the core stops, only event commands of
// "app.stopping" are taken.
func TestAppStopping(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerCommandType)
		defer f.stop()

		release := make(chan struct{})
		f.start(f.command("running", command.KindChat, f.journal.hold("x", "running", release)), engine.Params{})
		go f.stop()
		synctest.Wait()

		_, err := f.trigger(f.command("chat", command.KindChat), engine.Params{})
		require.ErrorIs(t, err, engine.ErrClosed)
		goodbye := f.command("goodbye", command.KindEvent, f.journal.note("goodbye"))
		_, err = f.engine.Trigger(t.Context(), engine.Request{Command: goodbye, Source: engine.SourceEvent, Event: eventtype.ChannelFollow})
		require.ErrorIs(t, err, engine.ErrClosed)
		res, err := f.engine.Trigger(t.Context(), engine.Request{Command: goodbye, Source: engine.SourceEvent, Event: eventtype.AppStopping})
		require.NoError(t, err)
		synctest.Wait()

		assert.Equal(t, engine.StateCompleted, f.state(res.Instances[0]))
		close(release)
	})
}

// TestTriggerSettingsError: without its settings, a command is not queued,
// and its place in the queue is given back.
func TestTriggerSettingsError(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		broken := errors.New("disk on fire")
		f := newFixture(t, settings.LockPerCommandType,
			engine.WithConfig(func(context.Context) (engine.Config, error) { return engine.Config{}, broken }))
		defer f.stop()

		for range engine.MaxPending + 1 {
			_, err := f.engine.Trigger(t.Context(), engine.Request{Command: f.command("x", command.KindChat), Source: engine.SourceTimer})
			require.ErrorIs(t, err, broken)
		}
	})
}
