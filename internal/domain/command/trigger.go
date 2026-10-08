// SPDX-License-Identifier: MIT

package command

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// TriggerMode says how a chat message names the triggers of a chat command
// (B11, B13). The editor shows it as the switch "prefix with !" and the
// option "wildcard"; a wildcard trigger never has the prefix.
type TriggerMode string

// Trigger modes.
const (
	// TriggerExclamation: the message starts with "!" and the trigger,
	// which is stored without the "!" (B11). New chat commands start with
	// it.
	TriggerExclamation TriggerMode = "exclamation"
	// TriggerLiteral: the message starts with the trigger as it is stored,
	// without a prefix or with one of its own, e.g. "?hallo" (B11).
	TriggerLiteral TriggerMode = "literal"
	// TriggerWildcard: the trigger occurs anywhere in the message as whole
	// words, regardless of case and without "!" (B13).
	TriggerWildcard TriggerMode = "wildcard"
)

// Valid reports whether m is a known trigger mode.
func (m TriggerMode) Valid() bool {
	switch m {
	case TriggerExclamation, TriggerLiteral, TriggerWildcard:
		return true
	default:
		return false
	}
}

// Typed returns trigger as a user writes it in the chat: with "!" for
// TriggerExclamation, otherwise as it is.
func (m TriggerMode) Typed(trigger string) string {
	if m == TriggerExclamation {
		return "!" + trigger
	}
	return trigger
}

// ParseTriggers splits the trigger input of a chat command with the trigger
// mode m into triggers (B11, B12). If the input contains a semicolon, it
// separates the triggers, which may then consist of several words;
// otherwise white space does. White space inside a trigger is collapsed,
// and with TriggerExclamation a trigger loses its leading "!". Empty
// entries and duplicates are dropped: the same trigger twice (B14), and for
// TriggerWildcard the same trigger in another spelling, where the first
// spelling wins.
func ParseTriggers(input string, m TriggerMode) []string {
	var parts []string
	if strings.Contains(input, ";") {
		parts = strings.Split(input, ";")
	} else {
		parts = strings.Fields(input)
	}
	triggers := []string{}
	seen := make(map[string]bool, len(parts))
	for _, p := range parts {
		t := normalizeTrigger(p, m)
		if t == "" || seen[TriggerKey(m, t)] {
			continue
		}
		seen[TriggerKey(m, t)] = true
		triggers = append(triggers, t)
	}
	return triggers
}

// normalizeTrigger collapses white space and, with TriggerExclamation,
// removes a leading "!".
func normalizeTrigger(t string, m TriggerMode) string {
	t = strings.TrimSpace(t)
	if m == TriggerExclamation {
		t = strings.TrimLeft(t, "!")
	}
	return strings.Join(strings.Fields(t), " ")
}

// TriggerKey returns the key under which a trigger of the mode m is unique
// among the triggers of the enabled chat commands (B14): the trigger as a
// user writes it, in exactly this spelling, so that "!Hallo" and "!hallo"
// are different triggers, and a literal "!hallo" is the same as "hallo"
// with TriggerExclamation. Wildcard triggers are unique among themselves
// regardless of case, since they never consider it (B13); their key is the
// trigger in lowercase.
func TriggerKey(m TriggerMode, trigger string) string {
	if m == TriggerWildcard {
		return strings.ToLower(trigger)
	}
	return m.Typed(trigger)
}

// validTriggers checks the triggers of a chat command with the trigger mode
// m: at least one (B61), in the form ParseTriggers returns, without
// duplicates by TriggerKey.
func validTriggers(triggers []string, m TriggerMode) error {
	if len(triggers) == 0 {
		return invalid("a chat command needs at least one trigger")
	}
	seen := make(map[string]bool, len(triggers))
	for _, t := range triggers {
		if t == "" || t != normalizeTrigger(t, m) {
			return invalid("trigger %q: no extra white space, with mode %q no leading \"!\"", t, m)
		}
		if strings.IndexFunc(t, unicode.IsControl) >= 0 {
			return invalid("trigger %q contains a control character", t)
		}
		if seen[TriggerKey(m, t)] {
			return invalid("trigger %q appears more than once", t)
		}
		seen[TriggerKey(m, t)] = true
	}
	return nil
}

// TriggerMatch says how a chat message matches a trigger (B16); a better
// match has a greater value.
type TriggerMatch int

// Kinds of matches.
const (
	// NoMatch: the message does not name the trigger.
	NoMatch TriggerMatch = iota
	// MatchIgnoringCase: the message names the trigger regardless of case.
	// Wildcard triggers match only so (B13).
	MatchIgnoringCase
	// MatchExact: the message names the trigger in exactly its spelling
	// (B14).
	MatchExact
)

// MatchTrigger reports how a chat message matches a trigger of the mode m.
// With TriggerExclamation and TriggerLiteral the message starts with the
// trigger as a user writes it (TriggerMode.Typed), followed by the end of
// the message or white space (B11, B16). With TriggerWildcard the trigger
// occurs anywhere in the message as whole words: where the trigger starts
// or ends with a letter or digit, the message must not continue with one
// (B13). Which command a message triggers if several triggers match is up
// to the recognition of chat messages (B16).
func MatchTrigger(message, trigger string, m TriggerMode) TriggerMatch {
	if trigger == "" {
		return NoMatch
	}
	switch m {
	case TriggerExclamation, TriggerLiteral:
		msg := strings.TrimLeftFunc(message, unicode.IsSpace)
		typed := m.Typed(trigger)
		if rest, ok := strings.CutPrefix(msg, typed); ok && endsWord(rest) {
			return MatchExact
		}
		if rest, ok := cutPrefixFold(msg, typed); ok && endsWord(rest) {
			return MatchIgnoringCase
		}
		return NoMatch
	case TriggerWildcard:
		if _, _, ok := findWords(message, trigger); ok {
			return MatchIgnoringCase
		}
		return NoMatch
	default:
		return NoMatch
	}
}

// endsWord reports whether the rest of a message after a trigger ends it:
// it is empty or starts with white space.
func endsWord(rest string) bool {
	next, _ := utf8.DecodeRuneInString(rest)
	return rest == "" || unicode.IsSpace(next)
}

// cutPrefixFold is strings.CutPrefix regardless of case, rune by rune with
// simple Unicode case folding.
func cutPrefixFold(s, prefix string) (string, bool) {
	for _, p := range prefix {
		r, size := utf8.DecodeRuneInString(s)
		if size == 0 || !strings.EqualFold(string(r), string(p)) {
			return "", false
		}
		s = s[size:]
	}
	return s, true
}

// isWord reports whether r belongs to a word: a letter or a digit.
func isWord(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}
