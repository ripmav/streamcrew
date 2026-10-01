// SPDX-License-Identifier: Apache-2.0

package values_test

import (
	"slices"
	"strconv"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/actiontest"
	"github.com/ripmav/streamcrew/internal/action/values"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/template"
)

// special returns a special identifier action of kind k that sets name to
// value, with global as given.
func (f *fixture) special(k values.SpecialKind, name, value string, global bool) values.SpecialIdentifier {
	f.t.Helper()
	d, ok := f.reg.Descriptor(values.TypeSpecialIdentifier)
	require.True(f.t, ok)
	s, ok := d.New().(values.SpecialIdentifier)
	require.True(f.t, ok)
	s.Kind, s.Name, s.Value, s.Global = k, action.ResultName(name), action.Template(value), global
	require.NoError(f.t, s.Validate())
	return s
}

func TestSpecialIdentifierConformance(t *testing.T) {
	t.Parallel()
	reg, _, _ := registry(t, &counters{})
	d, ok := reg.Descriptor(values.TypeSpecialIdentifier)
	require.True(t, ok)
	actiontest.Suite{Descriptor: d, Update: update(), Examples: []actiontest.Example{
		{Name: "text", Doc: `{"type":"special_identifier","kind":"text","name":"greeting","value":"Hello toupper($arg1text)"}`, Valid: true},
		{Name: "expression", Doc: `{"type":"special_identifier","kind":"expression","name":"sum","value":"$arg1text * 2"}`, Valid: true},
		{Name: "global", Doc: `{"type":"special_identifier","kind":"text","name":"top","value":"$arg1text","global":true}`, Valid: true},
		{Name: "empty text", Doc: `{"type":"special_identifier","kind":"text","name":"empty","value":""}`, Valid: true},
		{Name: "all members", Doc: `{"type":"special_identifier","schemaVersion":1,"enabled":false,"kind":"expression","name":"x1","value":"1 + 1","global":false}`, Valid: true},
		{Name: "kind missing", Doc: `{"type":"special_identifier","name":"x","value":"1"}`},
		{Name: "unknown kind", Doc: `{"type":"special_identifier","kind":"math","name":"x","value":"1"}`},
		{Name: "name missing", Doc: `{"type":"special_identifier","kind":"text","value":"x"}`},
		{Name: "value missing", Doc: `{"type":"special_identifier","kind":"text","name":"x"}`},
		{Name: "B5 name in upper case", Doc: `{"type":"special_identifier","kind":"text","name":"Greeting","value":"x"}`},
		{Name: "B50 name with $", Doc: `{"type":"special_identifier","kind":"text","name":"$x","value":"x"}`},
		{Name: "empty name", Doc: `{"type":"special_identifier","kind":"text","name":"","value":"x"}`},
		{Name: "empty expression", Doc: `{"type":"special_identifier","kind":"expression","name":"x","value":""}`},
		{Name: "switch instead of kind", Doc: `{"type":"special_identifier","kind":"text","name":"x","value":"1","calculate":true}`},
		{Name: "global as text", Doc: `{"type":"special_identifier","kind":"text","name":"x","value":"1","global":"yes"}`},
	}}.Run(t)
}

// TestSpecialIdentifier covers actions.md B5, B50 to B54 and B210: the
// value of the run, calculations, text functions and inserted values that
// stay values.
func TestSpecialIdentifier(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		in := f.start([]command.Action{
			f.special(values.SpecialText, "greeting", "Hello toupper($arg1text)", false), f.show("$greeting"),
			f.special(values.SpecialExpression, "sum", "$arg2text * 2 + 0.5", false), f.show("$sum"),
			f.special(values.SpecialExpression, "whole", "$sum - 0.5", false), f.show("$whole"),
			f.special(values.SpecialExpression, "zero", "0 * -1", false), f.show("$zero"),
			f.special(values.SpecialText, "greeting", "replace($greeting,Hello,Bye)", false), f.show("$greeting"),
			f.special(values.SpecialText, "inserted", "toupper($arg3text)", false), f.show("$inserted"),
			f.special(values.SpecialText, "empty", "", false), f.show("[$empty]"),
		}, "world", "10.5", "a),removespaces(b")
		assert.Empty(t, in.Errors)
		assert.Equal(t, []string{"Hello WORLD", "21.5", "21", "0", "Bye WORLD", "A),REMOVESPACES(B", "[]"}, f.lines.get())
		assert.Zero(t, f.globals.Len(), "values of the run only")
	})
}

