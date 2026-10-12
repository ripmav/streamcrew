// SPDX-License-Identifier: MIT

package twitch_test

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

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/actiontest"
	"github.com/ripmav/streamcrew/internal/action/schema"
	"github.com/ripmav/streamcrew/internal/action/twitch"
	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/template"
)

// recordingOps is a fake of twitch.Ops that records the calls and serves
// canned poll and prediction states.
type recordingOps struct {
	mu    sync.Mutex
	calls []string
	// polls and predictions answer their status from these maps; a state
	// with Ended false makes the action wait one tick, then the test flips
	// it.
	polls       map[string]twitch.PollStatus
	predictions map[string]twitch.PredictionStatus
}

func newRecordingOps() *recordingOps {
	return &recordingOps{
		polls:       make(map[string]twitch.PollStatus),
		predictions: make(map[string]twitch.PredictionStatus),
	}
}

func (o *recordingOps) call(name string, _ ...any) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls = append(o.calls, name)
}

func (o *recordingOps) Calls() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.calls)
}

func (o *recordingOps) Clip(_ context.Context) (string, error) {
	o.call("clip")
	return "https://clips.twitch.tv/x", nil
}

func (o *recordingOps) Marker(_ context.Context, description string) (string, error) {
	o.call("marker", description)
	return "m1", nil
}

func (o *recordingOps) StartAd(_ context.Context, lengthSeconds int) error {
	o.call("ad", lengthSeconds)
	return nil
}

func (o *recordingOps) Raid(_ context.Context, toLogin string) error {
	o.call("raid", toLogin)
	return nil
}

func (o *recordingOps) Shoutout(_ context.Context, toLogin string) error {
	o.call("shoutout", toLogin)
	return nil
}

func (o *recordingOps) CreatePoll(_ context.Context, in twitch.PollInput) (string, error) {
	o.call("poll", in.Title, strings.Join(in.Choices, "|"), in.Duration)
	return "p1", nil
}

func (o *recordingOps) PollStatus(_ context.Context, pollID string) (twitch.PollStatus, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.polls[pollID], nil
}

func (o *recordingOps) CreatePrediction(_ context.Context, in twitch.PredictionInput) (string, error) {
	o.call("prediction", in.OutcomeA, in.OutcomeB, in.Duration)
	return "pr1", nil
}

func (o *recordingOps) PredictionStatus(_ context.Context, id string) (twitch.PredictionStatus, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.predictions[id], nil
}

func (o *recordingOps) CreateReward(_ context.Context, title string, cost int) (string, error) {
	o.call("reward.create", title, cost)
	return "r1", nil
}

func (o *recordingOps) SetReward(_ context.Context, title string, enabled bool) error {
	o.call("reward.set", title, enabled)
	return nil
}

func (o *recordingOps) DeleteReward(_ context.Context, title string) error {
	o.call("reward.delete", title)
	return nil
}

// echo renders a template and records the result; it stands for the sublist
// child that uses $pollchoice or $predictionoutcome.
type echo struct {
	action.Common `json:",embed"`
	Text          action.Template `json:"text"`
	eng           *template.Engine
	out           *strings.Builder
}

func (*echo) DocType() string { return "echo" }
func (*echo) Validate() error { return nil }
func (e *echo) Perform(ctx context.Context, run *engine.Run) error {
	s, err := e.eng.Render(ctx, e.Text.Parse(), run.Scope(), template.Text)
	e.out.WriteString(s)
	e.out.WriteString("\n")
	return err
}

func (e *echo) String() string { return e.out.String() }

// fixture wires the twitch type (with fake ops) and the echo type into a
// running engine.
type fixture struct {
	h    *actiontest.Harness
	reg  *action.Registry
	ops  *recordingOps
	eng  *template.Engine
	echo *echo
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	reg, _ := template.NewRegistry()
	eng := template.New(reg)
	ops := newRecordingOps()
	out := &strings.Builder{}
	echo := &echo{Common: action.On(), eng: eng, out: out}
	ports := twitch.Ports{
		Templates: eng,
		Ops:       ops,
		PollTick:  time.Millisecond,
		Logger:    slog.New(slog.DiscardHandler),
	}
	descs, err := twitch.Descriptors(ports)
	require.NoError(t, err)
	echoDesc := action.Descriptor{
		Type:     "echo",
		Version:  1,
		Category: action.CategoryChat,
		Schema: schema.Document(schema.Property{
			Name: "text", Schema: schema.NonEmpty(schema.UITemplate), Required: true,
		}),
	}.WithNew(func() command.Action { return echo })
	descs = append(descs, echoDesc)
	registry, err := action.NewRegistry(capability.Set{}, descs...)
	require.NoError(t, err)
	h := actiontest.NewHarness(t, registry)
	return &fixture{h: h, reg: registry, ops: ops, eng: eng, echo: echo}
}

