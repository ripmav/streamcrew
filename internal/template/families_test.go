// SPDX-License-Identifier: MIT

package template_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/decimal"
	"github.com/ripmav/streamcrew/internal/domain/counter"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/template"
)

// The registry is what counter.CheckReserved needs.
var _ counter.Reserver = (*template.Registry)(nil)

// mvpRegistry returns a registry with the families of this package.
func mvpRegistry(t testing.TB, states template.StreamStates) *template.Registry {
	t.Helper()
	r, err := template.NewRegistry(
		template.CharacterFamily(),
		template.ArgumentFamily(),
		template.MessageFamily(),
		template.DateTimeFamily(),
		template.RandomFamily(),
		template.StreamFamily(states),
		template.RunFamily(),
	)
	require.NoError(t, err)
	return r
}

// profileZone is the time zone of the profile in the tests, 4 hours behind
// UTC like New York in summer.
func profileZone() *time.Location {
	return time.FixedZone("UTC-4", -4*60*60)
}

// sleepUntil advances the clock of a synctest bubble to t, which starts at
// midnight UTC on 1 January 2000.
func sleepUntil(t time.Time) {
	time.Sleep(time.Until(t))
}

func TestArgumentFamily_Golden(t *testing.T) {
	t.Parallel()
	s := template.Scope{
		Location: time.UTC, ArgDelimiter: "|",
		Args:     []string{"add", "Best stream ever", "|", "Alice"},
		ArgsText: `add "Best stream ever" | Alice`,
	}
	renderGolden(t, template.New(mvpRegistry(t, nil)), &s, "arguments")
}

func TestArgumentFamily(t *testing.T) {
	t.Parallel()
	e := template.New(mvpRegistry(t, nil))
	tests := []struct {
		name  string
		scope template.Scope
		text  string
		want  string
	}{
		{"no arguments", template.Scope{Location: time.UTC, ArgDelimiter: "|"}, "[$allargs] $argcount $argdelimitedcount $arg1text", "[] 0 0 $arg1text"},
		{"text after the trigger as written", template.Scope{Location: time.UTC, ArgDelimiter: "|", Args: []string{"a", "b"}, ArgsText: "a  b"}, "$allargs", "a  b"},
		{"no text after the number", template.Scope{Location: time.UTC, ArgDelimiter: "|", Args: []string{"a", "b"}, ArgsText: "a b"}, "$arg1x $arg2 $arg1:2", "$arg1x $arg2 $arg1:2"},
		{"own delimiter", template.Scope{Location: time.UTC, ArgsText: " a ; b;;c ", ArgDelimiter: ";"}, "$argdelimitedcount:$argdelimited2text:$argdelimited3text:$argdelimited4text", "4:b::c"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, render(t, e, tc.text, &tc.scope))
		})
	}
}

func TestMessageFamily_Golden(t *testing.T) {
	t.Parallel()
	s := template.Scope{
		Location: time.UTC, ArgDelimiter: "|",
		Message: "Hello Kappa world  Kappa PogChamp",
		Emotes:  []string{"Kappa", "Kappa", "PogChamp"},
	}
	renderGolden(t, template.New(mvpRegistry(t, nil)), &s, "message")
}

func TestMessageFamily(t *testing.T) {
	t.Parallel()
	e := template.New(mvpRegistry(t, nil))
	s := scope()
	assert.Equal(t, "$message $messagenoemotes $messageemotecount", render(t, e, "$message $messagenoemotes $messageemotecount", &s))

	s.SetValue(template.EventMessage, template.TextValue("Thanks for the sub!"))
	assert.Equal(t, "Thanks for the sub!", render(t, e, "$message", &s), "the event's message ranks first (B10)")
}

func TestDateTimeFamily_Golden_B40_B41(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sleepUntil(time.Date(2009, time.June, 15, 17, 45, 20, 0, time.UTC))
		s := template.Scope{ArgDelimiter: "|", Location: profileZone()}
		renderGolden(t, template.New(mvpRegistry(t, nil)), &s, "datetime")

		utc := scope()
		assert.Equal(t, "5:45 PM", render(t, template.New(mvpRegistry(t, nil)), "$time", &utc))
	})
}

func TestDateTimeFamily_Padding(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sleepUntil(time.Date(2026, time.January, 5, 9, 7, 3, 0, time.UTC))
		e := template.New(mvpRegistry(t, nil))
		assert.Equal(t, "1/5/2026 9:07 AM|2026-01-05|09:07:03|0907|Monday",
			render(t, e, "$datetime|$dateyear-$datemonth-$dateday|$timehour:$timeminute:$timesecond|$timedigits|$dayoftheweek", new(scope())))
	})
}

