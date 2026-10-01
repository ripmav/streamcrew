// SPDX-License-Identifier: Apache-2.0

package values_test

import (
	"context"
	"errors"
	"fmt"
	"math"
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
	"github.com/ripmav/streamcrew/internal/action/values"
	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/counter"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/template"
)

// update reports whether golden files are written first (Code-ADR-0006).
func update() bool {
	return os.Getenv("STREAMCREW_UPDATE_GOLDEN") != ""
}

// counters is a fake counter store: names regardless of case, every update
// under one lock, as the store does it in a transaction.
type counters struct {
	mu   sync.Mutex
	list []counter.Counter
}

var errNotFound = errors.New("not found")

func (s *counters) UpdateCounter(_ context.Context, name string, fn func(*counter.Counter) error) (counter.Counter, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.list, func(c counter.Counter) bool { return strings.EqualFold(c.Name, name) })
	if i < 0 {
		return counter.Counter{}, fmt.Errorf("counter %q: %w", name, errNotFound)
	}
	c := s.list[i]
	if err := fn(&c); err != nil {
		return counter.Counter{}, err
	}
	s.list[i] = c
	return c, nil
}

// Counters implements template.Counters.
func (s *counters) Counters(context.Context) ([]counter.Counter, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.list), nil
}

// value returns the value of the counter name.
func (s *counters) value(name string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.list, func(c counter.Counter) bool { return c.Name == name })
	return s.list[i].Value
}

// all returns the counters.
func (s *counters) all() []counter.Counter {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.list)
}

// fixture is a running engine with the counter action and its store.
type fixture struct {
	t         *testing.T
	reg       *action.Registry
	harness   *actiontest.Harness
	counters  *counters
	templates *template.Engine
	lines     *lines
}

// newFixture returns a fixture with the counters. Create it inside
// synctest.Test.
func newFixture(t *testing.T, list ...counter.Counter) *fixture {
	t.Helper()
	f := &fixture{t: t, counters: &counters{list: list}, lines: &lines{}}
	f.reg, f.templates = registry(t, f.counters)
	f.harness = actiontest.NewHarness(t, f.reg)
	return f
}

// registry returns the value types with a template engine that knows the
// counters.
func registry(t *testing.T, cs *counters) (*action.Registry, *template.Engine) {
	t.Helper()
	identifiers, err := template.NewRegistry(template.ArgumentFamily(), template.RunFamily())
	require.NoError(t, err)
	templates := template.New(identifiers, template.WithSources(template.CounterSource(cs)))
	ds, err := values.Descriptors(values.Ports{Templates: templates, Counters: cs})
	require.NoError(t, err)
	reg, err := action.NewRegistry(capability.Set{}, ds...)
	require.NoError(t, err)
	return reg, templates
}

// counter returns a counter action of kind k on the counter name.
func (f *fixture) counter(k values.CounterKind, name string, amount action.Amount) values.Counter {
	f.t.Helper()
	d, ok := f.reg.Descriptor(values.TypeCounter)
	require.True(f.t, ok)
	c, ok := d.New().(values.Counter)
	require.True(f.t, ok)
	c.Kind, c.Counter, c.Amount = k, name, action.Amount{}
	switch k {
	case values.CounterAdd:
		c.Amount = amount
	case values.CounterSet:
		c.Value = amount
	case values.CounterReset:
	}
	return c
}

// show returns an action that renders text and writes it.
func (f *fixture) show(text string) command.Action {
	return probe{fn: func(ctx context.Context, run *engine.Run) error {
		out, err := f.templates.Render(ctx, template.Parse(text), run.Scope(), template.Text)
		f.lines.add(out)
		return err
	}}
}

// start starts a command with the actions and the arguments args.
func (f *fixture) start(actions []command.Action, args ...string) engine.Instance {
	return f.harness.Start(f.harness.Command("x", actions...), engine.Params{Args: args, ArgsText: strings.Join(args, " ")})
}

// lines records what show writes.
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

