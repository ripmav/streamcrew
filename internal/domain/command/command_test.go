// SPDX-License-Identifier: MIT

package command_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/polydoc"
)

// TestParseTriggers covers B11, B12 and B14: only the mode exclamation
// drops a leading "!", and only wildcard triggers are the same in another
// spelling.
func TestParseTriggers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		mode  command.TriggerMode
		want  []string
	}{
		{"hug umarmen", command.TriggerExclamation, []string{"hug", "umarmen"}},
		{"!hug !Umarmen", command.TriggerExclamation, []string{"hug", "Umarmen"}},
		{"good night;gn; !gute   nacht ;", command.TriggerExclamation, []string{"good night", "gn", "gute nacht"}},
		{"hug HUG Hug hug", command.TriggerExclamation, []string{"hug", "HUG", "Hug"}},
		{"  ", command.TriggerExclamation, []string{}},
		{";;", command.TriggerExclamation, []string{}},
		{"!!hug", command.TriggerExclamation, []string{"hug"}},
		{"!", command.TriggerExclamation, []string{}},
		{"?hallo !hallo hallo ?hallo", command.TriggerLiteral, []string{"?hallo", "!hallo", "hallo"}},
		{"what WHAT !what; good   night", command.TriggerWildcard, []string{"what WHAT !what", "good night"}},
		{"what WHAT !what", command.TriggerWildcard, []string{"what", "!what"}},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, command.ParseTriggers(tt.input, tt.mode), "%q with %s", tt.input, tt.mode)
	}
}

// TestTriggerKey covers B14: triggers are unique as a user writes them, in
// exactly this spelling; wildcard triggers regardless of case.
func TestTriggerKey(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "!Hallo", command.TriggerKey(command.TriggerExclamation, "Hallo"))
	assert.Equal(t, "!hallo", command.TriggerKey(command.TriggerLiteral, "!hallo"), "the same as hallo with the mode exclamation")
	assert.Equal(t, "?Hallo", command.TriggerKey(command.TriggerLiteral, "?Hallo"))
	assert.Equal(t, "grüß dich", command.TriggerKey(command.TriggerWildcard, "GRÜß Dich"))
	assert.Equal(t, "!hug", command.TriggerExclamation.Typed("hug"))
	assert.Equal(t, "?hug", command.TriggerLiteral.Typed("?hug"))
	assert.Equal(t, "hug", command.TriggerWildcard.Typed("hug"))
	for _, m := range []command.TriggerMode{command.TriggerExclamation, command.TriggerLiteral, command.TriggerWildcard} {
		assert.True(t, m.Valid(), m)
	}
	assert.False(t, command.TriggerMode("").Valid())
	assert.False(t, command.TriggerMode("prefix").Valid())
}

