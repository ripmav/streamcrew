// SPDX-License-Identifier: Apache-2.0

package commandfile_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/commandfile"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
)

// problemStrings returns problems as text.
func problemStrings(problems []commandfile.Problem) []string {
	out := []string{}
	for _, p := range problems {
		out = append(out, p.String())
	}
	return out
}

// TestConflicts covers B14 and B20 of commands.md for files: triggers among
// the active chat commands in the spelling a user types them, and one
// event command per event type; commands the plan replaces do not count.
func TestConflicts(t *testing.T) {
	t.Parallel()
	replacedID := id.New()
	existing := commandfile.Existing{Commands: []command.Header{
		{ID: replacedID, Name: "Old", Kind: command.KindChat, Enabled: true, Triggers: []string{"free"}, TriggerMode: command.TriggerExclamation},
		{ID: id.New(), Name: "Off", Kind: command.KindChat, Enabled: false, Triggers: []string{"off"}, TriggerMode: command.TriggerExclamation},
		{ID: id.New(), Name: "Literal", Kind: command.KindChat, Enabled: true, Triggers: []string{"!lit"}, TriggerMode: command.TriggerLiteral},
		{ID: id.New(), Name: "Wild", Kind: command.KindChat, Enabled: true, Triggers: []string{"Hi There"}, TriggerMode: command.TriggerWildcard},
		{ID: id.New(), Name: "Follow", Kind: command.KindEvent, Event: "channel.follow"},
	}}
	plan, problems := convert(t, commandfile.Existing{Commands: []command.Header{{ID: replacedID, Name: "Old"}}}, "f.yaml", head+`kind: ChatCommand
metadata: {name: Old}
spec: {triggers: [free, off]}
---
`+head+`kind: ChatCommand
metadata: {name: A}
spec: {triggers: [lit]}
---
`+head+`kind: ChatCommand
metadata: {name: B}
spec: {triggers: [hi there], triggerMode: wildcard}
---
`+head+`kind: ChatCommand
metadata: {name: C}
spec: {triggers: [free]}
---
`+head+`kind: ChatCommand
metadata: {name: D}
spec: {triggers: [free], enabled: false}
---
`+head+`kind: EventCommand
metadata: {name: Greet}
spec: {event: channel.follow}
---
`+head+`kind: EventCommand
metadata: {name: Raid}
spec: {event: channel.raid}
`)
	require.Empty(t, problems)
	kept, conflicts := commandfile.Conflicts(plan, existing)
	assert.Equal(t, []string{
		`f.yaml:9:19: spec.triggers[0]: the trigger "!lit" is used by the active chat command "Literal"`,
		`f.yaml:14:19: spec.triggers[0]: the trigger "hi there" is used by the active chat command "Wild"`,
		`f.yaml:19:19: spec.triggers[0]: the trigger "!free" is used by the active chat command "Old"`,
		`f.yaml:29:15: spec.event: the event command "Follow" reacts to this event type`,
	}, problemStrings(conflicts))
	names := []string{}
	for _, p := range kept.Commands {
		names = append(names, p.Value.Name)
	}
	assert.Equal(t, []string{"Old", "D", "Raid"}, names, "the replaced command frees its trigger; a disabled one has none")
}

// fakeSaver records saves and fails as told.
type fakeSaver struct {
	saved []command.Command
	fail  map[string]error
	warn  map[string][]command.Warning
}

func (f *fakeSaver) SaveCooldownGroup(_ context.Context, g command.CooldownGroup) (command.CooldownGroup, error) {
	return g, f.fail[g.Name]
}

func (f *fakeSaver) SaveGroup(_ context.Context, g command.Group) (command.Group, error) {
	return g, f.fail[g.Name]
}

func (f *fakeSaver) Save(_ context.Context, cmd command.Command) (command.Saved, error) {
	f.saved = append(f.saved, cmd)
	key := cmd.Name
	if len(cmd.Actions) > 0 || cmd.Enabled {
		key += " full"
	}
	return command.Saved{Command: cmd, Warnings: f.warn[key]}, f.fail[key]
}

