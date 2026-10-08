// SPDX-License-Identifier: MIT

package command

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// ParseTriggers splits the trigger input of a chat command into triggers
// (B11, B12). If the input contains a semicolon, it separates the triggers,
// which may then consist of several words; otherwise white space does. Each
// trigger loses a leading "!", white space inside is collapsed, and empty
// entries and duplicates regardless of case are dropped. The first spelling
// wins.
func ParseTriggers(input string) []string {
	var parts []string
	if strings.Contains(input, ";") {
		parts = strings.Split(input, ";")
	} else {
		parts = strings.Fields(input)
	}
	triggers := []string{}
	seen := make(map[string]bool, len(parts))
	for _, p := range parts {
		t := normalizeTrigger(p)
		if t == "" || seen[TriggerKey(t)] {
			continue
		}
		seen[TriggerKey(t)] = true
		triggers = append(triggers, t)
	}
	return triggers
}

// normalizeTrigger removes leading "!" and collapses white space.
func normalizeTrigger(t string) string {
	return strings.Join(strings.Fields(strings.TrimLeft(strings.TrimSpace(t), "!")), " ")
}

// TriggerKey returns the key under which a trigger is unique: triggers are
// recognized regardless of case (B11, B14, B60).
func TriggerKey(trigger string) string {
	return strings.ToLower(trigger)
}

// validTriggers checks the triggers of a chat command: at least one (B61),
// in the form ParseTriggers returns, without duplicates regardless of case.
func validTriggers(triggers []string) error {
	if len(triggers) == 0 {
		return invalid("a chat command needs at least one trigger")
	}
	seen := make(map[string]bool, len(triggers))
	for _, t := range triggers {
		if t == "" || t != normalizeTrigger(t) {
			return invalid("trigger %q: no leading \"!\", no extra white space", t)
		}
		if strings.IndexFunc(t, unicode.IsControl) >= 0 {
			return invalid("trigger %q contains a control character", t)
		}
		if seen[TriggerKey(t)] {
			return invalid("trigger %q appears more than once", t)
		}
		seen[TriggerKey(t)] = true
	}
	return nil
}

// Matches reports whether a chat message triggers a command with this
// trigger, regardless of case. Without wildcard the message starts with "!"
// and the trigger, followed by the end of the message or white space (B11).
// With wildcard the trigger occurs anywhere in the message as whole words:
// where the trigger starts or ends with a letter or digit, the message must
// not continue with one (B13).
func Matches(message, trigger string, wildcard bool) bool {
	msg := strings.ToLower(message)
	trig := TriggerKey(trigger)
	if trig == "" {
		return false
	}
	if !wildcard {
		rest, ok := strings.CutPrefix(strings.TrimLeftFunc(msg, unicode.IsSpace), "!"+trig)
		if !ok {
			return false
		}
		next, _ := utf8.DecodeRuneInString(rest)
		return rest == "" || unicode.IsSpace(next)
	}

	first, _ := utf8.DecodeRuneInString(trig)
	last, _ := utf8.DecodeLastRuneInString(trig)
	for offset := 0; offset < len(msg); {
		i := strings.Index(msg[offset:], trig)
		if i < 0 {
			return false
		}
		start := offset + i
		end := start + len(trig)
		before, _ := utf8.DecodeLastRuneInString(msg[:start])
		after, _ := utf8.DecodeRuneInString(msg[end:])
		startsInWord := isWord(first) && start > 0 && isWord(before)
		endsInWord := isWord(last) && end < len(msg) && isWord(after)
		if !startsInWord && !endsInWord {
			return true
		}
		_, size := utf8.DecodeRuneInString(msg[start:])
		offset = start + size
	}
	return false
}

// isWord reports whether r belongs to a word: a letter or a digit.
func isWord(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}
