// SPDX-License-Identifier: MIT

package commandfile_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/action/commands"
	"github.com/ripmav/streamcrew/internal/action/flow"
	"github.com/ripmav/streamcrew/internal/action/schema"
	"github.com/ripmav/streamcrew/internal/app"
	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/commandfile"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/role"
)

// fileTypes returns every action and requirement type.
func fileTypes(t *testing.T) commandfile.Types {
	t.Helper()
	reg, err := app.ActionCatalog(capability.Set{})
	require.NoError(t, err)
	codec, err := command.NewCodec(reg.Entries()...)
	require.NoError(t, err)
	return commandfile.Types{Actions: reg.Descriptors(), Codec: codec}
}

// convert reads YAML files, given as name and content, and converts their
// documents.
func convert(t *testing.T, existing commandfile.Existing, files ...string) (commandfile.Plan, []string) {
	t.Helper()
	var docs []commandfile.Document
	for i := 0; i+1 < len(files); i += 2 {
		d, problems := commandfile.Read(files[i], []byte(files[i+1]))
		require.Empty(t, problems)
		docs = append(docs, d...)
	}
	plan, problems := commandfile.Convert(docs, existing, fileTypes(t))
	out := make([]string, len(problems))
	for i, p := range problems {
		out[i] = p.String()
	}
	return plan, out
}

const head = "apiVersion: streamcrew/v1alpha1\n"

// TestConvert covers B1 to B5, B10 to B13, B20 to B23 and B34: the
// documents of each kind become the objects of a plan, with defaults and
// with the IDs of the names they refer to.
func TestConvert(t *testing.T) {
	t.Parallel()
	plan, problems := convert(t, commandfile.Existing{}, "hug.yaml", head+`kind: ChatCommand
metadata: {name: Hug, group: Spaß}
spec:
  triggers: [hug, umarmen]
  requirements:
    arguments: {arguments: [{name: user, type: user, required: true, identifier: target}]}
    cooldown: {scope: group, group: hugs}
    role: {role: follower}
  actions:
    - {type: chat, kind: message, message: "$userdisplayname hugs $target!"}
    - type: conditional
      clauses: [{left: $arg1text, compare: in, right: a|b}]
      actions: [{type: command, kind: run, command: wave}]
      else: [{type: wait, seconds: 1.5}]
---
`+head+`kind: ActionGroup
metadata: {name: Wave}
spec: {enabled: false, unlocked: true, errorPolicy: abort}
---
`+head+`kind: CommandGroup
metadata: {name: Spaß}
spec: {timerInterval: 5m}
---
`+head+`kind: CooldownGroup
metadata: {name: Hugs}
spec: {duration: 1m30s}
---
`+head+`kind: EventCommand
metadata: {name: Follow}
spec: {event: channel.follow}
---
`+head+`kind: TimerCommand
metadata: {name: Tip}
spec: {}
`)
	require.Empty(t, problems)
	require.Len(t, plan.CooldownGroups, 1)
	require.Len(t, plan.Groups, 1)
	require.Len(t, plan.Commands, 4)

	hugs, fun := plan.CooldownGroups[0].Value, plan.Groups[0].Value
	assert.Equal(t, "Hugs", hugs.Name)
	assert.Equal(t, 90*time.Second, hugs.Duration)
	assert.Equal(t, 5*time.Minute, fun.TimerInterval)
	assert.False(t, hugs.ID.IsZero())
	assert.False(t, plan.CooldownGroups[0].Replaces, "new in the profile")

	hug, wave := plan.Commands[0].Value, plan.Commands[1].Value
	assert.Equal(t, "hug.yaml", plan.Commands[0].Doc.File)
	assert.Equal(t, command.KindChat, hug.Kind)
	assert.Equal(t, []string{"hug", "umarmen"}, hug.Triggers)
	assert.Equal(t, commandfile.DefaultTriggerMode, hug.TriggerMode, "B11: the default")
	assert.True(t, hug.Enabled, "B10: the default")
	assert.False(t, hug.Unlocked)
	assert.Equal(t, command.ErrorContinue, hug.ErrorPolicy)
	assert.Equal(t, fun.ID, hug.GroupID, "B23: the group by name")
	require.Len(t, hug.Requirements, 3)
	assert.Equal(t, command.RoleRequirement{Role: role.Follower}, hug.Requirements[0], "in the order of the catalog")
	assert.Equal(t, command.CooldownRequirement{Scope: command.CooldownGrouped, Group: hugs.ID}, hug.Requirements[1], "B23: the cooldown group by name, regardless of case")
	assert.IsType(t, command.ArgumentsRequirement{}, hug.Requirements[2])
	require.Len(t, hug.Actions, 2)
	cond, ok := hug.Actions[1].(flow.Conditional)
	require.True(t, ok, "%T", hug.Actions[1])
	run, ok := cond.Actions[0].(commands.Command)
	require.True(t, ok, "%T", cond.Actions[0])
	assert.Equal(t, wave.ID, run.Command, "B23, B61: a command of the same files by name")

	assert.Equal(t, command.KindActionGroup, wave.Kind)
	assert.False(t, wave.Enabled)
	assert.True(t, wave.Unlocked)
	assert.Equal(t, command.ErrorAbort, wave.ErrorPolicy)
	assert.Empty(t, wave.Actions)
	assert.NotNil(t, wave.Actions, "no actions is an empty list")
	assert.Equal(t, "channel.follow", string(plan.Commands[2].Value.Event))
	assert.Equal(t, command.KindTimer, plan.Commands[3].Value.Kind)
	assert.Empty(t, plan.Commands[3].Value.Requirements)
}