// TestRandomFamily_B21_B73 draws random numbers; each occurrence draws anew.
func TestRandomFamily_B21_B73(t *testing.T) {
	t.Parallel()
	e := template.New(mvpRegistry(t, nil))

	for text, want := range map[string]string{
		"$randomnumber1":                                  "1",
		"$randomnumber5:5":                                "5",
		"$randomnumber0:0x":                               "0x",
		"$randomnumber0":                                  "$randomnumber0",
		"$randomnumber6:1":                                "$randomnumber6:1",
		"$randomnumber1:0":                                "$randomnumber1:0",
		"$randomnumber1:1a":                               "1a",
		"$randomnumber1:":                                 "1:",
		"$randomnumber1:x":                                "1:x",
		"$randomnumber" + strings.Repeat("9", 20):         "$randomnumber" + strings.Repeat("9", 20),
		"$randomnumber1:" + strings.Repeat("9", 20):       "$randomnumber1:" + strings.Repeat("9", 20),
		"$randomnumber" + strconv.Itoa(1<<53) + ":" + "1": "$randomnumber" + strconv.Itoa(1<<53) + ":1",
	} {
		assert.Equal(t, want, render(t, e, text, new(scope())), text)
	}

	out := render(t, e, strings.Repeat("$randomnumber2 ", 64), new(scope()))
	seen := map[string]int{}
	for v := range strings.FieldsSeq(out) {
		seen[v]++
	}
	assert.Len(t, seen, 2, "both numbers appear in 64 draws: %v", seen)
	assert.Equal(t, 64, seen["1"]+seen["2"])

	for v := range strings.FieldsSeq(render(t, e, strings.Repeat("$randomnumber10:12 ", 32), new(scope()))) {
		n, err := strconv.Atoi(v)
		require.NoError(t, err)
		assert.True(t, n >= 10 && n <= 12, n)
	}
}

// fakeStream reports a fixed stream state and counts the calls.
type fakeStream struct {
	state template.StreamState
	err   error
	calls *atomic.Int64
}

func (f fakeStream) StreamState(_ context.Context, p platform.Name) (template.StreamState, error) {
	f.calls.Add(1)
	if p != platform.Twitch {
		return template.StreamState{}, errors.New("no stream on " + string(p))
	}
	return f.state, f.err
}

func TestStreamFamily_Golden_B43(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sleepUntil(time.Date(2009, time.June, 15, 17, 45, 20, 0, time.UTC))
		var calls atomic.Int64
		stream := fakeStream{calls: &calls, state: template.StreamState{
			Live:      true,
			Title:     "Let's play some chess!",
			Game:      "Chess",
			Viewers:   new(int64(50)),
			Chatters:  new(int64(30)),
			StartedAt: time.Now().Add(-(2*time.Hour + 21*time.Minute + 43*time.Second)),
		}}
		s := template.Scope{ArgDelimiter: "|", Platform: platform.Twitch, Location: profileZone()}
		renderGolden(t, template.New(mvpRegistry(t, stream)), &s, "stream")
		assert.Equal(t, int64(5), calls.Load(), "one call per render, and the golden file has five templates")
	})
}

func TestStreamFamily_Uptime_B43(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stream := fakeStream{calls: new(atomic.Int64), state: template.StreamState{
			Live:      true,
			StartedAt: time.Now(),
		}}
		e := template.New(mvpRegistry(t, stream))
		s := template.Scope{Location: time.UTC, ArgDelimiter: "|", Platform: platform.Twitch}
		const text = "$streamuptimetotal $streamuptimehours $streamuptimeminutes $streamuptimeseconds"
		assert.Equal(t, "0:00 0 0 0", render(t, e, text, &s))
		time.Sleep(26*time.Hour + 5*time.Minute + 7*time.Second)
		assert.Equal(t, "26:05 26 5 7", render(t, e, text, &s))
	})
}

func TestStreamFamily_NoValue(t *testing.T) {
	t.Parallel()
	const text = "$streamtitle $streamgamename $streamislive $streamviewercount $streamstarttime $streamuptimetotal"
	offline := fakeStream{calls: new(atomic.Int64), state: template.StreamState{Title: "Soon"}}
	failing := fakeStream{calls: new(atomic.Int64), err: errors.New("platform unavailable")}
	tests := []struct {
		name     string
		states   template.StreamStates
		platform platform.Name
		want     string
	}{
		{"offline", offline, platform.Twitch, "Soon $streamgamename false $streamviewercount $streamstarttime $streamuptimetotal"},
		{"no port", nil, platform.Twitch, text},
		{"error", failing, platform.Twitch, text},
		{"other platform", offline, platform.Kick, text},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := template.Scope{Location: time.UTC, ArgDelimiter: "|", Platform: tc.platform}
			assert.Equal(t, tc.want, render(t, template.New(mvpRegistry(t, tc.states)), text, &s))
		})
	}
}

