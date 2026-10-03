// SPDX-License-Identifier: Apache-2.0

package template

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
)

// [Interop] The identifier names in this file follow the original (spec
// template.md, purpose and scope) and may be replaced after the legal
// assessment (roadmap Gate O, O.1).

// maxRandom is the largest bound of a random number, 2^53 (template.md,
// change log of 2026-09-30); it stems from the time when expressions
// computed with float64.
const maxRandom = 1 << 53

// RandomFamily returns the random numbers $randomnumber<max> (1 to max) and
// $randomnumber<min>:<max> (both bounds included). Every occurrence draws a
// new number (B21).
func RandomFamily() Family {
	return Family{
		Name: "random",
		Patterns: []Pattern{{
			Name:     "randomnumber<max>",
			Prefixes: []string{"randomnumber"},
			Match:    matchRandomNumber,
			Uncached: true,
		}},
	}
}

// matchRandomNumber matches randomnumber<max> with max ≥ 1 and
// randomnumber<min>:<max> with min ≤ max. Invalid bounds are no match (B6,
// B73).
func matchRandomNumber(token string) (int, Resolver) {
	rest, ok := strings.CutPrefix(token, "randomnumber")
	if !ok {
		return 0, nil
	}
	lo := int64(1)
	hi, digits, ok := number[int64](rest, maxRandom)
	if !ok {
		return 0, nil
	}
	n := len("randomnumber") + digits
	if after, isRange := strings.CutPrefix(rest[digits:], ":"); isRange && after != "" && isDigit(after[0]) {
		upper, upperDigits, ok := number[int64](after, maxRandom)
		if !ok || upper < hi {
			return 0, nil
		}
		lo, hi, n = hi, upper, n+1+upperDigits
	} else if hi < 1 {
		return 0, nil
	}
	return n, func(context.Context, *Scope) (Value, bool, error) {
		v, err := randomBetween(lo, hi)
		if err != nil {
			return Value{}, false, err
		}
		return IntValue(v), true, nil
	}
}

// randomBetween returns a random number from lo to hi, both included. The
// numbers come from crypto/rand; math/rand is flagged by gosec (G404,
// Code-ADR-0004).
func randomBetween(lo, hi int64) (int64, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(hi-lo+1))
	if err != nil {
		return 0, fmt.Errorf("draw random number: %w", err)
	}
	return lo + n.Int64(), nil
}