// TestConvertExisting covers B22, B24, B33 and B63: a document replaces the
// object of the profile with its name, regardless of case, and keeps its
// ID; references find objects of the profile.
func TestConvertExisting(t *testing.T) {
	t.Parallel()
	hugID, funID, waveID := id.New(), id.New(), id.New()
	existing := commandfile.Existing{
		Commands: []command.Header{{ID: hugID, Name: "hug"}, {ID: waveID, Name: "Wave"}},
		Groups:   []command.Group{{ID: funID, Name: "Fun"}},
	}
	plan, problems := convert(t, existing, "hug.yaml", head+`kind: ChatCommand
metadata: {name: Hug, group: fun}
spec:
  triggers: [hug]
  actions: [{type: command, kind: run, command: WAVE}]
`)
	require.Empty(t, problems)
	require.Len(t, plan.Commands, 1)
	p := plan.Commands[0]
	assert.True(t, p.Replaces)
	assert.Equal(t, hugID, p.Value.ID, "B33: the ID stays")
	assert.Equal(t, "Hug", p.Value.Name, "B63: the name of the file")
	assert.Equal(t, funID, p.Value.GroupID)
	run, ok := p.Value.Actions[0].(commands.Command)
	require.True(t, ok)
	assert.Equal(t, waveID, run.Command)
}

