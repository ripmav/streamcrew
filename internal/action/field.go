// SPDX-License-Identifier: Apache-2.0

package action

import (
	"context"
	"encoding/json/jsontext"
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/ripmav/streamcrew/internal/action/schema"
	"github.com/ripmav/streamcrew/internal/decimal"
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
// (actions.md B4). The ends are whole numbers; the amounts are decimals
// (Code-ADR-0020).
type Range struct {
	Min, Max int64
	// Integer allows whole numbers only: a fraction fails, it is not
	// rounded.
	Integer bool
}

// Check returns an error wrapping ErrInvalid if v is not in r.
func (r Range) Check(v decimal.Decimal) error {
	switch {
	case r.Integer && !v.IsWhole():
		return fmt.Errorf("%w: %s is not a whole number", ErrInvalid, v)
	case v.Cmp(decimal.New(r.Min)) < 0 || v.Cmp(decimal.New(r.Max)) > 0:
		return fmt.Errorf("%w: %s is not between %d and %d", ErrInvalid, v, r.Min, r.Max)
	}
	return nil
}

// Schema returns the schema of an amount in r. JSON Schema has numbers
// only, so the ends become float64 there, exact up to 2^53.
func (r Range) Schema() *schema.Schema {
	return schema.Amount(float64(r.Min), float64(r.Max), r.Integer)
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

// Fixed returns the fixed amount v, written as a JSON number in its
// canonical form.
func Fixed(v decimal.Decimal) Amount {
	return Amount{number: jsontext.Value(v.String())}
}

// Expression returns the amount the expression text yields.
func Expression(text string) Amount {
	return Amount{expression: text}
}

// IsZero reports whether a is no amount.
func (a Amount) IsZero() bool {
	return a.number == nil && a.expression == ""
}

// Fixed returns the fixed number of a, read exactly from its text
// (Code-ADR-0020, point 9); ok is false for an expression, no amount or a
// number beyond the range of decimals.
func (a Amount) Fixed() (v decimal.Decimal, ok bool) {
	if a.number == nil {
		return decimal.Decimal{}, false
	}
	v, err := decimal.Parse(string(a.number))
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
	if a.number != nil {
		_, err := a.fixed(r)
		return err
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
func (a Amount) Eval(ctx context.Context, e *template.Engine, s *template.Scope, r Range) (decimal.Decimal, error) {
	if a.number != nil {
		return a.fixed(r)
	}
	x, err := a.compile()
	if err != nil {
		return decimal.Decimal{}, err
	}
	res, err := x.Eval(ctx, e, s)
	if err != nil {
		return decimal.Decimal{}, err
	}
	return number(res, r)
}

// fixed returns the fixed number of a if it is in r.
func (a Amount) fixed(r Range) (decimal.Decimal, error) {
	v, err := decimal.Parse(string(a.number))
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return v, r.Check(v)
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
func (a Amount) EvalWithTexts(texts []string, r Range) (decimal.Decimal, error) {
	if a.number != nil {
		return a.fixed(r)
	}
	x, err := a.compile()
	if err != nil {
		return decimal.Decimal{}, err
	}
	res, err := x.EvalWithTexts(texts)
	if err != nil {
		return decimal.Decimal{}, err
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
func number(res expr.Result, r Range) (decimal.Decimal, error) {
	if res.Kind != expr.Number {
		return decimal.Decimal{}, fmt.Errorf("%w: %q is not a number", ErrInvalid, res.String())
	}
	return res.Number, r.Check(res.Number)
}

// Seconds returns an amount of seconds as a duration, rounded to whole
// nanoseconds, half to even (Code-ADR-0020, point 3). A number beyond the
// range of time.Duration is an error wrapping ErrInvalid.
func Seconds(v decimal.Decimal) (time.Duration, error) {
	ns, err := v.Mul(decimal.New(int64(time.Second)))
	if err == nil {
		ns, err = ns.Round(0, decimal.RoundHalfEven)
	}
	var n int64
	if err == nil {
		n, err = ns.Int64()
	}
	if err != nil {
		return 0, fmt.Errorf("%w: %s seconds: %w", ErrInvalid, v, err)
	}
	return time.Duration(n), nil
}

// Whole returns a whole number of r, an amount that r checked with Integer
// set, as an int64.
func Whole(v decimal.Decimal) (int64, error) {
	n, err := v.Int64()
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return n, nil
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
