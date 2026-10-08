// SPDX-License-Identifier: MIT

package action

import (
	"context"
	"encoding/json/jsontext"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"

	"github.com/ripmav/streamcrew/internal/action/schema"
	"github.com/ripmav/streamcrew/internal/expr"
	"github.com/ripmav/streamcrew/internal/template"
)

// Template is a text with $ identifiers. It is rendered when the action
// runs, with the scope of the run (actions.md B3).
type Template string

// Parse returns the parsed template.
func (t Template) Parse() template.Template {
	return template.Parse(string(t))
}

// Range is the allowed range of an amount, both ends included
// (actions.md B4).
type Range struct {
	Min, Max float64
	// Integer allows whole numbers only: a fraction fails, it is not
	// rounded.
	Integer bool
}

// Check returns an error wrapping ErrInvalid if v is not in r.
func (r Range) Check(v float64) error {
	switch {
	case math.IsNaN(v) || math.IsInf(v, 0):
		return fmt.Errorf("%w: %v is not a number", ErrInvalid, v)
	case r.Integer && v != math.Trunc(v):
		return fmt.Errorf("%w: %v is not a whole number", ErrInvalid, v)
	case v < r.Min || v > r.Max:
		return fmt.Errorf("%w: %v is not between %v and %v", ErrInvalid, v, r.Min, r.Max)
	}
	return nil
}

// Schema returns the schema of an amount in r.
func (r Range) Schema() *schema.Schema {
	return schema.Amount(r.Min, r.Max, r.Integer)
}

// Amount is a quantity of an action, such as seconds or a count
// (actions.md B4): a fixed number or an expression (template.md, B50–B52).
// In JSON it is a number or a text. The zero value is no amount; a field
// that may be missing has the tag omitzero, and Validate rejects it.
type Amount struct {
	// number is the fixed number as written; nil for an expression.
	number jsontext.Value
	// expression is the expression; empty for a fixed number.
	expression string
}

// Fixed returns the fixed amount v.
func Fixed(v float64) Amount {
	return Amount{number: jsontext.Value(strconv.FormatFloat(v, 'g', -1, 64))}
}

// Expression returns the amount the expression text yields.
func Expression(text string) Amount {
	return Amount{expression: text}
}

// IsZero reports whether a is no amount.
func (a Amount) IsZero() bool {
	return a.number == nil && a.expression == ""
}

// Fixed returns the fixed number of a; ok is false for an expression or no
// amount.
func (a Amount) Fixed() (v float64, ok bool) {
	if a.number == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(string(a.number), 64)
	return v, err == nil
}

// String returns the amount as written.
func (a Amount) String() string {
	if a.number != nil {
		return string(a.number)
	}
	return a.expression
}

// MarshalJSONTo writes a number or a text.
func (a Amount) MarshalJSONTo(enc *jsontext.Encoder) error {
	switch {
	case a.number != nil:
		return enc.WriteValue(a.number)
	case a.expression != "":
		return enc.WriteToken(jsontext.String(a.expression))
	default:
		return fmt.Errorf("%w: no amount; tag the field omitzero", ErrInvalid)
	}
}

// UnmarshalJSONFrom reads a number or a text that is not empty.
func (a *Amount) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	switch dec.PeekKind() {
	case '0':
		v, err := dec.ReadValue()
		if err != nil {
			return err
		}
		*a = Amount{number: slices.Clone(v)}
		return nil
	case '"':
		tok, err := dec.ReadToken()
		if err != nil {
			return err
		}
		if tok.String() == "" {
			return fmt.Errorf("%w: an amount is a number or an expression, not empty text", ErrInvalid)
		}
		*a = Amount{expression: tok.String()}
		return nil
	default:
		return fmt.Errorf("%w: an amount is a number or an expression", ErrInvalid)
	}
}

// Validate checks a for saving: a fixed number must be in r, an expression
// must compile (actions.md B4). Expressions are checked against r when they
// are evaluated.
func (a Amount) Validate(r Range) error {
	if v, ok := a.Fixed(); ok {
		return r.Check(v)
	}
	if a.expression == "" {
		return fmt.Errorf("%w: no amount", ErrInvalid)
	}
	if _, err := expr.Compile(a.expression); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return nil
}

// Eval returns the value of a for a run: the fixed number, or the
// expression rendered with e and s and evaluated (actions.md B4). The value
// must be a number in r; otherwise Eval returns an error wrapping
// ErrInvalid that names the value.
func (a Amount) Eval(ctx context.Context, e *template.Engine, s *template.Scope, r Range) (float64, error) {
	if v, ok := a.Fixed(); ok {
		return v, r.Check(v)
	}
	x, err := a.compile()
	if err != nil {
		return 0, err
	}
	res, err := x.Eval(ctx, e, s)
	if err != nil {
		return 0, err
	}
	return number(res, r)
}

// Templates returns the templates of the identifiers in a, so that an
// action renders them together with its other templates, in one render
// (actions.md B3); EvalWithTexts takes their texts. A fixed number has
// none.
func (a Amount) Templates() ([]template.Template, error) {
	if _, ok := a.Fixed(); ok {
		return nil, nil
	}
	x, err := a.compile()
	if err != nil {
		return nil, err
	}
	return x.Templates(), nil
}

// EvalWithTexts returns the value of a as Eval does, from texts, the
// rendered templates of Templates.
func (a Amount) EvalWithTexts(texts []string, r Range) (float64, error) {
	if v, ok := a.Fixed(); ok {
		return v, r.Check(v)
	}
	x, err := a.compile()
	if err != nil {
		return 0, err
	}
	res, err := x.EvalWithTexts(texts)
	if err != nil {
		return 0, err
	}
	return number(res, r)
}

// compile returns the expression of a, which is not a fixed number.
func (a Amount) compile() (*expr.Expression, error) {
	if a.expression == "" {
		return nil, fmt.Errorf("%w: no amount", ErrInvalid)
	}
	x, err := expr.Compile(a.expression)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return x, nil
}

// number returns the result of an amount's expression; it must be a
// number in r.
func number(res expr.Result, r Range) (float64, error) {
	if res.Kind != expr.Number {
		return 0, fmt.Errorf("%w: %q is not a number", ErrInvalid, res.String())
	}
	return res.Number, r.Check(res.Number)
}

// namePattern is the form of the names of result values (actions.md B5).
var namePattern = regexp.MustCompile(schema.PatternName)

// ResultName is the name of a result value (actions.md B5): lowercase
// letters and digits, without "$". Whether it hides a built-in identifier
// or a fixed result name, the command service checks when saving.
type ResultName string

// Validate checks the form of n.
func (n ResultName) Validate() error {
	if !namePattern.MatchString(string(n)) {
		return fmt.Errorf("%w: result name %q is not lowercase letters and digits", ErrInvalid, n)
	}
	return nil
}
