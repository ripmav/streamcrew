// SPDX-License-Identifier: Apache-2.0

package i18n_test

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/i18n"
)

// pair is a message in English and German.
type pair struct{ en, de string }

// catalog returns a catalog of the messages by key.
func catalog(t *testing.T, messages map[i18n.Key]pair) *i18n.Catalog {
	t.Helper()
	en, de := map[i18n.Key]string{}, map[i18n.Key]string{}
	for k, p := range messages {
		en[k], de[k] = p.en, p.de
	}
	c, err := i18n.New(map[i18n.Language]map[i18n.Key]string{i18n.English: en, i18n.German: de})
	require.NoError(t, err)
	return c
}

// args returns the values of a message: one, and more with arg.
func args(name string, v i18n.Value, more ...arg) map[string]i18n.Value {
	m := map[string]i18n.Value{name: v}
	for _, a := range more {
		m[a.name] = a.value
	}
	return m
}

// arg is a further value for args.
type arg struct {
	name  string
	value i18n.Value
}

// TestRender covers ADR-0022, point 3: the subset of ICU MessageFormat in
// English and German.
func TestRender(t *testing.T) {
	t.Parallel()
	c := catalog(t, map[i18n.Key]pair{
		"text":     {"Hello", "Hallo"},
		"arg":      {"Hi { user }!", "Hallo {user}!"},
		"quote":    {"It''s '{literal}' and it's {name}.", "Es ist '{literal}' und {name}’s."},
		"open":     {"a '{b", "a '{b"},
		"icu":      {"This '{isn''t}' obvious", "Das ist '{nicht''s}' klar"},
		"close":    {"'}' and '#'", "'}' und '#'"},
		"number":   {"{n, number}", "{n, number}"},
		"integer":  {"{n, number, integer}", "{n, number, integer}"},
		"percent":  {"{n, number, percent}", "{n, number, percent}"},
		"plain":    {"{n}", "{n}"},
		"items":    {"{n, plural, =0 {none} one {# item} other {# items}}", "{n, plural, =0 {keine} one {# Sache} other {# Sachen}}"},
		"pound":    {"{n, plural, one {'#' #} other {# '#'}}", "{n, plural, one {'#' #} other {# '#'}}"},
		"place":    {"{n, selectordinal, one {#st} two {#nd} few {#rd} other {#th}}", "{n, selectordinal, other {#.}}"},
		"role":     {"{role, select, mod {moderators} other {{role}}}", "{role, select, mod {Moderatoren} other {{role}}}"},
		"nested":   {"{n, plural, one {{g, select, f {she has} other {he has}} # point} other {{g, select, f {she has} other {he has}} # points}}", "{n, plural, one {{g, select, f {sie hat} other {er hat}} # Punkt} other {{g, select, f {sie hat} other {er hat}} # Punkte}}"},
		"deep":     {"{n, plural, one {x} other {{g, select, other {#}}}}", "{n, plural, one {x} other {{g, select, other {#}}}}"},
		"spaced":   {"{ n , plural , one { a } other { b } }", "{n,plural,one{a}other{b}}"},
		"multiple": {"{a} and {a} and {b, number}", "{a} und {a} und {b, number}"},
	})
	for _, tc := range []struct {
		key    i18n.Key
		args   map[string]i18n.Value
		en, de string
	}{
		{"text", nil, "Hello", "Hallo"},
		{"arg", args("user", i18n.Text("ada")), "Hi ada!", "Hallo ada!"},
		{"quote", args("name", i18n.Text("x")), "It's {literal} and it's x.", "Es ist {literal} und x’s."},
		{"open", nil, "a {b", "a {b"},
		{"icu", nil, "This {isn't} obvious", "Das ist {nicht's} klar"},
		{"close", nil, "} and '#'", "} und '#'"},
		{"number", args("n", i18n.Decimal(1234567.5)), "1,234,567.5", "1.234.567,5"},
		{"number", args("n", i18n.Int(-1234567)), "-1,234,567", "-1.234.567"},
		{"number", args("n", i18n.Decimal(0.1234)), "0.123", "0,123"},
		{"integer", args("n", i18n.Decimal(2.5)), "2", "2"},
		{"integer", args("n", i18n.Decimal(1234.6)), "1,235", "1.235"},
		{"percent", args("n", i18n.Decimal(0.25)), "25%", "25 %"},
		{"plain", args("n", i18n.Decimal(1234567.5)), "1234567.5", "1234567.5"},
		{"plain", args("n", i18n.Int(42)), "42", "42"},
		{"plain", args("n", i18n.Text("x")), "x", "x"},
		{"items", args("n", i18n.Int(0)), "none", "keine"},
		{"items", args("n", i18n.Decimal(0)), "none", "keine"},
		{"items", args("n", i18n.Int(1)), "1 item", "1 Sache"},
		{"items", args("n", i18n.Decimal(1)), "1 item", "1 Sache"},
		{"items", args("n", i18n.Int(2)), "2 items", "2 Sachen"},
		{"items", args("n", i18n.Decimal(1.5)), "1.5 items", "1,5 Sachen"},
		{"items", args("n", i18n.Int(1000)), "1,000 items", "1.000 Sachen"},
		{"items", args("n", i18n.Int(-1)), "-1 item", "-1 Sache"},
		{"items", args("n", i18n.Int(21)), "21 items", "21 Sachen"},
		{"items", args("n", i18n.Int(10_000_001)), "10,000,001 items", "10.000.001 Sachen"},
		{"items", args("n", i18n.Int(math.MinInt64)), "-9,223,372,036,854,775,808 items", "-9.223.372.036.854.775.808 Sachen"},
		{"items", args("n", i18n.Decimal(1e21)), "1,000,000,000,000,000,000,000 items", "1.000.000.000.000.000.000.000 Sachen"},
		{"items", args("n", i18n.Decimal(1e-300)), "0 items", "0 Sachen"},
		{"pound", args("n", i18n.Int(1)), "# 1", "# 1"},
		{"pound", args("n", i18n.Int(2)), "2 #", "2 #"},
		{"place", args("n", i18n.Int(1)), "1st", "1."},
		{"place", args("n", i18n.Int(2)), "2nd", "2."},
		{"place", args("n", i18n.Int(3)), "3rd", "3."},
		{"place", args("n", i18n.Int(4)), "4th", "4."},
		{"place", args("n", i18n.Int(11)), "11th", "11."},
		{"place", args("n", i18n.Int(12)), "12th", "12."},
		{"place", args("n", i18n.Int(13)), "13th", "13."},
		{"place", args("n", i18n.Int(21)), "21st", "21."},
		{"place", args("n", i18n.Int(102)), "102nd", "102."},
		{"place", args("n", i18n.Int(111)), "111th", "111."},
		{"role", args("role", i18n.Text("mod")), "moderators", "Moderatoren"},
		{"role", args("role", i18n.Text("VIPs")), "VIPs", "VIPs"},
		{"nested", args("n", i18n.Int(1), arg{"g", i18n.Text("f")}), "she has 1 point", "sie hat 1 Punkt"},
		{"nested", args("n", i18n.Int(3), arg{"g", i18n.Text("m")}), "he has 3 points", "er hat 3 Punkte"},
		{"deep", args("n", i18n.Int(2), arg{"g", i18n.Text("x")}), "#", "#"},
		{"spaced", args("n", i18n.Int(1)), " a ", "a"},
		{"multiple", args("a", i18n.Text("x"), arg{"b", i18n.Int(1000)}), "x and x and 1,000", "x und x und 1.000"},
	} {
		got, err := c.Render(i18n.English, i18n.Message{Key: tc.key, Args: tc.args})
		require.NoError(t, err, tc.key)
		assert.Equal(t, tc.en, got, "%s %v", tc.key, tc.args)
		got, err = c.Render(i18n.German, i18n.Message{Key: tc.key, Args: tc.args})
		require.NoError(t, err, tc.key)
		assert.Equal(t, tc.de, got, "%s %v", tc.key, tc.args)
	}
}

