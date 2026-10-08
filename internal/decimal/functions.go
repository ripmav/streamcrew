// SPDX-License-Identifier: MIT

package decimal

import (
	"fmt"

	"github.com/cockroachdb/apd/v3"
)

// The functions of expressions that cannot be exact (template.md, B53;
// Code-ADR-0020, point 6). They compute with guard digits and round the
// result to the limits of the package.

// piText and eText are π and e with 125 significant digits, more than the
// reduction of angles needs (reduceDigits).
const (
	piText = "3.141592653589793238462643383279502884197169399375105820974944592307816406286208998628034825342117067982148086513282306647093"
	eText  = "2.718281828459045235360287471352662497757247093699959574966967627724076630353547594571382178525166427427466391932003059921817"
)

// constant returns the exact decimal number text, one of the constants of
// the package.
func constant(text string) *apd.Decimal {
	x, _, err := apd.NewFromString(text)
	if err != nil {
		panic(fmt.Sprintf("decimal: bad constant %q: %v", text, err)) // a programming error
	}
	return x
}

// Pi returns π, rounded to the limits.
func Pi() Decimal {
	d, _ := finish(constant(piText)) // within the limits
	return d
}

// E returns Euler's number e, rounded to the limits.
func E() Decimal {
	d, _ := finish(constant(eText)) // within the limits
	return d
}

// unary computes op(d) in the context of calculations that cannot be exact
// and finishes the result.
func unary(op func(c *apd.Context, r, x *apd.Decimal) (apd.Condition, error), d Decimal) (Decimal, error) {
	var r apd.Decimal
	if err := failed(op(work(workDigits), &r, &d.v)); err != nil {
		return Decimal{}, err
	}
	return finish(&r)
}

// Pow returns d to the power of e. A power of 0 is 1, also 0 to the power
// of 0. 0 to a negative power is an error wrapping ErrDivisionByZero, a
// negative number to a power with decimal places one wrapping ErrUndefined.
func (d Decimal) Pow(e Decimal) (Decimal, error) {
	switch {
	case e.IsZero():
		return New(1), nil
	case d.IsZero() && e.Sign() < 0:
		return Decimal{}, ErrDivisionByZero
	case d.IsZero():
		return Decimal{}, nil
	case d.Sign() < 0 && !e.IsWhole():
		return Decimal{}, fmt.Errorf("%w: %s to the power of %s", ErrUndefined, d, e)
	}
	return binary(work(workDigits), (*apd.Context).Pow, d, e)
}

// Sqrt returns the square root of d; a negative d is an error wrapping
// ErrUndefined.
func (d Decimal) Sqrt() (Decimal, error) {
	if d.Sign() < 0 {
		return Decimal{}, fmt.Errorf("%w: square root of %s", ErrUndefined, d)
	}
	return unary((*apd.Context).Sqrt, d)
}

// Exp returns e to the power of d.
func (d Decimal) Exp() (Decimal, error) {
	return unary((*apd.Context).Exp, d)
}

// Ln returns the natural logarithm of d; d ≤ 0 is an error wrapping
// ErrUndefined.
func (d Decimal) Ln() (Decimal, error) {
	if d.Sign() <= 0 {
		return Decimal{}, fmt.Errorf("%w: logarithm of %s", ErrUndefined, d)
	}
	return unary((*apd.Context).Ln, d)
}

// Log10 returns the logarithm of d to the base 10; d ≤ 0 is an error
// wrapping ErrUndefined.
func (d Decimal) Log10() (Decimal, error) {
	if d.Sign() <= 0 {
		return Decimal{}, fmt.Errorf("%w: logarithm of %s", ErrUndefined, d)
	}
	return unary((*apd.Context).Log10, d)
}

// Log returns the logarithm of d to base. d ≤ 0, base ≤ 0 and base 1 are
// errors wrapping ErrUndefined.
func (d Decimal) Log(base Decimal) (Decimal, error) {
	if d.Sign() <= 0 || base.Sign() <= 0 || base.Equal(New(1)) {
		return Decimal{}, fmt.Errorf("%w: logarithm of %s to the base %s", ErrUndefined, d, base)
	}
	c := work(workDigits)
	var ln, lnBase, r apd.Decimal
	if err := failed(c.Ln(&ln, &d.v)); err != nil {
		return Decimal{}, err
	}
	if err := failed(c.Ln(&lnBase, &base.v)); err != nil {
		return Decimal{}, err
	}
	if err := failed(c.Quo(&r, &ln, &lnBase)); err != nil {
		return Decimal{}, err
	}
	return finish(&r)
}

