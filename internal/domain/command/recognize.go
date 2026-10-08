// SPDX-License-Identifier: MIT

package command

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// RecognitionOutcome says what a chat message triggers (B16).
type RecognitionOutcome string

// Outcomes of the recognition.
const (
	// RecognitionNone means no trigger of an enabled chat command matches.
	RecognitionNone RecognitionOutcome = "none"
	// RecognitionTriggered means the message triggers Recognition.Command.
	RecognitionTriggered RecognitionOutcome = "triggered"
	// RecognitionAmbiguous means the best matches belong to different
	// commands, e.g. "Hallo" and "hallo" for the message "!HALLO"; the
	// message triggers none of them (B16, B65).
	RecognitionAmbiguous RecognitionOutcome = "ambiguous"
)

// Recognition is what a chat message triggers (B16).
type Recognition struct {
	Outcome RecognitionOutcome
	// Command is the triggered command for RecognitionTriggered.
	Command Command
	// Trigger is the trigger that matched for RecognitionTriggered, as
	// stored.
	Trigger string
	// ArgsText is the text after the trigger for RecognitionTriggered,
	// without the white space that separates it; empty if there is none.
	ArgsText string
	// Args are the arguments in ArgsText (SplitArgs).
	Args []string
	// Ambiguous are the commands whose triggers matched equally well, for
	// RecognitionAmbiguous, in the order of the index.
	Ambiguous []Command
}

// TriggerIndex finds the chat command a chat message triggers (B16) among
// the enabled chat commands it was built from. It does not change and is
// safe for concurrent use.
type TriggerIndex struct {
	// prefixed are the triggers a message starts with, wildcards those
	// anywhere in it (B11, B13).
	prefixed  []entry
	wildcards []entry
}

// entry is a trigger in the index.
type entry struct {
	// cmd is the command of the trigger, in the index's own copy of the
	// commands.
	cmd     *Command
	trigger string
	// typed is the trigger as a user writes it (TriggerMode.Typed).
	typed string
}

// NewTriggerIndex returns the index of the triggers of the enabled chat
// commands among cmds; other commands do not trigger on chat messages (B3).
func NewTriggerIndex(cmds []Command) *TriggerIndex {
	x := &TriggerIndex{}
	cmds = slices.Clone(cmds)
	for i := range cmds {
		cmd := &cmds[i]
		if cmd.Kind != KindChat || !cmd.Enabled {
			continue
		}
		for _, trigger := range cmd.Triggers {
			if trigger == "" {
				continue
			}
			e := entry{cmd: cmd, trigger: trigger, typed: cmd.TriggerMode.Typed(trigger)}
			switch cmd.TriggerMode {
			case TriggerExclamation, TriggerLiteral:
				x.prefixed = append(x.prefixed, e)
			case TriggerWildcard:
				x.wildcards = append(x.wildcards, e)
			}
		}
	}
	return x
}

// match is a trigger that matches a message.
type match struct {
	e entry
	// start and end are the place of the trigger in the message, in bytes.
	start, end int
}

// Recognize finds the chat command that message triggers (B16): first the
// triggers the message starts with in exactly their spelling, of which the
// longest wins; then those it starts with regardless of case, of which the
// longest wins if it belongs to one command only; then the wildcard
// triggers, of which the one furthest to the front wins, at the same place
// the longer one (B13). A message triggers at most one command.
func (x *TriggerIndex) Recognize(message string) Recognition {
	msg := strings.TrimLeftFunc(message, unicode.IsSpace)
	var exact, folded []match
	for _, e := range x.prefixed {
		if rest, ok := strings.CutPrefix(msg, e.typed); ok && endsWord(rest) {
			exact = append(exact, match{e: e, end: len(msg) - len(rest)})
		} else if rest, ok := cutPrefixFold(msg, e.typed); ok && endsWord(rest) {
			folded = append(folded, match{e: e, end: len(msg) - len(rest)})
		}
	}
	longest := func(a, b match) bool { return a.end > b.end }
	if r, ok := pick(msg, exact, longest); ok {
		return r
	}
	if r, ok := pick(msg, folded, longest); ok {
		return r
	}

	var wild []match
	for _, e := range x.wildcards {
		if start, end, ok := findWords(msg, e.typed); ok {
			wild = append(wild, match{e: e, start: start, end: end})
		}
	}
	frontLongest := func(a, b match) bool {
		if a.start != b.start {
			return a.start < b.start
		}
		return a.end > b.end
	}
	if r, ok := pick(msg, wild, frontLongest); ok {
		return r
	}
	return Recognition{Outcome: RecognitionNone}
}

