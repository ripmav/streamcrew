// SPDX-License-Identifier: Apache-2.0

package template

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// [Interop] The identifier names in this file follow the original (spec
// template.md, purpose and scope) and may be replaced after the legal
// assessment (roadmap Gate O, O.1).

// CharacterFamily returns the identifiers that insert single characters:
// $linebreak (B32) and $unicode<n> (B33).
func CharacterFamily() Family {
	return Family{
		Name: "character",
		Identifiers: []Identifier{
			// How a platform shows a line break is up to its adapter (B32).
			{Name: "linebreak", Resolve: constant(TextValue("\n"))},
		},
		Patterns: []Pattern{
			{Name: "unicode<n>", Prefixes: []string{"unicode"}, Match: matchUnicode},
		},
	}
}

// matchUnicode matches unicode<n> with the decimal number n of a character
// (B33). Invalid numbers and control characters other than the line break
// are no match (B6, B73).
func matchUnicode(token string) (int, Resolver) {
	rest, ok := strings.CutPrefix(token, "unicode")
	if !ok {
		return 0, nil
	}
	r, digits, ok := number[rune](rest, utf8.MaxRune)
	if !ok || !utf8.ValidRune(r) || unicode.IsControl(r) && r != '\n' {
		return 0, nil
	}
	return len("unicode") + digits, constant(TextValue(string(r)))
}

// number reads the decimal number at the beginning of s for a pattern and
// returns it with the count of its digits. ok is false if s does not start
// with a digit or the number exceeds limit; limit must be below a tenth of
// the largest value of T.
func number[T ~int | ~int32](s string, limit T) (n T, digits int, ok bool) {
	for digits < len(s) && s[digits] >= '0' && s[digits] <= '9' {
		n = n*10 + T(s[digits]-'0')
		if n > limit {
			return 0, 0, false
		}
		digits++
	}
	return n, digits, digits > 0
}
