// SPDX-License-Identifier: MIT

// Package expr evaluates expressions with $ identifiers (spec template.md,
// B50 to B52): the calculations of the special identifier action, amounts
// and the conditions of the conditional action.
//
// Its own parser reads the language that B50 names: numbers, text in quotes
// for comparisons and joining, true and false, + - * / % and the powers ^
// and **, parentheses, comparisons, and, or, not (also &&, || and !), and
// the functions of B53: abs, acos, acot, asin, atan, avg, ceil, ceiling,
// cos, cot, csc, floor, if, ifequal, ifless, ifmore, log10, loge, logn,
// max, median, min, random, randomrange, round, sec, sin, sqrt, tan,
// truncate, and the constants e and pi. random(n) and randomrange(a, b)
// include their upper bound, random(6) gives 1 to 6; WithRandom injects
// their source of the random numbers. Numbers are exact decimals of
// internal/decimal (Code-ADR-0020): 0.1 + 0.2 == 0.3 holds.
//
// Compile turns each $ token into a variable. Eval resolves the identifiers
// and passes their values, so a value never becomes part of the expression
// (B51): an argument like "1)+(2" stays text.
package expr

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"math/rand/v2"
	"strings"

	"github.com/ripmav/streamcrew/internal/decimal"
	"github.com/ripmav/streamcrew/internal/template"
)

var (
	// ErrInvalid is wrapped by the errors of Compile: a syntax error, a
	// part outside the language or an expression beyond the limits.
	ErrInvalid = errors.New("invalid expression")
	// ErrEvaluation is wrapped by the errors of Eval when an expression
	// cannot be evaluated, e.g. text times a number or a division by zero
	// (B52, B76).
	ErrEvaluation = errors.New("expression cannot be evaluated")
)

// Expression is a compiled expression. It is immutable and safe for
// concurrent use; actions compile their expressions when a command is
// loaded.
type Expression struct {
	src  string
	vars []variableDef
	tree node
	// rnd draws the random numbers of random(n) and randomrange(a, b): a
	// whole number from 1 to n, including both ends.
	rnd func(n int) int
}

// options are the settings of Compile.
type options struct {
	rnd func(n int) int
}

// Option configures Compile.
type Option func(*options)

// WithRandom sets the source of the random numbers of random(n) and
// randomrange(a, b): a function that returns a whole number from 1 to n,
// including both ends. The default is a random source on math/rand/v2.
func WithRandom(f func(n int) int) Option {
	return func(o *options) { o.rnd = f }
}

// variableDef is a variable of an expression: a $ token, or text in quotes
// with $ tokens in it.
type variableDef struct {
	tmpl template.Template
	// quoted variables are always text; the others are numbers if their
	// value is a decimal number (B51).
	quoted bool
}

// Compile compiles text with the options, none for the defaults. Each $
// token becomes a variable whose value Eval provides. Text in quotes that
// contains $ tokens becomes one variable with the rendered text, e.g.
// "$arg1text" or "Hi $username"; it must not contain escape sequences. A
// "$" that starts no token outside quotes is an error.
func Compile(text string, opts ...Option) (*Expression, error) {
	invalid := func(err error) error {
		return fmt.Errorf("%w: %q: %w", ErrInvalid, text, err)
	}
	o := options{rnd: func(n int) int {
		//nolint:gosec // not a cryptographic source, but the random numbers of the templates
		return rand.IntN(n) + 1
	}}
	for _, opt := range opts {
		opt(&o)
	}
	var (
		tokens []token
		vars   []variableDef
		err    error
	)
	add := func(v variableDef) {
		tokens = append(tokens, token{kind: tokVariable, text: v.tmpl.String(), variable: len(vars)})
		vars = append(vars, v)
	}
	for chunk, quoted := range chunks(text) {
		if quoted {
			content := chunk[1 : len(chunk)-1]
			tmpl := template.Parse(content)
			if !hasTokens(tmpl) {
				if tokens, err = lex(chunk, tokens); err != nil {
					return nil, invalid(err)
				}
				continue
			}
			if strings.ContainsRune(content, '\\') {
				return nil, invalid(errors.New("escape sequences in quotes with identifiers are not supported"))
			}
			add(variableDef{tmpl: tmpl, quoted: true})
			continue
		}
		for segment, isToken := range template.Parse(chunk).Segments() {
			switch {
			case isToken:
				add(variableDef{tmpl: template.Parse(segment)})
			case strings.Contains(segment, "$"):
				return nil, invalid(errors.New("a $ without an identifier name"))
			default:
				if tokens, err = lex(segment, tokens); err != nil {
					return nil, invalid(err)
				}
			}
		}
	}
	tree, err := parse(tokens)
	if err != nil {
		return nil, invalid(err)
	}
	return &Expression{src: text, vars: vars, tree: tree, rnd: o.rnd}, nil
}