// pick returns the recognition of the best of ms by better; ok is false if
// ms is empty. If the best matches belong to different commands, the
// recognition is ambiguous.
func pick(msg string, ms []match, better func(a, b match) bool) (r Recognition, ok bool) {
	if len(ms) == 0 {
		return Recognition{}, false
	}
	best := []match{ms[0]}
	for _, m := range ms[1:] {
		switch {
		case better(m, best[0]):
			best = []match{m}
		case !better(best[0], m):
			best = append(best, m)
		}
	}
	var cmds []*Command
	for _, m := range best {
		if !slices.Contains(cmds, m.e.cmd) {
			cmds = append(cmds, m.e.cmd)
		}
	}
	if len(cmds) > 1 {
		r = Recognition{Outcome: RecognitionAmbiguous}
		for _, cmd := range cmds {
			r.Ambiguous = append(r.Ambiguous, *cmd)
		}
		return r, true
	}
	m := best[0]
	argsText := strings.TrimLeftFunc(msg[m.end:], unicode.IsSpace)
	return Recognition{
		Outcome:  RecognitionTriggered,
		Command:  *m.e.cmd,
		Trigger:  m.e.trigger,
		ArgsText: argsText,
		Args:     SplitArgs(argsText),
	}, true
}

// findWords returns the place of the first occurrence of trig in msg as
// whole words, regardless of case (B13): where the trigger starts or ends
// with a letter or digit, the message must not continue with one. ok is
// false if there is none.
func findWords(msg, trig string) (start, end int, ok bool) {
	if trig == "" {
		return 0, 0, false
	}
	first, _ := utf8.DecodeRuneInString(trig)
	last, _ := utf8.DecodeLastRuneInString(trig)
	for start = 0; start < len(msg); {
		if rest, found := cutPrefixFold(msg[start:], trig); found {
			end = len(msg) - len(rest)
			before, _ := utf8.DecodeLastRuneInString(msg[:start])
			after, _ := utf8.DecodeRuneInString(rest)
			startsInWord := isWord(first) && start > 0 && isWord(before)
			endsInWord := isWord(last) && rest != "" && isWord(after)
			if !startsInWord && !endsInWord {
				return start, end, true
			}
		}
		_, size := utf8.DecodeRuneInString(msg[start:])
		start += size
	}
	return 0, 0, false
}

// SplitArgs splits the text after a trigger into arguments: white space
// separates them, and text in double quotes is one argument without the
// quotes, also if it is empty or has white space in it. A double quote
// opens such text only at the start of an argument, and closes it only
// before white space or at the end; any other double quote, and one that
// opens text that is never closed, is part of an argument. The result is
// empty, not nil, for text without arguments.
func SplitArgs(text string) []string {
	args := []string{}
	for {
		text = strings.TrimLeftFunc(text, unicode.IsSpace)
		if text == "" {
			return args
		}
		if quoted, rest, ok := cutQuoted(text); ok {
			args = append(args, quoted)
			text = rest
			continue
		}
		end := strings.IndexFunc(text, unicode.IsSpace)
		if end < 0 {
			end = len(text)
		}
		args = append(args, text[:end])
		text = text[end:]
	}
}

// cutQuoted cuts text in double quotes from the start of text, if text
// starts with a double quote that a later one closes before white space or
// at the end (SplitArgs).
func cutQuoted(text string) (quoted, rest string, ok bool) {
	inner, found := strings.CutPrefix(text, `"`)
	if !found {
		return "", "", false
	}
	for i := 0; i < len(inner); {
		j := strings.IndexByte(inner[i:], '"')
		if j < 0 {
			return "", "", false
		}
		closing := i + j
		after := inner[closing+1:]
		if endsWord(after) {
			return inner[:closing], after, true
		}
		i = closing + 1
	}
	return "", "", false
}
