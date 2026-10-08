// SPDX-License-Identifier: MIT

package expr

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ripmav/streamcrew/internal/decimal"
)

// maxText is the longest text an evaluation may build by joining texts,
// as long as the result of a text function (actions.md, B53).
const maxText = 1 << 20

// value is the value of a node: a number, a truth value or text.
type value struct {
	kind   Kind
	number decimal.Decimal
	bool   bool
	text   string
}

// describe names a value in an error.
func (v value) describe() string {
	switch v.kind {
	case Number:
		return "number " + v.number.String()
	case Bool:
		return fmt.Sprintf("truth value %t", v.bool)
	default:
		return fmt.Sprintf("text %q", v.text)
	}
}

// evaluator evaluates a syntax tree with the values of its variables.
type evaluator struct {
	vars []value
}

// eval returns the value of n.
func (e *evaluator) eval(n node) (value, error) {
	switch n := n.(type) {
	case numberNode:
		return value{kind: Number, number: n.value}, nil
	case textNode:
		return value{kind: Text, text: n.value}, nil
	case boolNode:
		return value{kind: Bool, bool: n.value}, nil
	case variableNode:
		return e.vars[n.index], nil
	case unaryNode:
		return e.unary(n)
	case binaryNode:
		return e.binary(n)
	case callNode:
		return e.call(n)
	default:
		return value{}, fmt.Errorf("unknown node %T", n)
	}
}

func (e *evaluator) unary(n unaryNode) (value, error) {
	v, err := e.eval(n.operand)
	if err != nil {
		return value{}, err
	}
	switch {
	case (n.op == "not" || n.op == "!") && v.kind == Bool:
		return value{kind: Bool, bool: !v.bool}, nil
	case n.op == "-" && v.kind == Number:
		return value{kind: Number, number: v.number.Neg()}, nil
	case n.op == "+" && v.kind == Number:
		return v, nil
	default:
		return value{}, fmt.Errorf("%s %s", n.op, v.describe())
	}
}

func (e *evaluator) binary(n binaryNode) (value, error) {
	left, err := e.eval(n.left)
	if err != nil {
		return value{}, err
	}
	// and and or evaluate their right side only if it decides the result.
	switch n.op {
	case "and", "&&", "or", "||":
		if left.kind != Bool {
			return value{}, fmt.Errorf("%s %s …", left.describe(), n.op)
		}
		if left.bool == (n.op == "or" || n.op == "||") {
			return left, nil
		}
		right, err := e.eval(n.right)
		if err != nil {
			return value{}, err
		}
		if right.kind != Bool {
			return value{}, fmt.Errorf("%s %s %s", left.describe(), n.op, right.describe())
		}
		return right, nil
	}
	right, err := e.eval(n.right)
	if err != nil {
		return value{}, err
	}
	switch n.op {
	case "==", "!=":
		return value{kind: Bool, bool: equal(left, right) == (n.op == "==")}, nil
	case "<", ">", "<=", ">=":
		return compare(n.op, left, right)
	case "+":
		if left.kind == Text && right.kind == Text {
			if len(left.text)+len(right.text) > maxText {
				return value{}, fmt.Errorf("text longer than %d bytes", maxText)
			}
			return value{kind: Text, text: left.text + right.text}, nil
		}
	}
	if left.kind != Number || right.kind != Number {
		return value{}, fmt.Errorf("%s %s %s", left.describe(), n.op, right.describe())
	}
	var r decimal.Decimal
	switch n.op {
	case "+":
		r, err = left.number.Add(right.number)
	case "-":
		r, err = left.number.Sub(right.number)
	case "*":
		r, err = left.number.Mul(right.number)
	case "/":
		r, err = left.number.Quo(right.number)
	case "%":
		r, err = left.number.Rem(right.number)
	case "^", "**":
		r, err = left.number.Pow(right.number)
	default:
		return value{}, fmt.Errorf("unknown operator %q", n.op)
	}
	if err != nil {
		return value{}, fmt.Errorf("%s %s %s: %w", left.number, n.op, right.number, err)
	}
	return value{kind: Number, number: r}, nil
}

// equal reports whether two values are the same; values of different kinds
// never are.
func equal(a, b value) bool {
	if a.kind != b.kind {
		return false
	}
	switch a.kind {
	case Number:
		return a.number.Equal(b.number)
	case Bool:
		return a.bool == b.bool
	default:
		return a.text == b.text
	}
}

// compare orders two numbers by their value or two texts by their bytes.
func compare(op string, a, b value) (value, error) {
	var c int
	switch {
	case a.kind == Number && b.kind == Number:
		c = a.number.Cmp(b.number)
	case a.kind == Text && b.kind == Text:
		c = strings.Compare(a.text, b.text)
	default:
		return value{}, fmt.Errorf("%s %s %s", a.describe(), op, b.describe())
	}
	var r bool
	switch op {
	case "<":
		r = c < 0
	case ">":
		r = c > 0
	case "<=":
		r = c <= 0
	default:
		r = c >= 0
	}
	return value{kind: Bool, bool: r}, nil
}

// call evaluates a function; all functions take numbers.
func (e *evaluator) call(n callNode) (value, error) {
	args := make([]decimal.Decimal, len(n.args))
	for i, arg := range n.args {
		v, err := e.eval(arg)
		if err != nil {
			return value{}, err
		}
		if v.kind != Number {
			return value{}, fmt.Errorf("%s(%s)", n.name, v.describe())
		}
		args[i] = v.number
	}
	r, err := function(n.name, args)
	if err != nil {
		return value{}, fmt.Errorf("%s: %w", n.name, err)
	}
	return value{kind: Number, number: r}, nil
}

// function computes a function of B50. round rounds half away from zero.
func function(name string, args []decimal.Decimal) (decimal.Decimal, error) {
	switch name {
	case "abs":
		return args[0].Abs(), nil
	case "ceil":
		return args[0].Round(0, decimal.RoundCeiling)
	case "floor":
		return args[0].Round(0, decimal.RoundFloor)
	case "round":
		return args[0].Round(0, decimal.RoundHalfAway)
	case "min", "max":
		r := args[0]
		for _, a := range args[1:] {
			if c := a.Cmp(r); name == "min" && c < 0 || name == "max" && c > 0 {
				r = a
			}
		}
		return r, nil
	default:
		return decimal.Decimal{}, errors.New("unknown function")
	}
}
