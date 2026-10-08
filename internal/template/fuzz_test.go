// SPDX-License-Identifier: MIT

package template_test

import (
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/counter"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/template"
)

// FuzzRender renders arbitrary texts: rendering never fails or panics, and
// without known identifiers the output equals the input in every encoding
// (spec template.md, acceptance criteria).
func FuzzRender(f *testing.F) {
	for _, seed := range []string{
		"", "$", "$$", "Hello $username!", "$usernames $arg1text2 $arg9text",
		"$unicode36username", "$unicode99999999999999999999", "$linebreak$",
		"$followage $targetusername", "a$:b$1:2$", "\xff$user\x00name",
		"$arg1:3text $argdelimited2text $randomnumber5:10 $randomnumber0",
		"$deathsdisplay $datetime $streamuptimetotal $messagenoemotes",
		"$userfollowage $targetuserroles $arg1username $randomuserid $botuser",
	} {
		f.Add(seed)
	}
	empty := template.New(nil)
	e := template.New(newRegistry(f), template.WithSources(mapSource{"deaths": "3"}))
	counters := fakeCounters{calls: new(atomic.Int64), list: []counter.Counter{{Name: "deaths", Value: 1234}}}
	stream := fakeStream{calls: new(atomic.Int64), state: template.StreamState{Live: true, Title: "t", StartedAt: time.Now()}}
	alice, bob, users := testUsers()
	all, err := template.NewRegistry(
		template.CharacterFamily(), template.ArgumentFamily(), template.MessageFamily(),
		template.DateTimeFamily(), template.RandomFamily(), template.StreamFamily(stream),
		template.RunFamily(), template.UserFamily(users),
	)
	require.NoError(f, err)
	mvp := template.New(all, template.WithSources(template.CounterSource(counters)))
	scope := template.Scope{
		Platform: platform.Twitch,
		User:     &alice,
		Target:   &bob,
		Message:  "!cmd a b | c Kappa",
		Emotes:   []string{"Kappa"},
		Args:     []string{"a", "b", "|", "c", "Kappa"},
	}
	f.Fuzz(func(t *testing.T, text string) {
		tmpl := template.Parse(text)
		assert.Equal(t, text, tmpl.String())
		for _, enc := range []template.Encoding{template.Text, template.URL, template.HTML, template.JSON} {
			out, err := empty.Render(t.Context(), tmpl, nil, enc)
			require.NoError(t, err)
			assert.Equal(t, text, out, "without identifiers the output equals the input")

			for _, out := range []string{renderNoError(t, e, tmpl, nil, enc), renderNoError(t, mvp, tmpl, &scope, enc)} {
				if utf8.ValidString(text) {
					assert.True(t, utf8.ValidString(out), "the output of valid UTF-8 is valid UTF-8")
				}
			}
		}
	})
}

// renderNoError renders and fails the test on an error.
func renderNoError(t *testing.T, e *template.Engine, tmpl template.Template, s *template.Scope, enc template.Encoding) string {
	t.Helper()
	out, err := e.Render(t.Context(), tmpl, s, enc)
	require.NoError(t, err)
	return out
}