// TestConvertProblems covers B1, B4, B5, B10 to B14, B21, B24, B32 and
// B60: each document reports its first problem at its place; the others
// still become the plan.
func TestConvertProblems(t *testing.T) {
	t.Parallel()
	chat := func(spec string) string {
		return head + "kind: ChatCommand\nmetadata: {name: hug}\nspec:\n  triggers: [hug]\n" + spec
	}
	group := func(actions string) string {
		return head + "kind: ActionGroup\nmetadata: {name: a}\nspec:\n  actions:\n" + actions
	}
	tests := []struct {
		name, content, want string
	}{
		{"api version", "apiVersion: streamcrew/v1\nkind: TimerCommand\nmetadata: {name: a}\nspec: {}\n", `f.yaml:1:13: apiVersion: must be "streamcrew/v1alpha1"`},
		{"no kind", head + "metadata: {name: a}\nspec: {}\n", `f.yaml:1:1: missing member "kind"`},
		{"unknown kind", head + "kind: Command\nmetadata: {name: a}\nspec: {}\n", `f.yaml:2:7: kind: must be one of "ChatCommand", "EventCommand", "TimerCommand", "ActionGroup", "CommandGroup", "CooldownGroup"`},
		{"unknown member", head + "kind: TimerCommand\nmetadata: {name: a}\nspec: {}\nstatus: {}\n", "f.yaml:5:1: status: unknown member"},
		{"no spec", head + "kind: TimerCommand\nmetadata: {name: a}\n", `f.yaml:1:1: missing member "spec"`},
		{"spec not an object", head + "kind: TimerCommand\nmetadata: {name: a}\nspec: []\n", "f.yaml:4:7: spec: must be an object, not a list"},
		{"no name", head + "kind: TimerCommand\nmetadata: {}\nspec: {}\n", `f.yaml:3:11: metadata: missing member "name"`},
		{"name with space", head + "kind: TimerCommand\nmetadata: {name: ' a'}\nspec: {}\n", `f.yaml:3:18: metadata.name: name " a" has leading or trailing space`},
		{"member of metadata", head + "kind: TimerCommand\nmetadata: {name: a, id: 7}\nspec: {}\n", "f.yaml:3:21: metadata.id: unknown member"},
		{"member of a cooldown group", head + "kind: CooldownGroup\nmetadata: {name: a}\nspec: {duration: 5s, scope: all}\n", "f.yaml:4:22: spec.scope: unknown member"},
		{"group of a group", head + "kind: CommandGroup\nmetadata: {name: a, group: b}\nspec: {}\n", "f.yaml:3:21: metadata.group: unknown member"},
		{"group not text", head + "kind: TimerCommand\nmetadata: {name: a, group: 7}\nspec: {}\n", "f.yaml:3:28: metadata.group: must be text, not the number 7"},
		{"unknown group", head + "kind: TimerCommand\nmetadata: {name: a, group: Nope}\nspec: {}\n", `f.yaml:3:28: metadata.group: no command group is named "Nope"`},
		{"no triggers", head + "kind: ChatCommand\nmetadata: {name: hug}\nspec: {}\n", `f.yaml:4:7: spec: missing member "triggers"`},
		{"trigger not text", head + "kind: ChatCommand\nmetadata: {name: hug}\nspec: {triggers: [1]}\n", "f.yaml:4:19: spec.triggers[0]: must be text, not the number 1"},
		{"triggers not a list", head + "kind: ChatCommand\nmetadata: {name: hug}\nspec: {triggers: hug}\n", `f.yaml:4:18: spec.triggers: must be a list, not the text "hug"`},
		{"trigger mode", chat("  triggerMode: prefix\n"), `f.yaml:6:16: spec.triggerMode: must be one of "exclamation", "literal", "wildcard"`},
		{"error policy", chat("  errorPolicy: ignore\n"), `f.yaml:6:16: spec.errorPolicy: must be one of "continue", "abort"`},
		{"switch as text", chat("  enabled: yes\n"), `f.yaml:6:12: spec.enabled: must be true or false, not the text "yes"`},
		{"trigger with !", head + "kind: ChatCommand\nmetadata: {name: hug}\nspec: {triggers: ['!hug']}\n", `f.yaml:4:7: spec: trigger "!hug": no extra white space, with mode "exclamation" no leading "!"`},
		{"members of another kind", head + "kind: TimerCommand\nmetadata: {name: a}\nspec: {triggers: [a]}\n", "f.yaml:4:8: spec.triggers: unknown member"},
		{"no event", head + "kind: EventCommand\nmetadata: {name: a}\nspec: {}\n", `f.yaml:4:7: spec: missing member "event"`},
		{"unknown event", head + "kind: EventCommand\nmetadata: {name: a}\nspec: {event: channel.wave}\n", "f.yaml:4:15: spec.event: unknown event type"},
		{"duration missing", head + "kind: CooldownGroup\nmetadata: {name: a}\nspec: {}\n", `f.yaml:4:7: spec: missing member "duration"`},
		{"duration without unit", head + "kind: CooldownGroup\nmetadata: {name: a}\nspec: {duration: '30'}\n", "f.yaml:4:18: spec.duration: must be a duration such as 30s or 1m30s"},
		{"duration as number", head + "kind: CooldownGroup\nmetadata: {name: a}\nspec: {duration: 30}\n", "f.yaml:4:18: spec.duration: must be a duration such as 30s, not the number 30"},
		{"duration not positive", head + "kind: CooldownGroup\nmetadata: {name: a}\nspec: {duration: -5s}\n", `f.yaml:4:18: spec.duration: the duration of cooldown group "a" must be positive`},
		{"negative interval", head + "kind: CommandGroup\nmetadata: {name: a}\nspec: {timerInterval: -5s}\n", "f.yaml:4:23: spec.timerInterval: negative timer interval"},
		{"requirements not a map", chat("  requirements: [role]\n"), "f.yaml:6:17: spec.requirements: must be an object, not a list"},
		{"unknown requirement", chat("  requirements: {karma: {}}\n"), "f.yaml:6:18: spec.requirements.karma: unknown requirement type"},
		{"requirement shorthand", chat("  requirements: {role: follower}\n"), `f.yaml:6:24: spec.requirements.role: must be an object, not the text "follower"`},
		{"requirement with type", chat("  requirements: {role: {type: role, role: follower}}\n"), "f.yaml:6:25: spec.requirements.role.type: unknown member"},
		{"requirement with version", chat("  requirements: {role: {schemaVersion: 2, role: follower}}\n"), "f.yaml:6:25: spec.requirements.role.schemaVersion: unknown member"},
		{"unknown role", chat("  requirements: {role: {role: king}}\n"), `f.yaml:6:24: spec.requirements.role: unknown role "king"`},
		{"unknown cooldown group", chat("  requirements: {cooldown: {scope: group, group: Nope}}\n"), `f.yaml:6:50: spec.requirements.cooldown.group: no cooldown group is named "Nope"`},
		{"currency", chat("  requirements: {currency: {currency: Gold, mode: required, amount: 5}}\n"), `f.yaml:6:39: spec.requirements.currency.currency: no currency is named "Gold"`},
		{"requirement member type", chat("  requirements: {threshold: {users: two, within: 1m}}\n"), `f.yaml:6:37: spec.requirements.threshold.users: has the wrong type: the text "two"`},
		{"requirement member", chat("  requirements: {threshold: {users: 2, within: 1m, every: true}}\n"), "f.yaml:6:52: spec.requirements.threshold.every: unknown member"},
		{"actions not a list", chat("  actions: {}\n"), "f.yaml:6:12: spec.actions: must be a list, not an object"},
		{"action not an object", group("    - wait\n"), `f.yaml:6:7: spec.actions[0]: must be an object, not the text "wait"`},
		{"action without type", group("    - {message: hi}\n"), `f.yaml:6:7: spec.actions[0]: missing member "type"`},
		{"unknown action", group("    - {type: launch_rocket}\n"), "f.yaml:6:14: spec.actions[0].type: unknown action type"},
		{"action with version", group("    - {type: wait, schemaVersion: 1, seconds: 1}\n"), "f.yaml:6:20: spec.actions[0].schemaVersion: unknown member"},
		{"action member", group("    - {type: wait, seconds: 1, minutes: 2}\n"), "f.yaml:6:32: spec.actions[0].minutes: unknown member"},
		{"action type of a field", group("    - {type: chat, kind: message, message: hi, reply: 'no'}\n"), `f.yaml:6:55: spec.actions[0].reply: has the wrong type: the text "no"`},
		{"invalid action", group("    - {type: wait, seconds: 5000}\n"), "f.yaml:6:7: spec.actions[0]: seconds: 5000 is not between 0 and 3600"},
		{"command not a name", group("    - {type: command, kind: run, command: 7}\n"), "f.yaml:6:43: spec.actions[0].command: must be a name, not the number 7"},
		{"member of a list entry", chat("  requirements: {arguments: {arguments: [{name: x, type: text, required: maybe}]}}\n"), `f.yaml:6:74: spec.requirements.arguments.arguments[0].required: has the wrong type: the text "maybe"`},
		{"unknown command", group("    - {type: command, kind: run, command: Nope}\n"), `f.yaml:6:43: spec.actions[0].command: no command is named "Nope"`},
		{"child action", group("    - type: group\n      actions:\n        - {type: wait}\n"), `f.yaml:8:11: spec.actions[0].actions[0]: member "seconds" is missing`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			plan, got := convert(t, commandfile.Existing{}, "f.yaml", tc.content)
			assert.Equal(t, []string{tc.want}, got)
			assert.Empty(t, plan.Commands)
			assert.Empty(t, plan.Groups)
			assert.Empty(t, plan.CooldownGroups)
		})
	}
}

