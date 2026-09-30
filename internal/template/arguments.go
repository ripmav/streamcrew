// SPDX-License-Identifier: Apache-2.0

package template

import (
	"context"
	"strings"
)

// [Interop] The identifier names in this file follow the original (spec
// template.md, purpose and scope) and may be replaced after the legal
// assessment (roadmap Gate O, O.1).

// maxArg is the highest argument number a pattern accepts.
const maxArg = 9999

// ArgumentFamily returns the identifiers of the arguments of a run: $allargs,
// $argcount, $arg<n>text, $arg<n>:<m>text (arguments n to m),
// $argdelimited<n>text and $argdelimitedcount. Numbers start at 1; an
// argument that does not exist has no value (B4).
func ArgumentFamily() Family {
	return Family{
		Name: "argument",
		Identifiers: []Identifier{
			{Name: "allargs", Resolve: allArgs},
			{Name: "argcount", Resolve: func(_ context.Context, s *Scope) (Value, bool, error) {
				return IntValue(int64(len(s.Args))), true, nil
			}},
			{Name: "argdelimitedcount", Resolve: func(_ context.Context, s *Scope) (Value, bool, error) {
				return IntValue(int64(len(s.delimitedArgs()))), true, nil
			}},
		},
		Patterns: []Pattern{
			{Name: "arg<n>text", Prefixes: []string{"arg"}, Match: matchArgText},
			{Name: "argdelimited<n>text", Prefixes: []string{"argdelimited"}, Match: matchArgDelimited},
		},
	}
}

// allArgs resolves $allargs: the text after the trigger, empty without
// arguments.
func allArgs(_ context.Context, s *Scope) (Value, bool, error) {
	return TextValue(s.argsText()), true, nil
}

// matchArgText matches arg<n>text and arg<n>:<m>text with 1 ≤ n ≤ m (B6).
// A range beyond the last argument ends at the last argument.
func matchArgText(token string) (int, Resolver) {
	rest, ok := strings.CutPrefix(token, "arg")
	if !ok {
		return 0, nil
	}
	first, digits, ok := number(rest, maxArg)
	if !ok || first < 1 {
		return 0, nil
	}
	last, n := first, len("arg")+digits
	if after, isRange := strings.CutPrefix(rest[digits:], ":"); isRange {
		m, mDigits, ok := number(after, maxArg)
		if !ok || m < first {
			return 0, nil
		}
		last, n = m, n+1+mDigits
	}
	if !strings.HasPrefix(token[n:], "text") {
		return 0, nil
	}
	return n + len("text"), func(_ context.Context, s *Scope) (Value, bool, error) {
		if first > len(s.Args) {
			return Value{}, false, nil
		}
		return TextValue(strings.Join(s.Args[first-1:min(last, len(s.Args))], " ")), true, nil
	}
}

// matchArgDelimited matches argdelimited<n>text with n ≥ 1 (B6).
func matchArgDelimited(token string) (int, Resolver) {
	rest, ok := strings.CutPrefix(token, "argdelimited")
	if !ok {
		return 0, nil
	}
	i, digits, ok := number(rest, maxArg)
	if !ok || i < 1 || !strings.HasPrefix(rest[digits:], "text") {
		return 0, nil
	}
	return len("argdelimited") + digits + len("text"), func(_ context.Context, s *Scope) (Value, bool, error) {
		parts := s.delimitedArgs()
		if i > len(parts) {
			return Value{}, false, nil
		}
		return TextValue(parts[i-1]), true, nil
	}
}

// argsText returns the text after the trigger.
func (s *Scope) argsText() string {
	return s.ArgsText
}

// delimitedArgs splits the text after the trigger at the delimiter and
// trims white space around the parts; no text has no parts.
func (s *Scope) delimitedArgs() []string {
	text := strings.TrimSpace(s.argsText())
	if text == "" {
		return nil
	}
	parts := strings.Split(text, s.ArgDelimiter)
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return parts
}
