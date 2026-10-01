// SPDX-License-Identifier: Apache-2.0

package flow_test

import (
	"context"
	json "encoding/json/v2"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/actiontest"
	"github.com/ripmav/streamcrew/internal/action/flow"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/template"
)

// args returns the parameters of a run with the arguments args.
func args(args ...string) engine.Params {
	return engine.Params{Args: args, ArgsText: strings.Join(args, " ")}
}

// TestClauses covers each comparison with numbers, texts, case and edge
// cases (actions.md B21 to B26, B205 to B207). Each row runs a conditional
// with one clause and checks which branch ran.
func TestClauses(t *testing.T) {
	t.Parallel()
	two := func(left string, c flow.Comparison, right string) flow.Clause {
		return flow.TwoValues{Left: action.Template(left), Compare: c, Right: action.Template(right)}
	}
	between := func(left, lowest, highest string) flow.Clause {
		return flow.Between{Left: action.Template(left), Min: action.Template(lowest), Max: action.Template(highest)}
	}
	for _, tc := range []struct {
		name          string
		clause        flow.Clause
		args          []string
		caseSensitive bool
		want          bool
	}{
		{name: "equals numbers", clause: two("$arg1text", flow.CompareEquals, "10.0"), args: []string{"10"}, want: true},
		{name: "equals leading zero", clause: two("01", flow.CompareEquals, "1"), want: true},
		{name: "equals exponent", clause: two("1e1", flow.CompareEquals, "10"), want: true},
		{name: "equals hex is text", clause: two("0x10", flow.CompareEquals, "16"), want: false},
		{name: "equals text ignoring case", clause: two("$arg1text", flow.CompareEquals, "ABC"), args: []string{"abc"}, want: true},
		{name: "equals text with case", clause: two("$arg1text", flow.CompareEquals, "ABC"), args: []string{"abc"}, caseSensitive: true, want: false},
		{name: "equals long s ignoring case", clause: two("ſ", flow.CompareEquals, "S"), want: true},
		{name: "equals empty", clause: two("", flow.CompareEquals, ""), want: true},
		{name: "not_equals", clause: two("a", flow.CompareNotEquals, "b"), want: true},
		{name: "not_equals numbers", clause: two("2", flow.CompareNotEquals, "2.0"), want: false},
		{name: "B206 greater numbers", clause: two("10", flow.CompareGreater, "9"), want: true},
		{name: "B206 greater text", clause: two("10", flow.CompareGreater, "9a"), want: false},
		{name: "greater equal values", clause: two("5", flow.CompareGreater, "5"), want: false},
		{name: "greater_or_equal", clause: two("5", flow.CompareGreaterOrEqual, "5"), want: true},
		{name: "less ignoring case", clause: two("a", flow.CompareLess, "B"), want: true},
		{name: "less by code points", clause: two("a", flow.CompareLess, "B"), caseSensitive: true, want: false},
		{name: "less negative", clause: two("-2", flow.CompareLess, "-1.5"), want: true},
		{name: "less_or_equal", clause: two("-1", flow.CompareLessOrEqual, "-1.0"), want: true},
		{name: "contains ignoring case", clause: two("Hello World", flow.CompareContains, "WORLD"), want: true},
		{name: "contains with case", clause: two("Hello World", flow.CompareContains, "WORLD"), caseSensitive: true, want: false},
		{name: "contains empty text", clause: two("abc", flow.CompareContains, ""), want: true},
		{name: "not_contains", clause: two("abc", flow.CompareNotContains, "x"), want: true},
		{name: "not_contains empty text", clause: two("abc", flow.CompareNotContains, ""), want: false},
		{name: "B205 in", clause: two("B", flow.CompareIn, "a | b|c"), want: true},
		{name: "in with case", clause: two("B", flow.CompareIn, "a | b|c"), caseSensitive: true, want: false},
		{name: "in numbers", clause: two("2", flow.CompareIn, "1|2.0|3"), want: true},
		{name: "in no entry", clause: two("d", flow.CompareIn, "a|b|c"), want: false},
		{name: "in by argument", clause: two("$arg1text", flow.CompareIn, "$arg2text"), args: []string{"x", "w|x"}, want: true},
		{name: "not_in", clause: two("d", flow.CompareNotIn, "a|b|c"), want: true},
		{name: "regex", clause: two("abc123", flow.CompareRegex, `\d+`), want: true},
		{name: "regex ignoring case", clause: two("ABC", flow.CompareRegex, "^abc$"), want: true},
		{name: "regex with case", clause: two("ABC", flow.CompareRegex, "^abc$"), caseSensitive: true, want: false},
		{name: "regex anywhere", clause: two("say hi now", flow.CompareRegex, "hi"), want: true},
		{name: "between", clause: between("5", "1", "10"), want: true},
		{name: "between lower bound", clause: between("1", "1", "10"), want: true},
		{name: "between upper bound", clause: between("$arg1text", "1", "10"), args: []string{"10.0"}, want: true},
		{name: "between above", clause: between("11", "1", "10"), want: false},
		{name: "between text", clause: between("abc", "1", "10"), want: false},
		{name: "between bound no number", clause: between("5", "x", "10"), want: false},
		{name: "between bounds swapped", clause: between("5", "10", "1"), want: false},
		{name: "B207 replaced", clause: flow.OneValue{Left: "$arg2text", Compare: flow.CompareReplaced}, args: []string{"a"}, want: false},
		{name: "B207 not_replaced", clause: flow.OneValue{Left: "$arg2text", Compare: flow.CompareNotReplaced}, args: []string{"a"}, want: true},
		{name: "replaced", clause: flow.OneValue{Left: "$arg1text!", Compare: flow.CompareReplaced}, args: []string{"a"}, want: true},
		{name: "replaced without tokens", clause: flow.OneValue{Left: "plain $", Compare: flow.CompareReplaced}, want: true},
		{name: "expression", clause: flow.Expression{Left: "$arg1text * 2 > 9"}, args: []string{"5"}, want: true},
		{name: "expression false", clause: flow.Expression{Left: `$arg1text == "yes"`}, args: []string{"no"}, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				reg := registry(t, numbers(0))
				h := actiontest.NewHarness(t, reg)
				j := &actiontest.Journal{}
				c := newAction[flow.Conditional](t, reg, flow.TypeConditional)
				c.Clauses, c.CaseSensitive = flow.Clauses{tc.clause}, tc.caseSensitive
				c.Actions, c.Else = []command.Action{j.Note("true")}, []command.Action{j.Note("false")}
				require.NoError(t, c.Validate())

				in := h.Start(h.Command("x", c), args(tc.args...))
				assert.Empty(t, in.Errors)
				assert.Equal(t, []string{strconv.FormatBool(tc.want)}, j.Lines())
			})
		})
	}
}

