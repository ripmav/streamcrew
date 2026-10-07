// SPDX-License-Identifier: Apache-2.0

package expr

import (
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/ripmav/streamcrew/internal/decimal"
)

// toFloat returns the float64 nearest to d. The absolute value of a
// decimal number is below 10^34, so it always fits a float64; the error
// cannot happen.
func toFloat(d decimal.Decimal) float64 {
	f, err := strconv.ParseFloat(d.String(), 64)
	if err != nil {
		return 0
	}
	return f
}

// fromFloat returns the decimal number of f; NaN and infinity have no
// decimal number, so they are an error wrapping decimal.ErrUndefined.
// The conversion goes through the shortest text that names f, so the
// result is the exact decimal number of the float64, at most 17
// significant digits, well within the limits of the package.
func fromFloat(f float64) (decimal.Decimal, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return decimal.Decimal{}, decimal.ErrUndefined
	}
	return decimal.Parse(strconv.FormatFloat(f, 'g', -1, 64))
}

// mathFunction computes a function that has no exact decimal result,
// through float64 (B53). A value outside the domain of the function or a
// result beyond the float64 range is an error.
func mathFunction(name string, args []decimal.Decimal) (decimal.Decimal, error) {
	x := toFloat(args[0])
	var r float64
	switch name {
	case "sin":
		r = math.Sin(x)
	case "cos":
		r = math.Cos(x)
	case "tan":
		r = math.Tan(x)
	case "asin":
		if x < -1 || x > 1 {
			return decimal.Decimal{}, fmt.Errorf("%s(%s): the value must be between -1 and 1", name, args[0])
		}
		r = math.Asin(x)
	case "acos":
		if x < -1 || x > 1 {
			return decimal.Decimal{}, fmt.Errorf("%s(%s): the value must be between -1 and 1", name, args[0])
		}
		r = math.Acos(x)
	case "atan":
		r = math.Atan(x)
	case "acot":
		r = math.Pi/2 - math.Atan(x)
	case "csc":
		s := math.Sin(x)
		if s == 0 {
			return decimal.Decimal{}, fmt.Errorf("%s(%s): the sine is 0", name, args[0])
		}
		r = 1 / s
	case "sec":
		s := math.Cos(x)
		if s == 0 {
			return decimal.Decimal{}, fmt.Errorf("%s(%s): the cosine is 0", name, args[0])
		}
		r = 1 / s
	case "cot":
		s := math.Sin(x)
		if s == 0 {
			return decimal.Decimal{}, fmt.Errorf("%s(%s): the sine is 0", name, args[0])
		}
		r = math.Cos(x) / s
	case "loge":
		if x <= 0 {
			return decimal.Decimal{}, fmt.Errorf("%s(%s): the value must be above 0", name, args[0])
		}
		r = math.Log(x)
	case "log10":
		if x <= 0 {
			return decimal.Decimal{}, fmt.Errorf("%s(%s): the value must be above 0", name, args[0])
		}
		r = math.Log10(x)
	case "sqrt":
		if x < 0 {
			return decimal.Decimal{}, fmt.Errorf("%s(%s): the value must be above 0", name, args[0])
		}
		r = math.Sqrt(x)
	case "logn":
		base, v := toFloat(args[0]), toFloat(args[1])
		if base <= 0 || base == 1 {
			return decimal.Decimal{}, fmt.Errorf("logn(%s, %s): the base must be above 0 and not 1", args[0], args[1])
		}
		if v <= 0 {
			return decimal.Decimal{}, fmt.Errorf("logn(%s, %s): the value must be above 0", args[0], args[1])
		}
		r = math.Log(v) / math.Log(base)
	default:
		return decimal.Decimal{}, errors.New("unknown function")
	}
	return fromFloat(r)
}
