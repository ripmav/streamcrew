// SPDX-License-Identifier: MIT

package command_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
)

// chatCommand returns an enabled chat command with the triggers.
func chatCommand(name string, mode command.TriggerMode, triggers ...string) command.Command {
	return command.Command{
		Name: name, Kind: command.KindChat, Enabled: true,
		TriggerMode: mode, Triggers: triggers, ErrorPolicy: command.ErrorContinue,
	}
}

// TestRecognize covers B16 of commands.md with the edge cases B60 and B65
// to B67.
func TestRecognize(t *testing.T) {
	t.Parallel()
	const (
		ex   = command.TriggerExclamation
		lit  = command.TriggerLiteral
		wild = command.TriggerWildcard
	)
	disabled := chatCommand("disabled", ex, "hug")
	disabled.Enabled = false
	event := command.Command{Name: "event", Kind: command.KindEvent, Event: eventtype.ChannelFollow, Enabled: true}
	cmds := []command.Command{
		chatCommand("a", ex, "a"),
		chatCommand("a b", ex, "a b"),
		chatCommand("Hug", ex, "Hug"),
		chatCommand("question", lit, "?hallo"),
		chatCommand("Hallo upper", ex, "Hallo"),
		chatCommand("hallo lower", ex, "hallo"),
		chatCommand("both", ex, "Wink", "wink"),
		chatCommand("what", wild, "what"),
		chatCommand("what is", wild, "what is"),
		chatCommand("hallo wild", wild, "hallo"),
		chatCommand("night", wild, "good night"),
		chatCommand("c++", wild, "c++"),
		disabled,
		event,
	}
	x := command.NewTriggerIndex(cmds)

	tests := []struct {
		message  string
		command  string
		trigger  string
		argsText string
		args     []string
	}{
		{"!a b c", "a b", "a b", "c", []string{"c"}},
		{"!a", "a", "a", "", []string{}},
		{"!a  b c", "a", "a", "b c", []string{"b", "c"}},
		{"!A B c", "a b", "a b", "c", []string{"c"}},
		{"  !a x", "a", "a", "x", []string{"x"}},
		{"!hug", "Hug", "Hug", "", []string{}},
		{"!HUG @bob", "Hug", "Hug", "@bob", []string{"@bob"}},
		{"?hallo welt", "question", "?hallo", "welt", []string{"welt"}},
		{"!hallo", "hallo lower", "hallo", "", []string{}},
		{"!Hallo du", "Hallo upper", "Hallo", "du", []string{"du"}},
		{"!WINK", "both", "Wink", "", []string{}},
		{"so what is up", "what is", "what is", "up", []string{"up"}},
		{"what? what is", "what", "what", "? what is", []string{"?", "what", "is"}},
		{"Good Night, chat", "night", "good night", ", chat", []string{",", "chat"}},
		{"i love c++!", "c++", "c++", "!", []string{"!"}},
		{"!say \"hello world\" now", "", "", "", nil},
	}
	for _, tt := range tests {
		r := x.Recognize(tt.message)
		if tt.command == "" {
			assert.Equal(t, command.RecognitionNone, r.Outcome, tt.message)
			continue
		}
		require.Equal(t, command.RecognitionTriggered, r.Outcome, tt.message)
		assert.Equal(t, tt.command, r.Command.Name, tt.message)
		assert.Equal(t, tt.trigger, r.Trigger, tt.message)
		assert.Equal(t, tt.argsText, r.ArgsText, tt.message)
		assert.Equal(t, tt.args, r.Args, tt.message)
	}

	t.Run("B65 ambiguous", func(t *testing.T) {
		t.Parallel()
		r := x.Recognize("!HALLO")
		require.Equal(t, command.RecognitionAmbiguous, r.Outcome)
		names := []string{r.Ambiguous[0].Name, r.Ambiguous[1].Name}
		assert.ElementsMatch(t, []string{"Hallo upper", "hallo lower"}, names)
		assert.Equal(t, command.RecognitionAmbiguous, x.Recognize("!HALLO and hallo").Outcome,
			"an ambiguous prefix keeps the wildcards from counting")
	})
	t.Run("B67 normal before wildcard", func(t *testing.T) {
		t.Parallel()
		r := x.Recognize("!hallo")
		assert.Equal(t, "hallo lower", r.Command.Name)
		r = x.Recognize("na hallo")
		assert.Equal(t, "hallo wild", r.Command.Name)
	})
	t.Run("B3 disabled and other kinds", func(t *testing.T) {
		t.Parallel()
		only := command.NewTriggerIndex([]command.Command{disabled, event})
		assert.Equal(t, command.RecognitionNone, only.Recognize("!hug").Outcome)
	})
	t.Run("B60 Hug without hug", func(t *testing.T) {
		t.Parallel()
		only := command.NewTriggerIndex([]command.Command{chatCommand("Hug", ex, "Hug")})
		r := only.Recognize("!hug")
		assert.Equal(t, "Hug", r.Command.Name)
	})
	t.Run("B66 own prefix", func(t *testing.T) {
		t.Parallel()
		only := command.NewTriggerIndex([]command.Command{chatCommand("question", lit, "?hallo")})
		assert.Equal(t, "question", only.Recognize("?hallo welt").Command.Name)
		assert.Equal(t, command.RecognitionNone, only.Recognize("!?hallo").Outcome)
		assert.Equal(t, "hallo wild", x.Recognize("!?hallo").Command.Name, "a wildcard still finds the word")
	})
	t.Run("the index keeps its own commands", func(t *testing.T) {
		t.Parallel()
		own := []command.Command{chatCommand("mine", ex, "mine")}
		ix := command.NewTriggerIndex(own)
		own[0].Name = "changed"
		assert.Equal(t, "mine", ix.Recognize("!mine").Command.Name)
	})
}

func TestSplitArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		text string
		want []string
	}{
		{"", []string{}},
		{"   ", []string{}},
		{"a b  c", []string{"a", "b", "c"}},
		{"\ta\nb ", []string{"a", "b"}},
		{`"hello world" now`, []string{"hello world", "now"}},
		{`say "hello world"`, []string{"say", "hello world"}},
		{`"" x`, []string{"", "x"}},
		{`"unclosed text`, []string{`"unclosed`, "text"}},
		{`a"b c"`, []string{`a"b`, `c"`}},
		{`"a b"c d`, []string{`"a`, `b"c`, "d"}},
		{`"a "b" c"`, []string{`a "b`, `c"`}},
		{`"x"`, []string{"x"}},
		{`"`, []string{`"`}},
		{`"ü ß" ö`, []string{"ü ß", "ö"}},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, command.SplitArgs(tt.text), "%q", tt.text)
	}
}

// FuzzRecognize checks that the recognition never fails and that the
// arguments always come from the text after the trigger.
func FuzzRecognize(f *testing.F) {
	for _, seed := range []string{"!a b c", "!HALLO", "so what is up", `!say "a b" c`, "  !Ä x", "!\xff"} {
		f.Add(seed)
	}
	x := command.NewTriggerIndex([]command.Command{
		chatCommand("a", command.TriggerExclamation, "a"),
		chatCommand("a b", command.TriggerExclamation, "a b"),
		chatCommand("ä", command.TriggerExclamation, "ä"),
		chatCommand("Hallo", command.TriggerExclamation, "Hallo"),
		chatCommand("hallo", command.TriggerExclamation, "hallo"),
		chatCommand("say", command.TriggerLiteral, "!say"),
		chatCommand("what", command.TriggerWildcard, "what"),
		chatCommand("what is", command.TriggerWildcard, "what is"),
	})
	f.Fuzz(func(t *testing.T, message string) {
		r := x.Recognize(message)
		switch r.Outcome {
		case command.RecognitionTriggered:
			if !strings.HasSuffix(message, r.ArgsText) {
				t.Fatalf("arguments %q not from the end of %q", r.ArgsText, message)
			}
			assert.Equal(t, command.SplitArgs(r.ArgsText), r.Args)
		case command.RecognitionAmbiguous:
			assert.GreaterOrEqual(t, len(r.Ambiguous), 2)
		case command.RecognitionNone:
		default:
			t.Fatalf("unknown outcome %q", r.Outcome)
		}
	})
}

// FuzzSplitArgs checks that splitting never fails and loses no text other
// than white space and quotes.
func FuzzSplitArgs(f *testing.F) {
	for _, seed := range []string{`a "b c" d`, `"`, `""`, `"a"b" c`, " x"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		args := command.SplitArgs(text)
		require.NotNil(t, args)
		if !utf8.ValidString(text) {
			return
		}
		strip := func(s string) string {
			return strings.Map(func(r rune) rune {
				if r == '"' || strings.ContainsRune(" \t\n\v\f\r\u0085 ", r) {
					return -1
				}
				return r
			}, s)
		}
		joined := strings.Join(args, "")
		if strip(joined) != strip(text) && !strings.ContainsFunc(text, isOtherSpace) {
			t.Fatalf("text %q became %q", text, args)
		}
	})
}

// isOtherSpace reports white space that strip in FuzzSplitArgs does not
// know.
func isOtherSpace(r rune) bool {
	return r > 0x00a0 && strings.TrimSpace(string(r)) == ""
}