// String returns the text x was compiled from.
func (x *Expression) String() string {
	return x.src
}

// Eval resolves the identifiers of x with e and s, which may be nil, in one
// render, and evaluates x (B50, B51). The value of a $ token that is a
// decimal number counts as a number, anything else as text; a token without
// value is its own text (B4). Eval returns an error wrapping ErrEvaluation if
// x cannot be evaluated (B52), and the error of the context when it is
// done.
func (x *Expression) Eval(ctx context.Context, e *template.Engine, s *template.Scope) (Result, error) {
	rendered, err := e.RenderEach(ctx, x.Templates(), s)
	if err != nil {
		return Result{}, fmt.Errorf("evaluate %q: %w", x.src, err)
	}
	texts := make([]string, len(rendered))
	for i, r := range rendered {
		texts[i] = r.Text
	}
	return x.EvalWithTexts(texts)
}

// Templates returns the templates of the identifiers of x, in the order
// EvalWithTexts takes their texts. A caller that renders them together with
// other templates, in one render, uses them, e.g. the conditional action
// (spec actions.md, B27).
func (x *Expression) Templates() []template.Template {
	ts := make([]template.Template, len(x.vars))
	for i, v := range x.vars {
		ts[i] = v.tmpl
	}
	return ts
}

// EvalWithTexts evaluates x with texts, the rendered templates of
// Templates, as Eval does after its render. It returns an error wrapping
// ErrEvaluation if the number of texts is not that of the templates.
func (x *Expression) EvalWithTexts(texts []string) (Result, error) {
	if len(texts) != len(x.vars) {
		return Result{}, fmt.Errorf("%w: %q: %d texts for %d identifiers", ErrEvaluation, x.src, len(texts), len(x.vars))
	}
	e := evaluator{vars: make([]value, len(texts)), rnd: x.rnd}
	for i, text := range texts {
		e.vars[i] = value{kind: Text, text: text}
		if n, ok := ParseNumber(text); ok && !x.vars[i].quoted {
			e.vars[i] = value{kind: Number, number: n}
		}
	}
	v, err := e.eval(x.tree)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %q: %w", ErrEvaluation, x.src, err)
	}
	return Result{Kind: v.kind, Number: v.number, Bool: v.bool, Text: v.text}, nil
}

// ParseNumber returns the number text stands for if it counts as a number
// (B51): a decimal number with an optional sign, decimal places and
// exponent, whose absolute value is below 10^34 (Code-ADR-0020).
// Hexadecimal numbers, infinity, NaN and text with spaces count as text.
// The conditional action compares by this rule (spec actions.md, B22).
func ParseNumber(text string) (decimal.Decimal, bool) {
	d, err := decimal.Parse(text)
	return d, err == nil
}

// chunks splits text into code and literals in quotes, with their quotes.
// Double and single quotes end at the next quote that no backslash escapes;
// back quotes at the next back quote. An open literal at the end counts as
// code, so that the parser reports it.
func chunks(text string) iter.Seq2[string, bool] {
	return func(yield func(string, bool) bool) {
		start := 0
		for i := 0; i < len(text); i++ {
			quote := text[i]
			if quote != '"' && quote != '\'' && quote != '`' {
				continue
			}
			end := closing(text, i)
			if end < 0 {
				break
			}
			if start < i && !yield(text[start:i], false) {
				return
			}
			if !yield(text[i:end+1], true) {
				return
			}
			start, i = end+1, end
		}
		if start < len(text) {
			yield(text[start:], false)
		}
	}
}

// closing returns the index of the quote that closes the literal opened at
// text[open], or -1.
func closing(text string, open int) int {
	for i := open + 1; i < len(text); i++ {
		switch {
		case text[i] == '\\' && text[open] != '`':
			i++
		case text[i] == text[open]:
			return i
		}
	}
	return -1
}

// hasTokens reports whether t contains a $ token.
func hasTokens(t template.Template) bool {
	for _, token := range t.Segments() {
		if token {
			return true
		}
	}
	return false
}