func TestConformance(t *testing.T) {
	t.Parallel()
	reg, _ := registry(t, &counters{})
	d, ok := reg.Descriptor(values.TypeCounter)
	require.True(t, ok)
	actiontest.Suite{Descriptor: d, Update: update(), Examples: []actiontest.Example{
		{Name: "add", Doc: `{"type":"counter","kind":"add","counter":"deaths","amount":-2}`, Valid: true},
		{Name: "add with the default", Doc: `{"type":"counter","kind":"add","counter":"deaths"}`, Valid: true},
		{Name: "add an expression", Doc: `{"type":"counter","kind":"add","counter":"Deaths2","amount":"$arg1text * 2"}`, Valid: true},
		{Name: "set", Doc: `{"type":"counter","kind":"set","counter":"deaths","value":9007199254740991}`, Valid: true},
		{Name: "reset", Doc: `{"type":"counter","enabled":false,"kind":"reset","counter":"deaths"}`, Valid: true},
		{Name: "counter missing", Doc: `{"type":"counter","kind":"reset"}`},
		{Name: "name with a space", Doc: `{"type":"counter","kind":"reset","counter":"my deaths"}`},
		{Name: "name too long", Doc: `{"type":"counter","kind":"reset","counter":"` + strings.Repeat("a", 65) + `"}`},
		{Name: "empty name", Doc: `{"type":"counter","kind":"reset","counter":""}`},
		{Name: "kind missing", Doc: `{"type":"counter","counter":"deaths"}`},
		{Name: "unknown kind", Doc: `{"type":"counter","kind":"multiply","counter":"deaths"}`},
		{Name: "B220 fraction", Doc: `{"type":"counter","kind":"add","counter":"deaths","amount":1.5}`},
		{Name: "amount beyond exact numbers", Doc: `{"type":"counter","kind":"add","counter":"deaths","amount":9007199254740992}`},
		{Name: "add with a value", Doc: `{"type":"counter","kind":"add","counter":"deaths","value":1}`},
		{Name: "set without value", Doc: `{"type":"counter","kind":"set","counter":"deaths"}`},
		{Name: "set with an amount", Doc: `{"type":"counter","kind":"set","counter":"deaths","value":1,"amount":1}`},
		{Name: "reset with an amount", Doc: `{"type":"counter","kind":"reset","counter":"deaths","amount":1}`},
		{Name: "empty expression", Doc: `{"type":"counter","kind":"add","counter":"deaths","amount":""}`},
	}}.Run(t)
}

// TestCounter covers actions.md B40 and B43: following actions see the new
// value; names are case-insensitive.
func TestCounter(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, counter.Counter{Name: "deaths", Value: 5}, counter.Counter{Name: "other", Value: 7})
		in := f.start([]command.Action{
			f.counter(values.CounterAdd, "deaths", action.Fixed(1)), f.show("$deaths"),
			f.counter(values.CounterAdd, "DEATHS", action.Expression("$arg1text")), f.show("$deaths"),
			f.counter(values.CounterSet, "deaths", action.Fixed(1234567)), f.show("$deaths $deathsdisplay"),
			f.counter(values.CounterReset, "Deaths", action.Amount{}), f.show("$deaths"),
		}, "-10")
		assert.Empty(t, in.Errors)
		assert.Equal(t, []string{"6", "-4", "1234567 1,234,567", "0"}, f.lines.get())
		assert.Equal(t, int64(7), f.counters.value("other"))
	})
}

// TestCounterFails covers actions.md B4, B41, B42 and B220: the action
// fails, and the values stay as they were.
func TestCounterFails(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		kind values.CounterKind
		of   string
		by   action.Amount
		args []string
		want string
	}{
		{"B220 fraction", values.CounterAdd, "deaths", action.Expression("$arg1text"), []string{"1.5"}, "amount: invalid action: 1.5 is not a whole number"},
		{"not a number", values.CounterSet, "deaths", action.Expression("$arg1text"), []string{"abc"}, `value: invalid action: "abc" is not a number`},
		{"B41 missing counter", values.CounterReset, "lives", action.Amount{}, nil, `counter: counter "lives": not found`},
		{"B42 beyond 64 bits", values.CounterAdd, "big", action.Fixed(10), nil, "counter value out of range"},
		{"B42 below 64 bits", values.CounterAdd, "small", action.Fixed(-2), nil, "counter value out of range"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				f := newFixture(t,
					counter.Counter{Name: "deaths", Value: 5},
					counter.Counter{Name: "big", Value: math.MaxInt64 - 1},
					counter.Counter{Name: "small", Value: math.MinInt64 + 1})
				before := f.counters.all()
				a := f.counter(tc.kind, tc.of, tc.by)
				require.NoError(t, a.Validate())
				in := f.start([]command.Action{a}, tc.args...)
				require.Len(t, in.Errors, 1)
				assert.Contains(t, in.Errors[0].Message, tc.want)
				assert.Equal(t, before, f.counters.all(), "the values stay")
			})
		})
	}
}