// TestClauseFailures: clauses that cannot be tested let the action fail,
// with the clause and the field in the message (actions.md B6, B25, B26).
func TestClauseFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		clause flow.Clause
		args   []string
		want   string
	}{
		{
			name:   "B25 invalid regular expression from an argument",
			clause: flow.TwoValues{Left: "a", Compare: flow.CompareRegex, Right: "$arg1text"}, args: []string{"(a"},
			want: "clause 1: right: invalid action: error parsing regexp",
		},
		{
			name:   "B26 expression that is not true or false",
			clause: flow.Expression{Left: "$arg1text + 1"}, args: []string{"1"},
			want: `clause 1: left: invalid action: "2" is not true or false`,
		},
		{
			name:   "B26 expression that cannot be evaluated",
			clause: flow.Expression{Left: "$arg1text * 2 > 1"}, args: []string{"x"},
			want: "clause 1: left: expression cannot be evaluated",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				reg := registry(t, numbers(0))
				h := actiontest.NewHarness(t, reg)
				j := &actiontest.Journal{}
				c := newAction[flow.Conditional](t, reg, flow.TypeConditional)
				c.Clauses = flow.Clauses{tc.clause}
				c.Actions, c.Else = []command.Action{j.Note("true")}, []command.Action{j.Note("false")}
				require.NoError(t, c.Validate())

				in := h.Start(h.Command("x", c, j.Note("after")), args(tc.args...))
				assert.Equal(t, []string{"after"}, j.Lines(), "neither branch runs")
				require.Len(t, in.Errors, 1)
				assert.Contains(t, in.Errors[0].Message, tc.want)
				assert.Equal(t, []int{1}, in.Errors[0].Path)
			})
		})
	}
}

