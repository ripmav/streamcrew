// SPDX-License-Identifier: MIT

package requirement_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/action/actiontest"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/i18n"
	"github.com/ripmav/streamcrew/internal/polydoc"
	"github.com/ripmav/streamcrew/internal/template"
)

// arg returns an argument; required ones are marked with a leading "!" in
// name, and the identifier is the name.
func arg(name string, typ command.ArgumentType) command.Argument {
	required := strings.HasPrefix(name, "!")
	name = strings.TrimPrefix(name, "!")
	return command.Argument{Name: name, Type: typ, Required: required, Identifier: name}
}

// hug returns a chat command with the trigger "hug" and the arguments.
func hug(args ...command.Argument) command.Command {
	c := cmd(command.ArgumentsRequirement{Arguments: args})
	c.Triggers = []string{"hug", "umarmen", "big hug", "hug me"}
	return c
}

// said returns the parameters of the chat message text of ada on Twitch,
// with the words after the first as arguments.
func said(text string) engine.Params {
	p := chat(person("ada", platform.Twitch))
	p.Message = text
	words := strings.Fields(text)
	if len(words) > 1 {
		p.Args = words[1:]
		p.ArgsText = strings.Join(words[1:], " ")
	}
	return p
}

// usage returns the rejection that shows the usage.
func usage(text string, tell bool) engine.Rejection {
	return engine.Rejection{
		Requirement: command.TypeArguments,
		Reason:      i18n.Message{Key: i18n.KeyRequirementArgumentsUsage, Args: map[string]i18n.Value{"usage": i18n.Text(text)}},
		Tell:        tell,
	}
}

// wrongType returns the rejection of a value of the wrong type.
func wrongType(argument string, typ command.ArgumentType) engine.Rejection {
	return engine.Rejection{
		Requirement: command.TypeArguments,
		Reason: i18n.Message{Key: i18n.KeyRequirementArgumentsType, Args: map[string]i18n.Value{
			"argument": i18n.Text(argument), "type": i18n.Text(string(typ)),
		}},
		Tell: true,
	}
}

// number returns the value of a number argument as typed.
func number(text string, n float64) template.Value {
	return template.Value{Text: text, Number: n, IsNumber: true}
}