// tw makes a twitch action with the fixture's ports and applies mutate.
func (f *fixture) tw(t *testing.T, mutate func(a *twitch.Twitch)) twitch.Twitch {
	t.Helper()
	d, ok := f.reg.Descriptor(twitch.TypeTwitch)
	require.True(t, ok)
	a, ok := d.New().(twitch.Twitch)
	require.True(t, ok)
	mutate(&a)
	return a
}

// params is a bare trigger with no platform or user.
func params() engine.Params {
	return engine.Params{}
}

func TestTwitchValidate(t *testing.T) {
	cases := []struct {
		name string
		a    twitch.Twitch
		ok   bool
	}{
		{"clip default", twitchAction(twitch.KindClip), true},
		{"marker with text", marker("hi"), true},
		{"marker empty", marker(""), false},
		{"ad 30", ad(30), true},
		{"ad 60", ad(60), true},
		{"ad 45", ad(45), false},
		{"raid with target", target("other"), true},
		{"raid empty", target(""), false},
		{"shoutout with target", func() twitch.Twitch { a := target("other"); a.Kind = twitch.KindShoutout; return a }(), true},
		{"poll valid", poll("t", []string{"a", "b"}, 5), true},
		{"poll one choice", poll("t", []string{"a"}, 5), false},
		{"poll five choices", poll("t", []string{"a", "b", "c", "d", "e"}, 5), false},
		{"poll empty title", poll("", []string{"a", "b"}, 5), false},
		{"poll duration 0", poll("t", []string{"a", "b"}, 0), false},
		{"poll duration 61", poll("t", []string{"a", "b"}, 61), false},
		{"prediction valid", prediction("a", "b", 5), true},
		{"prediction empty outcome", prediction("a", "", 5), false},
		{"reward create", reward(twitch.RewardCreate, 100), true},
		{"reward create no cost", reward(twitch.RewardCreate, 0), false},
		{"reward delete", reward(twitch.RewardDelete, 0), true},
		{"reward bad op", func() twitch.Twitch { a := reward(twitch.RewardCreate, 1); a.Reward.Op = "nope"; return a }(), false},
		{"marker members on clip", func() twitch.Twitch {
			a := twitchAction(twitch.KindClip)
			a.Marker = &twitch.MarkerOptions{Description: "x"}
			return a
		}(), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.a.Validate()
			if c.ok {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

// TestTwitchPerform runs each operation and checks the ops call.
func TestTwitchPerform(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)

		f.h.Start(f.h.Command("clip", f.tw(t, func(a *twitch.Twitch) { a.Kind = twitch.KindClip })), params())
		f.h.Start(f.h.Command("marker", f.tw(t, func(a *twitch.Twitch) {
			a.Kind = twitch.KindMarker
			a.Marker = &twitch.MarkerOptions{Description: "hi"}
		})), params())
		f.h.Start(f.h.Command("ad", f.tw(t, func(a *twitch.Twitch) { a.Kind = twitch.KindAd; a.Ad = &twitch.AdOptions{Length: 60} })), params())
		f.h.Start(f.h.Command("raid", f.tw(t, func(a *twitch.Twitch) { a.Kind = twitch.KindRaid; a.Target = &twitch.TargetOptions{Target: "other"} })), params())
		f.h.Start(f.h.Command("shoutout", f.tw(t, func(a *twitch.Twitch) {
			a.Kind = twitch.KindShoutout
			a.Target = &twitch.TargetOptions{Target: "other"}
		})), params())
		f.h.Start(f.h.Command("reward.create", f.tw(t, func(a *twitch.Twitch) {
			a.Kind = twitch.KindReward
			a.Reward = &twitch.RewardOptions{Op: twitch.RewardCreate, Title: "gold", Cost: 500}
		})), params())
		f.h.Start(f.h.Command("reward.enable", f.tw(t, func(a *twitch.Twitch) {
			a.Kind = twitch.KindReward
			a.Reward = &twitch.RewardOptions{Op: twitch.RewardEnable, Title: "gold"}
		})), params())
		f.h.Start(f.h.Command("reward.disable", f.tw(t, func(a *twitch.Twitch) {
			a.Kind = twitch.KindReward
			a.Reward = &twitch.RewardOptions{Op: twitch.RewardDisable, Title: "gold"}
		})), params())
		f.h.Start(f.h.Command("reward.delete", f.tw(t, func(a *twitch.Twitch) {
			a.Kind = twitch.KindReward
			a.Reward = &twitch.RewardOptions{Op: twitch.RewardDelete, Title: "gold"}
		})), params())

		got := f.ops.Calls()
		assert.Contains(t, got, "clip")
		assert.Contains(t, got, "marker")
		assert.Contains(t, got, "ad")
		assert.Contains(t, got, "raid")
		assert.Contains(t, got, "shoutout")
		assert.Contains(t, got, "reward.create")
		assert.Contains(t, got, "reward.set")
		assert.Contains(t, got, "reward.delete")
	})
}

// TestTwitchPollRunsTheSublist starts a poll, ends it, and checks the
// sublist saw $pollchoice.
func TestTwitchPollRunsTheSublist(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		// The poll is already ended, so the action reads the winner at once
		// and runs the sublist.
		f.ops.mu.Lock()
		f.ops.polls["p1"] = twitch.PollStatus{Ended: true, Winner: "yes"}
		f.ops.mu.Unlock()
		a := f.tw(t, func(a *twitch.Twitch) {
			a.Kind = twitch.KindPoll
			a.Poll = &twitch.PollOptions{Title: "t", Choices: []action.Template{"yes", "no"}, DurationMinutes: 1}
			a.Poll.Actions = []command.Action{f.echoAction("winner=$pollchoice")}
		})
		f.h.Start(f.h.Command("poll", a), params())
		assert.Equal(t, "winner=yes\n", f.echo.String())
	})
}

