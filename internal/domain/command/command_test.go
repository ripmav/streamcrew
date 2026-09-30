// SPDX-License-Identifier: Apache-2.0

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

// TestParseTriggers covers B11 and B12.
func TestParseTriggers(t *testing.T) {
	t.Parallel()
	tests := map[string][]string{
		"hug umarmen":                    {"hug", "umarmen"},
		"!hug !Umarmen":                  {"hug", "Umarmen"},
		"good night;gn; !gute   nacht ;": {"good night", "gn", "gute nacht"},
		"hug HUG Hug":                    {"hug"},
		"  ":                             {},
		";;":                             {},
		"!!hug":                          {"hug"},
	}
	for input, want := range tests {
		assert.Equal(t, want, command.ParseTriggers(input), input)
	}
}

// TestMatches covers B11 and B13, including the word boundaries of
// wildcard triggers.
func TestMatches(t *testing.T) {
	t.Parallel()
	tests := []struct {
		message  string
		trigger  string
		wildcard bool
		want     bool
	}{
		{"!hug", "hug", false, true},
		{"!HUG @ada", "hug", false, true},
		{"  !hug", "hug", false, true},
		{"!hugs", "hug", false, false},
		{"hug", "hug", false, false},
		{"say !hug", "hug", false, false},
		{"!good night all", "good night", false, true},
		{"!", "hug", false, false},

		{"what is going on?", "what", true, true},
		{"WHAT?", "what", true, true},
		{"so, what.", "what", true, true},
		{"what", "what", true, true},
		{"!what", "what", true, true},
		{"what's up", "what", true, true},
		{"somewhat", "what", true, false},
		{"whatever", "what", true, false},
		{"somewhat, but what", "what", true, true},
		{"good night everyone", "good night", true, true},
		{"goodnight", "good night", true, false},
		{"i love c++!", "c++", true, true},
		{"abc++", "c++", true, false},
		{"Grüß dich", "grüß", true, true},
		{"grüßen", "grüß", true, false},
		{"42 is it", "42", true, true},
		{"1420", "42", true, false},
		{"", "what", true, false},
		{"anything", "", true, false},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, command.Matches(tt.message, tt.trigger, tt.wildcard),
			"message %q, trigger %q, wildcard %v", tt.message, tt.trigger, tt.wildcard)
	}
}

func validChat() command.Command {
	return command.Command{
		Name: "hug", Kind: command.KindChat, Enabled: true, Triggers: []string{"hug", "umarmen"},
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
		Name: "follow alert", Kind: command.KindEvent, Event: eventtype.ChannelFollow}.Validate())
	require.NoError(t, command.Command{Name: "reminder", Kind: command.KindTimer}.Validate())
	require.NoError(t, command.Command{Name: "shared", Kind: command.KindActionGroup}.Validate())
	for _, p := range []command.ErrorPolicy{"", command.ErrorContinue, command.ErrorAbort} {
		c := validChat()
		c.ErrorPolicy = p
		require.NoError(t, c.Validate(), p)
	}

	tests := map[string]func(*command.Command){
		"empty name":          func(c *command.Command) { c.Name = " " },
		"padded name":         func(c *command.Command) { c.Name = "hug " },
		"unknown kind":        func(c *command.Command) { c.Kind = "webhook" },
		"B61: no triggers":    func(c *command.Command) { c.Triggers = nil },
		"trigger with !":      func(c *command.Command) { c.Triggers = []string{"!hug"} },
		"duplicate triggers":  func(c *command.Command) { c.Triggers = []string{"hug", "HUG"} },
		"control character":   func(c *command.Command) { c.Triggers = []string{"hu\x00g"} },
		"chat with event":     func(c *command.Command) { c.Event = eventtype.ChannelFollow },
		"B20: unknown event":  func(c *command.Command) { c.Kind, c.Triggers, c.Event = command.KindEvent, nil, "channel.nope" },
		"event with triggers": func(c *command.Command) { c.Kind, c.Event = command.KindEvent, eventtype.ChannelFollow },
		"timer with wildcard": func(c *command.Command) { c.Kind, c.Triggers, c.Wildcard = command.KindTimer, nil, true },
		"timer with event": func(c *command.Command) {
			c.Kind, c.Triggers, c.Event = command.KindTimer, nil, eventtype.ChannelFollow
		},
		"requirement twice": func(c *command.Command) {
			c.Requirements = append(c.Requirements, command.RoleRequirement{Role: role.VIP})
		},
		"invalid requirement": func(c *command.Command) { c.Requirements = []command.Requirement{command.RoleRequirement{}} },
		"nil requirement":     func(c *command.Command) { c.Requirements = []command.Requirement{nil} },
		"nil action":          func(c *command.Command) { c.Actions = []command.Action{nil} },
		"unknown policy":      func(c *command.Command) { c.ErrorPolicy = "retry" },
		"unknown requirement x2": func(c *command.Command) {
			c.Requirements = []command.Requirement{unknownRequirement("x"), unknownRequirement("x")}
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

// TestErrorPolicyDefault covers B71 of the spec command-engine.md: an empty
// policy means continue.
func TestErrorPolicyDefault(t *testing.T) {
	t.Parallel()
	assert.Equal(t, command.ErrorContinue, command.ErrorPolicy("").OrDefault())
	assert.Equal(t, command.ErrorAbort, command.ErrorAbort.OrDefault())
}

func TestValidateGroup(t *testing.T) {
	t.Parallel()
	require.NoError(t, command.Group{Name: "Spaß", TimerInterval: 10 * time.Minute}.Validate())
	require.NoError(t, command.Group{Name: "only order"}.Validate(), "B31: without interval")
	require.ErrorIs(t, command.Group{Name: ""}.Validate(), command.ErrInvalid)
	require.ErrorIs(t, command.Group{Name: "x", TimerInterval: -time.Second}.Validate(), command.ErrInvalid)
}

func TestRequirementValidation(t *testing.T) {
	t.Parallel()
	item := id.MustParse("0192f0c4-8f7e-7c3a-9b1d-2f4e6a8c0b1d")
	invalid := map[string]command.Requirement{
		"unknown role":               command.RoleRequirement{Role: "admin"},
		"unknown cooldown scope":     command.CooldownRequirement{Scope: "global", Duration: polydoc.Duration(time.Second)},
		"zero cooldown":              command.CooldownRequirement{Scope: command.CooldownStandard},
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
		"no threshold users":  command.ThresholdRequirement{Within: polydoc.Duration(time.Minute)},
		"no threshold window": command.ThresholdRequirement{Users: 2},
	}
	for name, r := range invalid {
		assert.Error(t, r.Validate(), name)
	}
}
