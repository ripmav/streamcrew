// SPDX-License-Identifier: MIT

package flow

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/schema"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/template"
)

// ErrPassLimit is the error of a conditional that repeats while it is true
// and is still true after maxRepeats passes (actions.md B29;
// command-engine.md B74).
var ErrPassLimit = errors.New("pass limit reached")

// Combine says how the clauses of a conditional combine (actions.md B27).
type Combine string

// The ways to combine clauses.
const (
	// CombineAnd is true if every clause is. New conditionals combine so.
	CombineAnd Combine = "and"
	// CombineOr is true if at least one clause is.
	CombineOr Combine = "or"
	// CombineXor is true if exactly one clause is.
	CombineXor Combine = "xor"
)

// Combines returns the ways to combine clauses, in the order editors show
// them.
func Combines() []Combine {
	return []Combine{CombineAnd, CombineOr, CombineXor}
}

// Valid reports whether c is a known way to combine clauses.
func (c Combine) Valid() bool {
	return slices.Contains(Combines(), c)
}

// Conditional runs its child actions for "true" or for "false", depending
// on its clauses (actions.md B20 to B29).
//
// Its child actions are Actions followed by Else, so the first action of
// Else has the position len(Actions)+1 in the path of the history
// (actions.md B9).
type Conditional struct {
	action.Common `json:",embed"`
	// Clauses are one or more.
	Clauses Clauses `json:"clauses,omitzero"`
	// Combine says how the results of the clauses combine.
	Combine Combine `json:"combine"`
	// CaseSensitive makes texts compare with regard to case; a new
	// conditional ignores case (B20).
	CaseSensitive bool `json:"caseSensitive"`
	// Actions run if the condition is true.
	Actions []command.Action `json:"actions"`
	// Else run if the condition is false; with RepeatWhileTrue only if it
	// is false at once (B29).
	Else []command.Action `json:"else"`
	// RepeatWhileTrue evaluates the condition again after Actions, until it
	// is false (B29).
	RepeatWhileTrue bool `json:"repeatWhileTrue"`
	ports           *ports
}

// DocType implements command.Action.
func (Conditional) DocType() string { return TypeConditional }

// Validate implements command.Action. A regular expression without $
// tokens and an expression must compile; a regular expression with tokens
// is checked when it is rendered.
func (c Conditional) Validate() error {
	if !c.Combine.Valid() {
		return field("combine", fmt.Errorf("%w: unknown way to combine %q", action.ErrInvalid, c.Combine))
	}
	_, err := c.prepare()
	return err
}

// Children implements command.Parent: Actions, then Else.
func (c Conditional) Children() []command.Action {
	return slices.Concat(c.Actions, c.Else)
}

// Perform implements engine.Performer. Each evaluation of the condition
// has the default time limit, the child actions their own (actions.md B8).
func (c Conditional) Perform(ctx context.Context, run *engine.Run) error {
	clauses, err := c.prepare()
	if err != nil {
		return err
	}
	for passes := 0; ; passes++ {
		if passes > 0 {
			if err := run.LimitTo(engine.DefaultTimeLimit); err != nil {
				return err
			}
		}
		holds, err := c.holds(ctx, run.Scope(), clauses)
		if err != nil {
			return err
		}
		switch {
		case !holds && passes == 0:
			_, err := runRange(ctx, run, len(c.Actions), len(c.Actions)+len(c.Else))
			return err
		case !holds:
			return nil
		case passes == maxRepeats:
			return field("repeatWhileTrue", fmt.Errorf("%w: the condition is still true after %d passes", ErrPassLimit, maxRepeats))
		}
		next, err := runRange(ctx, run, 0, len(c.Actions))
		if err != nil || next == engine.ChildEnd || !c.RepeatWhileTrue {
			return err
		}
	}
}

// prepare checks the clauses and returns them ready to be tested.
func (c Conditional) prepare() ([]prepared, error) {
	if len(c.Clauses) == 0 {
		return nil, field("clauses", fmt.Errorf("%w: a condition needs a clause", action.ErrInvalid))
	}
	clauses := make([]prepared, len(c.Clauses))
	for i, cl := range c.Clauses {
		if cl == nil {
			return nil, clauseField(i, fmt.Errorf("%w: no clause", action.ErrInvalid))
		}
		p, err := cl.prepare()
		if err != nil {
			return nil, clauseField(i, err)
		}
		clauses[i] = p
	}
	return clauses, nil
}

// holds renders the values of all clauses in one render (B27), tests each
// clause and combines the results.
func (c Conditional) holds(ctx context.Context, s *template.Scope, clauses []prepared) (bool, error) {
	var values []template.Template
	for _, p := range clauses {
		values = append(values, p.values...)
	}
	rendered, err := c.ports.Templates.RenderEach(ctx, values, s)
	if err != nil {
		return false, err
	}
	o := compareOptions{caseSensitive: c.CaseSensitive, delimiter: s.ArgDelimiter}
	held := 0
	for i, p := range clauses {
		ok, err := p.test(rendered[:len(p.values)], o)
		if err != nil {
			return false, clauseField(i, err)
		}
		rendered = rendered[len(p.values):]
		if ok {
			held++
		}
	}
	switch c.Combine {
	case CombineAnd:
		return held == len(clauses), nil
	case CombineOr:
		return held > 0, nil
	case CombineXor:
		return held == 1, nil
	default:
		return false, field("combine", fmt.Errorf("%w: unknown way to combine %q", action.ErrInvalid, c.Combine))
	}
}

// conditionalSchema returns the schema of conditional.
func conditionalSchema() *schema.Schema {
	return schema.Document(
		schema.Property{Name: "clauses", Schema: schema.List(clauseSchema(), 1), Required: true},
		schema.Property{Name: "combine", Schema: schema.Choice(texts(Combines())...)},
		schema.Property{Name: "caseSensitive", Schema: schema.Switch()},
		schema.Property{Name: "actions", Schema: schema.Actions()},
		schema.Property{Name: "else", Schema: schema.Actions()},
		schema.Property{Name: "repeatWhileTrue", Schema: schema.Switch()},
	)
}

// clauseSchema returns the schema of a clause: its comparison decides
// whether it has a right value, two bounds or nothing besides the left
// value.
func clauseSchema() *schema.Schema {
	value := func(name string) schema.Property {
		return schema.Property{Name: name, Schema: schema.Template(), Required: true}
	}
	return schema.Pick("compare", []schema.Property{value("left")},
		schema.Alternative{Values: texts(twoValueComparisons()), Props: []schema.Property{value("right")}},
		schema.Alternative{Values: texts([]Comparison{CompareBetween}), Props: []schema.Property{value("min"), value("max")}},
		schema.Alternative{Values: texts(oneValueComparisons())},
		schema.Alternative{Values: texts([]Comparison{CompareExpression})},
	)
}

// clauseField names the clause i of an error, counting from 1.
func clauseField(i int, err error) error {
	return field(fmt.Sprintf("clause %d", i+1), err)
}