// TestApply covers B31, B32, B34 and B61: the order of saving, commands
// first as stubs, and the errors and warnings of saving at their places.
func TestApply(t *testing.T) {
	t.Parallel()
	plan, problems := convert(t, commandfile.Existing{}, "f.yaml", head+`kind: ActionGroup
metadata: {name: Deep}
spec:
  requirements: {arguments: {arguments: [{name: x, type: text}]}}
  actions:
    - {type: wait, seconds: 1}
    - type: conditional
      clauses: [{left: a, compare: in, right: a}]
      actions: [{type: wait, seconds: 1}, {type: wait, seconds: 2}]
      else: [{type: wait, seconds: 3}]
---
`+head+`kind: TimerCommand
metadata: {name: Stub}
spec: {}
---
`+head+`kind: TimerCommand
metadata: {name: Plain}
spec: {requirements: {arguments: {arguments: [{name: x, type: text}]}}, actions: [{type: wait, seconds: 1}]}
---
`+head+`kind: CommandGroup
metadata: {name: Fun}
spec: {}
---
`+head+`kind: CooldownGroup
metadata: {name: Hugs}
spec: {duration: 5s}
`)
	require.Empty(t, problems)
	s := &fakeSaver{
		fail: map[string]error{
			"Deep full": fmt.Errorf("%w: %w", command.ErrInvalid, &command.ActionError{Path: []int{2, 3}, Type: "wait", Err: errors.New("bad wait")}),
			"Stub":      errors.New("conflict"),
			"Fun":       fmt.Errorf("%w: %w", command.ErrInvalid, &command.RequirementError{Type: "role", Err: errors.New("bad role")}),
			"Hugs":      fmt.Errorf("%w: taken", command.ErrInvalid),
		},
		warn: map[string][]command.Warning{"Plain full": {
			{Kind: command.WarnCapability, Path: []int{1}, ActionType: "wait", Subject: "host:fs"},
			{Kind: command.WarnUnknownReference, Requirement: "arguments", Subject: "x"},
			{Kind: command.WarnUnknownReference, Requirement: "currency", Subject: "y"},
			{Kind: command.WarnFileRoot, Subject: "notes"},
		}},
	}
	got, err := commandfile.Apply(t.Context(), plan, s, fileTypes(t))
	require.NoError(t, err)
	assert.Equal(t, []string{
		"f.yaml:31:7: spec: taken",
		"f.yaml:26:7: spec: bad role",
		"f.yaml:16:7: spec: conflict",
		"f.yaml:11:14: spec.actions[1].else[0]: bad wait",
		"f.yaml:21:83: spec.actions[0]: warning: needs the capability \"host:fs\", which this core does not have; the action fails when it runs",
		"f.yaml:21:34: spec.requirements.arguments: warning: refers to x, which does not exist; the command does not run",
		"f.yaml:21:22: spec.requirements: warning: refers to y, which does not exist; the command does not run",
		"f.yaml:21:7: spec: warning: the start configuration releases no root \"notes\"; the action fails when it runs",
	}, problemStrings(got), "cooldown groups, groups, then commands; a requirement the document lacks falls back to the nearest place")
	names := []string{}
	for _, c := range s.saved {
		names = append(names, fmt.Sprintf("%s %t %d", c.Name, c.Enabled, len(c.Actions)))
	}
	assert.Equal(t, []string{"Deep false 0", "Stub false 0", "Plain false 0", "Deep true 2", "Plain true 1"}, names,
		"stubs first, switched off and without actions; a failed stub is not saved again")
	assert.Empty(t, s.saved[0].Requirements, "the stub has no requirements")
	assert.Len(t, s.saved[3].Requirements, 1)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = commandfile.Apply(ctx, commandfile.Plan{}, s, fileTypes(t))
	require.ErrorIs(t, err, context.Canceled)
}
