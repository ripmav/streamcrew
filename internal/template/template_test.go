// SPDX-License-Identifier: MIT

package template_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/template"
)

// TestRender_Golden renders the templates of testdata/syntax.txt and
// compares them with testdata/syntax.golden (B1–B4, B6, B7, B23, B32, B33,
// B70–B73, B78).
func TestRender_Golden(t *testing.T) {
	t.Parallel()
	renderGolden(t, template.New(newRegistry(t)), nil, "syntax")
}

func TestParse_String(t *testing.T) {
	t.Parallel()
	for _, text := range []string{"", "$", "plain", "$username", "a $$b $ c$"} {
		assert.Equal(t, text, template.Parse(text).String())
	}
}

// TestRender_TokensStay covers tokens that stay as written (B3, B4, B6,
// B73).
func TestRender_TokensStay(t *testing.T) {
	t.Parallel()
	e := template.New(newRegistry(t))
	for _, text := range []string{
		"",
		"$",
		"$$",
		"costs 5$",
		"$ünicode937",
		"$unknown",
		"$user",
		"$targetusername",
		"$arg3text",
		"$arg0text",
		"$argtext",
		"$unicode",
		"$unicode0",
		"$unicode99999999",
		"$UNICODE55296",
	} {
		t.Run(text, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, text, render(t, e, text, nil))
		})
	}
}

// TestRender_ZeroValues renders with the zero values of engine parts.
func TestRender_ZeroValues(t *testing.T) {
	t.Parallel()
	var tmpl template.Template
	out, err := template.New(nil).Render(t.Context(), tmpl, nil, template.Text)
	require.NoError(t, err)
	assert.Empty(t, out)

	var s template.Scope
	s.SetValue("Name", template.TextValue("Alice"))
	assert.Equal(t, "Hi Alice", render(t, template.New(nil), "Hi $name", &s))
}
