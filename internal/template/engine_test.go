// SPDX-License-Identifier: MIT

package template_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/template"
)

// TestRender_ValuesAreNotEvaluatedAgain_B5 inserts values that contain
// identifiers from every source.
func TestRender_ValuesAreNotEvaluatedAgain_B5(t *testing.T) {
	t.Parallel()
	s := scope()
	s.SetValue("message", template.TextValue("$username $unicode36time $$time"))
	e := template.New(newRegistry(t), template.WithSources(mapSource{"quote": "$time says $username"}))

	assert.Equal(t,
		"$username $unicode36time $$time|$time says $username|$username <b>&\"'",
		render(t, e, "$message|$quote|$evil", &s))
}

// TestRender_Ranking_B10_B11 checks the order of the sources and the longest
// prefix across them.
func TestRender_Ranking_B10_B11(t *testing.T) {
	t.Parallel()
	builtIn := template.Family{Name: "rank", Identifiers: []template.Identifier{{Name: "score", Resolve: constant("built-in")}}}
	registry := newRegistry(t, builtIn)
	global := mapSource{"score": "global", "scorex": "global long"}
	counters := mapSource{"score": "counter", "scorexy": "counter long"}
	local := func() *template.Scope {
		s := scope()
		s.SetValue("score", template.TextValue("local"))
		s.SetValue("user", template.TextValue("local user"))
		return &s
	}

	tests := []struct {
		name    string
		sources []template.Source
		scope   *template.Scope
		text    string
		want    string
	}{
		{"value of the run first", []template.Source{global, counters}, local(), "$score", "local"},
		{"then global values", []template.Source{global, counters}, new(scope()), "$score", "global"},
		{"then dynamic names", []template.Source{counters}, new(scope()), "$score", "counter"},
		{"then built-in identifiers", nil, new(scope()), "$score", "built-in"},
		{"longer global beats value of the run", []template.Source{global, counters}, local(), "$scorex", "global long"},
		{"longer counter beats global", []template.Source{global, counters}, local(), "$scorexyz", "counter longz"},
		{"longer built-in beats value of the run", nil, local(), "$username $userx", "Alice local userx"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := template.New(registry, template.WithSources(tc.sources...))
			assert.Equal(t, tc.want, render(t, e, tc.text, tc.scope))
		})
	}
}

// TestRender_ResolvesOnDemandOnce_B20_B21 counts the calls of resolvers.
func TestRender_ResolvesOnDemandOnce_B20_B21(t *testing.T) {
	t.Parallel()
	var cached, uncached atomic.Int64
	family := template.Family{
		Name:        "count",
		Identifiers: []template.Identifier{{Name: "expensive", Resolve: counting("x", &cached)}},
		Patterns: []template.Pattern{{
			Name:     "dice",
			Prefixes: []string{"dice"},
			Match: func(token string) (int, template.Resolver) {
				if !strings.HasPrefix(token, "dice") {
					return 0, nil
				}
				return len("dice"), counting("6", &uncached)
			},
			Uncached: true,
		}},
	}
	e := template.New(newRegistry(t, family))
	s := scope()

	assert.Equal(t, "Alice", render(t, e, "$username", &s))
	assert.Zero(t, cached.Load(), "an identifier that does not occur is not resolved")

	assert.Equal(t, "x x xX.x", render(t, e, "$expensive $EXPENSIVE $expensiveX.$expensive", &s))
	assert.Equal(t, int64(1), cached.Load(), "an identifier is resolved once per render")

	assert.Equal(t, "6 6 6", render(t, e, "$dice $dice $dice", &s))
	assert.Equal(t, int64(3), uncached.Load(), "an uncached identifier is resolved at every occurrence")

	render(t, e, "$expensive", &s)
	assert.Equal(t, int64(2), cached.Load(), "every render resolves anew")
}

// TestRender_ResolverError_B23 leaves a failing identifier as written and
// logs one warning without values.
func TestRender_ResolverError_B23(t *testing.T) {
	t.Parallel()
	logger, logs := logBuffer()
	e := template.New(newRegistry(t), template.WithLogger(logger))
	s := scope()
	s.SetValue("secret", template.TextValue("hunter2"))

	assert.Equal(t, "$followage, $FollowAge; Alice", render(t, e, "$followage, $FollowAge; $username", &s))
	assert.Equal(t, 1, strings.Count(logs.String(), "identifier left unresolved"), logs.String())
	assert.Contains(t, logs.String(), "identifier=followage")
	assert.Contains(t, logs.String(), errPlatform.Error())
	assert.NotContains(t, logs.String(), "hunter2")
}