// TestCounterConcurrent covers actions.md B42: when two instances add at
// the same time, both count.
func TestCounterConcurrent(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, counter.Counter{Name: "hugs"})
		add := make([]command.Action, 100)
		for i := range add {
			add[i] = f.counter(values.CounterAdd, "hugs", action.Fixed(1))
		}
		var started []engine.Instance
		for _, name := range []string{"a", "b", "c"} {
			cmd := f.harness.Command(name, add...)
			cmd.Unlocked = true
			f.harness.Put(cmd)
			id, err := f.harness.Engine().Start(t.Context(), cmd, engine.Params{})
			require.NoError(t, err)
			started = append(started, engine.Instance{ID: id})
		}
		synctest.Wait()
		for _, in := range started {
			assert.Equal(t, engine.StateCompleted, f.harness.Instance(in.ID).State)
		}
		assert.Equal(t, int64(300), f.counters.value("hugs"))
	})
}

func TestValidate(t *testing.T) {
	t.Parallel()
	reg, _ := registry(t, &counters{})
	d, ok := reg.Descriptor(values.TypeCounter)
	require.True(t, ok)
	c, ok := d.New().(values.Counter)
	require.True(t, ok)
	assert.Equal(t, values.CounterAdd, c.Kind, "a new counter action adds")
	assert.Equal(t, action.Fixed(1), c.Amount, "one")
	require.ErrorContains(t, c.Validate(), "counter: invalid action: invalid counter", "it has no counter yet")

	for _, tc := range []struct {
		name   string
		change func(c *values.Counter)
		want   string
	}{
		{"unknown kind", func(c *values.Counter) { c.Kind = "multiply" }, `kind: invalid action: unknown kind "multiply"`},
		{"add without amount", func(c *values.Counter) { c.Amount = action.Amount{} }, "amount: invalid action: no amount"},
		{"add with a value", func(c *values.Counter) { c.Value = action.Fixed(1) }, "value: invalid action: only set has a value"},
		{"set with an amount", func(c *values.Counter) { c.Kind, c.Value = values.CounterSet, action.Fixed(1) }, "amount: invalid action: only add has an amount"},
		{"set without value", func(c *values.Counter) { c.Kind, c.Amount = values.CounterSet, action.Amount{} }, "value: invalid action: no amount"},
		{"reset with an amount", func(c *values.Counter) { c.Kind = values.CounterReset }, "amount: invalid action: only add has an amount"},
		{"bad name", func(c *values.Counter) { c.Counter = "my deaths" }, "counter: invalid action: invalid counter"},
		{"beyond exact numbers", func(c *values.Counter) { c.Amount = action.Fixed(1 << 53) }, "amount: invalid action"},
	} {
		c, ok := d.New().(values.Counter)
		require.True(t, ok)
		c.Counter = "deaths"
		require.NoError(t, c.Validate(), tc.name)
		tc.change(&c)
		assert.ErrorContains(t, c.Validate(), tc.want, tc.name)
	}
	for _, k := range values.CounterKinds() {
		require.True(t, k.Valid(), k)
	}
}

// TestReferences: saving creates a counter that does not exist (B41).
func TestReferences(t *testing.T) {
	t.Parallel()
	c := values.Counter{Kind: values.CounterReset, Counter: "lives"}
	assert.Equal(t, []command.Reference{{Kind: command.RefCounter, Name: "lives"}}, c.References())
}

func TestDescriptorsNeedPorts(t *testing.T) {
	t.Parallel()
	_, err := values.Descriptors(values.Ports{Counters: &counters{}})
	require.Error(t, err)
	_, err = values.Descriptors(values.Ports{Templates: template.New(nil)})
	require.Error(t, err)
}