// TestArguments covers B30 to B35 and B105, B106, B111 to B113: the words
// in order, the rest in a last text argument, each type with fitting and
// unfitting values, and the values under their identifier names.
func TestArguments(t *testing.T) {
	t.Parallel()
	f := newFixture(t, language{lang: i18n.English})
	bigHug := said("!big hug")
	bigHug.Args, bigHug.ArgsText = nil, "" // the trigger has two words
	hugMe := said("!hug me")
	hugMe.Args, hugMe.ArgsText = nil, "" // "hug" and "hug me" match
	event := engine.Params{Platform: platform.Twitch, Args: []string{"Fremd"}, ArgsText: "Fremd"}
	for name, tc := range map[string]struct {
		cmd    command.Command
		p      engine.Params
		values map[string]template.Value
		rej    engine.Rejection
	}{
		"user and reason": {
			cmd: hug(arg("!target", command.ArgumentUser), arg("reason", command.ArgumentText)), p: said("!hug @bob thanks a  lot"),
			values: map[string]template.Value{"target": template.TextValue("bob"), "reason": template.TextValue("thanks a lot")},
		},
		"B105 other case": {
			cmd: hug(arg("!target", command.ArgumentUser)), p: said("!hug @BOB"),
			values: map[string]template.Value{"target": template.TextValue("bob")},
		},
		"user without @": {
			cmd: hug(arg("!target", command.ArgumentUser)), p: said("!hug Bob"),
			values: map[string]template.Value{"target": template.TextValue("bob")},
		},
		"B113 unknown with @": {
			cmd: hug(arg("!target", command.ArgumentUser)), p: said("!hug @Fremd"),
			values: map[string]template.Value{"target": template.TextValue("Fremd")},
		},
		"B113 unknown without @": {
			cmd: hug(arg("!target", command.ArgumentUser)), p: said("!hug Fremd"),
			rej: engine.Rejection{Requirement: command.TypeArguments, Reason: i18n.Message{Key: i18n.KeyRequirementArgumentsUser, Args: map[string]i18n.Value{
				"argument": i18n.Text("target"), "name": i18n.Text("Fremd"),
			}}, Tell: true},
		},
		"@ alone": {
			cmd: hug(arg("!target", command.ArgumentUser)), p: said("!hug @"),
			rej: wrongType("target", command.ArgumentUser),
		},
		"user on another platform": {
			cmd: hug(arg("!target", command.ArgumentUser)), p: said("!hug kim"),
			rej: engine.Rejection{Requirement: command.TypeArguments, Reason: i18n.Message{Key: i18n.KeyRequirementArgumentsUser, Args: map[string]i18n.Value{
				"argument": i18n.Text("target"), "name": i18n.Text("kim"),
			}}, Tell: true},
		},
		"B111 rest of the text": {
			cmd: hug(arg("!aktion", command.ArgumentText), arg("!zitat", command.ArgumentText)), p: said("!quote add Das ist ein Zitat"),
			values: map[string]template.Value{"aktion": template.TextValue("add"), "zitat": template.TextValue("Das ist ein Zitat")},
		},
		"extra words": {
			cmd: hug(arg("!n", command.ArgumentInteger)), p: said("!hug 5 more words"),
			values: map[string]template.Value{"n": number("5", 5)},
		},
		"number": {
			cmd: hug(arg("!n", command.ArgumentNumber)), p: said("!hug -1.5e2"),
			values: map[string]template.Value{"n": number("-1.5e2", -150)},
		},
		"B106 number with comma": {
			cmd: hug(arg("!n", command.ArgumentNumber)), p: said("!hug 1,5"),
			rej: wrongType("n", command.ArgumentNumber),
		},
		"integer": {
			cmd: hug(arg("!n", command.ArgumentInteger)), p: said("!hug -42"),
			values: map[string]template.Value{"n": number("-42", -42)},
		},
		"integer beyond 32 bits": {
			cmd: hug(arg("!n", command.ArgumentInteger)), p: said("!hug 9223372036854775807"),
			values: map[string]template.Value{"n": number("9223372036854775807", 9223372036854775807)},
		},
		"B112 integer with fraction": {
			cmd: hug(arg("!n", command.ArgumentInteger)), p: said("!hug 1.5"),
			rej: wrongType("n", command.ArgumentInteger),
		},
		"B112 integer beyond 64 bits": {
			cmd: hug(arg("!n", command.ArgumentInteger)), p: said("!hug 9223372036854775808"),
			rej: wrongType("n", command.ArgumentInteger),
		},
		"B32 missing": {
			cmd: hug(arg("!target", command.ArgumentUser), arg("reason", command.ArgumentText)), p: said("!hug"),
			rej: usage("!hug <target> [reason]", true),
		},
		"B32 missing, alias": {
			cmd: hug(arg("!target", command.ArgumentUser)), p: said("!UMARMEN"),
			rej: usage("!umarmen <target>", true),
		},
		"B32 missing, longest trigger": {
			cmd: hug(arg("!target", command.ArgumentUser)), p: bigHug,
			rej: usage("!big hug <target>", true),
		},
		"B32 missing, two triggers match": {
			cmd: hug(arg("!target", command.ArgumentUser)), p: hugMe,
			rej: usage("!hug me <target>", true),
		},
		"unknown user, no message": {
			cmd: hug(arg("!target", command.ArgumentUser)), p: event,
			rej: engine.Rejection{Requirement: command.TypeArguments, Reason: i18n.Message{Key: i18n.KeyRequirementArgumentsUser, Args: map[string]i18n.Value{
				"argument": i18n.Text("target"), "name": i18n.Text("Fremd"),
			}}},
		},
		"B32 missing, no message": {
			cmd: hug(arg("!target", command.ArgumentUser)), p: engine.Params{Platform: platform.Twitch},
			rej: usage("!hug <target>", false),
		},
		"optional missing": {
			cmd: hug(arg("!target", command.ArgumentUser), arg("n", command.ArgumentInteger)), p: said("!hug bob"),
			values: map[string]template.Value{"target": template.TextValue("bob")},
		},
		"optional checked": {
			cmd: hug(arg("!target", command.ArgumentUser), arg("n", command.ArgumentInteger)), p: said("!hug bob x"),
			rej: wrongType("n", command.ArgumentInteger),
		},
	} {
		got, err := apply(t.Context(), f.service, tc.cmd, tc.p)
		require.NoError(t, err, name)
		if tc.values == nil {
			assert.Equal(t, engine.Rejected(tc.rej), got, name)
			continue
		}
		require.Equal(t, engine.VerdictMet, got.Verdict, "%s: %+v", name, got.Rejection)
		require.Len(t, got.Runs, 1, name)
		assert.Equal(t, tc.values, got.Runs[0].Values, name)
		assert.Equal(t, tc.p.Args, got.Runs[0].Args, "%s: the words stay", name)
	}
}

