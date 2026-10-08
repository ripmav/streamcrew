// SPDX-License-Identifier: MIT

// Package decimal is the number of streamcrew (Code-ADR-0020): an exact
// decimal number with at most 34 significant digits and 34 decimal places,
// below 10^34 in magnitude. Expressions, the numbers of templates, the
// amounts of actions and counters all compute with it.
//
// A calculation is exact if its result fits these limits: 0.1 + 0.2 is
// 0.3. Otherwise the result is rounded to them, half to even, as 10 / 3 is.
// A result of 10^34 or more, a division by zero and a calculation without
// a result, such as the square root of a negative number, are errors; there
// is no NaN and no infinity.
//
// The package wraps github.com/cockroachdb/apd/v3; its types appear in no
// interface of other packages.
package decimal

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/cockroachdb/apd/v3"
)

// The limits of a Decimal (Code-ADR-0020, point 4).
const (
	// Digits is the most significant digits of a Decimal.
	Digits = 34
	// Places is the most decimal places of a Decimal.
	Places = 34
	// Magnitude is the power of ten that the absolute value of a Decimal
	// stays below.
	Magnitude = 34
)

// Errors of calculations and of reading numbers.
var (
	// ErrSyntax is wrapped by the error of reading text that is no decimal
	// number.
	ErrSyntax = errors.New("not a decimal number")
	// ErrRange is wrapped by the error of a result whose absolute value is
	// 10^Magnitude or more.
	ErrRange = errors.New("decimal number out of range")
	// ErrDivisionByZero is wrapped by the error of a division by zero.
	ErrDivisionByZero = errors.New("division by zero")
	// ErrUndefined is wrapped by the error of a calculation without a
	// result, such as the square root of a negative number.
	ErrUndefined = errors.New("result undefined")
	// ErrNotWhole is wrapped by the error of converting a number with
	// decimal places to a whole number.
	ErrNotWhole = errors.New("not a whole number")
)

// Decimal is a number within the limits of the package. The zero value is
// 0. Methods never change a Decimal, so copies are safe; compare with Cmp
// or Equal, not with ==.
type Decimal struct {
	// v is finite, without trailing zeros and within the limits; it is
	// never changed after the Decimal was made.
	v apd.Decimal
}

// maxExponent bounds the exponents of intermediate results, so that a
// calculation far beyond the limits fails at once instead of computing
// with huge exponents, which apd does slowly.
const maxExponent = 1000

// exact returns the context of calculations that are always exact: their
// results have at most twice the digits of their operands.
func exact() *apd.Context {
	return &apd.Context{
		MaxExponent: maxExponent,
		MinExponent: -maxExponent,
		Traps:       traps,
		Rounding:    apd.RoundHalfEven,
	}
}

// work returns the context of calculations that cannot always be exact,
// such as a division, with precision significant digits; finish rounds
// their results to the limits.
func work(precision uint32) *apd.Context {
	c := exact()
	c.Precision = precision
	return c
}

// workDigits is the precision of the intermediate results of calculations
// that cannot always be exact. The guard digits beyond Digits keep the error
// of the final rounding below one unit in the last place.
const workDigits = Digits + 20

// traps are the conditions that make a calculation fail. Results too small
// for the exponents do not fail: finish rounds them to 0.
const traps = apd.SystemOverflow | apd.Overflow | apd.DivisionUndefined | apd.DivisionByZero |
	apd.DivisionImpossible | apd.InvalidOperation

// failed maps the error of an apd calculation to the errors of the package.
func failed(c apd.Condition, err error) error {
	if err == nil {
		return nil
	}
	switch {
	case c.DivisionByZero():
		return ErrDivisionByZero
	case c.Overflow(), c.SystemOverflow():
		return ErrRange
	default:
		return fmt.Errorf("%w: %w", ErrUndefined, err)
	}
}

// adjusted returns the exponent of the most significant digit of x, which
// is not zero.
func adjusted(x *apd.Decimal) int64 {
	return int64(x.Exponent) + x.NumDigits() - 1
}

// toInt32 returns v as an int32; ok is false if it does not fit.
func toInt32(v int64) (int32, bool) {
	if v < math.MinInt32 || v > math.MaxInt32 {
		return 0, false
	}
	return int32(v), true
}

