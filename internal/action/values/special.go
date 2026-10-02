// SPDX-License-Identifier: Apache-2.0

package values

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/schema"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/expr"
	"github.com/ripmav/streamcrew/internal/template"
	"github.com/ripmav/streamcrew/internal/textfunc"
)

// TypeSpecialIdentifier is the type ID of the special identifier action
// (Code-ADR-0013, point 1).
const TypeSpecialIdentifier = "special_identifier"

// SpecialKind says how a special identifier action reads its value
// (actions.md B50, B51).
type SpecialKind string

// The kinds of the special identifier action.
const (
	// SpecialText reads the value as a template with text functions (B52
	// to B55). New special identifier actions do this.
	SpecialText SpecialKind = "text"
	// SpecialExpression evaluates the value as an expression, the option
	// "calculate" of B50; the result is a number (B51).
	SpecialExpression SpecialKind = "expression"
)

// SpecialKinds returns the kinds of the special identifier action, in the
// order editors show them.
func SpecialKinds() []SpecialKind {
	return []SpecialKind{SpecialText, SpecialExpression}
}

// Valid reports whether k is a known kind.
func (k SpecialKind) Valid() bool {
	return slices.Contains(SpecialKinds(), k)
}

// Globals hold the global values (actions.md B56, B57);
// *template.Globals implements it. The template engine reads them as a
// source (template.md, B10).
type Globals interface {
	// Set sets the global value name; a new name beyond the limit fails.
	Set(name string, v template.Value) error
}

// specialIdentifierSchema returns the schema of the special identifier
// action: the kind decides whether the value is a template or an
// expression, which is not empty.
func specialIdentifierSchema() *schema.Schema {
	return schema.Kinds(
		[]schema.Property{
			{Name: "name", Schema: schema.ResultName(), Required: true},
			{Name: "global", Schema: schema.Switch()},
		},
		schema.Variant{Kind: string(SpecialText), Props: []schema.Property{
			{Name: "value", Schema: schema.Template(), Required: true},
		}},
		schema.Variant{Kind: string(SpecialExpression), Props: []schema.Property{
			{Name: "value", Schema: schema.Expression(), Required: true},
		}},
	)
}

// SpecialIdentifier sets a value under a name that following actions read
// as $<name> (actions.md B50 to B57): a value of the run, and with Global
// also a global value.
type SpecialIdentifier struct {
	action.Common `json:",embed"`
	Kind          SpecialKind `json:"kind"`
	// Name is the name of the value, lowercase letters and digits (B5,
	// B50). Saving rejects names that hide a built-in identifier or a fixed
	// result name.
	Name action.ResultName `json:"name"`
	// Value is a template with text functions (B52 to B55) for the kind
	// text, an expression (B51) for the kind expression.
	Value action.Template `json:"value"`
	// Global also sets the value as a global value, which applies to every
	// later render of all instances until an action changes it or the core
	// ends (B56). New actions set a value of the run only.
	Global bool `json:"global"`
	ports  *ports
}

// DocType implements command.Action.
func (SpecialIdentifier) DocType() string { return TypeSpecialIdentifier }

// Validate implements command.Action: the name has the form of B5, an
// expression compiles, text functions are known and have the right number
// of parameters, and fixed patterns and dates are valid (B55).
func (s SpecialIdentifier) Validate() error {
	if !s.Kind.Valid() {
		return field("kind", fmt.Errorf("%w: unknown kind %q", action.ErrInvalid, s.Kind))
	}
	if err := s.Name.Validate(); err != nil {
		return field("name", err)
	}
	if s.Kind == SpecialExpression {
		_, err := expr.Compile(string(s.Value))
		return field("value", invalid(err))
	}
	_, err := textfunc.Parse(string(s.Value))
	return field("value", invalid(err))
}

// ResultNames implements command.ResultSetter: saving rejects a name that
// hides a built-in identifier or a fixed result name (B5; template.md,
// B12).
func (s SpecialIdentifier) ResultNames() []string {
	return []string{string(s.Name)}
}

// Perform implements engine.Performer. It sets the value only if it could
// determine it and, with Global, set the global value: a new global name
// beyond the limit lets the action fail without a value (B57).
func (s SpecialIdentifier) Perform(ctx context.Context, run *engine.Run) error {
	v, err := s.evaluate(ctx, run.Scope())
	if err != nil {
		return field("value", err)
	}
	if s.Global {
		if err := s.ports.Globals.Set(string(s.Name), v); err != nil {
			return field("global", err)
		}
	}
	run.Scope().SetValue(string(s.Name), v)
	return nil
}

// evaluate returns the value for the scope s. All identifiers of the value
// come from one render (B3).
func (s SpecialIdentifier) evaluate(ctx context.Context, sc *template.Scope) (template.Value, error) {
	switch s.Kind {
	case SpecialText:
		return s.text(ctx, sc)
	case SpecialExpression:
		return s.calculate(ctx, sc)
	default:
		return template.Value{}, fmt.Errorf("%w: unknown kind %q", action.ErrInvalid, s.Kind)
	}
}

// text returns the value as text with its functions applied (B52 to B54).
func (s SpecialIdentifier) text(ctx context.Context, sc *template.Scope) (template.Value, error) {
	t, err := textfunc.Parse(string(s.Value))
	if err != nil {
		return template.Value{}, invalid(err)
	}
	rendered, err := s.ports.Templates.RenderEach(ctx, t.Templates(), sc)
	if err != nil {
		return template.Value{}, err
	}
	texts := make([]string, len(rendered))
	for i, r := range rendered {
		texts[i] = r.Text
	}
	out, err := t.EvalWithTexts(texts, time.Now(), sc.Location)
	if err != nil {
		return template.Value{}, err
	}
	return template.TextValue(out), nil
}

// calculate returns the result of the expression as a number (B51): whole
// numbers without decimals, others with a point and as few digits as
// needed.
func (s SpecialIdentifier) calculate(ctx context.Context, sc *template.Scope) (template.Value, error) {
	x, err := expr.Compile(string(s.Value))
	if err != nil {
		return template.Value{}, invalid(err)
	}
	res, err := x.Eval(ctx, s.ports.Templates, sc)
	if err != nil {
		return template.Value{}, err
	}
	if res.Kind != expr.Number {
		return template.Value{}, fmt.Errorf("%w: %q is not a number", action.ErrInvalid, res.String())
	}
	n := res.Number
	if n == 0 {
		n = 0 // -0 equals 0; it is written as 0
	}
	return template.FloatValue(n), nil
}

// invalid wraps err with action.ErrInvalid; nil stays nil.
func invalid(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %w", action.ErrInvalid, err)
}