// TestMatchTrigger covers B11, B13, B14 and B66, including the word
// boundaries of wildcard triggers.
func TestMatchTrigger(t *testing.T) {
	t.Parallel()
	const (
		ex   = command.TriggerExclamation
		lit  = command.TriggerLiteral
		wild = command.TriggerWildcard
	)
	tests := []struct {
		message string
		trigger string
		mode    command.TriggerMode
		want    command.TriggerMatch
	}{
		{"!hug", "hug", ex, command.MatchExact},
		{"!HUG @ada", "hug", ex, command.MatchIgnoringCase},
		{"!Hug", "Hug", ex, command.MatchExact},
		{"!hug", "Hug", ex, command.MatchIgnoringCase},
		{"  !hug", "hug", ex, command.MatchExact},
		{"!hug\tnow", "hug", ex, command.MatchExact},
		{"!hugs", "hug", ex, command.NoMatch},
		{"!HUGS", "hug", ex, command.NoMatch},
		{"hug", "hug", ex, command.NoMatch},
		{"say !hug", "hug", ex, command.NoMatch},
		{"!good night all", "good night", ex, command.MatchExact},
		{"!Good Night all", "good night", ex, command.MatchIgnoringCase},
		{"!", "hug", ex, command.NoMatch},
		{"!ǅemal", "ǆemal", ex, command.MatchIgnoringCase},
		{"!straße", "STRASSE", ex, command.NoMatch},
		{"!hug", "", ex, command.NoMatch},

		{"?hallo welt", "?hallo", lit, command.MatchExact},
		{"!?hallo", "?hallo", lit, command.NoMatch},
		{"?HALLO", "?hallo", lit, command.MatchIgnoringCase},
		{"hallo", "hallo", lit, command.MatchExact},
		{"!hallo", "hallo", lit, command.NoMatch},
		{"!hallo", "!hallo", lit, command.MatchExact},
		{"hallowelt", "hallo", lit, command.NoMatch},

		{"what is going on?", "what", wild, command.MatchIgnoringCase},
		{"WHAT?", "what", wild, command.MatchIgnoringCase},
		{"so, what.", "what", wild, command.MatchIgnoringCase},
		{"what", "what", wild, command.MatchIgnoringCase},
		{"!what", "what", wild, command.MatchIgnoringCase},
		{"what's up", "what", wild, command.MatchIgnoringCase},
		{"somewhat", "what", wild, command.NoMatch},
		{"whatever", "what", wild, command.NoMatch},
		{"somewhat, but what", "what", wild, command.MatchIgnoringCase},
		{"good night everyone", "good night", wild, command.MatchIgnoringCase},
		{"goodnight", "good night", wild, command.NoMatch},
		{"i love c++!", "c++", wild, command.MatchIgnoringCase},
		{"abc++", "c++", wild, command.NoMatch},
		{"Grüß dich", "grüß", wild, command.MatchIgnoringCase},
		{"grüßen", "grüß", wild, command.NoMatch},
		{"42 is it", "42", wild, command.MatchIgnoringCase},
		{"1420", "42", wild, command.NoMatch},
		{"say !what now", "!what", wild, command.MatchIgnoringCase},
		{"say what now", "!what", wild, command.NoMatch},
		{"", "what", wild, command.NoMatch},
		{"anything", "", wild, command.NoMatch},

		{"!hug", "hug", "", command.NoMatch},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, command.MatchTrigger(tt.message, tt.trigger, tt.mode),
			"message %q, trigger %q, mode %q", tt.message, tt.trigger, tt.mode)
	}
	assert.Greater(t, command.MatchExact, command.MatchIgnoringCase, "B16: an exact match is better")
	assert.Greater(t, command.MatchIgnoringCase, command.NoMatch)
}