// TestCombine covers actions.md B27.
func TestCombine(t *testing.T) {
	t.Parallel()
	clause := func(holds bool) flow.Clause {
		return flow.TwoValues{Left: "a", Compare: flow.CompareEquals, Right: action.Template(map[bool]string{true: "a", false: "b"}[holds])}
	}
	for _, tc := range []struct {
		combine flow.Combine
		results []bool
		want    bool
	}{
		{flow.CombineAnd, []bool{true, true, true}, true},
		{flow.CombineAnd, []bool{true, false, true}, false},
		{flow.CombineOr, []bool{false, false, true}, true},
		{flow.CombineOr, []bool{false, false, false}, false},
		{flow.CombineXor, []bool{false, true, false}, true},
		{flow.CombineXor, []bool{true, false, true}, false},
		{flow.CombineXor, []bool{true, true, true}, false},
		{flow.CombineXor, []bool{false, false, false}, false},
		{flow.CombineAnd, []bool{false}, false},
		{flow.CombineXor, []bool{true}, true},
	} {
		t.Run(string(tc.combine)+" "+strings.Join(texts(tc.results), " "), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				reg := registry(t, numbers(0))
				h := actiontest.NewHarness(t, reg)
				j := &actiontest.Journal{}
				c := newAction[flow.Conditional](t, reg, flow.TypeConditional)
				c.Combine = tc.combine
				for _, holds := range tc.results {
					c.Clauses = append(c.Clauses, clause(holds))
				}
				c.Actions, c.Else = []command.Action{j.Note("true")}, []command.Action{j.Note("false")}

				in := h.Start(h.Command("x", c), engine.Params{})
				assert.Empty(t, in.Errors)
				assert.Equal(t, []string{strconv.FormatBool(tc.want)}, j.Lines())
			})
		})
	}
}

// texts writes truth values as texts.
func texts(values []bool) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = strconv.FormatBool(v)
	}
	return out
}

// TestOneRender covers actions.md B27: all values of a condition come from
// one render, so an identifier has the same value in every clause and is
// resolved once.
func TestOneRender(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int64
		family := template.Family{Name: "calls", Identifiers: []template.Identifier{{
			Name: "calls",
			Resolve: func(context.Context, *template.Scope) (template.Value, bool, error) {
				return template.IntValue(calls.Add(1)), true, nil
			},
		}}}
		reg := registry(t, numbers(0), family)
		h := actiontest.NewHarness(t, reg)
		j := &actiontest.Journal{}
		c := newAction[flow.Conditional](t, reg, flow.TypeConditional)
		c.Clauses = flow.Clauses{
			flow.TwoValues{Left: "$calls", Compare: flow.CompareEquals, Right: "1"},
			flow.Between{Left: "$calls", Min: "$calls", Max: "1"},
			flow.Expression{Left: "$calls == 1 and $calls < 2"},
			flow.OneValue{Left: "$calls", Compare: flow.CompareReplaced},
		}
		c.Actions = []command.Action{j.Note("true")}

		in := h.Start(h.Command("x", c), engine.Params{})
		assert.Empty(t, in.Errors)
		assert.Equal(t, []string{"true"}, j.Lines())
		assert.Equal(t, int64(1), calls.Load())
	})
}

