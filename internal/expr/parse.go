// SPDX-License-Identifier: MIT

package expr

import (
	"errors"
	"fmt"
	"slices"

	"github.com/ripmav/streamcrew/internal/decimal"
)

// Limits of an expression (B52).
const (
	// maxNodes is the largest syntax tree an expression may have.
	maxNodes = 500
	// maxDepth is the deepest nesting of an expression, e.g. of
	// parentheses.
	maxDepth = 100
)

// node is a node of the syntax tree of an expression.
type node interface {
	isNode()
}

type (
	// numberNode is a number in the expression.
	numberNode struct{ value decimal.Decimal }
	// textNode is text in quotes without identifiers.
	textNode struct{ value string }
	// boolNode is true or false.
	boolNode struct{ value bool }
	// variableNode is the value of the i-th variable.
	variableNode struct{ index int }
	// unaryNode is -x, +x, not x and !x.
	unaryNode struct {
		op      string
		operand node
	}
	// binaryNode is x op y.
	binaryNode struct {
		op          string
		left, right node
	}
	// callNode is a function call.
	callNode struct {
		name string
		args []node
	}
)

func (numberNode) isNode()   {}
func (textNode) isNode()     {}
func (boolNode) isNode()     {}
func (variableNode) isNode() {}
func (unaryNode) isNode()    {}
func (binaryNode) isNode()   {}
func (callNode) isNode()     {}

// precedence returns how strongly a binary operator binds, higher binds
// stronger; ok is false for anything else. The powers bind to the right,
// all others to the left.
func precedence(op string) (int, bool) {
	switch op {
	case "or", "||":
		return 10, true
	case "and", "&&":
		return 15, true
	case "==", "!=", "<", ">", "<=", ">=":
		return 20, true
	case "+", "-":
		return 30, true
	case "*", "/", "%":
		return 60, true
	case "^", "**":
		return 100, true
	default:
		return 0, false
	}
}

// unaryPrecedence returns how strongly a unary operator binds its operand:
// -2 ^ 2 is -(2 ^ 2), not 1 == 1 is (not 1) == 1.
func unaryPrecedence(op string) (int, bool) {
	switch op {
	case "not", "!":
		return 50, true
	case "-", "+":
		return 90, true
	default:
		return 0, false
	}
}

// functions are the functions of expressions and their least and most
// numbers of arguments; -1 is any number.
func functions() map[string][2]int {
	return map[string][2]int{
		"abs":   {1, 1},
		"ceil":  {1, 1},
		"floor": {1, 1},
		"round": {1, 1},
		"min":   {1, -1},
		"max":   {1, -1},
	}
}

// parser reads tokens into a syntax tree.
type parser struct {
	tokens []token
	pos    int
	nodes  int
	depth  int
}

// parse returns the syntax tree of tokens.
func parse(tokens []token) (node, error) {
	p := &parser{tokens: append(slices.Clip(tokens), token{kind: tokEnd})}
	n, err := p.expression(0)
	if err != nil {
		return nil, err
	}
	if t := p.peek(); t.kind != tokEnd {
		return nil, fmt.Errorf("unexpected %q", t.text)
	}
	return n, nil
}

func (p *parser) peek() token {
	return p.tokens[p.pos]
}

func (p *parser) next() token {
	t := p.tokens[p.pos]
	if t.kind != tokEnd {
		p.pos++
	}
	return t
}

// add counts a node against the limit.
func (p *parser) add(n node) (node, error) {
	p.nodes++
	if p.nodes > maxNodes {
		return nil, fmt.Errorf("more than %d parts", maxNodes)
	}
	return n, nil
}

// operator returns the binary operator at the position, if there is one.
func (p *parser) operator() (string, int, bool) {
	t := p.peek()
	if t.kind != tokOperator && t.kind != tokName {
		return "", 0, false
	}
	prec, ok := precedence(t.text)
	return t.text, prec, ok
}

// expression reads operands joined by binary operators that bind at least
// as strongly as minimum.
func (p *parser) expression(minimum int) (node, error) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxDepth {
		return nil, fmt.Errorf("nested more than %d levels deep", maxDepth)
	}
	left, err := p.operand()
	if err != nil {
		return nil, err
	}
	for {
		op, prec, ok := p.operator()
		if !ok || prec < minimum {
			return left, nil
		}
		p.next()
		next := prec + 1
		if op == "^" || op == "**" {
			next = prec // right-associative
		}
		right, err := p.expression(next)
		if err != nil {
			return nil, err
		}
		if left, err = p.add(binaryNode{op: op, left: left, right: right}); err != nil {
			return nil, err
		}
	}
}

// operand reads a number, text, a truth value, a variable, an expression in
// parentheses, a function call or a unary operator with its operand.
func (p *parser) operand() (node, error) {
	t := p.next()
	switch t.kind {
	case tokNumber:
		return p.add(numberNode{value: t.number})
	case tokText:
		return p.add(textNode{value: t.text})
	case tokVariable:
		return p.add(variableNode{index: t.variable})
	case tokOpen:
		n, err := p.expression(0)
		if err != nil {
			return nil, err
		}
		if c := p.next(); c.kind != tokClose {
			return nil, fmt.Errorf("missing ) before %q", c.text)
		}
		return n, nil
	case tokOperator, tokName:
		if prec, ok := unaryPrecedence(t.text); ok {
			operand, err := p.expression(prec)
			if err != nil {
				return nil, err
			}
			return p.add(unaryNode{op: t.text, operand: operand})
		}
		if t.kind == tokName {
			return p.name(t.text)
		}
	case tokClose:
		return nil, errors.New("unexpected )")
	case tokEnd:
		return nil, errors.New("unexpected end")
	}
	return nil, fmt.Errorf("unexpected %q", t.text)
}

// name reads a truth value or a function call.
func (p *parser) name(name string) (node, error) {
	switch name {
	case "true", "false":
		return p.add(boolNode{value: name == "true"})
	}
	arity, ok := functions()[name]
	if !ok {
		return nil, fmt.Errorf("unknown name %q", name)
	}
	if t := p.next(); t.kind != tokOpen {
		return nil, fmt.Errorf("function %q needs (", name)
	}
	var args []node
	if p.peek().kind != tokClose {
		for {
			arg, err := p.expression(0)
			if err != nil {
				return nil, err
			}
			args = append(args, arg)
			if p.peek().kind != tokOperator || p.peek().text != "," {
				break
			}
			p.next()
		}
	}
	if t := p.next(); t.kind != tokClose {
		return nil, fmt.Errorf("function %q: missing ) before %q", name, t.text)
	}
	if len(args) < arity[0] || arity[1] >= 0 && len(args) > arity[1] {
		return nil, fmt.Errorf("function %q: %d arguments", name, len(args))
	}
	return p.add(callNode{name: name, args: args})
}