// TestSpecialIdentifierGlobal covers actions.md B56: a global value applies
// to later instances, a value of the run does not, and a value of the run
// hides a global one.
func TestSpecialIdentifierGlobal(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		first := f.start([]command.Action{
			f.special(values.SpecialText, "top", "$arg1text", true), f.show("$top"),
			f.special(values.SpecialText, "mine", "only here", false),
		}, "alice")
		second := f.start([]command.Action{
			f.show("$top $mine"),
			f.special(values.SpecialText, "top", "run value", false), f.show("$top"),
		})
		third := f.start([]command.Action{f.show("$top")})
		for _, in := range []engine.Instance{first, second, third} {
			assert.Empty(t, in.Errors)
		}
		assert.Equal(t, []string{"alice", "alice $mine", "run value", "alice"}, f.lines.get())
		assert.Equal(t, 1, f.globals.Len())
	})
}

// TestSpecialIdentifierFails covers actions.md B4, B6, B51 and B55: the
// action fails and sets no value.
func TestSpecialIdentifierFails(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		kind  values.SpecialKind
		value string
		arg   string
		want  string
	}{
		{"B51 truth value", values.SpecialExpression, `$arg1text == "a"`, "a", `value: invalid action: "true" is not a number`},
		{"B52 text times a number", values.SpecialExpression, "$arg1text * 2", "abc", "value: expression cannot be evaluated"},
		{"division by zero", values.SpecialExpression, "1 / ($arg1text - 1)", "1", "value: expression cannot be evaluated"},
		{"B55 invalid pattern", values.SpecialText, "count(abc,$arg1text)", "[", "value: text functions cannot be evaluated: count: invalid pattern"},
		{"B55 date in the future", values.SpecialText, "datefrom($arg1text)", "2999-01-01", "value: text functions cannot be evaluated: datefrom: 2999-01-01 is in the future"},
		{"B55 invalid date", values.SpecialText, "dateto($arg1text)", "soon", `value: text functions cannot be evaluated: dateto: invalid date "soon"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				f := newFixture(t)
				in := f.start([]command.Action{f.special(tc.kind, "x", tc.value, true), f.show("$x")}, tc.arg)
				require.Len(t, in.Errors, 1)
				assert.Contains(t, in.Errors[0].Message, tc.want)
				assert.Equal(t, []string{"$x"}, f.lines.get(), "no value of the run")
				assert.Zero(t, f.globals.Len(), "no global value")
			})
		})
	}
}

// TestSpecialIdentifierGlobalLimit covers actions.md B57: a new global
// name beyond the limit fails, existing ones can still be changed.
func TestSpecialIdentifierGlobalLimit(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		for i := range template.MaxGlobals {
			require.NoError(t, f.globals.Set("g"+strconv.Itoa(i), template.TextValue("old")))
		}
		in := f.start([]command.Action{
			f.special(values.SpecialText, "new", "x", true), f.show("$new"),
			f.special(values.SpecialText, "g1", "changed", true), f.show("$g1"),
		})
		require.Len(t, in.Errors, 1)
		assert.Contains(t, in.Errors[0].Message, "global: global value \"new\": too many global values")
		assert.Equal(t, []string{"$new", "changed"}, f.lines.get())
		assert.Equal(t, template.MaxGlobals, f.globals.Len())
	})
}

// TestSpecialIdentifierConcurrent covers actions.md B211: instances that
// set the same global value at the same time do not disturb each other;
// one of their values remains.
func TestSpecialIdentifierConcurrent(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		var started []engine.Instance
		names := []string{"a", "b", "c"}
		for _, name := range names {
			set := make([]command.Action, 50)
			for i := range set {
				set[i] = f.special(values.SpecialText, "last", name, true)
			}
			cmd := f.harness.Command(name, set...)
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
		in := f.start([]command.Action{f.show("$last")})
		assert.Empty(t, in.Errors)
		require.Len(t, f.lines.get(), 1)
		assert.True(t, slices.Contains(names, f.lines.get()[0]), f.lines.get())
		assert.Equal(t, 1, f.globals.Len())
	})
}

func TestSpecialIdentifierValidate(t *testing.T) {
	t.Parallel()
	reg, _, _ := registry(t, &counters{})
	d, ok := reg.Descriptor(values.TypeSpecialIdentifier)
	require.True(t, ok)
	s, ok := d.New().(values.SpecialIdentifier)
	require.True(t, ok)
	assert.Equal(t, values.SpecialText, s.Kind, "a new action reads text")
	assert.False(t, s.Global, "and sets a value of the run only")
	require.ErrorContains(t, s.Validate(), "name: invalid action: result name", "it has no name yet")

	for _, tc := range []struct {
		name   string
		change func(s *values.SpecialIdentifier)
		want   string
	}{
		{"unknown kind", func(s *values.SpecialIdentifier) { s.Kind = "math" }, `kind: invalid action: unknown kind "math"`},
		{"B5 upper case", func(s *values.SpecialIdentifier) { s.Name = "Greeting" }, "name: invalid action"},
		{"B50 $ in the name", func(s *values.SpecialIdentifier) { s.Name = "$x" }, "name: invalid action"},
		{"B55 unknown function", func(s *values.SpecialIdentifier) { s.Value = "Score(1)" }, `value: invalid action: invalid text functions: unknown function "score"`},
		{"wrong number of parameters", func(s *values.SpecialIdentifier) { s.Value = "replace(a,b)" }, "replace takes 3 parameters, not 2"},
		{"B55 fixed invalid pattern", func(s *values.SpecialIdentifier) { s.Value = "count(a,[)" }, "count: invalid pattern"},
		{"B55 fixed invalid date", func(s *values.SpecialIdentifier) { s.Value = "datefrom(1.1.2025)" }, `datefrom: invalid date "1.1.2025"`},
		{"B52 invalid expression", func(s *values.SpecialIdentifier) { s.Kind, s.Value = values.SpecialExpression, "1 +" }, "value: invalid action: invalid expression"},
		{"empty expression", func(s *values.SpecialIdentifier) { s.Kind, s.Value = values.SpecialExpression, "" }, "value: invalid action: invalid expression"},
		{"text functions are no expression", func(s *values.SpecialIdentifier) { s.Kind = values.SpecialExpression }, "value: invalid action: invalid expression"},
	} {
		s, ok := d.New().(values.SpecialIdentifier)
		require.True(t, ok)
		s.Name, s.Value = "greeting", "toupper($arg1text)"
		require.NoError(t, s.Validate(), tc.name)
		tc.change(&s)
		assert.ErrorContains(t, s.Validate(), tc.want, tc.name)
	}
	for _, k := range values.SpecialKinds() {
		require.True(t, k.Valid(), k)
	}
}

// TestSpecialIdentifierResultNames: saving checks the name (actions.md B5;
// template.md, B12).
func TestSpecialIdentifierResultNames(t *testing.T) {
	t.Parallel()
	s := values.SpecialIdentifier{Kind: values.SpecialText, Name: "greeting"}
	assert.Equal(t, []string{"greeting"}, s.ResultNames())
}