// TestBranches covers actions.md B28 with B1 and B9: the actions for
// "false" follow those for "true" in the path.
func TestBranches(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		reg := registry(t, numbers(0))
		h := actiontest.NewHarness(t, reg)
		j := &actiontest.Journal{}
		conditional := func(holds bool) flow.Conditional {
			c := newAction[flow.Conditional](t, reg, flow.TypeConditional)
			right := map[bool]action.Template{true: "$arg1text", false: "other"}[holds]
			c.Clauses = flow.Clauses{flow.TwoValues{Left: "$arg1text", Compare: flow.CompareEquals, Right: right}}
			c.Actions = []command.Action{j.Note("t1"), j.Inactive("not run"), j.Fail("t3")}
			c.Else = []command.Action{j.Note("f1"), j.Fail("f2")}
			return c
		}

		in := h.Start(h.Command("true", conditional(true), j.Note("after")), args("a"))
		assert.Equal(t, []string{"t1", "t3", "after"}, j.Lines())
		require.Len(t, in.Errors, 1)
		assert.Equal(t, []int{1, 3}, in.Errors[0].Path)

		j = &actiontest.Journal{}
		in = h.Start(h.Command("false", conditional(false), j.Note("after")), args("a"))
		assert.Equal(t, []string{"f1", "f2", "after"}, j.Lines())
		require.Len(t, in.Errors, 1)
		assert.Equal(t, []int{1, 5}, in.Errors[0].Path, "the second action for false is child 5")

		j = &actiontest.Journal{}
		off := conditional(true)
		off.Active = false
		in = h.Start(h.Command("inactive", off, j.Note("after")), args("a"))
		assert.Empty(t, in.Errors)
		assert.Equal(t, []string{"after"}, j.Lines())

		j = &actiontest.Journal{}
		empty := conditional(false)
		empty.Else = []command.Action{}
		in = h.Start(h.Command("no actions for false", empty, j.Note("after")), args("a"))
		assert.Empty(t, in.Errors)
		assert.Equal(t, []string{"after"}, j.Lines())

		j = &actiontest.Journal{}
		aborting := h.Command("aborting", conditional(true), j.Note("not reached"))
		aborting.ErrorPolicy = command.ErrorAbort
		h.Put(aborting)
		in = h.Start(aborting, args("a"))
		assert.Equal(t, engine.StateFailed, in.State)
		assert.Equal(t, []string{"t1", "t3"}, j.Lines())
	})
}

// TestRepeatWhileTrue covers actions.md B29.
func TestRepeatWhileTrue(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		reg := registry(t, numbers(0))
		h := actiontest.NewHarness(t, reg)
		j := &actiontest.Journal{}
		loop := func(below string) flow.Conditional {
			c := newAction[flow.Conditional](t, reg, flow.TypeConditional)
			c.RepeatWhileTrue = true
			c.Clauses = flow.Clauses{flow.Expression{Left: "$n < " + below}}
			c.Actions = []command.Action{count{}, j.Note("pass")}
			c.Else = []command.Action{j.Note("false at once")}
			return c
		}
		start := func(cmd command.Command, n int) engine.Instance {
			return h.Start(cmd, engine.Params{Values: map[string]template.Value{"n": template.IntValue(int64(n))}})
		}

		in := start(h.Command("three passes", loop("3")), 0)
		assert.Empty(t, in.Errors)
		assert.Equal(t, []string{"pass", "pass", "pass"}, j.Lines(), "the actions for false do not run after a pass")

		j = &actiontest.Journal{}
		in = start(h.Command("false at once", loop("3")), 5)
		assert.Empty(t, in.Errors)
		assert.Equal(t, []string{"false at once"}, j.Lines())

		j = &actiontest.Journal{}
		in = start(h.Command("1000 passes", loop("1000")), 0)
		assert.Empty(t, in.Errors, "1000 passes are allowed")
		assert.Len(t, j.Lines(), 1000)

		j = &actiontest.Journal{}
		in = start(h.Command("endless", loop("1000000"), j.Note("after")), 0)
		assert.Equal(t, engine.StateCompleted, in.State)
		assert.Len(t, j.Lines(), 1001, "1000 passes and the next action")
		require.Len(t, in.Errors, 1)
		assert.Contains(t, in.Errors[0].Message, "repeatWhileTrue: pass limit reached: the condition is still true after 1000 passes")

		j = &actiontest.Journal{}
		once := loop("3")
		once.RepeatWhileTrue = false
		in = start(h.Command("without repeating", once), 0)
		assert.Empty(t, in.Errors)
		assert.Equal(t, []string{"pass"}, j.Lines())

		j = &actiontest.Journal{}
		stopping := loop("3")
		stopping.Actions = []command.Action{j.Note("pass"), stop{}}
		in = start(h.Command("stop", stopping, j.Note("not reached")), 0)
		assert.Equal(t, engine.StateCompleted, in.State)
		assert.Equal(t, []string{"pass"}, j.Lines())
	})
}