// finish makes the result x of a calculation a Decimal (Code-ADR-0020,
// point 5): rounded to Digits significant digits and at most Places
// decimal places, half to even, in one step, without trailing zeros and
// without a negative zero.
func finish(x *apd.Decimal) (Decimal, error) {
	switch x.Form {
	case apd.Finite:
	case apd.Infinite:
		return Decimal{}, ErrRange
	default:
		return Decimal{}, ErrUndefined
	}
	if x.IsZero() {
		return Decimal{}, nil
	}
	if adjusted(x) >= Magnitude {
		return Decimal{}, ErrRange
	}
	r := new(apd.Decimal).Set(x)
	// The adjusted exponent is below Magnitude here, so the target is
	// from -Places to 0.
	target, ok := toInt32(max(adjusted(x)-(Digits-1), -Places))
	if !ok {
		return Decimal{}, ErrRange
	}
	if x.Exponent < target {
		c := work(Digits + 1) // one more for a carry, e.g. from 9.99… to 10.0…
		if _, err := c.Quantize(r, x, target); err != nil {
			return Decimal{}, fmt.Errorf("round %s: %w", x.Text('g'), err)
		}
	}
	if r.IsZero() {
		return Decimal{}, nil
	}
	if adjusted(r) >= Magnitude {
		return Decimal{}, ErrRange
	}
	var d Decimal
	d.v.Reduce(r)
	return d, nil
}

// New returns the whole number n.
func New(n int64) Decimal {
	var d Decimal
	d.v.Reduce(apd.New(n, 0))
	return d
}

// Parse reads a decimal number: an optional sign, digits with an optional
// decimal point, and an optional exponent, as numbers in expressions
// (template.md, B51), e.g. "2.5", "-.5", "1e3". Text with more digits than
// a Decimal has is rounded like the result of a calculation; an absolute
// value of 10^34 or more is an error wrapping ErrRange.
func Parse(s string) (Decimal, error) {
	if !syntax(s) {
		return Decimal{}, fmt.Errorf("%w: %q", ErrSyntax, s)
	}
	var x apd.Decimal
	if _, _, err := x.SetString(s); err != nil {
		// The syntax is valid, so only the exponent can be too far from 0
		// for apd: a negative one makes the number round to 0, a positive
		// one leaves the range.
		if _, exponent, _ := strings.Cut(strings.ToLower(s), "e"); strings.HasPrefix(exponent, "-") {
			return Decimal{}, nil
		}
		return Decimal{}, fmt.Errorf("%w: %q", ErrRange, s)
	}
	d, err := finish(&x)
	if err != nil {
		return Decimal{}, fmt.Errorf("read %q: %w", s, err)
	}
	return d, nil
}

// syntax reports whether s is a decimal number by the rules of Parse.
func syntax(s string) bool {
	s = strings.ToLower(s)
	if strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-") {
		s = s[1:]
	}
	mantissa, exponent, hasExponent := strings.Cut(s, "e")
	whole, fraction, _ := strings.Cut(mantissa, ".")
	if whole+fraction == "" || !digits(whole) || !digits(fraction) {
		return false
	}
	if !hasExponent {
		return true
	}
	if strings.HasPrefix(exponent, "+") || strings.HasPrefix(exponent, "-") {
		exponent = exponent[1:]
	}
	return exponent != "" && digits(exponent)
}

// digits reports whether s consists of ASCII digits only; the empty text
// does.
func digits(s string) bool {
	return !strings.ContainsFunc(s, func(r rune) bool { return r < '0' || r > '9' })
}

// String returns the canonical form (Code-ADR-0020, point 7): no exponent,
// a point as the decimal separator, no trailing zeros and no negative zero,
// e.g. "2.5", "-0.3" or "100".
func (d Decimal) String() string {
	return d.v.Text('f')
}

// MarshalText implements encoding.TextMarshaler with the canonical form;
// JSON holds a Decimal as text (Code-ADR-0020, point 9).
func (d Decimal) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler with Parse.
func (d *Decimal) UnmarshalText(text []byte) error {
	v, err := Parse(string(text))
	if err != nil {
		return err
	}
	*d = v
	return nil
}

// Cmp compares d and e: -1 if d < e, 0 if they are equal, +1 if d > e.
func (d Decimal) Cmp(e Decimal) int {
	return d.v.Cmp(&e.v)
}

// Equal reports whether d and e are the same number.
func (d Decimal) Equal(e Decimal) bool {
	return d.Cmp(e) == 0
}

// Sign returns -1, 0 or +1 for a negative number, zero or a positive one.
func (d Decimal) Sign() int {
	return d.v.Sign()
}

// IsZero reports whether d is 0.
func (d Decimal) IsZero() bool {
	return d.v.IsZero()
}

// IsWhole reports whether d has no decimal places.
func (d Decimal) IsWhole() bool {
	return d.v.Exponent >= 0 || d.IsZero()
}

// Int64 returns d as a whole number. A number with decimal places is an
// error wrapping ErrNotWhole, one beyond int64 an error wrapping ErrRange.
func (d Decimal) Int64() (int64, error) {
	if !d.IsWhole() {
		return 0, fmt.Errorf("%w: %s", ErrNotWhole, d)
	}
	n, err := d.v.Int64()
	if err != nil {
		return 0, fmt.Errorf("%w: %s", ErrRange, d)
	}
	return n, nil
}

// Neg returns -d.
func (d Decimal) Neg() Decimal {
	var r Decimal
	if !d.IsZero() {
		r.v.Neg(&d.v)
	}
	return r
}

