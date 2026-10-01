// SPDX-License-Identifier: Apache-2.0

package flow

import (
	"cmp"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/expr"
	"github.com/ripmav/streamcrew/internal/template"
)

// Comparison is the comparison of a clause (actions.md B21).
type Comparison string

// The comparisons.
const (
	CompareEquals         Comparison = "equals"
	CompareNotEquals      Comparison = "not_equals"
	CompareGreater        Comparison = "greater"
	CompareGreaterOrEqual Comparison = "greater_or_equal"
	CompareLess           Comparison = "less"
	CompareLessOrEqual    Comparison = "less_or_equal"
	CompareContains       Comparison = "contains"
	CompareNotContains    Comparison = "not_contains"
	CompareRegex          Comparison = "regex"
	CompareIn             Comparison = "in"
	CompareNotIn          Comparison = "not_in"
	CompareBetween        Comparison = "between"
	CompareReplaced       Comparison = "replaced"
	CompareNotReplaced    Comparison = "not_replaced"
	CompareExpression     Comparison = "expression"
)

// Comparisons returns every comparison, in the order editors show them:
// those of TwoValues, then those of Between, OneValue and Expression.
func Comparisons() []Comparison {
	return slices.Concat(twoValueComparisons(), []Comparison{CompareBetween}, oneValueComparisons(), []Comparison{CompareExpression})
}

// Valid reports whether c is a known comparison.
func (c Comparison) Valid() bool {
	return slices.Contains(Comparisons(), c)
}

// twoValueComparisons returns the comparisons of TwoValues.
func twoValueComparisons() []Comparison {
	return []Comparison{
		CompareEquals, CompareNotEquals, CompareGreater, CompareGreaterOrEqual, CompareLess, CompareLessOrEqual,
		CompareContains, CompareNotContains, CompareRegex, CompareIn, CompareNotIn,
	}
}

// oneValueComparisons returns the comparisons of OneValue.
func oneValueComparisons() []Comparison {
	return []Comparison{CompareReplaced, CompareNotReplaced}
}

// Clause is a clause of a conditional (actions.md B21): a left value, a
// comparison and, depending on the comparison, a right value or two
// bounds. It is one of TwoValues, Between, OneValue and Expression, so a
// clause has exactly the values its comparison uses (Code-ADR-0017).
type Clause interface {
	// Comparison returns the comparison of the clause.
	Comparison() Comparison
	// prepare checks the clause and returns it ready to be tested.
	prepare() (prepared, error)
	// doc returns the clause as stored.
	doc() clauseDoc
}

// prepared is a clause ready to be tested.
type prepared struct {
	// values are the templates of the clause; they are rendered with those
	// of the other clauses, in one render (B27).
	values []template.Template
	// test reports whether the clause holds for its rendered values.
	test func(values []template.Rendered, o compareOptions) (bool, error)
}

// compareOptions are what the tests need from the conditional and its run.
type compareOptions struct {
	caseSensitive bool
	// delimiter separates the entries of the list of in and not_in (B23).
	delimiter string
}

// TwoValues compares a left and a right value (actions.md B22, B23, B25).
type TwoValues struct {
	Left action.Template
	// Compare is equals, not_equals, greater, greater_or_equal, less,
	// less_or_equal, contains, not_contains, regex, in or not_in.
	Compare Comparison
	Right   action.Template
}

// Comparison implements Clause.
func (c TwoValues) Comparison() Comparison { return c.Compare }

func (c TwoValues) prepare() (prepared, error) {
	test, err := twoValueTest(c.Compare, c.Right)
	if err != nil {
		return prepared{}, err
	}
	return prepared{
		values: []template.Template{c.Left.Parse(), c.Right.Parse()},
		test: func(v []template.Rendered, o compareOptions) (bool, error) {
			return test(v[0].Text, v[1].Text, o)
		},
	}, nil
}

func (c TwoValues) doc() clauseDoc {
	return clauseDoc{Left: &c.Left, Compare: c.Compare, Right: &c.Right}
}