func validChat() command.Command {
	return command.Command{
		Name: "hug", Kind: command.KindChat, Enabled: true, Triggers: []string{"hug", "umarmen"},
		TriggerMode: command.TriggerExclamation, ErrorPolicy: command.ErrorContinue,
		Requirements: []command.Requirement{
			command.RoleRequirement{Role: role.Follower},
			command.CooldownRequirement{Scope: command.CooldownPerUser, Duration: polydoc.Duration(30 * time.Second)},
		},
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	require.NoError(t, validChat().Validate())
	require.NoError(t, command.Command{
		Name: "follow alert", Kind: command.KindEvent, Event: eventtype.ChannelFollow, ErrorPolicy: command.ErrorContinue}.Validate())
	require.NoError(t, command.Command{Name: "reminder", Kind: command.KindTimer, ErrorPolicy: command.ErrorContinue}.Validate())
	require.NoError(t, command.Command{Name: "shared", Kind: command.KindActionGroup, ErrorPolicy: command.ErrorAbort}.Validate())
	for _, p := range []command.ErrorPolicy{command.ErrorContinue, command.ErrorAbort} {
		c := validChat()
		c.ErrorPolicy = p
		require.NoError(t, c.Validate(), p)
	}
	spellings := validChat()
	spellings.Triggers = []string{"Hug", "hug"}
	require.NoError(t, spellings.Validate(), "B14: triggers in other spellings")
	literal := validChat()
	literal.TriggerMode, literal.Triggers = command.TriggerLiteral, []string{"!hug", "?hug", "hug"}
	require.NoError(t, literal.Validate(), "B11: literal triggers keep their prefix")
	menu := validChat()
	menu.Requirements = []command.Requirement{command.SettingsRequirement{ShowInChatMenu: true}}
	require.NoError(t, menu.Validate(), "requirements.md B62: a chat command in the context menu")
	deleting := command.Command{Name: "reminder", Kind: command.KindTimer, ErrorPolicy: command.ErrorContinue,
		Requirements: []command.Requirement{command.SettingsRequirement{DeleteTriggerMessage: true}}}
	require.NoError(t, deleting.Validate(), "other settings fit every kind")

	tests := map[string]func(*command.Command){
		"empty name":         func(c *command.Command) { c.Name = " " },
		"padded name":        func(c *command.Command) { c.Name = "hug " },
		"unknown kind":       func(c *command.Command) { c.Kind = "webhook" },
		"B61: no triggers":   func(c *command.Command) { c.Triggers = nil },
		"trigger with !":     func(c *command.Command) { c.Triggers = []string{"!hug"} },
		"duplicate triggers": func(c *command.Command) { c.Triggers = []string{"hug", "hug"} },
		"B14: wildcard triggers regardless of case": func(c *command.Command) {
			c.TriggerMode, c.Triggers = command.TriggerWildcard, []string{"hug", "HUG"}
		},
		"no trigger mode":      func(c *command.Command) { c.TriggerMode = "" },
		"unknown trigger mode": func(c *command.Command) { c.TriggerMode = "prefix" },
		"control character":    func(c *command.Command) { c.Triggers = []string{"hu\x00g"} },
		"chat with event":      func(c *command.Command) { c.Event = eventtype.ChannelFollow },
		"B20: unknown event": func(c *command.Command) {
			c.Kind, c.Triggers, c.TriggerMode, c.Event = command.KindEvent, nil, "", "channel.nope"
		},
		"event with triggers": func(c *command.Command) {
			c.Kind, c.Event, c.TriggerMode = command.KindEvent, eventtype.ChannelFollow, ""
		},
		"timer with trigger mode": func(c *command.Command) {
			c.Kind, c.Triggers, c.TriggerMode = command.KindTimer, nil, command.TriggerWildcard
		},
		"timer with event": func(c *command.Command) {
			c.Kind, c.Triggers, c.TriggerMode, c.Event = command.KindTimer, nil, "", eventtype.ChannelFollow
		},
		"requirement twice": func(c *command.Command) {
			c.Requirements = append(c.Requirements, command.RoleRequirement{Role: role.VIP})
		},
		"invalid requirement": func(c *command.Command) { c.Requirements = []command.Requirement{command.RoleRequirement{}} },
		"nil requirement":     func(c *command.Command) { c.Requirements = []command.Requirement{nil} },
		"nil action":          func(c *command.Command) { c.Actions = []command.Action{nil} },
		"unknown policy":      func(c *command.Command) { c.ErrorPolicy = "retry" },
		"no policy":           func(c *command.Command) { c.ErrorPolicy = "" },
		"unknown requirement x2": func(c *command.Command) {
			c.Requirements = []command.Requirement{unknownRequirement("x"), unknownRequirement("x")}
		},
		"requirements.md B62: context menu of a timer": func(c *command.Command) {
			c.Kind, c.Triggers = command.KindTimer, nil
			c.Requirements = []command.Requirement{command.SettingsRequirement{ShowInChatMenu: true}}
		},
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c := validChat()
			change(&c)
			require.ErrorIs(t, c.Validate(), command.ErrInvalid)
		})
	}
}

func unknownRequirement(typ string) command.UnknownRequirement {
	return command.UnknownRequirement{Type: typ, Version: 1, Raw: []byte(`{"type":"` + typ + `"}`)}
}

func TestValidateGroup(t *testing.T) {
	t.Parallel()
	require.NoError(t, command.Group{Name: "Spaß", TimerInterval: 10 * time.Minute}.Validate())
	require.NoError(t, command.Group{Name: "only order"}.Validate(), "B31: without interval")
	require.ErrorIs(t, command.Group{Name: ""}.Validate(), command.ErrInvalid)
	require.ErrorIs(t, command.Group{Name: "x", TimerInterval: -time.Second}.Validate(), command.ErrInvalid)
}

// TestValidateCooldownGroup covers B33: a name and a positive duration.
func TestValidateCooldownGroup(t *testing.T) {
	t.Parallel()
	require.NoError(t, command.CooldownGroup{Name: "Sounds", Duration: 30 * time.Second}.Validate())
	require.ErrorIs(t, command.CooldownGroup{Name: "", Duration: time.Second}.Validate(), command.ErrInvalid)
	require.ErrorIs(t, command.CooldownGroup{Name: "x"}.Validate(), command.ErrInvalid)
	require.ErrorIs(t, command.CooldownGroup{Name: "x", Duration: -time.Second}.Validate(), command.ErrInvalid)
}