// TestTwitchPredictionRunsTheSublist starts a prediction, ends it, and
// checks the sublist saw $predictionoutcome.
func TestTwitchPredictionRunsTheSublist(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		f.ops.mu.Lock()
		f.ops.predictions["pr1"] = twitch.PredictionStatus{Ended: true, Winner: "B"}
		f.ops.mu.Unlock()
		a := f.tw(t, func(a *twitch.Twitch) {
			a.Kind = twitch.KindPrediction
			a.Prediction = &twitch.PredictionOptions{OutcomeA: "A", OutcomeB: "B", DurationMinutes: 1}
			a.Prediction.Actions = []command.Action{f.echoAction("outcome=$predictionoutcome")}
		})
		f.h.Start(f.h.Command("prediction", a), params())
		assert.Equal(t, "outcome=B\n", f.echo.String())
	})
}

// TestTwitchOperationError records the error of a failed operation.
func TestTwitchOperationError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reg, _ := template.NewRegistry()
		eng := template.New(reg)
		ports := twitch.Ports{Templates: eng, Ops: failingOps{}, PollTick: time.Millisecond, Logger: slog.New(slog.DiscardHandler)}
		descs, err := twitch.Descriptors(ports)
		require.NoError(t, err)
		registry, err := action.NewRegistry(capability.Set{}, descs...)
		require.NoError(t, err)
		h := actiontest.NewHarness(t, registry)
		d, _ := registry.Descriptor(twitch.TypeTwitch)
		a, _ := d.New().(twitch.Twitch)
		a.Kind = twitch.KindClip
		inst := h.Start(h.Command("clip", a), params())
		require.NotEmpty(t, inst.Errors)
		assert.Equal(t, twitch.TypeTwitch, inst.Errors[0].Type)
	})
}

