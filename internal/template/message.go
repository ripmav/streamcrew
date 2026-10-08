// SPDX-License-Identifier: MIT

package template

import (
	"context"
	"slices"
	"strings"
)

// [Interop] The identifier names in this file follow the original (spec
// template.md, purpose and scope) and may be replaced after the legal
// assessment (roadmap Gate O, O.1).

// MessageFamily returns the identifiers of the triggering chat message:
// $message (spec commands.md, B15), $messagenoemotes and $messageemotecount.
// Without a message they have no value (B4). An event can set its own
// $message as a value of the run (spec events.md, B7), which ranks first
// (B10).
func MessageFamily() Family {
	return Family{
		Name: "message",
		Identifiers: []Identifier{
			{Name: "message", Resolve: withMessage(func(s *Scope) Value {
				return TextValue(s.Message)
			})},
			{Name: "messagenoemotes", Resolve: withMessage(func(s *Scope) Value {
				words := strings.Fields(s.Message)
				words = slices.DeleteFunc(words, func(w string) bool { return slices.Contains(s.Emotes, w) })
				return TextValue(strings.Join(words, " "))
			})},
			{Name: "messageemotecount", Resolve: withMessage(func(s *Scope) Value {
				return IntValue(int64(len(s.Emotes)))
			})},
		},
	}
}

// withMessage returns a resolver that has a value only if the run has a
// message.
func withMessage(value func(s *Scope) Value) Resolver {
	return func(_ context.Context, s *Scope) (Value, bool, error) {
		if s.Message == "" {
			return Value{}, false, nil
		}
		return value(s), true, nil
	}
}