// TestRender_SourceError_B23 skips a failing source for the rest of the
// render.
func TestRender_SourceError_B23(t *testing.T) {
	t.Parallel()
	logger, logs := logBuffer()
	var calls atomic.Int64
	e := template.New(newRegistry(t),
		template.WithLogger(logger),
		template.WithSources(failingSource{&calls}, mapSource{"deaths": "3"}))

	assert.Equal(t, "Alice died 3 times, $wins wins", render(t, e, "$username died $deaths times, $wins wins", new(scope())))
	assert.Equal(t, int64(1), calls.Load())
	assert.Equal(t, 1, strings.Count(logs.String(), "identifier source skipped"), logs.String())

	render(t, e, "$deaths", new(scope()))
	assert.Equal(t, int64(2), calls.Load(), "the next render asks the source again")
}

// TestRender_Canceled_B24 ends a render with an error when its context is
// done.
func TestRender_Canceled_B24(t *testing.T) {
	t.Parallel()
	registry := newRegistry(t)

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := template.New(registry).Render(canceled, template.Parse("Hi $username"), new(scope()), template.Text)
	require.ErrorIs(t, err, context.Canceled)

	expired, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	_, err = template.New(registry).Render(expired, template.Parse("Hi $username"), new(scope()), template.Text)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	out, err := template.New(registry).Render(canceled, template.Parse("no identifiers"), new(scope()), template.Text)
	require.NoError(t, err, "a text without tokens needs no context")
	assert.Equal(t, "no identifiers", out)
}

// TestRender_CanceledWhileResolving_B24 cancels the context in a resolver.
func TestRender_CanceledWhileResolving_B24(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	slow := template.Family{Name: "slow", Identifiers: []template.Identifier{{
		Name: "slow",
		Resolve: func(ctx context.Context, _ *template.Scope) (template.Value, bool, error) {
			cancel()
			return template.Value{}, false, ctx.Err()
		},
	}}}
	_, err := template.New(newRegistry(t, slow)).Render(ctx, template.Parse("$slow"), new(scope()), template.Text)
	require.ErrorIs(t, err, context.Canceled)

	var calls atomic.Int64
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	source := cancelingSource{cancel: cancel, calls: &calls}
	_, err = template.New(nil, template.WithSources(source)).Render(ctx, template.Parse("$x"), new(scope()), template.Text)
	require.ErrorIs(t, err, context.Canceled)
}

// cancelingSource cancels the render's context in Match.
type cancelingSource struct {
	cancel context.CancelFunc
	calls  *atomic.Int64
}

func (c cancelingSource) Match(ctx context.Context, _ *template.Scope, _ string) (int, template.Resolver, error) {
	c.calls.Add(1)
	c.cancel()
	return 0, nil, ctx.Err()
}

func TestRender_UnknownEncoding(t *testing.T) {
	t.Parallel()
	_, err := template.New(nil).Render(t.Context(), template.Parse("x"), new(scope()), template.Encoding(99))
	require.Error(t, err)
	assert.Equal(t, "unknown", template.Encoding(99).String())
}