// TestRepeatWhileTrueTimeLimit covers actions.md B8: each evaluation of
// the condition has the default time limit, not all of them together.
func TestRepeatWhileTrueTimeLimit(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		// slow takes 40 s, so three evaluations take longer than the
		// default limit of 60 s, and one does not.
		family := template.Family{Name: "slow", Identifiers: []template.Identifier{{
			Name: "slow",
			Resolve: func(ctx context.Context, _ *template.Scope) (template.Value, bool, error) {
				select {
				case <-time.After(40 * time.Second):
					return template.IntValue(1), true, nil
				case <-ctx.Done():
					return template.Value{}, false, context.Cause(ctx)
				}
			},
		}}}
		reg := registry(t, numbers(0), family)
		h := actiontest.NewHarness(t, reg)
		j := &actiontest.Journal{}
		c := newAction[flow.Conditional](t, reg, flow.TypeConditional)
		c.RepeatWhileTrue = true
		c.Clauses = flow.Clauses{flow.Expression{Left: "$n < 3 and $slow == 1"}}
		c.Actions = []command.Action{count{}, j.Note("pass")}

		in := h.Start(h.Command("slow", c), engine.Params{Values: map[string]template.Value{"n": template.IntValue(0)}})
		time.Sleep(time.Hour)
		synctest.Wait()
		in = h.Instance(in.ID)
		assert.Empty(t, in.Errors)
		assert.Equal(t, []string{"pass", "pass", "pass"}, j.Lines())
		assert.Equal(t, 4*40*time.Second, in.EndedAt.Sub(in.StartedAt), "four evaluations")
	})
}

// count is an action that adds 1 to the value n of the run.
type count struct{}

func (count) DocType() string { return "count" }
func (count) Validate() error { return nil }
func (count) Enabled() bool   { return true }
func (count) Perform(_ context.Context, run *engine.Run) error {
	n := run.Scope().Values()["n"]
	run.Scope().SetValue("n", template.FloatValue(n.Number+1))
	return nil
}

func TestValidateConditional(t *testing.T) {
	t.Parallel()
	reg := registry(t, numbers(0))
	c := newAction[flow.Conditional](t, reg, flow.TypeConditional)
	assert.Equal(t, flow.CombineAnd, c.Combine, "a new conditional combines with and")
	assert.False(t, c.CaseSensitive, "B20: a new conditional ignores case")
	assert.False(t, c.RepeatWhileTrue)
	require.ErrorContains(t, c.Validate(), "clauses: invalid action: a condition needs a clause")

	valid := flow.TwoValues{Left: "a", Compare: flow.CompareEquals, Right: "b"}
	for _, tc := range []struct {
		name   string
		clause flow.Clause
		want   string
	}{
		{"no clause", nil, "clause 1: invalid action: no clause"},
		{"two values with between", flow.TwoValues{Left: "a", Compare: flow.CompareBetween, Right: "b"}, `clause 1: compare: invalid action: "between" does not compare two values`},
		{"two values with replaced", flow.TwoValues{Left: "a", Compare: flow.CompareReplaced, Right: "b"}, "does not compare two values"},
		{"two values without comparison", flow.TwoValues{Left: "a", Right: "b"}, `"" does not compare two values`},
		{"one value with equals", flow.OneValue{Left: "a", Compare: flow.CompareEquals}, `clause 1: compare: invalid action: "equals" does not test one value`},
		{"invalid regular expression", flow.TwoValues{Left: "a", Compare: flow.CompareRegex, Right: "(a"}, "clause 1: right: invalid action: error parsing regexp"},
		{"invalid expression", flow.Expression{Left: "1 +"}, "clause 1: left: invalid action: invalid expression"},
		{"empty expression", flow.Expression{Left: ""}, "clause 1: left: invalid action"},
	} {
		c.Clauses = flow.Clauses{valid, tc.clause}
		err := c.Validate()
		require.ErrorIs(t, err, action.ErrInvalid, tc.name)
		assert.ErrorContains(t, err, strings.Replace(tc.want, "clause 1", "clause 2", 1), tc.name)
	}

	c.Clauses = flow.Clauses{flow.TwoValues{Left: "a", Compare: flow.CompareRegex, Right: "($arg1text"}}
	require.NoError(t, c.Validate(), "a regular expression with tokens is checked when it is rendered")
	for _, cmp := range flow.Comparisons() {
		require.True(t, cmp.Valid(), cmp)
	}
	assert.Len(t, flow.Comparisons(), 15)
	assert.False(t, flow.Comparison("like").Valid())

	c.Clauses = flow.Clauses{valid}
	for _, combine := range flow.Combines() {
		c.Combine = combine
		require.NoError(t, c.Validate(), combine)
	}
	c.Combine = "nand"
	require.ErrorContains(t, c.Validate(), `combine: invalid action: unknown way to combine "nand"`)
	c.Combine = ""
	require.ErrorIs(t, c.Validate(), action.ErrInvalid, "the empty value is no way to combine")
}