// trigDigits is the precision of the series of the trigonometric
// functions. Their results are at most 1 in magnitude, or come from such
// results, so an absolute error far below the last of the Places decimal
// places keeps the final rounding right.
const trigDigits = Places + 26

// reduceDigits is the precision of reducing an angle by multiples of 2π:
// the multiple has up to Magnitude digits, and the rest keeps trigDigits
// more.
const reduceDigits = Magnitude + trigDigits + 10

// maxTerms bounds the terms of a series; the series of the package need far
// fewer for their reduced arguments.
const maxTerms = 500

// calc runs the steps of a calculation in c and stops at the first error.
type calc struct {
	c   *apd.Context
	err error
}

func (k *calc) do(op func(c *apd.Context, r, x, y *apd.Decimal) (apd.Condition, error), x, y *apd.Decimal) *apd.Decimal {
	r := new(apd.Decimal)
	if k.err == nil {
		k.err = failed(op(k.c, r, x, y))
	}
	return r
}

func (k *calc) add(x, y *apd.Decimal) *apd.Decimal { return k.do((*apd.Context).Add, x, y) }
func (k *calc) sub(x, y *apd.Decimal) *apd.Decimal { return k.do((*apd.Context).Sub, x, y) }
func (k *calc) mul(x, y *apd.Decimal) *apd.Decimal { return k.do((*apd.Context).Mul, x, y) }
func (k *calc) quo(x, y *apd.Decimal) *apd.Decimal { return k.do((*apd.Context).Quo, x, y) }

func (k *calc) sqrt(x *apd.Decimal) *apd.Decimal {
	r := new(apd.Decimal)
	if k.err == nil {
		k.err = failed(k.c.Sqrt(r, x))
	}
	return r
}

// series sums the terms that next yields after first until a term is
// below 10^-(trigDigits+2) in absolute value; k computes with trigDigits.
func (k *calc) series(first *apd.Decimal, next func(n int64, term *apd.Decimal) *apd.Decimal) *apd.Decimal {
	limit := apd.New(1, -trigDigits-2)
	sum, term := first, first
	for n := int64(1); k.err == nil; n++ {
		if n > maxTerms {
			k.err = fmt.Errorf("%w: series does not converge", ErrUndefined)
			break
		}
		term = next(n, term)
		sum = k.add(sum, term)
		var abs apd.Decimal
		if abs.Abs(term).Cmp(limit) < 0 {
			break
		}
	}
	return sum
}

// reduce returns the angle x reduced by a multiple of 2π to about [-π, π].
func reduce(x *apd.Decimal) (*apd.Decimal, error) {
	k := &calc{c: work(reduceDigits)}
	twoPi := k.mul(constant(piText), apd.New(2, 0))
	turns := k.quo(x, twoPi)
	if k.err == nil {
		k.err = failed(k.c.RoundToIntegralValue(turns, turns))
	}
	r := k.sub(x, k.mul(turns, twoPi))
	return r, k.err
}

// sinCos returns the sine of d, or its cosine if cos is set, by their
// Taylor series after reducing the angle.
func sinCos(d Decimal, cos bool) (*apd.Decimal, error) {
	r, err := reduce(&d.v)
	if err != nil {
		return nil, err
	}
	k := &calc{c: work(trigDigits)}
	minusSquare := k.mul(r, r)
	minusSquare.Negative = !minusSquare.IsZero()
	first, offset := r, int64(0) // sin: r - r³/3! + …
	if cos {
		first, offset = apd.New(1, 0), -1 // cos: 1 - r²/2! + …
	}
	sum := k.series(first, func(n int64, term *apd.Decimal) *apd.Decimal {
		// The next term is the last times -r² / ((2n + offset)(2n + 1 + offset)).
		return k.quo(k.mul(term, minusSquare), apd.New((2*n+offset)*(2*n+1+offset), 0))
	})
	return sum, k.err
}