// twoValueTest returns the test of a comparison of two values. A regular
// expression without $ tokens must compile now.
func twoValueTest(c Comparison, right action.Template) (func(left, right string, o compareOptions) (bool, error), error) {
	ordered := func(holds func(order int) bool) func(string, string, compareOptions) (bool, error) {
		return func(left, right string, o compareOptions) (bool, error) {
			return holds(order(left, right, o)), nil
		}
	}
	switch c {
	case CompareEquals:
		return ordered(func(n int) bool { return n == 0 }), nil
	case CompareNotEquals:
		return ordered(func(n int) bool { return n != 0 }), nil
	case CompareGreater:
		return ordered(func(n int) bool { return n > 0 }), nil
	case CompareGreaterOrEqual:
		return ordered(func(n int) bool { return n >= 0 }), nil
	case CompareLess:
		return ordered(func(n int) bool { return n < 0 }), nil
	case CompareLessOrEqual:
		return ordered(func(n int) bool { return n <= 0 }), nil
	case CompareContains:
		return func(left, right string, o compareOptions) (bool, error) {
			return strings.Contains(o.text(left), o.text(right)), nil
		}, nil
	case CompareNotContains:
		return func(left, right string, o compareOptions) (bool, error) {
			return !strings.Contains(o.text(left), o.text(right)), nil
		}, nil
	case CompareIn:
		return func(left, right string, o compareOptions) (bool, error) {
			return inList(left, right, o), nil
		}, nil
	case CompareNotIn:
		return func(left, right string, o compareOptions) (bool, error) {
			return !inList(left, right, o), nil
		}, nil
	case CompareRegex:
		if !hasTokens(right.Parse()) {
			if _, err := regexp.Compile(string(right)); err != nil {
				return nil, field("right", fmt.Errorf("%w: %w", action.ErrInvalid, err))
			}
		}
		return matches, nil
	default:
		return nil, field("compare", fmt.Errorf("%w: %q does not compare two values", action.ErrInvalid, c))
	}
}

// order orders left and right (actions.md B22): as numbers if both are
// numbers by the rule of expressions (template.md, B51), otherwise as texts
// by code points.
func order(left, right string, o compareOptions) int {
	if x, ok := expr.ParseNumber(left); ok {
		if y, ok := expr.ParseNumber(right); ok {
			return cmp.Compare(x, y)
		}
	}
	return strings.Compare(o.text(left), o.text(right))
}

// inList reports whether left equals an entry of the list right, by the
// rule of equals; entries are separated by the argument delimiter, without
// the space around them (actions.md B23).
func inList(left, right string, o compareOptions) bool {
	for entry := range strings.SplitSeq(right, o.delimiter) {
		if order(left, strings.TrimSpace(entry), o) == 0 {
			return true
		}
	}
	return false
}

// matches reports whether the regular expression right matches anywhere in
// left (actions.md B25).
func matches(left, right string, o compareOptions) (bool, error) {
	pattern := right
	if !o.caseSensitive {
		pattern = "(?i)" + right
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false, field("right", fmt.Errorf("%w: %w", action.ErrInvalid, err))
	}
	return re.MatchString(left), nil
}

// text returns s as texts compare: with regard to case only if the switch
// says so.
func (o compareOptions) text(s string) string {
	if o.caseSensitive {
		return s
	}
	return fold(s)
}

// fold maps each letter to one form for all its cases, the lower case of
// its upper case, so that "ſ", "S" and "s" are the same.
func fold(s string) string {
	return strings.Map(func(r rune) rune { return unicode.ToLower(unicode.ToUpper(r)) }, s)
}

// hasTokens reports whether t has a $ token.
func hasTokens(t template.Template) bool {
	for _, token := range t.Segments() {
		if token {
			return true
		}
	}
	return false
}

// Between holds if the left value is a number from Min to Max, both
// included; if one of the three is no number, it does not hold
// (actions.md B22).
type Between struct {
	Left, Min, Max action.Template
}

// Comparison implements Clause.
func (Between) Comparison() Comparison { return CompareBetween }

func (c Between) prepare() (prepared, error) {
	return prepared{
		values: []template.Template{c.Left.Parse(), c.Min.Parse(), c.Max.Parse()},
		test: func(v []template.Rendered, _ compareOptions) (bool, error) {
			x, xOK := expr.ParseNumber(v[0].Text)
			lowest, lowOK := expr.ParseNumber(v[1].Text)
			highest, highOK := expr.ParseNumber(v[2].Text)
			return xOK && lowOK && highOK && lowest <= x && x <= highest, nil
		},
	}, nil
}

func (c Between) doc() clauseDoc {
	return clauseDoc{Left: &c.Left, Compare: CompareBetween, Min: &c.Min, Max: &c.Max}
}

// OneValue tests whether every $ token of the left value got a value when
// it was rendered (actions.md B24).
type OneValue struct {
	Left action.Template
	// Compare is replaced or not_replaced.
	Compare Comparison
}

// Comparison implements Clause.
func (c OneValue) Comparison() Comparison { return c.Compare }