// TestRenderFails covers ADR-0022, point 9: a missing or unfitting value,
// an unknown key and an unknown language are errors.
func TestRenderFails(t *testing.T) {
	t.Parallel()
	c := catalog(t, map[i18n.Key]pair{
		"arg":    {"{x}", "{x}"},
		"number": {"{n, number}", "{n, number}"},
		"plural": {"{n, plural, one {a} other {b}}", "{n, plural, one {a} other {b}}"},
		"select": {"{s, select, other {a}}", "{s, select, other {a}}"},
	})
	for _, tc := range []struct {
		lang i18n.Language
		key  i18n.Key
		args map[string]i18n.Value
		want error
	}{
		{i18n.English, "arg", nil, i18n.ErrMissingValue},
		{i18n.English, "arg", args("x", i18n.Value{}), i18n.ErrMissingValue},
		{i18n.English, "number", args("n", i18n.Value{}), i18n.ErrMissingValue},
		{i18n.English, "select", args("s", i18n.Value{}), i18n.ErrMissingValue},
		{i18n.English, "number", args("n", i18n.Text("1")), i18n.ErrValueType},
		{i18n.English, "number", args("n", i18n.Decimal(math.NaN())), i18n.ErrValueType},
		{i18n.English, "number", args("n", i18n.Decimal(math.Inf(1))), i18n.ErrValueType},
		{i18n.English, "number", args("n", i18n.Duration(time.Second)), i18n.ErrValueType},
		{i18n.German, "plural", args("n", i18n.Text("1")), i18n.ErrValueType},
		{i18n.German, "plural", nil, i18n.ErrMissingValue},
		{i18n.English, "select", args("s", i18n.Int(1)), i18n.ErrValueType},
		{i18n.English, "select", nil, i18n.ErrMissingValue},
		{i18n.English, "missing", nil, i18n.ErrUnknownKey},
		{"fr", "arg", args("x", i18n.Text("x")), i18n.ErrUnknownLanguage},
		{"", "arg", args("x", i18n.Text("x")), i18n.ErrUnknownLanguage},
	} {
		got, err := c.Render(tc.lang, i18n.Message{Key: tc.key, Args: tc.args})
		require.ErrorIs(t, err, tc.want, "%s %s %v", tc.lang, tc.key, tc.args)
		assert.Empty(t, got)
	}
}