// TestArgumentValues: the values of the run stay, those of arguments
// without an identifier name or input are not set, an empty word counts as
// missing, and the decision does not change the parameters it was given.
func TestArgumentValues(t *testing.T) {
	t.Parallel()
	f := newFixture(t, language{lang: i18n.English})
	unnamed := command.Argument{Name: "who", Type: command.ArgumentText, Required: true}
	p := said("!hug someone")
	p.Values = map[string]template.Value{"event": template.TextValue("follow")}

	got, err := apply(t.Context(), f.service, hug(unnamed, arg("why", command.ArgumentText)), p)
	require.NoError(t, err)
	require.Equal(t, engine.VerdictMet, got.Verdict)
	assert.Equal(t, p.Values, got.Runs[0].Values, "nothing to add")

	got, err = apply(t.Context(), f.service, hug(arg("!who", command.ArgumentText)), p)
	require.NoError(t, err)
	assert.Equal(t, map[string]template.Value{"event": template.TextValue("follow"), "who": template.TextValue("someone")}, got.Runs[0].Values)
	assert.Equal(t, map[string]template.Value{"event": template.TextValue("follow")}, p.Values, "the given values stay as they were")

	empty := said("!hug")
	empty.Args, empty.ArgsText = []string{""}, `""`
	got, err = apply(t.Context(), f.service, hug(arg("!who", command.ArgumentText)), empty)
	require.NoError(t, err)
	assert.Equal(t, engine.Rejected(usage("!hug <who>", true)), got)

	wildcard := hug(arg("!who", command.ArgumentText))
	wildcard.Wildcard = true
	got, err = apply(t.Context(), f.service, wildcard, engine.Params{Platform: platform.Twitch, Message: "a hug"})
	require.NoError(t, err)
	assert.Equal(t, engine.Rejected(usage("hug <who>", true)), got, "a wildcard trigger without !")

	nameless := cmd(command.ArgumentsRequirement{Arguments: []command.Argument{arg("!who", command.ArgumentText)}})
	got, err = apply(t.Context(), f.service, nameless, engine.Params{})
	require.NoError(t, err)
	assert.Equal(t, engine.Rejected(usage("hug <who>", false)), got, "a command without triggers by its name")
}

// TestArgumentUsers: a run without a platform looks users up on the
// default platform; a failed lookup is an error.
func TestArgumentUsers(t *testing.T) {
	t.Parallel()
	f := newFixture(t, language{lang: i18n.English})
	c := hug(arg("!target", command.ArgumentUser))
	got, err := apply(t.Context(), f.service, c, engine.Params{Args: []string{"bob"}, ArgsText: "bob"})
	require.NoError(t, err)
	require.Equal(t, engine.VerdictMet, got.Verdict, "bob is on Twitch")
	assert.Equal(t, template.TextValue("bob"), got.Runs[0].Values["target"])

	broken := newUsers()
	broken.err = errors.New("store gone")
	_, err = applyWith(t.Context(), f.service, c, said("!hug bob"), broken)
	require.ErrorContains(t, err, "store gone")
	_, err = applyWith(t.Context(), f.service, c, said("!hug bob"), nil)
	require.ErrorContains(t, err, "no lookup of users")
	got, err = applyWith(t.Context(), f.service, hug(arg("!n", command.ArgumentInteger)), said("!hug 5"), nil)
	require.NoError(t, err)
	assert.Equal(t, engine.VerdictMet, got.Verdict, "only users need the lookup")
}

// TestArgumentsOrder covers B2: the cooldown comes before the arguments;
// unfitting arguments start no cooldown.
func TestArgumentsOrder(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, language{lang: i18n.English})
		c := hug(arg("!n", command.ArgumentInteger))
		c.Requirements = append(c.Requirements, command.CooldownRequirement{Scope: command.CooldownStandard, Duration: polydoc.Duration(time.Minute)})

		got, err := apply(t.Context(), f.service, c, said("!hug x"))
		require.NoError(t, err)
		assert.Equal(t, engine.Rejected(wrongType("n", command.ArgumentInteger)), got)
		assert.Empty(t, f.cooldowns.running(), "no cooldown for a rejection")

		got, err = apply(t.Context(), f.service, c, said("!hug 1"))
		require.NoError(t, err)
		require.Equal(t, engine.VerdictMet, got.Verdict)
		got, err = apply(t.Context(), f.service, c, said("!hug x"))
		require.NoError(t, err)
		assert.Equal(t, command.TypeCooldown, got.Rejection.Requirement)
	})
}