// TestTwitchOwnershipGate rejects the twitch operation from a run that has
// a channel (B7): it is bound to the streamer channel and must not act on
// someone else's; a home run (no channel) runs as usual.
func TestTwitchOwnershipGate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		a := f.tw(t, func(a *twitch.Twitch) { a.Kind = twitch.KindClip })
		// From another channel: refused, nothing runs.
		inst := f.h.Start(f.h.Command("clip", a), engine.Params{Channel: "other"})
		require.NotEmpty(t, inst.Errors)
		assert.Equal(t, twitch.TypeTwitch, inst.Errors[0].Type)
		assert.Contains(t, inst.Errors[0].Message, "streamer channel")
		assert.Contains(t, inst.Errors[0].Message, "other")
		assert.Empty(t, f.ops.calls)
		// From the home channel (no channel): runs.
		f.h.Start(f.h.Command("clip", a), params())
		assert.Equal(t, []string{"clip"}, f.ops.calls)
	})
}

func (f *fixture) echoAction(text string) command.Action {
	f.echo.Text = action.Template(text)
	return f.echo
}

// failingOps is a twitch.Ops that always fails.
type failingOps struct{}

func (failingOps) Clip(context.Context) (string, error) {
	return "", errors.New("the operation failed")
}
func (failingOps) Marker(context.Context, string) (string, error) {
	return "", errors.New("the operation failed")
}
func (failingOps) StartAd(context.Context, int) error     { return errors.New("the operation failed") }
func (failingOps) Raid(context.Context, string) error     { return errors.New("the operation failed") }
func (failingOps) Shoutout(context.Context, string) error { return errors.New("the operation failed") }
func (failingOps) CreatePoll(context.Context, twitch.PollInput) (string, error) {
	return "", errors.New("the operation failed")
}
func (failingOps) PollStatus(context.Context, string) (twitch.PollStatus, error) {
	return twitch.PollStatus{}, errors.New("the operation failed")
}
func (failingOps) CreatePrediction(context.Context, twitch.PredictionInput) (string, error) {
	return "", errors.New("the operation failed")
}
func (failingOps) PredictionStatus(context.Context, string) (twitch.PredictionStatus, error) {
	return twitch.PredictionStatus{}, errors.New("the operation failed")
}
func (failingOps) CreateReward(context.Context, string, int) (string, error) {
	return "", errors.New("the operation failed")
}
func (failingOps) SetReward(context.Context, string, bool) error {
	return errors.New("the operation failed")
}
func (failingOps) DeleteReward(context.Context, string) error {
	return errors.New("the operation failed")
}

// --- builders -----------------------------------------------------------

func twitchAction(k twitch.Kind) twitch.Twitch {
	return twitch.Twitch{Common: action.On(), Kind: k}
}

func marker(desc string) twitch.Twitch {
	return twitch.Twitch{Common: action.On(), Kind: twitch.KindMarker, Marker: &twitch.MarkerOptions{Description: action.Template(desc)}}
}

func ad(length int) twitch.Twitch {
	return twitch.Twitch{Common: action.On(), Kind: twitch.KindAd, Ad: &twitch.AdOptions{Length: length}}
}

func target(login string) twitch.Twitch {
	return twitch.Twitch{Common: action.On(), Kind: twitch.KindRaid, Target: &twitch.TargetOptions{Target: action.Template(login)}}
}

func poll(title string, choices []string, minutes int) twitch.Twitch {
	cs := make([]action.Template, len(choices))
	for i, c := range choices {
		cs[i] = action.Template(c)
	}
	return twitch.Twitch{Common: action.On(), Kind: twitch.KindPoll, Poll: &twitch.PollOptions{
		Title: action.Template(title), Choices: cs, DurationMinutes: minutes,
	}}
}

func prediction(a, b string, minutes int) twitch.Twitch {
	return twitch.Twitch{Common: action.On(), Kind: twitch.KindPrediction, Prediction: &twitch.PredictionOptions{
		OutcomeA: action.Template(a), OutcomeB: action.Template(b), DurationMinutes: minutes,
	}}
}

func reward(op twitch.RewardOp, cost int) twitch.Twitch {
	return twitch.Twitch{Common: action.On(), Kind: twitch.KindReward, Reward: &twitch.RewardOptions{
		Op: op, Title: action.Template("t"), Cost: cost,
	}}
}