// TestClausesJSON: each clause is stored with exactly the members its
// comparison uses, and an empty text stays a value.
func TestClausesJSON(t *testing.T) {
	t.Parallel()
	clauses := flow.Clauses{
		flow.TwoValues{Left: "$arg1text", Compare: flow.CompareContains, Right: ""},
		flow.Between{Left: "$x", Min: "", Max: "9"},
		flow.OneValue{Left: "$arg2text", Compare: flow.CompareNotReplaced},
		flow.Expression{Left: "$x > 1"},
	}
	data, err := json.Marshal(clauses)
	require.NoError(t, err)
	assert.JSONEq(t, `[
		{"left": "$arg1text", "compare": "contains", "right": ""},
		{"left": "$x", "compare": "between", "min": "", "max": "9"},
		{"left": "$arg2text", "compare": "not_replaced"},
		{"left": "$x > 1", "compare": "expression"}
	]`, string(data))

	var back flow.Clauses
	require.NoError(t, json.Unmarshal(data, &back))
	assert.Equal(t, clauses, back)
	for i, c := range back {
		assert.Equal(t, clauses[i].Comparison(), c.Comparison())
	}

	_, err = json.Marshal(flow.Clauses{nil})
	require.ErrorIs(t, err, action.ErrInvalid)
	err = json.Unmarshal([]byte(`[{"left":"a","compare":"replaced","right":"b"}]`), &back)
	require.ErrorContains(t, err, `clause 1: invalid action: replaced has no member "right"`)
	err = json.Unmarshal([]byte(`[{"left":"a","compare":"equals","right":"a"},{"left":"a","compare":"between","min":"1"}]`), &back)
	require.ErrorContains(t, err, `clause 2: invalid action: member "max" is missing`)
	err = json.Unmarshal([]byte(`[{"compare":"equals","right":"a"}]`), &back)
	require.ErrorContains(t, err, `member "left" is missing`)
	err = json.Unmarshal([]byte(`[{"left":"a","compare":"like","right":"a"}]`), &back)
	require.ErrorContains(t, err, `compare: invalid action: unknown comparison "like"`)
	err = json.Unmarshal([]byte(`[{"left":"a","compare":"equals","right":"a","extra":1}]`), &back)
	require.Error(t, err, "unknown members are rejected")
}

// FuzzClausesJSON: whatever decodes encodes to a form that decodes to the
// same clauses.
func FuzzClausesJSON(f *testing.F) {
	for _, seed := range []string{
		`[{"left":"a","compare":"equals","right":"b"}]`,
		`[{"left":"$x","compare":"between","min":"1","max":"2"},{"left":"$y","compare":"replaced"}]`,
		`[{"left":"1 > 0","compare":"expression"}]`,
		`[{"left":"a","compare":"replaced","right":"b"}]`,
		`[]`,
		`null`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data string) {
		var clauses flow.Clauses
		if json.Unmarshal([]byte(data), &clauses) != nil {
			return
		}
		out, err := json.Marshal(clauses)
		require.NoError(t, err)
		var again flow.Clauses
		require.NoError(t, json.Unmarshal(out, &again))
		out2, err := json.Marshal(again)
		require.NoError(t, err)
		assert.Equal(t, string(out), string(out2))
	})
}