// TestArgumentMessages covers B70 and B32, B33: the messages in both
// languages.
func TestArgumentMessages(t *testing.T) {
	t.Parallel()
	catalog, err := i18n.Load()
	require.NoError(t, err)
	unknown := i18n.Message{Key: i18n.KeyRequirementArgumentsUser, Args: map[string]i18n.Value{"argument": i18n.Text("target"), "name": i18n.Text("Fremd")}}
	for _, tc := range []struct {
		m      i18n.Message
		en, de string
	}{
		{usage("!hug <target> [reason]", true).Reason, "Usage: !hug <target> [reason]", "Verwendung: !hug <target> [reason]"},
		{wrongType("n", command.ArgumentInteger).Reason, "n must be a whole number.", "n muss eine ganze Zahl sein."},
		{wrongType("n", command.ArgumentNumber).Reason, "n must be a number.", "n muss eine Zahl sein."},
		{wrongType("target", command.ArgumentUser).Reason, "target must be a user.", "target muss ein Nutzer sein."},
		{unknown, "There is no user Fremd for target.", "Für target gibt es keinen Nutzer Fremd."},
	} {
		en, err := catalog.Render(i18n.English, tc.m)
		require.NoError(t, err)
		assert.Equal(t, tc.en, en)
		de, err := catalog.Render(i18n.German, tc.m)
		require.NoError(t, err)
		assert.Equal(t, tc.de, de)
	}
}

// TestArgumentsWithEngine: a run with the values of its arguments is
// queued; a missing argument is told with the usage.
func TestArgumentsWithEngine(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, language{lang: i18n.German})
		h := actiontest.NewHarnessWith(t, noTypes{}, actiontest.NewCommands(), engine.WithRequirements(f.service))
		c := h.Command("hug")
		c.Triggers = []string{"hug"}
		c.Requirements = []command.Requirement{command.ArgumentsRequirement{Arguments: []command.Argument{arg("!target", command.ArgumentUser)}}}
		h.Put(c)

		res, err := h.Engine().Trigger(t.Context(), engine.Request{Command: c, Source: engine.SourceChat, Params: said("!hug @bob")})
		require.NoError(t, err)
		assert.Equal(t, engine.OutcomeQueued, res.Outcome)

		res, err = h.Engine().Trigger(t.Context(), engine.Request{Command: c, Source: engine.SourceChat, Params: said("!hug")})
		require.NoError(t, err)
		assert.Equal(t, engine.OutcomeRejected, res.Outcome)
		synctest.Wait()
		require.Len(t, f.twitch.Calls(), 1)
		assert.Equal(t, "Verwendung: !hug <target>", f.twitch.Calls()[0].Text)
	})
}

// TestPrepareWithoutLock covers command-engine.md, B16: preparing, e.g.
// looking up the users of arguments, does not wait for a decision that
// holds the lock of the decisions.
func TestPrepareWithoutLock(t *testing.T) {
	t.Parallel()
	f := newFixture(t, language{lang: i18n.English})
	blocking := &blockingCooldowns{cooldowns: f.cooldowns, entered: make(chan struct{}), block: make(chan struct{})}
	svc := f.withCooldowns(t, blocking)
	decide, err := svc.Prepare(t.Context(), cmd(command.CooldownRequirement{Scope: command.CooldownStandard, Duration: polydoc.Duration(time.Minute)}), said("!hug"), newUsers())
	require.NoError(t, err)
	decided := make(chan error, 1)
	go func() {
		_, err := decide(t.Context())
		decided <- err
	}()
	<-blocking.entered

	prepared := make(chan error, 1)
	go func() {
		_, err := svc.Prepare(t.Context(), hug(arg("!target", command.ArgumentUser)), said("!hug bob"), newUsers(person("bob", platform.Twitch)))
		prepared <- err
	}()
	select {
	case err := <-prepared:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Prepare waited for the lock of the decisions")
	}
	close(blocking.block)
	require.NoError(t, <-decided)
}

// blockingCooldowns is a cooldown store whose first read of an end waits
// until block is closed; entered is closed when it starts waiting.
type blockingCooldowns struct {
	*cooldowns
	entered, block chan struct{}
	once           sync.Once
}

func (b *blockingCooldowns) CooldownEnd(ctx context.Context, key command.CooldownKey) (time.Time, bool, error) {
	b.once.Do(func() {
		close(b.entered)
		<-b.block
	})
	return b.cooldowns.CooldownEnd(ctx, key)
}