// TestConvertKeepsGoing covers B32 and B60: a document with a problem does
// not stop the others; duplicate names stop both documents.
func TestConvertKeepsGoing(t *testing.T) {
	t.Parallel()
	timer := func(name, spec string) string {
		return head + "kind: TimerCommand\nmetadata: {name: " + name + "}\nspec: {" + spec + "}\n"
	}
	plan, problems := convert(t, commandfile.Existing{},
		"a.yaml", timer("A", "enabled: 1")+"---\n"+timer("B", "")+"---\n"+timer("dup", ""),
		"b.yaml", timer("DUP", "")+"---\n"+timer("C", "actions: [{type: command, kind: run, command: A}]"))
	assert.Equal(t, []string{
		"a.yaml:13:18: metadata.name: the name is used again, regardless of case, at b.yaml:3:18",
		"b.yaml:3:18: metadata.name: the name is used again, regardless of case, at a.yaml:13:18",
		"a.yaml:4:17: spec.enabled: must be true or false, not the number 1",
	}, problems)
	names := []string{}
	for _, p := range plan.Commands {
		names = append(names, p.Value.Name)
	}
	assert.Equal(t, []string{"B", "C"}, names, "C refers to A, which has a problem of its own")
}

// TestConvertDepth checks that actions nest at most 16 levels deep
// (Code-ADR-0013, point 5).
func TestConvertDepth(t *testing.T) {
	t.Parallel()
	nest := func(levels int) string {
		inner := "{type: wait, seconds: 1}"
		for range levels - 1 {
			inner = "{type: group, actions: [" + inner + "]}"
		}
		return head + "kind: ActionGroup\nmetadata: {name: a}\nspec:\n  actions: [" + inner + "]\n"
	}
	_, problems := convert(t, commandfile.Existing{}, "f.yaml", nest(16))
	assert.Empty(t, problems)
	_, problems = convert(t, commandfile.Existing{}, "f.yaml", nest(17))
	require.Len(t, problems, 1)
	assert.Equal(t, "f.yaml:5:13: spec.actions[0]: actions nested more than 16 levels deep", problems[0])
}

