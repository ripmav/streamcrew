// SPDX-License-Identifier: Apache-2.0

// Package expr evaluates expressions with $ identifiers (spec template.md,
// B50 to B52; Code-ADR-0012, point 8): the calculations of the special
// identifier action, amounts and the conditions of the conditional action.
//
// Compile replaces each $ token with a variable and compiles the resulting
// text once with github.com/expr-lang/expr. Eval resolves the identifiers
// and passes their values as variables, so a value never becomes part of
// the expression text (B51): an argument like "1)+(2" stays text.
//
// The language is the part of expr that B50 names: numbers, text in quotes
// for comparisons, + - * / % and the powers ^ and **, parentheses,
// comparisons, and, or, not, and the functions abs, ceil, floor, round, min
// and max. All numbers are float64. Everything else, such as arrays, member
// access, ranges or other functions, is rejected when compiling.
package expr

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"math"
	"slices"
	"strconv"
	"strings"

	exprlang "github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
	"github.com/expr-lang/expr/vm"

	"github.com/ripmav/streamcrew/internal/template"
)

// Limits of an expression (B52).
const (
	// maxNodes is the largest syntax tree an expression may have.
	maxNodes = 500
	// memoryBudget limits the allocations of an evaluation.
	memoryBudget = 10_000
)

var (
	// ErrInvalid is wrapped by the errors of Compile: a syntax error, an
	// unsupported part of the language or an expression beyond the limits.
	ErrInvalid = errors.New("invalid expression")
	// ErrEvaluation is wrapped by the errors of Eval when an expression
	// cannot be evaluated, e.g. text times a number or a division by zero
	// (B52, B76).
	ErrEvaluation = errors.New("expression cannot be evaluated")
)

// builtins are the functions of expr that expressions may call.
func builtins() []string {
	return []string{"abs", "ceil", "floor", "round", "min", "max"}
}

// Expression is a compiled expression. It is immutable and safe for
// concurrent use; actions compile their expressions when a command is
// loaded.
type Expression struct {
	src string
	// prefix starts the names of the variables; it does not occur in src,
	// so no name in the text can be taken for a variable.
	prefix  string
	vars    []variableDef
	program *vm.Program
}

// variableDef is a variable of an expression: a $ token, or text in quotes
// with $ tokens in it.
type variableDef struct {
	tmpl template.Template
	// quoted variables are always text; the others are numbers if their
	// value is a decimal number (B51).
	quoted bool
}

// Compile compiles text. Each $ token becomes a variable whose value Eval
// provides. Text in quotes that contains $ tokens becomes one variable with
// the rendered text, e.g. "$arg1text" or "Hi $username"; it must not contain
// escape sequences. A "$" that starts no token outside quotes is an error.
func Compile(text string) (*Expression, error) {
	var src strings.Builder
	var vars []variableDef
	names := make(map[string]bool)
	prefix := variablePrefix(text)
	add := func(v variableDef) {
		name := variable(prefix, len(vars))
		vars = append(vars, v)
		names[name] = true
		src.WriteString(" " + name + " ")
	}
	for chunk, quoted := range chunks(text) {
		if quoted {
			content := chunk[1 : len(chunk)-1]
			tmpl := template.Parse(content)
			if !hasTokens(tmpl) {
				src.WriteString(chunk)
				continue
			}
			if strings.ContainsRune(content, '\\') {
				return nil, fmt.Errorf("%w: %q: escape sequences in quotes with identifiers are not supported", ErrInvalid, text)
			}
			add(variableDef{tmpl: tmpl, quoted: true})
			continue
		}
		for segment, token := range template.Parse(chunk).Segments() {
			switch {
			case token:
				add(variableDef{tmpl: template.Parse(segment)})
			case strings.Contains(segment, "$"):
				return nil, fmt.Errorf("%w: %q: a $ without an identifier name", ErrInvalid, text)
			default:
				src.WriteString(segment)
			}
		}
	}

	tree, err := parser.Parse(src.String())
	if err != nil {
		return nil, fmt.Errorf("%w: %q: %w", ErrInvalid, text, err)
	}
	check := checker{vars: names}
	ast.Walk(&tree.Node, &check)
	if check.err != nil {
		return nil, fmt.Errorf("%w: %q: %w", ErrInvalid, text, check.err)
	}

	opts := []exprlang.Option{
		exprlang.AllowUndefinedVariables(), // variables are typed when evaluated (B51)
		exprlang.DisableAllBuiltins(),
		exprlang.DisableIfOperator(),
		exprlang.MaxNodes(maxNodes),
		exprlang.Patch(floats{}),
		exprlang.Function(modFunction, mod),
	}
	for _, name := range builtins() {
		opts = append(opts, exprlang.EnableBuiltin(name))
	}
	program, err := exprlang.Compile(src.String(), opts...)
	if err != nil {
		return nil, fmt.Errorf("%w: %q: %w", ErrInvalid, text, err)
	}
	return &Expression{src: text, prefix: prefix, vars: vars, program: program}, nil
}

// String returns the text x was compiled from.
func (x *Expression) String() string {
	return x.src
}

