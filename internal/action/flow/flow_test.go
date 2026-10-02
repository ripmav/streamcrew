// SPDX-License-Identifier: Apache-2.0

package flow_test

import (
	"context"
	"os"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/actiontest"
	"github.com/ripmav/streamcrew/internal/action/flow"
	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/template"
)

// update reports whether golden files are written first (Code-ADR-0006).
func update() bool {
	return os.Getenv("STREAMCREW_UPDATE_GOLDEN") != ""
}

// numbers returns random numbers that cycle through values, each taken
// modulo n.
func numbers(values ...int) func(n int) int {
	var mu sync.Mutex
	next := 0
	return func(n int) int {
		mu.Lock()
		defer mu.Unlock()
		v := values[next%len(values)]
		next++
		return v % n
	}
}

// registry returns the flow types with the template engine of the tests
// and the random numbers of intN.
func registry(t *testing.T, intN func(int) int) *action.Registry {
	t.Helper()
	identifiers, err := template.NewRegistry(template.ArgumentFamily(), template.RunFamily())
	require.NoError(t, err)
	ds, err := flow.Descriptors(flow.Ports{Templates: template.New(identifiers), IntN: intN})
	require.NoError(t, err)
	reg, err := action.NewRegistry(capability.Set{}, ds...)
	require.NoError(t, err)
	return reg
}

// newAction returns a new action of type typ as A.
func newAction[A command.Action](t *testing.T, reg *action.Registry, typ string) A {
	t.Helper()
	d, ok := reg.Descriptor(typ)
	require.True(t, ok, typ)
	a, ok := d.New().(A)
	require.True(t, ok, "%s makes %T", typ, d.New())
	return a
}

func TestDescriptorsNeedPorts(t *testing.T) {
	t.Parallel()
	_, err := flow.Descriptors(flow.Ports{IntN: numbers(0)})
	require.Error(t, err)
	_, err = flow.Descriptors(flow.Ports{Templates: template.New(nil)})
	require.Error(t, err)
}

func TestConformance(t *testing.T) {
	t.Parallel()
	reg := registry(t, numbers(0))
	suites := map[string][]actiontest.Example{
		flow.TypeWait: {
			{Name: "fixed", Doc: `{"type":"wait","seconds":1.5}`, Valid: true},
			{Name: "expression", Doc: `{"type":"wait","enabled":false,"seconds":"$arg1text"}`, Valid: true},
			{Name: "zero", Doc: `{"type":"wait","seconds":0}`, Valid: true},
			{Name: "missing", Doc: `{"type":"wait"}`},
			{Name: "too long", Doc: `{"type":"wait","seconds":3601}`},
			{Name: "negative", Doc: `{"type":"wait","seconds":-1}`},
		},
		flow.TypeRandom: {
			{Name: "full", Doc: `{"type":"random","count":2,"draw":"unique_remembered","actions":[{"type":"wait","seconds":1},{"type":"obs_scene"}]}`, Valid: true},
			{Name: "defaults", Doc: `{"type":"random"}`, Valid: true},
			{Name: "count expression", Doc: `{"type":"random","count":"$arg1text","draw":"unique"}`, Valid: true},
			{Name: "free", Doc: `{"type":"random","draw":"free"}`, Valid: true},
			{Name: "unknown way to draw", Doc: `{"type":"random","draw":"remembered"}`},
			{Name: "empty way to draw", Doc: `{"type":"random","draw":""}`},
			{Name: "old switch", Doc: `{"type":"random","unique":true}`},
			{Name: "count fraction", Doc: `{"type":"random","count":1.5}`},
			{Name: "count too high", Doc: `{"type":"random","count":1001}`},
		},
		flow.TypeGroup: {
			{Name: "children", Doc: `{"type":"group","actions":[{"type":"group","actions":[]},{"type":"wait","seconds":2}]}`, Valid: true},
			{Name: "defaults", Doc: `{"type":"group"}`, Valid: true},
			{Name: "child without type", Doc: `{"type":"group","actions":[{"seconds":1}]}`},
		},
		flow.TypeRepeat: {
			{Name: "full", Doc: `{"type":"repeat","count":1000,"actions":[{"type":"wait","seconds":0}]}`, Valid: true},
			{Name: "count expression", Doc: `{"type":"repeat","count":"$arg1text * 2"}`, Valid: true},
			{Name: "count missing", Doc: `{"type":"repeat","actions":[]}`},
			{Name: "count 1001", Doc: `{"type":"repeat","count":1001}`},
			{Name: "count fraction", Doc: `{"type":"repeat","count":2.5}`},
		},
	}
	for typ, examples := range suites {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()
			d, ok := reg.Descriptor(typ)
			require.True(t, ok)
			actiontest.Suite{Descriptor: d, Examples: examples, Update: update()}.Run(t)
		})
	}
}