// TestCooldownKey covers requirements.md, B20: the command or the cooldown
// group by the scope, and the user only for the scopes per user.
func TestCooldownKey(t *testing.T) {
	t.Parallel()
	cmd, group, usr := id.New(), id.New(), id.New()
	second := polydoc.Duration(time.Second)
	for scope, want := range map[command.CooldownScope]command.CooldownKey{
		command.CooldownStandard:       {Command: cmd},
		command.CooldownPerUser:        {Command: cmd, User: usr},
		command.CooldownGrouped:        {Group: group},
		command.CooldownPerUserGrouped: {Group: group, User: usr},
	} {
		r := command.CooldownRequirement{Scope: scope, Duration: second}
		if scope.Grouped() {
			r = command.CooldownRequirement{Scope: scope, Group: group}
		}
		got, err := r.Key(cmd, usr)
		require.NoError(t, err, scope)
		assert.Equal(t, want, got, scope)
		assert.Equal(t, !want.User.IsZero(), scope.PerUser(), scope)

		if scope.PerUser() {
			_, err := r.Key(cmd, id.ID{})
			require.Error(t, err, "%s without a user", scope)
		} else {
			got, err := r.Key(cmd, id.ID{})
			require.NoError(t, err, scope)
			assert.Equal(t, want, got, "%s needs no user", scope)
		}
	}

	_, err := command.CooldownRequirement{Scope: command.CooldownGrouped}.Key(cmd, usr)
	require.Error(t, err, "grouped without a cooldown group")
	_, err = command.CooldownRequirement{Scope: command.CooldownStandard, Duration: second}.Key(id.ID{}, usr)
	require.Error(t, err, "without a command")
}

func TestRequirementValidation(t *testing.T) {
	t.Parallel()
	item := id.MustParse("0192f0c4-8f7e-7c3a-9b1d-2f4e6a8c0b1d")
	invalid := map[string]command.Requirement{
		"unknown role":               command.RoleRequirement{Role: "admin"},
		"unknown cooldown scope":     command.CooldownRequirement{Scope: "global", Duration: polydoc.Duration(time.Second)},
		"zero cooldown":              command.CooldownRequirement{Scope: command.CooldownStandard},
		"zero per-user cooldown":     command.CooldownRequirement{Scope: command.CooldownPerUser},
		"standard with a group":      command.CooldownRequirement{Scope: command.CooldownStandard, Duration: polydoc.Duration(time.Second), Group: item},
		"grouped without a group":    command.CooldownRequirement{Scope: command.CooldownGrouped},
		"grouped with a duration":    command.CooldownRequirement{Scope: command.CooldownPerUserGrouped, Group: item, Duration: polydoc.Duration(time.Second)},
		"no currency":                command.CurrencyRequirement{Mode: command.CurrencyRequired, Amount: 1},
		"negative amount":            command.CurrencyRequirement{Currency: item, Mode: command.CurrencyRequired, Amount: -1},
		"maximum outside range mode": command.CurrencyRequirement{Currency: item, Mode: command.CurrencyMinimum, Amount: 1, Maximum: 5},
		"range below amount":         command.CurrencyRequirement{Currency: item, Mode: command.CurrencyRange, Amount: 5, Maximum: 1},
		"unknown currency mode":      command.CurrencyRequirement{Currency: item, Mode: "all", Amount: 1},
		"no rank":                    command.RankRequirement{Match: command.RankAtLeast},
		"unknown rank comparison":    command.RankRequirement{Rank: item, Match: "above"},
		"no item":                    command.InventoryRequirement{Amount: 1},
		"zero items":                 command.InventoryRequirement{Item: item},
		"no arguments":               command.ArgumentsRequirement{},
		"unnamed argument":           command.ArgumentsRequirement{Arguments: []command.Argument{{Type: command.ArgumentText}}},
		"argument twice": command.ArgumentsRequirement{Arguments: []command.Argument{
			{Name: "a", Type: command.ArgumentText}, {Name: "a", Type: command.ArgumentNumber},
		}},
		"unknown argument type": command.ArgumentsRequirement{Arguments: []command.Argument{{Name: "a", Type: "date"}}},
		"invalid identifier":    command.ArgumentsRequirement{Arguments: []command.Argument{{Name: "a", Type: command.ArgumentText, Identifier: "My Arg"}}},
		"identifier twice": command.ArgumentsRequirement{Arguments: []command.Argument{
			{Name: "a", Type: command.ArgumentText, Identifier: "x"}, {Name: "b", Type: command.ArgumentText, Identifier: "x"},
		}},
		"B34: required after optional": command.ArgumentsRequirement{Arguments: []command.Argument{
			{Name: "a", Type: command.ArgumentText, Required: true}, {Name: "b", Type: command.ArgumentText}, {Name: "c", Type: command.ArgumentText, Required: true},
		}},
		"no threshold users":  command.ThresholdRequirement{Within: polydoc.Duration(time.Minute)},
		"no threshold window": command.ThresholdRequirement{Users: 2},
	}
	for name, r := range invalid {
		assert.Error(t, r.Validate(), name)
	}
	for name, r := range map[string]command.Requirement{
		"standard": command.CooldownRequirement{Scope: command.CooldownStandard, Duration: polydoc.Duration(time.Second)},
		"per user": command.CooldownRequirement{Scope: command.CooldownPerUser, Duration: polydoc.Duration(time.Second)},
		"grouped":  command.CooldownRequirement{Scope: command.CooldownGrouped, Group: item},
		"all argument types": command.ArgumentsRequirement{Arguments: []command.Argument{
			{Name: "a", Type: command.ArgumentText}, {Name: "b", Type: command.ArgumentNumber},
			{Name: "c", Type: command.ArgumentInteger}, {Name: "d", Type: command.ArgumentUser},
		}},
		"per user group": command.CooldownRequirement{Scope: command.CooldownPerUserGrouped, Group: item},
		"B34: required first": command.ArgumentsRequirement{Arguments: []command.Argument{
			{Name: "a", Type: command.ArgumentUser, Required: true}, {Name: "b", Type: command.ArgumentText, Required: true},
			{Name: "c", Type: command.ArgumentText}, {Name: "d", Type: command.ArgumentText},
		}},
	} {
		assert.NoError(t, r.Validate(), name)
	}
	err := command.ArgumentsRequirement{Arguments: []command.Argument{
		{Name: "a", Type: command.ArgumentText}, {Name: "b", Type: command.ArgumentText}, {Name: "c", Type: command.ArgumentText, Required: true},
	}}.Validate()
	require.EqualError(t, err, `required argument "c" after the optional argument "a"`, "B34")
}