// Eval resolves the identifiers of x with e and s, which may be nil, in one
// render, and evaluates x (B50, B51). The value of a $ token that is a
// decimal number counts as a number, anything else as text; a token without
// value is its own text (B4). Eval returns an error wrapping ErrEvaluation if
// x cannot be evaluated or its result is not a finite number, a truth value
// or text (B52), and the error of the context when it is done.
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
	env := make(map[string]any, len(texts))
	for i, text := range texts {
		if x.vars[i].quoted {
			env[variable(x.prefix, i)] = text
		} else {
			env[variable(x.prefix, i)] = typed(text)
		}
	}
	machine := vm.VM{MemoryBudget: memoryBudget}
	out, err := machine.Run(x.program, env)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %q: %w", ErrEvaluation, x.src, err)
	}
	r, err := result(out)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %q: %w", ErrEvaluation, x.src, err)
	}
	return r, nil
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

// variablePrefix returns the start of the variable names for text: "v",
// followed by as many underscores as it takes so that it occurs nowhere in
// text. A name in the text, such as v0, is then never taken for a variable
// and stays an unknown name, which the checker rejects (B50).
func variablePrefix(text string) string {
	prefix := "v"
	for strings.Contains(text, prefix) {
		prefix += "_"
	}
	return prefix
}

// variable returns the name of the variable for the i-th token.
func variable(prefix string, i int) string {
	return prefix + strconv.Itoa(i)
}

// typed returns a decimal number as float64 and anything else as text
// (B51).
func typed(text string) any {
	if f, ok := ParseNumber(text); ok {
		return f
	}
	return text
}

// ParseNumber returns the number text stands for if it counts as a number
// (B51): a decimal number in the range of float64, with an optional sign,
// fraction and exponent. Hexadecimal numbers, infinity, NaN and text with
// spaces count as text. The conditional action compares by this rule
// (spec actions.md, B22).
func ParseNumber(text string) (float64, bool) {
	if text == "" || strings.ContainsFunc(text, func(r rune) bool {
		return !strings.ContainsRune("0123456789+-.eE", r)
	}) {
		return 0, false
	}
	// ParseFloat reports numbers beyond float64 as an error.
	f, err := strconv.ParseFloat(text, 64)
	return f, err == nil
}

// checker rejects the parts of expr that B50 does not name.
type checker struct {
	vars map[string]bool
	err  error
}

// Visit implements ast.Visitor.
func (c *checker) Visit(node *ast.Node) {
	if c.err != nil {
		return
	}
	switch n := (*node).(type) {
	case *ast.IntegerNode, *ast.FloatNode, *ast.StringNode, *ast.BoolNode:
	case *ast.IdentifierNode:
		if !c.vars[n.Value] {
			c.err = fmt.Errorf("unknown name %q", n.Value)
		}
	case *ast.UnaryNode:
		switch n.Operator {
		case "-", "+", "!", "not":
		default:
			c.err = fmt.Errorf("operator %q is not supported", n.Operator)
		}
	case *ast.BinaryNode:
		switch n.Operator {
		case "+", "-", "*", "/", "%", "^", "**", "==", "!=", "<", ">", "<=", ">=", "&&", "||", "and", "or":
		default:
			c.err = fmt.Errorf("operator %q is not supported", n.Operator)
		}
	case *ast.BuiltinNode:
		if !slices.Contains(builtins(), n.Name) {
			c.err = fmt.Errorf("function %q is not supported", n.Name)
		}
	default:
		kind := strings.TrimSuffix(strings.TrimPrefix(fmt.Sprintf("%T", n), "*ast."), "Node")
		c.err = fmt.Errorf("%s syntax is not supported", strings.ToLower(kind))
	}
}

// modFunction is the function that replaces the operator %.
const modFunction = "mod"

// floats makes every number a float64 and replaces % with math.Mod, which
// expr only provides for whole numbers.
type floats struct{}

// Visit implements ast.Visitor.
func (floats) Visit(node *ast.Node) {
	switch n := (*node).(type) {
	case *ast.IntegerNode:
		ast.Patch(node, &ast.FloatNode{Value: float64(n.Value)})
	case *ast.BinaryNode:
		if n.Operator == "%" {
			ast.Patch(node, &ast.CallNode{
				Callee:    &ast.IdentifierNode{Value: modFunction},
				Arguments: []ast.Node{n.Left, n.Right},
			})
		}
	}
}

// mod is the remainder of a division of two numbers, with the sign of the
// dividend.
func mod(params ...any) (any, error) {
	a, aOK := params[0].(float64)
	b, bOK := params[1].(float64)
	if !aOK || !bOK {
		return nil, fmt.Errorf("invalid operation: %T %% %T", params[0], params[1])
	}
	return math.Mod(a, b), nil
}

// result converts the value of an expression.
func result(v any) (Result, error) {
	switch v := v.(type) {
	case float64:
		if math.IsInf(v, 0) || math.IsNaN(v) {
			return Result{}, errors.New("result is not a finite number")
		}
		return Result{Kind: Number, Number: v}, nil
	case bool:
		return Result{Kind: Bool, Bool: v}, nil
	case string:
		return Result{Kind: Text, Text: v}, nil
	default:
		return Result{}, fmt.Errorf("result of type %T is not supported", v)
	}
}