// TestWait covers actions.md B8, B10 and B200.
func TestWait(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		seconds  action.Amount
		args     []string
		want     time.Duration
		wantErrs []string
	}{
		{name: "fraction", seconds: action.Fixed(1.5), want: 1500 * time.Millisecond},
		{name: "zero", seconds: action.Fixed(0), want: 0},
		{name: "longer than the default limit", seconds: action.Fixed(600), want: 10 * time.Minute},
		{name: "from an argument", seconds: action.Expression("$arg1text * 2"), args: []string{"3"}, want: 6 * time.Second},
		{name: "B200 not a number", seconds: action.Expression("$arg1text"), args: []string{"abc"}, wantErrs: []string{"seconds", `"abc" is not a number`}},
		{name: "out of range", seconds: action.Expression("$arg1text"), args: []string{"3601"}, wantErrs: []string{"seconds", "3601 is not between 0 and 3600"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				reg := registry(t, numbers(0))
				h := actiontest.NewHarness(t, reg)
				j := &actiontest.Journal{}
				w := newAction[flow.Wait](t, reg, flow.TypeWait)
				w.Seconds = tc.seconds

				p := engine.Params{Args: tc.args}
				if len(tc.args) > 0 {
					p.ArgsText = tc.args[0]
				}
				began := time.Now()
				in := h.Start(h.Command("x", j.Note("before"), w, j.Note("after")), p)
				time.Sleep(time.Hour)
				synctest.Wait()
				in = h.Instance(in.ID)
				assert.Equal(t, engine.StateCompleted, in.State)
				assert.Equal(t, []string{"before", "after"}, j.Lines())
				if tc.wantErrs != nil {
					require.Len(t, in.Errors, 1)
					for _, want := range tc.wantErrs {
						assert.Contains(t, in.Errors[0].Message, want)
					}
					return
				}
				assert.Empty(t, in.Errors)
				assert.Equal(t, tc.want, in.EndedAt.Sub(began))
			})
		})
	}
}

// TestWaitCancel covers actions.md B10: a cancellation ends the wait at
// once.
func TestWaitCancel(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		reg := registry(t, numbers(0))
		h := actiontest.NewHarness(t, reg)
		w := newAction[flow.Wait](t, reg, flow.TypeWait)
		w.Seconds = action.Fixed(3600)

		in := h.Start(h.Command("x", w), engine.Params{})
		time.Sleep(time.Minute)
		require.NoError(t, h.Engine().Cancel(t.Context(), in.ID))
		synctest.Wait()
		in = h.Instance(in.ID)
		assert.Equal(t, engine.StateCanceled, in.State)
		assert.Equal(t, time.Minute, in.EndedAt.Sub(in.StartedAt))
	})
}

// TestGroup covers actions.md B14 with B1 and B9.
func TestGroup(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		reg := registry(t, numbers(0))
		h := actiontest.NewHarness(t, reg)
		j := &actiontest.Journal{}
		inner := newAction[flow.Group](t, reg, flow.TypeGroup)
		inner.Actions = []command.Action{j.Fail("2"), j.Note("3")}
		g := newAction[flow.Group](t, reg, flow.TypeGroup)
		g.Actions = []command.Action{j.Note("1"), inner, j.Inactive("not run"), j.Note("4")}
		off := newAction[flow.Group](t, reg, flow.TypeGroup)
		off.Active = false
		off.Actions = []command.Action{j.Note("in an inactive group")}

		in := h.Start(h.Command("x", g, off, j.Note("after")), engine.Params{})
		assert.Equal(t, engine.StateCompleted, in.State)
		assert.Equal(t, []string{"1", "2", "3", "4", "after"}, j.Lines())
		require.Len(t, in.Errors, 1)
		assert.Equal(t, []int{1, 2, 1}, in.Errors[0].Path)
	})
}

