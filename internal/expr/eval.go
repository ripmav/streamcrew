// SPDX-License-Identifier: Apache-2.0

package expr

import (
	"fmt"
	"slices"
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
	// rnd draws the random numbers of random(n) and randomrange(a, b): a
	// whole number from 1 to n, including both ends.
	rnd func(n int) int
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

// call evaluates a function (B53).
func (e *evaluator) call(n callNode) (value, error) {
	switch functions()[n.name].class {
	case fnIf:
		return e.callIf(n)
	case fnCompare:
		return e.callCompare(n)
	case fnNumber:
	}
	args, err := e.numberArgs(n)
	if err != nil {
		return value{}, err
	}
	switch n.name {
	case "random":
		return e.random(n.name, args[0])
	case "randomrange":
		return e.randomRange(args[0], args[1])
	}
	r, err := function(n.name, args)
	if err != nil {
		return value{}, fmt.Errorf("%s: %w", n.name, err)
	}
	return value{kind: Number, number: r}, nil
}

// numberArgs evaluates all arguments of n as numbers.
func (e *evaluator) numberArgs(n callNode) ([]decimal.Decimal, error) {
	args := make([]decimal.Decimal, len(n.args))
	for i, arg := range n.args {
		v, err := e.eval(arg)
		if err != nil {
			return nil, err
		}
		if v.kind != Number {
			return nil, fmt.Errorf("%s(%s)", n.name, v.describe())
		}
		args[i] = v.number
	}
	return args, nil
}

// callIf evaluates if(condition, then, else): the condition is a truth
// value or a number, zero for false; the branches are values of one kind.
func (e *evaluator) callIf(n callNode) (value, error) {
	cond, err := e.eval(n.args[0])
	if err != nil {
		return value{}, err
	}
	var ok bool
	switch cond.kind {
	case Bool:
		ok = cond.bool
	case Number:
		ok = !cond.number.IsZero()
	default:
		return value{}, fmt.Errorf("if(%s): the condition", cond.describe())
	}
	then, err := e.eval(n.args[1])
	if err != nil {
		return value{}, err
	}
	els, err := e.eval(n.args[2])
	if err != nil {
		return value{}, err
	}
	if then.kind != els.kind {
		return value{}, fmt.Errorf("if: %s and %s of different kinds", then.describe(), els.describe())
	}
	if ok {
		return then, nil
	}
	return els, nil
}

// callCompare evaluates ifless, ifmore and ifequal: it compares the first
// two numbers and returns the third or the fourth value, which are of one
// kind.
func (e *evaluator) callCompare(n callNode) (value, error) {
	a, err := e.eval(n.args[0])
	if err != nil {
		return value{}, err
	}
	b, err := e.eval(n.args[1])
	if err != nil {
		return value{}, err
	}
	if a.kind != Number || b.kind != Number {
		return value{}, fmt.Errorf("%s: numbers to compare, got %s and %s", n.name, a.describe(), b.describe())
	}
	var ok bool
	switch n.name {
	case "ifless":
		ok = a.number.Cmp(b.number) < 0
	case "ifmore":
		ok = a.number.Cmp(b.number) > 0
	default:
		ok = a.number.Equal(b.number)
	}
	then, err := e.eval(n.args[2])
	if err != nil {
		return value{}, err
	}
	els, err := e.eval(n.args[3])
	if err != nil {
		return value{}, err
	}
	if then.kind != els.kind {
		return value{}, fmt.Errorf("%s: %s and %s of different kinds", n.name, then.describe(), els.describe())
	}
	if ok {
		return then, nil
	}
	return els, nil
}

// random evaluates random(n): a whole number from 1 to n, including n.
func (e *evaluator) random(name string, n decimal.Decimal) (value, error) {
	k, err := whole(name, n)
	if err != nil {
		return value{}, err
	}
	if k < 1 {
		return value{}, fmt.Errorf("%s(%s): n must be at least 1", name, n)
	}
	return value{kind: Number, number: decimal.New(int64(e.rnd(int(k))))}, nil
}

// randomRange evaluates randomrange(a, b): a whole number from a to b,
// including b.
func (e *evaluator) randomRange(a, b decimal.Decimal) (value, error) {
	lo, err := whole("randomrange", a)
	if err != nil {
		return value{}, err
	}
	hi, err := whole("randomrange", b)
	if err != nil {
		return value{}, err
	}
	if hi < lo {
		return value{}, fmt.Errorf("randomrange(%s, %s): b is less than a", a, b)
	}
	return value{kind: Number, number: decimal.New(lo + int64(e.rnd(int(hi-lo+1))-1))}, nil
}

// whole returns the whole number d stands for; numbers with decimal places
// and numbers beyond the whole numbers are an error.
func whole(name string, d decimal.Decimal) (int64, error) {
	if !d.IsWhole() {
		return 0, fmt.Errorf("%s(%s): not a whole number", name, d)
	}
	k, err := d.Int64()
	if err != nil {
		return 0, fmt.Errorf("%s(%s): beyond the whole numbers", name, d)
	}
	return k, nil
}

// function computes a function of B50 and B53 with numbers. round rounds
// half away from zero, truncate toward zero.
func function(name string, args []decimal.Decimal) (decimal.Decimal, error) {
	switch name {
	case "abs":
		return args[0].Abs(), nil
	case "ceil", "ceiling":
		return args[0].Round(0, decimal.RoundCeiling)
	case "floor":
		return args[0].Round(0, decimal.RoundFloor)
	case "truncate":
		return args[0].Round(0, decimal.RoundDown)
	case "round":
		return args[0].Round(0, decimal.RoundHalfAway)
	case "avg":
		sum := decimal.Decimal{}
		for _, a := range args {
			s, err := sum.Add(a)
			if err != nil {
				return decimal.Decimal{}, err
			}
			sum = s
		}
		return sum.Quo(decimal.New(int64(len(args))))
	case "median":
		s := slices.Clone(args)
		slices.SortFunc(s, func(a, b decimal.Decimal) int { return a.Cmp(b) })
		m := len(s) / 2
		if len(s)%2 == 1 {
			return s[m], nil
		}
		mid, err := s[m-1].Add(s[m])
		if err != nil {
			return decimal.Decimal{}, err
		}
		return mid.Quo(decimal.New(2))
	case "min", "max":
		r := args[0]
		for _, a := range args[1:] {
			if c := a.Cmp(r); name == "min" && c < 0 || name == "max" && c > 0 {
				r = a
			}
		}
		return r, nil
	}
	return mathFunction(name, args)
}