// TestConvertSameLine checks that duplicate names stop only their own
// documents, also when several names stand on one line (B60).
func TestConvertSameLine(t *testing.T) {
	t.Parallel()
	doc := func(name string) string {
		return `{"apiVersion":"streamcrew/v1alpha1","kind":"TimerCommand","metadata":{"name":"` + name + `"},"spec":{}}`
	}
	docs, problems := commandfile.Read("a.json", []byte("["+doc("A")+","+doc("a")+","+doc("B")+"]"))
	require.Empty(t, problems)
	plan, problems := commandfile.Convert(docs, commandfile.Existing{}, fileTypes(t))
	assert.Len(t, problems, 2)
	require.Len(t, plan.Commands, 1)
	assert.Equal(t, "B", plan.Commands[0].Value.Name)
}

// TestReferencesAtTop checks that the references of every action and
// requirement type stand among the members of its document, where Convert
// resolves them; lists of child actions it converts as actions.
func TestReferencesAtTop(t *testing.T) {
	t.Parallel()
	var schemas []*schema.Schema
	for _, d := range fileTypes(t).Actions {
		schemas = append(schemas, d.Schema)
	}
	for _, d := range command.RequirementCatalog() {
		schemas = append(schemas, d.Schema)
	}
	for _, s := range schemas {
		top := map[*schema.Schema]bool{}
		for _, p := range s.Properties {
			top[p.Schema] = true
		}
		for _, alt := range s.OneOf {
			for _, p := range alt.Properties {
				top[p.Schema] = true
			}
		}
		walk(s, func(f *schema.Schema) {
			if f.Pattern == schema.PatternID {
				assert.True(t, top[f], "a reference below the members of a document")
			}
		})
	}
}
