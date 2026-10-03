// SPDX-License-Identifier: Apache-2.0

package expr

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ripmav/streamcrew/internal/decimal"
)

// tokenKind is the kind of a token of an expression.
type tokenKind int

// The kinds of tokens.
const (
	tokNumber   tokenKind = iota + 1 // a number in the expression
	tokText                          // text in quotes without identifiers
	tokVariable                      // an identifier, or text in quotes with identifiers
	tokName                          // a name: a function, true, false, and, or, not
	tokOperator                      // an operator or a comma
	tokOpen                          // (
	tokClose                         // )
	tokEnd                           // the end of the expression
)

// token is a token of an expression.
type token struct {
	kind tokenKind
	// text is the operator, the name or the text of a literal.
	text   string
	number decimal.Decimal
	// variable is the index of the variable of a tokVariable.
	variable int
}

// operators are the operators of the language, longest first, so that "**"
// wins over "*" and "<=" over "<" (B50).
func operators() []string {
	return []string{"**", "==", "!=", "<=", ">=", "&&", "||", "+", "-", "*", "/", "%", "^", "<", ">", "!", ","}
}

// lex appends the tokens of code, a part of an expression outside quotes
// and identifiers, to tokens.
func lex(code string, tokens []token) ([]token, error) {
	for i := 0; i < len(code); {
		r, size := utf8.DecodeRuneInString(code[i:])
		rest := code[i:]
		switch {
		case unicode.IsSpace(r):
			i += size
		case r == '(':
			tokens = append(tokens, token{kind: tokOpen, text: "("})
			i++
		case r == ')':
			tokens = append(tokens, token{kind: tokClose, text: ")"})
			i++
		case isDigit(r) || r == '.' && len(rest) > 1 && isDigit(rune(rest[1])):
			n := numberLength(rest)
			d, err := decimal.Parse(rest[:n])
			if err != nil {
				return nil, fmt.Errorf("number %q: %w", rest[:n], err)
			}
			tokens = append(tokens, token{kind: tokNumber, text: rest[:n], number: d})
			i += n
		case r == '"' || r == '\'' || r == '`':
			end := closing(code, i)
			if end < 0 {
				return nil, fmt.Errorf("text in quotes at %d does not end", i)
			}
			text, err := unquote(code[i : end+1])
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, token{kind: tokText, text: text})
			i = end + 1
		case unicode.IsLetter(r) || r == '_':
			n := strings.IndexFunc(rest, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' })
			if n < 0 {
				n = len(rest)
			}
			tokens = append(tokens, token{kind: tokName, text: rest[:n]})
			i += n
		default:
			op := ""
			for _, o := range operators() {
				if strings.HasPrefix(rest, o) {
					op = o
					break
				}
			}
			if op == "" {
				return nil, fmt.Errorf("unexpected %q", r)
			}
			tokens = append(tokens, token{kind: tokOperator, text: op})
			i += len(op)
		}
	}
	return tokens, nil
}

// isDigit reports whether r is an ASCII digit.
func isDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

// numberLength returns the length of the number that s starts with: digits
// with an optional point and decimal places, and an exponent if one
// follows, as decimal.Parse reads it.
func numberLength(s string) int {
	n := 0
	for n < len(s) && isDigit(rune(s[n])) {
		n++
	}
	if n < len(s) && s[n] == '.' {
		n++
		for n < len(s) && isDigit(rune(s[n])) {
			n++
		}
	}
	if n < len(s) && (s[n] == 'e' || s[n] == 'E') {
		m := n + 1
		if m < len(s) && (s[m] == '+' || s[m] == '-') {
			m++
		}
		if m < len(s) && isDigit(rune(s[m])) {
			for m < len(s) && isDigit(rune(s[m])) {
				m++
			}
			n = m
		}
	}
	return n
}

// unquote returns the text of a literal in quotes. Back quotes take the
// text as it is; double and single quotes know the escape sequences \\,
// \", \', \n, \r and \t.
func unquote(literal string) (string, error) {
	content := literal[1 : len(literal)-1]
	if literal[0] == '`' {
		return content, nil
	}
	var b strings.Builder
	for i := 0; i < len(content); i++ {
		if content[i] != '\\' {
			b.WriteByte(content[i])
			continue
		}
		i++
		switch content[i] {
		case '\\', '"', '\'':
			b.WriteByte(content[i])
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		default:
			return "", fmt.Errorf("text %s: unknown escape sequence \\%c", literal, content[i])
		}
	}
	return b.String(), nil
}