// TestRepeat covers actions.md B15, B201 and B202.
func TestRepeat(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		reg := registry(t, numbers(0))
		h := actiontest.NewHarness(t, reg)
		j := &actiontest.Journal{}
		repeat := func(count action.Amount, children ...command.Action) flow.Repeat {
			r := newAction[flow.Repeat](t, reg, flow.TypeRepeat)
			r.Count, r.Actions = count, children
			return r
		}

		in := h.Start(h.Command("three", repeat(action.Fixed(3), j.Note("a"), j.Note("b"))), engine.Params{})
		assert.Empty(t, in.Errors)
		assert.Equal(t, []string{"a", "b", "a", "b", "a", "b"}, j.Lines())

		j = &actiontest.Journal{}
		in = h.Start(h.Command("zero", repeat(action.Fixed(0), j.Note("a")), j.Note("after")), engine.Params{})
		assert.Empty(t, in.Errors)
		assert.Equal(t, []string{"after"}, j.Lines())

		j = &actiontest.Journal{}
		in = h.Start(h.Command("B201", repeat(action.Expression("$arg1text"), j.Note("a"))),
			engine.Params{Args: []string{"1001"}, ArgsText: "1001"})
		require.Len(t, in.Errors, 1)
		assert.Contains(t, in.Errors[0].Message, "count: ")
		assert.Contains(t, in.Errors[0].Message, "1001 is not between 0 and 1000")
		assert.Empty(t, j.Lines(), "B201: no pass before the limit is checked")

		j = &actiontest.Journal{}
		in = h.Start(h.Command("B202", repeat(action.Fixed(1000), repeat(action.Fixed(2), j.Note("n")))), engine.Params{})
		assert.Empty(t, in.Errors)
		assert.Len(t, j.Lines(), 2000, "B202: the limit holds per action")

		j = &actiontest.Journal{}
		in = h.Start(h.Command("failing", repeat(action.Fixed(2), j.Fail("f"))), engine.Params{})
		assert.Equal(t, engine.StateCompleted, in.State)
		assert.Equal(t, []string{"f", "f"}, j.Lines(), "B9: under continue the next pass runs")
		require.Len(t, in.Errors, 2)
		assert.Equal(t, []int{1, 1}, in.Errors[1].Path)

		j = &actiontest.Journal{}
		aborting := h.Command("aborting", repeat(action.Fixed(2), j.Fail("f")), j.Note("not reached"))
		aborting.ErrorPolicy = command.ErrorAbort
		h.Put(aborting)
		in = h.Start(aborting, engine.Params{})
		assert.Equal(t, engine.StateFailed, in.State)
		assert.Equal(t, []string{"f"}, j.Lines())
	})
}

// TestRandom covers actions.md B11, B12, B203 and B204.
func TestRandom(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		numbers  []int
		count    int
		draw     flow.Draw
		children func(j *actiontest.Journal) []command.Action
		want     []string
	}{
		{
			name: "B11 repeats allowed", numbers: []int{2, 0, 2}, count: 3, draw: flow.DrawFree,
			children: abc, want: []string{"c", "a", "c"},
		},
		{
			name: "B11 more draws than child actions", numbers: []int{0}, count: 4, draw: flow.DrawFree,
			children: abc, want: []string{"a", "a", "a", "a"},
		},
		{
			name: "B204 unique stops when all are drawn", numbers: []int{1}, count: 5, draw: flow.DrawUnique,
			children: abc, want: []string{"b", "c", "a"},
		},
		{
			name: "only active child actions are drawn", numbers: []int{0}, count: 2, draw: flow.DrawUnique,
			children: func(j *actiontest.Journal) []command.Action {
				return []command.Action{j.Inactive("a"), j.Note("b"), j.Note("c")}
			},
			want: []string{"b", "c"},
		},
		{
			name: "B203 no active child actions", numbers: []int{0}, count: 3, draw: flow.DrawFree,
			children: func(j *actiontest.Journal) []command.Action {
				return []command.Action{j.Inactive("a")}
			},
			want: nil,
		},
		{
			name: "zero draws", numbers: []int{0}, count: 0, draw: flow.DrawFree,
			children: abc, want: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				reg := registry(t, numbers(tc.numbers...))
				h := actiontest.NewHarness(t, reg)
				j := &actiontest.Journal{}
				r := newAction[flow.Random](t, reg, flow.TypeRandom)
				r.Count, r.Draw, r.Actions = action.Fixed(float64(tc.count)), tc.draw, tc.children(j)

				in := h.Start(h.Command("x", r), engine.Params{})
				assert.Empty(t, in.Errors)
				assert.Equal(t, tc.want, j.Lines())
			})
		})
	}
}

