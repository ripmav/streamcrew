// SPDX-License-Identifier: MIT

package template

// number reads the decimal number at the beginning of s for a pattern and
// returns it with the count of its digits. ok is false if s does not start
// with a digit or the number exceeds limit; limit must be below a tenth of
// the largest value of T.
func number[T ~int | ~int32 | ~int64](s string, limit T) (n T, digits int, ok bool) {
	for digits < len(s) && isDigit(s[digits]) {
		n = n*10 + T(s[digits]-'0')
		if n > limit {
			return 0, 0, false
		}
		digits++
	}
	return n, digits, digits > 0
}

// isDigit reports whether c is an ASCII digit.
func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}
