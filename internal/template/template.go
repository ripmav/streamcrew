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

import "strings"

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
// by as many ASCII letters, digits and colons as possible (B1); a "$" without
// such characters is text (B3). There is no escaping (B7). Parse accepts
// every text.
func Parse(text string) Template {
	t := Template{src: text}
	literal := 0 // start of the pending literal text
	for i := 0; i < len(text); {
		if text[i] != '$' {
			i++
			continue
		}
		end := i + 1
		for end < len(text) && isTokenByte(text[end]) {
			end++
		}
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

// isTokenByte reports whether c continues a token (B1).
func isTokenByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == ':'
}

// isNameByte reports whether c may appear in the name of an identifier:
// lower-case ASCII letters and digits. Colons only separate numbers in
// patterns.
func isNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
}