func (c OneValue) prepare() (prepared, error) {
	var want bool
	switch c.Compare {
	case CompareReplaced:
		want = true
	case CompareNotReplaced:
		want = false
	default:
		return prepared{}, field("compare", fmt.Errorf("%w: %q does not test one value", action.ErrInvalid, c.Compare))
	}
	return prepared{
		values: []template.Template{c.Left.Parse()},
		test: func(v []template.Rendered, _ compareOptions) (bool, error) {
			return v[0].Replaced == want, nil
		},
	}, nil
}

func (c OneValue) doc() clauseDoc {
	return clauseDoc{Left: &c.Left, Compare: c.Compare}
}

// Expression evaluates the left value as an expression (actions.md B26,
// template.md B50–B52); the result must be true or false.
type Expression struct {
	Left string
}

// Comparison implements Clause.
func (Expression) Comparison() Comparison { return CompareExpression }

func (c Expression) prepare() (prepared, error) {
	x, err := expr.Compile(c.Left)
	if err != nil {
		return prepared{}, field("left", fmt.Errorf("%w: %w", action.ErrInvalid, err))
	}
	return prepared{
		values: x.Templates(),
		test: func(v []template.Rendered, _ compareOptions) (bool, error) {
			texts := make([]string, len(v))
			for i, r := range v {
				texts[i] = r.Text
			}
			r, err := x.EvalWithTexts(texts)
			if err != nil {
				return false, field("left", err)
			}
			if r.Kind != expr.Bool {
				return false, field("left", fmt.Errorf("%w: %q is not true or false", action.ErrInvalid, r.String()))
			}
			return r.Bool, nil
		},
	}, nil
}

func (c Expression) doc() clauseDoc {
	return clauseDoc{Left: new(action.Template(c.Left)), Compare: CompareExpression}
}

// Clauses are the clauses of a conditional. In JSON each clause is an
// object with the members "left" and "compare"; the comparison decides
// whether it has "right", "min" and "max" as well. Members it does not use
// are rejected.
type Clauses []Clause

// MarshalJSONTo writes the clauses.
func (cs Clauses) MarshalJSONTo(enc *jsontext.Encoder) error {
	docs := make([]clauseDoc, len(cs))
	for i, c := range cs {
		if c == nil {
			return clauseField(i, fmt.Errorf("%w: no clause", action.ErrInvalid))
		}
		docs[i] = c.doc()
	}
	return json.MarshalEncode(enc, docs)
}

// UnmarshalJSONFrom reads the clauses.
func (cs *Clauses) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var docs []clauseDoc
	if err := json.UnmarshalDecode(dec, &docs, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	clauses := make(Clauses, len(docs))
	for i, d := range docs {
		c, err := d.clause()
		if err != nil {
			return clauseField(i, err)
		}
		clauses[i] = c
	}
	*cs = clauses
	return nil
}

// clauseDoc is a clause as stored. Its members are pointers, so that a
// missing member differs from an empty text.
type clauseDoc struct {
	Left    *action.Template `json:"left,omitzero"`
	Compare Comparison       `json:"compare"`
	Right   *action.Template `json:"right,omitzero"`
	Min     *action.Template `json:"min,omitzero"`
	Max     *action.Template `json:"max,omitzero"`
}

// clause returns the clause d stands for, if d has exactly the members its
// comparison uses.
func (d clauseDoc) clause() (Clause, error) {
	if d.Left == nil {
		return nil, fmt.Errorf("%w: member %q is missing", action.ErrInvalid, "left")
	}
	var right, bounds bool
	switch {
	case slices.Contains(twoValueComparisons(), d.Compare):
		right = true
	case d.Compare == CompareBetween:
		bounds = true
	case d.Compare == CompareExpression, slices.Contains(oneValueComparisons(), d.Compare):
	default:
		return nil, field("compare", fmt.Errorf("%w: unknown comparison %q", action.ErrInvalid, d.Compare))
	}
	for _, m := range []struct {
		name      string
		has, uses bool
	}{
		{"right", d.Right != nil, right},
		{"min", d.Min != nil, bounds},
		{"max", d.Max != nil, bounds},
	} {
		switch {
		case m.has && !m.uses:
			return nil, fmt.Errorf("%w: %s has no member %q", action.ErrInvalid, d.Compare, m.name)
		case !m.has && m.uses:
			return nil, fmt.Errorf("%w: member %q is missing", action.ErrInvalid, m.name)
		}
	}
	switch {
	case right:
		return TwoValues{Left: *d.Left, Compare: d.Compare, Right: *d.Right}, nil
	case bounds:
		return Between{Left: *d.Left, Min: *d.Min, Max: *d.Max}, nil
	case d.Compare == CompareExpression:
		return Expression{Left: string(*d.Left)}, nil
	default:
		return OneValue{Left: *d.Left, Compare: d.Compare}, nil
	}
}