// Sin returns the sine of the angle d in radians.
func (d Decimal) Sin() (Decimal, error) {
	s, err := sinCos(d, false)
	if err != nil {
		return Decimal{}, err
	}
	return finish(s)
}

// Cos returns the cosine of the angle d in radians.
func (d Decimal) Cos() (Decimal, error) {
	c, err := sinCos(d, true)
	if err != nil {
		return Decimal{}, err
	}
	return finish(c)
}

// Tan returns the tangent of the angle d in radians. An angle whose cosine
// is 0 is an error wrapping ErrUndefined.
func (d Decimal) Tan() (Decimal, error) {
	s, err := sinCos(d, false)
	if err != nil {
		return Decimal{}, err
	}
	c, err := sinCos(d, true)
	if err != nil {
		return Decimal{}, err
	}
	if c.IsZero() {
		return Decimal{}, fmt.Errorf("%w: tangent of %s", ErrUndefined, d)
	}
	k := &calc{c: work(trigDigits)}
	t := k.quo(s, c)
	if k.err != nil {
		return Decimal{}, k.err
	}
	return finish(t)
}

// atan returns the arctangent of x ≥ 0 in k by halving the angle until
// x ≤ 0.1 and the Taylor series.
func atan(k *calc, x *apd.Decimal) *apd.Decimal {
	one := apd.New(1, 0)
	tenth := apd.New(1, -1)
	halvings := int64(0)
	for k.err == nil && x.Cmp(tenth) > 0 {
		// atan(x) = 2 atan(x / (1 + √(1 + x²)))
		x = k.quo(x, k.add(one, k.sqrt(k.add(one, k.mul(x, x)))))
		halvings++
	}
	minusSquare := k.mul(x, x)
	minusSquare.Negative = !minusSquare.IsZero()
	power := x
	sum := k.series(x, func(n int64, _ *apd.Decimal) *apd.Decimal {
		// The terms are (-1)^n x^(2n+1) / (2n + 1).
		power = k.mul(power, minusSquare)
		return k.quo(power, apd.New(2*n+1, 0))
	})
	return k.mul(sum, apd.New(1<<halvings, 0))
}

// Atan returns the arctangent of d in radians, from -π/2 to π/2.
func (d Decimal) Atan() (Decimal, error) {
	k := &calc{c: work(trigDigits)}
	var abs apd.Decimal
	abs.Abs(&d.v)
	r := atan(k, &abs)
	if k.err != nil {
		return Decimal{}, k.err
	}
	r.Negative = d.Sign() < 0 && !r.IsZero()
	return finish(r)
}

// asin returns the arcsine of d in k; |d| ≤ 1.
func asin(k *calc, d Decimal) *apd.Decimal {
	one := apd.New(1, 0)
	var abs apd.Decimal
	abs.Abs(&d.v)
	var r *apd.Decimal
	if abs.Cmp(one) == 0 {
		r = k.quo(constant(piText), apd.New(2, 0))
	} else {
		// asin(x) = atan(x / √(1 - x²))
		r = atan(k, k.quo(&abs, k.sqrt(k.sub(one, k.mul(&abs, &abs)))))
	}
	r.Negative = d.Sign() < 0 && !r.IsZero()
	return r
}

// Asin returns the arcsine of d in radians, from -π/2 to π/2. |d| > 1 is an
// error wrapping ErrUndefined.
func (d Decimal) Asin() (Decimal, error) {
	if d.Abs().Cmp(New(1)) > 0 {
		return Decimal{}, fmt.Errorf("%w: arcsine of %s", ErrUndefined, d)
	}
	k := &calc{c: work(trigDigits)}
	r := asin(k, d)
	if k.err != nil {
		return Decimal{}, k.err
	}
	return finish(r)
}

// Acos returns the arccosine of d in radians, from 0 to π. |d| > 1 is an
// error wrapping ErrUndefined.
func (d Decimal) Acos() (Decimal, error) {
	if d.Abs().Cmp(New(1)) > 0 {
		return Decimal{}, fmt.Errorf("%w: arccosine of %s", ErrUndefined, d)
	}
	k := &calc{c: work(trigDigits)}
	// acos(x) = π/2 - asin(x)
	r := k.sub(k.quo(constant(piText), apd.New(2, 0)), asin(k, d))
	if k.err != nil {
		return Decimal{}, k.err
	}
	return finish(r)
}