// Abs returns the absolute value of d.
func (d Decimal) Abs() Decimal {
	var r Decimal
	r.v.Abs(&d.v)
	return r
}

// binary computes op(d, e) in c and finishes the result.
func binary(c *apd.Context, op func(c *apd.Context, r, x, y *apd.Decimal) (apd.Condition, error), d, e Decimal) (Decimal, error) {
	var r apd.Decimal
	if err := failed(op(c, &r, &d.v, &e.v)); err != nil {
		return Decimal{}, err
	}
	return finish(&r)
}

// Add returns d + e, exact if it fits the limits (Code-ADR-0020, point 5).
func (d Decimal) Add(e Decimal) (Decimal, error) {
	return binary(exact(), (*apd.Context).Add, d, e)
}

// Sub returns d - e, exact if it fits the limits.
func (d Decimal) Sub(e Decimal) (Decimal, error) {
	return binary(exact(), (*apd.Context).Sub, d, e)
}

// Mul returns d × e, exact if it fits the limits.
func (d Decimal) Mul(e Decimal) (Decimal, error) {
	return binary(exact(), (*apd.Context).Mul, d, e)
}

// Quo returns d / e, rounded to the limits; e = 0 is an error wrapping
// ErrDivisionByZero.
func (d Decimal) Quo(e Decimal) (Decimal, error) {
	if e.IsZero() {
		return Decimal{}, ErrDivisionByZero
	}
	return binary(work(workDigits), (*apd.Context).Quo, d, e)
}

// Rem returns the remainder of d / e with the sign of d, as math.Mod does:
// d - e × trunc(d / e). It is exact; e = 0 is an error wrapping
// ErrDivisionByZero.
func (d Decimal) Rem(e Decimal) (Decimal, error) {
	if e.IsZero() {
		return Decimal{}, ErrDivisionByZero
	}
	// The integer part of the quotient has at most Magnitude + Places
	// digits.
	return binary(work(Magnitude+Places+1), (*apd.Context).Rem, d, e)
}

// Rounding says how Round rounds.
type Rounding string

// The roundings of Round.
const (
	// RoundHalfEven rounds to the nearest number, a half to the even digit.
	RoundHalfEven Rounding = "half_even"
	// RoundHalfAway rounds to the nearest number, a half away from zero,
	// as in commerce.
	RoundHalfAway Rounding = "half_away"
	// RoundDown rounds toward zero; it truncates.
	RoundDown Rounding = "down"
	// RoundFloor rounds toward negative infinity.
	RoundFloor Rounding = "floor"
	// RoundCeiling rounds toward positive infinity.
	RoundCeiling Rounding = "ceiling"
)

// rounder returns the rounding of apd for m.
func (m Rounding) rounder() (apd.Rounder, bool) {
	switch m {
	case RoundHalfEven:
		return apd.RoundHalfEven, true
	case RoundHalfAway:
		// The half_up of the General Decimal Arithmetic rounds the
		// magnitude, so a half goes away from zero.
		return apd.RoundHalfUp, true
	case RoundDown:
		return apd.RoundDown, true
	case RoundFloor:
		return apd.RoundFloor, true
	case RoundCeiling:
		return apd.RoundCeiling, true
	default:
		return "", false
	}
}

// Round rounds d to places decimal places, from -Magnitude to Places, with
// m; negative places round to tens, hundreds and so on. A carry beyond the
// limits is an error wrapping ErrRange.
func (d Decimal) Round(places int, m Rounding) (Decimal, error) {
	r, err := d.quantize(places, m)
	if err != nil {
		return Decimal{}, err
	}
	return finish(r)
}

// quantize returns d rounded to places decimal places with m, with all of
// them, also trailing zeros.
func (d Decimal) quantize(places int, m Rounding) (*apd.Decimal, error) {
	rounder, ok := m.rounder()
	switch {
	case !ok:
		return nil, fmt.Errorf("round %s: unknown rounding %q", d, m)
	case places < -Magnitude || places > Places:
		return nil, fmt.Errorf("round %s to %d places: want %d to %d", d, places, -Magnitude, Places)
	}
	c := work(Magnitude + Places + 1)
	c.Rounding = rounder
	var r apd.Decimal
	if err := failed(c.Quantize(&r, &d.v, int32(-places))); err != nil {
		return nil, fmt.Errorf("round %s: %w", d, err)
	}
	return &r, nil
}

// Fixed returns d with exactly places decimal places, from 0 to Places,
// rounded half away from zero, e.g. "1234.50" for 1234.5 and two places
// (counters-and-quotes.md, B4; Code-ADR-0020, point 10). A rounded zero has
// no sign.
func (d Decimal) Fixed(places int) (string, error) {
	if places < 0 {
		return "", fmt.Errorf("fixed %s: %d places: want at least 0", d, places)
	}
	r, err := d.quantize(places, RoundHalfAway)
	if err != nil {
		return "", err
	}
	if r.IsZero() {
		r.Negative = false
	}
	return r.Text('f'), nil
}