// TestNewFails covers ADR-0022, points 3, 4 and 8: messages outside the
// subset, plural forms the language does not have or misses, and catalogs
// whose languages differ in keys or values are rejected.
func TestNewFails(t *testing.T) {
	t.Parallel()
	ok := "fine"
	for name, tc := range map[string]struct {
		en, de map[i18n.Key]string
		want   string
	}{
		"unclosed argument":    {msgs("k", "{"), msgs("k", ok), "want an argument name"},
		"name only":            {msgs("k", "{n"), msgs("k", ok), `want "}" or ","`},
		"no type":              {msgs("k", "{n,"), msgs("k", ok), `argument type "" is not supported`},
		"uppercase name":       {msgs("k", "{N}"), msgs("k", ok), "want an argument name"},
		"number name":          {msgs("k", "{1}"), msgs("k", ok), "want an argument name"},
		"stray brace":          {msgs("k", "a } b"), msgs("k", ok), `unexpected '}'`},
		"date":                 {msgs("k", "{d, date}"), msgs("k", ok), `argument type "date" is not supported`},
		"time":                 {msgs("k", "{d, time, short}"), msgs("k", ok), `argument type "time"`},
		"spellout":             {msgs("k", "{n, spellout}"), msgs("k", ok), `argument type "spellout"`},
		"ordinal":              {msgs("k", "{n, ordinal}"), msgs("k", ok), `argument type "ordinal"`},
		"currency":             {msgs("k", "{n, number, currency}"), msgs("k", ok), `number style "currency"`},
		"skeleton":             {msgs("k", "{n, number, ::percent}"), msgs("k", ok), `number style ""`},
		"number unclosed":      {msgs("k", "{n, number, integer"), msgs("k", ok), `want '}'`},
		"offset":               {msgs("k", "{n, plural, offset:1 one {a} other {b}}"), msgs("k", ok), "offset is not supported"},
		"plural without other": {msgs("k", "{n, plural, one {a}}"), msgs("k", ok), "without the case other"},
		"plural form twice":    {msgs("k", "{n, plural, one {a} one {b} other {c}}"), msgs("k", ok), "case one twice"},
		"exact case twice":     {msgs("k", "{n, plural, =1 {a} =1 {b} one {c} other {d}}"), msgs("k", ok), "case =1 twice"},
		"exact not a number":   {msgs("k", "{n, plural, =x {a} other {b}}"), msgs("k", ok), `after "="`},
		"unknown form":         {msgs("k", "{n, plural, lots {a} other {b}}"), msgs("k", ok), `not "lots"`},
		"plural unclosed":      {msgs("k", "{n, plural, one {a} other {b}"), msgs("k", ok), `not ""`},
		"case unclosed":        {msgs("k", "{n, plural, one {a"), msgs("k", ok), "without its closing"},
		"case without brace":   {msgs("k", "{n, plural, one a other {b}}"), msgs("k", ok), `want '{'`},
		"select without other": {msgs("k", "{s, select, a {x}}"), msgs("k", ok), "without the case other"},
		"select case twice":    {msgs("k", "{s, select, a {x} a {y} other {z}}"), msgs("k", ok), "case a twice"},
		"select bad case":      {msgs("k", "{s, select, A {x} other {z}}"), msgs("k", ok), "want a case"},
		"number and choice":    {msgs("k", "{x, number} {x, select, other {y}}"), msgs("k", ok), "as a number and as a choice"},
		"few in German":        {msgs("k", "{n, plural, one {a} other {b}}"), msgs("k", "{n, plural, one {a} few {b} other {c}}"), "de has no plural form few"},
		"English without one":  {msgs("k", "{n, plural, other {b}}"), msgs("k", "{n, plural, one {a} other {b}}"), "without the form one that en needs"},
		"German ordinal one":   {msgs("k", "{n, selectordinal, one {a} two {b} few {c} other {d}}"), msgs("k", "{n, selectordinal, one {a} other {b}}"), "de has no plural form one"},
		"English ordinal two":  {msgs("k", "{n, selectordinal, one {a} few {c} other {d}}"), msgs("k", "{n, selectordinal, other {b}}"), "without the form two"},
		"German misses a key":  {msgs("k", ok, msg{"j", ok}), msgs("k", ok), "de: no message j"},
		"German has more keys": {msgs("k", ok), msgs("k", ok, msg{"j", ok}), "de: message j that English does not have"},
		"other values":         {msgs("k", "{a}"), msgs("k", "{b}"), "values [b], but English has [a]"},
		"other kind of value":  {msgs("k", "{a, number}"), msgs("k", "{a, select, other {x}}"), `value "a" of another kind`},
		"invalid key":          {msgs("Bad", ok), msgs("Bad", ok), `invalid key "Bad"`},
		"empty key part":       {msgs("a..b", ok), msgs("a..b", ok), `invalid key "a..b"`},
		"empty key":            {msgs("", ok), msgs("", ok), `invalid key ""`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := i18n.New(map[i18n.Language]map[i18n.Key]string{i18n.English: tc.en, i18n.German: tc.de})
			require.ErrorIs(t, err, i18n.ErrInvalidCatalog)
			assert.ErrorContains(t, err, tc.want)
		})
	}

	_, err := i18n.New(map[i18n.Language]map[i18n.Key]string{i18n.English: msgs("k", ok)})
	require.ErrorContains(t, err, "no messages for de")
	_, err = i18n.New(map[i18n.Language]map[i18n.Key]string{i18n.English: msgs("k", ok), i18n.German: msgs("k", ok), "fr": msgs("k", ok)})
	require.ErrorIs(t, err, i18n.ErrUnknownLanguage)
	_, err = i18n.New(map[i18n.Language]map[i18n.Key]string{i18n.English: msgs("k", "{a, number}"), i18n.German: msgs("k", "{a}")})
	require.NoError(t, err, "{a} takes any value")
}

// msgs returns one message, and more with msg.
func msgs(key i18n.Key, text string, more ...msg) map[i18n.Key]string {
	m := map[i18n.Key]string{key: text}
	for _, x := range more {
		m[x.key] = x.text
	}
	return m
}

// msg is a further message for msgs.
type msg struct {
	key  i18n.Key
	text string
}

// TestLoad: the embedded catalogs load, and Keys lists them.
func TestLoad(t *testing.T) {
	t.Parallel()
	c, err := i18n.Load()
	require.NoError(t, err)
	assert.Contains(t, c.Keys(), i18n.KeyDurationSeconds)
	assert.ElementsMatch(t, []i18n.Language{i18n.English, i18n.German}, i18n.Languages())
	assert.True(t, i18n.German.Valid())
	assert.False(t, i18n.Language("fr").Valid())
}