// TestScope_Memo checks that Memo calls its function once per render.
func TestScope_Memo(t *testing.T) {
	t.Parallel()
	var loads atomic.Int64
	loader := func(v string, err error) func() (string, error) {
		return func() (string, error) {
			loads.Add(1)
			return v, err
		}
	}
	memo := func(name, key string, load func() (string, error)) template.Identifier {
		return template.Identifier{Name: name, Resolve: func(_ context.Context, s *template.Scope) (template.Value, bool, error) {
			v, err := s.Memo(key, load)
			return template.TextValue(v), err == nil, err
		}}
	}
	family := template.Family{Name: "memo", Identifiers: []template.Identifier{
		memo("streamtitle", "memo.stream", loader("Stream title", nil)),
		memo("streamtitleagain", "memo.stream", loader("Other title", nil)),
		memo("streamgame", "memo.game", loader("", errors.New("stream state unavailable"))),
		memo("streamgameagain", "memo.game", loader("Chess", nil)),
		{Name: "wrongtype", Resolve: func(_ context.Context, s *template.Scope) (template.Value, bool, error) {
			v, err := s.Memo("memo.stream", func() (int64, error) { return 7, nil })
			return template.IntValue(v), err == nil, err
		}},
	}}
	e := template.New(newRegistry(t, family))
	s := scope()

	assert.Equal(t, "Stream title|Stream title", render(t, e, "$streamtitle|$streamtitleagain", &s))
	assert.Equal(t, int64(1), loads.Load())

	assert.Equal(t, "$streamgame|$streamgameagain", render(t, e, "$streamgame|$streamgameagain", &s))
	assert.Equal(t, int64(2), loads.Load(), "an error is remembered for the render")

	assert.Equal(t, "Stream title|7", render(t, e, "$streamtitle|$wrongtype", &s), "another type does not share the result")

	loads.Store(0)
	load := loader("Stream title", nil)
	for range 2 {
		_, err := s.Memo("memo.stream", load)
		require.NoError(t, err)
	}
	assert.Equal(t, int64(2), loads.Load(), "outside a render Memo does not remember")
}

// TestScope_Share checks the values of the run that a called command shares
// with its caller, and the copy for one that does not wait (spec
// command-engine.md, B35).
func TestScope_Share(t *testing.T) {
	t.Parallel()
	e := template.New(nil)
	caller := scope()
	assert.Empty(t, caller.Values())

	called := caller.Share()
	called.CommandName = "called"
	called.SetValue("Mood", template.TextValue("happy"))
	caller.SetValue("round", template.IntValue(2))
	assert.Equal(t, "happy 2", render(t, e, "$mood $round", &caller))
	assert.Equal(t, "happy 2", render(t, e, "$mood $round", called))
	assert.Empty(t, caller.CommandName, "only the values are shared")

	values := caller.Values()
	assert.Equal(t, map[string]template.Value{"mood": template.TextValue("happy"), "round": template.IntValue(2)}, values)
	values["mood"] = template.TextValue("sad")
	assert.Equal(t, "happy", render(t, e, "$mood", &caller), "Values returns a copy")
}

func TestValues(t *testing.T) {
	t.Parallel()
	assert.Equal(t, template.Value{Text: "x"}, template.TextValue("x"))
	assert.Equal(t, template.Value{Text: "-12", Number: -12, IsNumber: true}, template.IntValue(-12))
	assert.Equal(t, template.Value{Text: "0.1", Number: 0.1, IsNumber: true}, template.FloatValue(0.1))
}

func BenchmarkRender(b *testing.B) {
	e := template.New(newRegistry(b), template.WithSources(mapSource{"deaths": "3"}))
	tmpl := template.Parse("Hey $username, it is $time ($timedigits). $deaths deaths so far; " +
		"arguments: $arg1text and $arg2text. Unknown: $nothing. Cost: 5$.")
	s := scope()
	s.SetValue("raidviewercount", template.IntValue(12))
	for b.Loop() {
		if _, err := e.Render(b.Context(), tmpl, &s, template.Text); err != nil {
			b.Fatal(err)
		}
	}
}

// TestRender_InvalidScope: a scope without the time zone or the argument
// delimiter of the profile, or with arguments but without their text, is
// rejected instead of falling back to a default (Code-ADR-0017).
func TestRender_InvalidScope(t *testing.T) {
	t.Parallel()
	e := template.New(newRegistry(t))
	valid := scope()
	for name, s := range map[string]*template.Scope{
		"no scope":               nil,
		"no time zone":           {ArgDelimiter: "|"},
		"no argument delimiter":  {Location: time.UTC},
		"arguments without text": {Location: time.UTC, ArgDelimiter: "|", Args: []string{"a"}},
	} {
		_, err := e.Render(t.Context(), template.Parse("$time"), s, template.Text)
		require.ErrorIs(t, err, template.ErrInvalidScope, name)
		_, err = e.RenderEach(t.Context(), []template.Template{template.Parse("$time")}, s)
		require.ErrorIs(t, err, template.ErrInvalidScope, name)
	}
	_, err := e.Render(t.Context(), template.Parse("$time"), &valid, template.Text)
	require.NoError(t, err)
}
