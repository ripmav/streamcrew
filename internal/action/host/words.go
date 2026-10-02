// SPDX-License-Identifier: Apache-2.0

package host

import (
	"errors"
	"strings"
	"unicode"
)

// errOpenQuote is the error of arguments with a quote that is not closed.
var errOpenQuote = errors.New("a double quote is not closed")

// words splits the arguments of an external program into words before any
// identifier is inserted (actions.md B111): white space separates words,
// double quotes join text with white space into one word and are not part
// of it. "" is an empty word. There are no escapes; a word cannot contain a
// double quote. An unclosed quote is an error.
func words(args string) ([]string, error) {
	var list []string
	var b strings.Builder
	inWord, quoted := false, false
	for _, r := range args {
		switch {
		case r == '"':
			quoted, inWord = !quoted, true
		case unicode.IsSpace(r) && !quoted:
			if inWord {
				list = append(list, b.String())
				b.Reset()
				inWord = false
			}
		default:
			b.WriteRune(r)
			inWord = true
		}
	}
	if quoted {
		return nil, errOpenQuote
	}
	if inWord {
		list = append(list, b.String())
	}
	return list, nil
}
