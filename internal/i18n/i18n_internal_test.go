// SPDX-License-Identifier: MIT

package i18n

import (
	"go/ast"
	goparser "go/parser"
	"go/token"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/text/feature/plural"
)

// TestKeys covers ADR-0022, point 8: every constant of keys.go has a
// message in every catalog, and every message has a constant.
func TestKeys(t *testing.T) {
	t.Parallel()
	file, err := goparser.ParseFile(token.NewFileSet(), "keys.go", nil, 0)
	require.NoError(t, err)
	var constants []Key
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		if typ, ok := spec.Type.(*ast.Ident); !ok || typ.Name != "Key" {
			return true
		}
		for _, v := range spec.Values {
			lit, ok := v.(*ast.BasicLit)
			require.True(t, ok, "keys are string literals")
			s, err := strconv.Unquote(lit.Value)
			require.NoError(t, err)
			constants = append(constants, Key(s))
		}
		return true
	})
	require.NotEmpty(t, constants)

	sources, err := embedded()
	require.NoError(t, err)
	for _, lang := range Languages() {
		assert.ElementsMatch(t, constants, slices.Collect(maps.Keys(sources[lang])), lang)
	}
	c, err := Load()
	require.NoError(t, err)
	assert.ElementsMatch(t, constants, c.Keys())
}

// TestPluralForms checks the plural forms of each language against the
// rules of CLDR in golang.org/x/text/feature/plural (ADR-0022, point 4).
func TestPluralForms(t *testing.T) {
	t.Parallel()
	for _, lang := range Languages() {
		for _, ordinal := range []bool{false, true} {
			rules := plural.Cardinal
			if ordinal {
				rules = plural.Ordinal
			}
			seen := map[plural.Form]bool{}
			for i := range 10_000 {
				seen[rules.MatchPlural(lang.tag(), i, 0, 0, 0, 0)] = true
				if !ordinal {
					for _, digits := range []int{1, 2, 3} {
						seen[rules.MatchPlural(lang.tag(), i, digits, digits, i%1000+1, i%1000+1)] = true
					}
				}
			}
			assert.ElementsMatch(t, pluralForms(lang, ordinal), slices.Collect(maps.Keys(seen)), "%s ordinal=%v", lang, ordinal)
		}
	}
}

// TestDuration covers ADR-0022, point 6: durations in whole seconds,
// rounded up, with every unit that is not 0, from the messages of the
// embedded catalogs.
func TestDuration(t *testing.T) {
	t.Parallel()
	sources, err := embedded()
	require.NoError(t, err)
	sources[English]["test.wait"] = "wait {d}"
	sources[German]["test.wait"] = "warte {d}"
	c, err := New(sources)
	require.NoError(t, err)
	for _, tc := range []struct {
		d      time.Duration
		en, de string
	}{
		{0, "0 seconds", "0 Sekunden"},
		{time.Nanosecond, "1 second", "1 Sekunde"},
		{time.Second, "1 second", "1 Sekunde"},
		{1200 * time.Millisecond, "2 seconds", "2 Sekunden"},
		{65 * time.Second, "1 minute 5 seconds", "1 Minute 5 Sekunden"},
		{time.Hour, "1 hour", "1 Stunde"},
		{2 * time.Hour, "2 hours", "2 Stunden"},
		{time.Hour + time.Minute + time.Second, "1 hour 1 minute 1 second", "1 Stunde 1 Minute 1 Sekunde"},
		{25*time.Hour + 61*time.Second, "1 day 1 hour 1 minute 1 second", "1 Tag 1 Stunde 1 Minute 1 Sekunde"},
		{48*time.Hour + 30*time.Second, "2 days 30 seconds", "2 Tage 30 Sekunden"},
		{1000 * 24 * time.Hour, "1,000 days", "1.000 Tage"},
	} {
		m := Message{Key: "test.wait", Args: map[string]Value{"d": Duration(tc.d)}}
		got, err := c.Render(English, m)
		require.NoError(t, err)
		assert.Equal(t, "wait "+tc.en, got, tc.d)
		got, err = c.Render(German, m)
		require.NoError(t, err)
		assert.Equal(t, "warte "+tc.de, got, tc.d)
	}
	_, err = c.Render(English, Message{Key: "test.wait", Args: map[string]Value{"d": Duration(-time.Second)}})
	require.ErrorIs(t, err, ErrValueType)
}

// FuzzParse: the parser never panics, and a message it accepts renders
// with values that fit how it uses them (ADR-0022, point 4).
func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"Hello", "Hi {user}!", "It''s '{literal}' and it's {name}.", "a '{b", "'}' and '#'",
		"{n, number}", "{n, number, integer}", "{n, number, percent}",
		"{n, plural, =0 {none} one {# item} other {# items}}",
		"{n, plural, one {'#' #} other {# '#'}}",
		"{n, selectordinal, one {#st} two {#nd} few {#rd} other {#th}}",
		"{role, select, mod {moderators} other {{role}}}",
		"{n, plural, one {{g, select, f {she} other {he}} #} other {{g, select, f {she} other {he}} #}}",
		"{", "}", "{n, plural, one {a}", "{n, date}", "{n, plural, offset:1 other {a}}",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, src string) {
		m, err := parse(src)
		if err != nil {
			return
		}
		classes, err := argClasses(m)
		if err != nil {
			return
		}
		args := make(map[string]Value, len(classes))
		for name, class := range classes {
			switch class {
			case classNumber:
				args[name] = Decimal(float64(len(name)) + 0.5)
			case classText, classChoice:
				args[name] = Text(strings.ToUpper(name))
			}
		}
		for _, lang := range Languages() {
			var b strings.Builder
			if err := (renderer{lang: lang, args: args}).message(&b, m, nil); err != nil {
				t.Fatalf("render %q in %s: %v", src, lang, err)
			}
		}
	})
}