// abc returns three notes.
func abc(j *actiontest.Journal) []command.Action {
	return []command.Action{j.Note("a"), j.Note("b"), j.Note("c")}
}

// TestRandomRemember covers actions.md B13: drawn child actions stay out
// across runs until all were drawn once, per command and action; a change
// of the command starts anew.
func TestRandomRemember(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		reg := registry(t, numbers(0))
		h := actiontest.NewHarness(t, reg)
		j := &actiontest.Journal{}
		remember := func(count int, children ...command.Action) flow.Random {
			r := newAction[flow.Random](t, reg, flow.TypeRandom)
			r.Count, r.Draw, r.Actions = action.Fixed(float64(count)), flow.DrawUniqueRemembered, children
			return r
		}
		run := func(cmd command.Command) []string {
			before := len(j.Lines())
			in := h.Start(cmd, engine.Params{})
			require.Empty(t, in.Errors)
			return j.Lines()[before:]
		}

		four := h.Command("four", remember(2, j.Note("a"), j.Note("b"), j.Note("c"), j.Note("d")))
		assert.Equal(t, []string{"a", "b"}, run(four))
		assert.Equal(t, []string{"c", "d"}, run(four))
		assert.Equal(t, []string{"a", "b"}, run(four), "all were drawn once: the draw starts anew")

		three := h.Command("three", remember(2, j.Note("a"), j.Note("b"), j.Note("c")))
		assert.Equal(t, []string{"a", "b"}, run(three))
		assert.Equal(t, []string{"c"}, run(three), "B12: only what is left in this round")
		assert.Equal(t, []string{"a", "b"}, run(three))

		changed := three
		changed.UpdatedAt = time.Now().Add(time.Second)
		h.Put(changed)
		assert.Equal(t, []string{"c"}, run(three), "the memory of the first version")
		assert.Equal(t, []string{"a", "b"}, run(changed), "a changed command starts anew")

		two := h.Command("two actions",
			remember(1, j.Note("x1"), j.Note("x2")),
			remember(1, j.Note("y1"), j.Note("y2")))
		assert.Equal(t, []string{"x1", "y1"}, run(two))
		assert.Equal(t, []string{"x2", "y2"}, run(two), "each action has its own memory")
	})
}

// TestRandomWithStop: a child action that ends the instance stops the
// draws.
func TestRandomWithStop(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		reg := registry(t, numbers(0))
		h := actiontest.NewHarness(t, reg)
		j := &actiontest.Journal{}
		r := newAction[flow.Random](t, reg, flow.TypeRandom)
		r.Count, r.Actions = action.Fixed(3), []command.Action{stop{}}

		in := h.Start(h.Command("x", r, j.Note("not reached")), engine.Params{})
		assert.Equal(t, engine.StateCompleted, in.State)
		assert.Empty(t, j.Lines())
	})
}

// stop is an action that ends its instance, like "exit" (actions.md B38).
type stop struct{}

func (stop) DocType() string                            { return "exit" }
func (stop) Validate() error                            { return nil }
func (stop) Enabled() bool                              { return true }
func (stop) Perform(context.Context, *engine.Run) error { return engine.ErrStop }

func TestValidate(t *testing.T) {
	t.Parallel()
	reg := registry(t, numbers(0))
	r := newAction[flow.Random](t, reg, flow.TypeRandom)
	assert.Equal(t, flow.DrawFree, r.Draw, "a new random action draws freely")
	require.NoError(t, r.Validate())
	for _, d := range flow.Draws() {
		r.Draw = d
		require.NoError(t, r.Validate(), d)
	}
	r.Draw = "remembered"
	require.ErrorContains(t, r.Validate(), `draw: invalid action: unknown way to draw "remembered"`)
	r.Draw = ""
	require.ErrorIs(t, r.Validate(), action.ErrInvalid, "the empty value is no way to draw")

	w := newAction[flow.Wait](t, reg, flow.TypeWait)
	require.ErrorContains(t, w.Validate(), "seconds", "a new wait has no duration yet")
	rep := newAction[flow.Repeat](t, reg, flow.TypeRepeat)
	require.ErrorContains(t, rep.Validate(), "count")
}
