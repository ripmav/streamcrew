// SPDX-License-Identifier: MIT

package template_test

import (
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	} {
		f.Add(seed)
	}
	empty := template.New(nil)
	e := template.New(newRegistry(f), template.WithSources(mapSource{"deaths": "3"}))
	f.Fuzz(func(t *testing.T, text string) {
		tmpl := template.Parse(text)
		assert.Equal(t, text, tmpl.String())
		for _, enc := range []template.Encoding{template.Text, template.URL, template.HTML, template.JSON} {
			out, err := empty.Render(t.Context(), tmpl, nil, enc)
			require.NoError(t, err)
			assert.Equal(t, text, out, "without identifiers the output equals the input")

			out, err = e.Render(t.Context(), tmpl, nil, enc)
			require.NoError(t, err)
			if utf8.ValidString(text) {
				assert.True(t, utf8.ValidString(out), "the output of valid UTF-8 is valid UTF-8")
			}
		}
	})
}
