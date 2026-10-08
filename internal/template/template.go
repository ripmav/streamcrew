// SPDX-License-Identifier: MIT

// Package template renders texts with $ identifiers (spec template.md,
// Code-ADR-0012): chat messages of actions, overlays, web requests and files.
//
// Parse splits a text once into literal text and tokens. An Engine renders
// the parsed Template with the values of a Scope: for each token it finds the
// longest known identifier across all sources, resolves it at most once per
// render and inserts its value, encoded for the place the text goes to.
// Inserted values are never read again, so text from viewers cannot trigger
// identifiers (B5).
//
// The built-in identifiers come in families, such as CharacterFamily, which
// the composition root assembles into a Registry (Code-ADR-0002). Their names
// follow the original for compatibility and are subject to the interop
// reservation of the spec; each family keeps its names in one file marked
// [Interop].
package template

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Template is a parsed text. The zero value is the empty text. A Template is
// immutable and may be rendered concurrently; actions parse their texts when
// a command is loaded, not on every run.
type Template struct {
	src    string
	pieces []piece
}

// piece is literal text or a token.
type piece struct {
	// text is the literal text, or the token as written, without "$".
	text string
	// name is the token in lower case; empty for literal text.
	name string
}

// Parse splits text into literal text and tokens. A token is a "$" followed
// by as many letters, digits and colons as possible, in the sense of
// Unicode (B1); a "$" without such characters is text (B3). There is no
// escaping (B7). Parse accepts every text.
func Parse(text string) Template {
	t := Template{src: text}
	literal := 0 // start of the pending literal text
	for i := 0; i < len(text); {
		if text[i] != '$' {
			i++
			continue
		}
		end := TokenEnd(text, i)
		if end == i+1 {
			i = end
			continue
		}
		if literal < i {
			t.pieces = append(t.pieces, piece{text: text[literal:i]})
		}
		token := text[i+1 : end]
		t.pieces = append(t.pieces, piece{text: token, name: strings.ToLower(token)})
		literal, i = end, end
	}
	if literal < len(text) {
		t.pieces = append(t.pieces, piece{text: text[literal:]})
	}
	return t
}

// String returns the text t was parsed from.
func (t Template) String() string {
	return t.src
}

// rest returns the token as written after the first n bytes of its name.
// The name is the token in lower case, rune by rune, so both have the same
// runes, but a rune may change its length, e.g. "ẞ" has three bytes and
// "ß" two.
func (p piece) rest(n int) string {
	i := 0
	for range utf8.RuneCountInString(p.name[:n]) {
		_, size := utf8.DecodeRuneInString(p.text[i:])
		i += size
	}
	return p.text[i:]
}

// TokenEnd returns the end of the token whose "$" is at text[i]: the index
// after as many letters, digits and colons as possible (B1), or i + 1 if
// none follow. Other packages that find tokens, e.g. internal/textfunc, use
// it.
func TokenEnd(text string, i int) int {
	end := i + 1
	for end < len(text) {
		r, size := utf8.DecodeRuneInString(text[end:])
		if !isTokenRune(r) {
			break
		}
		end += size
	}
	return end
}

// isTokenRune reports whether r continues a token (B1): a letter, a mark
// that belongs to a letter, such as an accent or a vowel sign of Devanagari,
// a digit or a colon, all in the sense of Unicode.
func isTokenRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsDigit(r) || r == ':'
}

// isNameByte reports whether c may appear in the name of an identifier:
// lower-case ASCII letters and digits. Colons only separate numbers in
// patterns.
func isNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
}