// fakeCounters lists fixed counters and counts the calls.
type fakeCounters struct {
	list  []counter.Counter
	err   error
	calls *atomic.Int64
}

func (f fakeCounters) Counters(context.Context) ([]counter.Counter, error) {
	f.calls.Add(1)
	return f.list, f.err
}

func TestCounterSource_Golden(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	counters := fakeCounters{calls: &calls, list: []counter.Counter{
		{Name: "Deaths", Value: decimal.New(1234567)},
		{Name: "neg", Value: decimal.New(-1234)},
		{Name: "xdisplay", Value: decimal.New(7)},
		{Name: "x", Value: decimal.New(5)},
	}}
	e := template.New(mvpRegistry(t, nil), template.WithSources(template.CounterSource(counters)))
	renderGolden(t, e, new(scope()), "counter")
	assert.Equal(t, int64(5), calls.Load(), "one call per render, and the golden file has five templates")
}

func TestCounterSource_Display(t *testing.T) {
	t.Parallel()
	for value, want := range map[string]string{
		"0":                    "0",
		"999":                  "999",
		"-999":                 "-999",
		"1000":                 "1,000",
		"-1000":                "-1,000",
		"123456":               "123,456",
		"9223372036854775807":  "9,223,372,036,854,775,807",
		"-9223372036854775808": "-9,223,372,036,854,775,808",
		"1234.5":               "1,234.50",
		"-1234567.891":         "-1,234,567.89",
		"0.005":                "0.01",
		"-0.004":               "0.00",
		"999.999":              "1,000.00",
		"0.1":                  "0.10",
	} {
		v, err := decimal.Parse(value)
		require.NoError(t, err)
		counters := fakeCounters{calls: new(atomic.Int64), list: []counter.Counter{{Name: "c", Value: v}}}
		e := template.New(nil, template.WithSources(template.CounterSource(counters)))
		assert.Equal(t, want, render(t, e, "$cdisplay", new(scope())), value)
		assert.Equal(t, v.String(), render(t, e, "$c", new(scope())), "%s: $c is exact", value)
	}
}

func TestCounterSource_Error(t *testing.T) {
	t.Parallel()
	logger, logs := logBuffer()
	counters := fakeCounters{calls: new(atomic.Int64), err: errors.New("database locked")}
	e := template.New(mvpRegistry(t, nil), template.WithLogger(logger), template.WithSources(template.CounterSource(counters)))
	assert.Equal(t, "$deaths and 2 arguments", render(t, e, "$deaths and $argcount arguments", &template.Scope{Location: time.UTC, ArgDelimiter: "|", Args: []string{"a", "b"}, ArgsText: "a b"}))
	assert.Contains(t, logs.String(), "database locked")
}

func TestRunFamily_Golden(t *testing.T) {
	t.Parallel()
	s := template.Scope{Location: time.UTC, ArgDelimiter: "|", Platform: platform.Twitch, CommandName: "shoutout"}
	renderGolden(t, template.New(mvpRegistry(t, nil)), &s, "run")
}

func TestRunFamily(t *testing.T) {
	t.Parallel()
	e := template.New(mvpRegistry(t, nil))
	assert.Equal(t, "$commandname $streamingplatform", render(t, e, "$commandname $streamingplatform", new(scope())))
	assert.Equal(t, "velora", render(t, e, "$streamingplatform", &template.Scope{Location: time.UTC, ArgDelimiter: "|", Platform: "velora"}))
}

// TestRegistry_ReservedMVP checks typical counter names against the families
// of this package (B12).
func TestRegistry_ReservedMVP(t *testing.T) {
	t.Parallel()
	r := mvpRegistry(t, nil)
	for name, want := range map[string]string{
		"deaths":   "",
		"wins":     "",
		"hugs":     "",
		"d":        "datetime",
		"timer":    "time",
		"messages": "message",
		"stream":   "streamtitle",
		"random":   "randomnumber<max>",
		"args":     "arg<n>text",
	} {
		with, reserved := r.Reserved(name)
		assert.Equal(t, want != "", reserved, name)
		assert.Equal(t, want, with, name)
	}
}