// TestResultNames covers requirements.md B36: the identifiers of the
// arguments are the names that saving checks.
func TestResultNames(t *testing.T) {
	t.Parallel()
	r := command.ArgumentsRequirement{Arguments: []command.Argument{
		{Name: "target", Type: command.ArgumentUser, Identifier: "target"},
		{Name: "reason", Type: command.ArgumentText},
		{Name: "times", Type: command.ArgumentInteger, Identifier: "times"},
	}}
	var setter command.ResultSetter = r
	assert.Equal(t, []string{"target", "times"}, setter.ResultNames())
	assert.Empty(t, command.ArgumentsRequirement{Arguments: []command.Argument{{Name: "a", Type: command.ArgumentText}}}.ResultNames())
}

// step is an action for tests; with children it is a command.Parent.
type step struct {
	err      error
	children []command.Action
}

func (step) DocType() string              { return "step" }
func (s step) Validate() error            { return s.err }
func (s step) Children() []command.Action { return s.children }

// nest returns a step that nests depth levels deep.
func nest(depth int) command.Action {
	var a command.Action = step{}
	for range depth - 1 {
		a = step{children: []command.Action{a}}
	}
	return a
}

// TestValidateActions covers Code-ADR-0013, points 5 and 7: actions and
// their child actions are checked, with the path in the error.
func TestValidateActions(t *testing.T) {
	t.Parallel()
	require.NoError(t, command.ValidateActions([]command.Action{step{}, step{children: []command.Action{step{}}}}))
	require.NoError(t, command.ValidateActions([]command.Action{nest(polydoc.MaxDepth)}))

	for name, tc := range map[string]struct {
		actions []command.Action
		want    string
	}{
		"empty action":       {[]command.Action{step{}, nil}, "empty action at 2"},
		"empty child action": {[]command.Action{step{children: []command.Action{step{}, nil}}}, "empty action at 1.2"},
		"invalid child":      {[]command.Action{step{}, step{children: []command.Action{step{err: assert.AnError}}}}, "action 2.1 (step)"},
		"too deep":           {[]command.Action{nest(polydoc.MaxDepth + 1)}, "nested more than"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := command.ValidateActions(tc.actions)
			require.ErrorIs(t, err, command.ErrInvalid)
			assert.ErrorContains(t, err, tc.want)
		})
	}
}
