// SPDX-License-Identifier: MIT

package commandfile_test

import (
	"encoding/json/jsontext"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/commandfile"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
)

// sourceOf returns the objects of plan as the profile stores them.
func sourceOf(t *testing.T, plan commandfile.Plan) commandfile.Source {
	t.Helper()
	types := fileTypes(t)
	var src commandfile.Source
	for _, p := range plan.Commands {
		rec, err := types.Codec.Record(p.Value)
		require.NoError(t, err)
		src.Commands = append(src.Commands, rec)
	}
	for _, p := range plan.Groups {
		src.Groups = append(src.Groups, p.Value)
	}
	for _, p := range plan.CooldownGroups {
		src.CooldownGroups = append(src.CooldownGroups, p.Value)
	}
	return src
}

// existingOf returns the objects of src as Convert knows them.
func existingOf(src commandfile.Source) commandfile.Existing {
	e := commandfile.Existing{Groups: src.Groups, CooldownGroups: src.CooldownGroups}
	for _, r := range src.Commands {
		e.Commands = append(e.Commands, r.Header)
	}
	return e
}

const roundTrip = head + `kind: ChatCommand
metadata: {name: Hug, group: Spaß}
spec:
  triggers: ["no", "123", "true", "a: b", hug]
  triggerMode: literal
  unlocked: true
  requirements:
    role: {role: follower}
    cooldown: {scope: group, group: Hugs}
    arguments: {arguments: [{name: user, type: user, required: true, identifier: target}]}
    settings: {deleteTriggerMessage: true, showInChatMenu: true}
  actions:
    - {type: chat, kind: message, message: "line one\nline two"}
    - type: conditional
      clauses: [{left: $arg1text, compare: between, min: "1", max: "5"}]
      actions: [{type: command, kind: run, command: Wave}]
      else: [{type: wait, seconds: 2.5}]
---
` + head + `kind: ActionGroup
metadata: {name: Wave}
spec: {enabled: false, errorPolicy: abort}
---
` + head + `kind: EventCommand
metadata: {name: Follow}
spec: {event: channel.follow, actions: [{type: command, kind: enable_group, group: Spaß}]}
---
` + head + `kind: CommandGroup
metadata: {name: Spaß}
spec: {timerInterval: 5m}
---
` + head + `kind: CommandGroup
metadata: {name: Quiet}
spec: {}
---
` + head + `kind: CooldownGroup
metadata: {name: Hugs}
spec: {duration: 1m30s}
`

// TestExportRoundTrip covers B35 and B36: exporting all objects and
// reading the export again gives the same objects, in YAML and in JSON.
func TestExportRoundTrip(t *testing.T) {
	t.Parallel()
	plan, problems := convert(t, commandfile.Existing{}, "f.yaml", roundTrip)
	require.Empty(t, problems)
	src := sourceOf(t, plan)
	docs, err := commandfile.Export(src, nil, fileTypes(t))
	require.NoError(t, err)

	kinds := []string{}
	for _, d := range docs {
		kinds = append(kinds, string(d.Kind)+" "+d.Name)
	}
	assert.Equal(t, []string{
		"CooldownGroup Hugs", "CommandGroup Quiet", "CommandGroup Spaß", "EventCommand Follow", "ChatCommand Hug", "ActionGroup Wave",
	}, kinds, "B36: cooldown groups, groups, commands, each by name")

	yamlOut, err := commandfile.EncodeYAML(docs)
	require.NoError(t, err)
	jsonOut, err := commandfile.EncodeJSON(docs, false)
	require.NoError(t, err)
	assert.True(t, jsontext.Value(jsonOut).IsValid())
	for name, data := range map[string][]byte{"export.yaml": yamlOut, "export.json": jsonOut} {
		again, problems := commandfile.Read(name, data)
		require.Empty(t, problems, "%s", data)
		plan2, problems := commandfile.Convert(again, existingOf(src), fileTypes(t))
		require.Empty(t, problems, "%s", data)
		assert.Equal(t, byName(plan.Commands), byName(plan2.Commands), name)
		assert.ElementsMatch(t, values(plan.Groups), values(plan2.Groups), name)
		assert.ElementsMatch(t, values(plan.CooldownGroups), values(plan2.CooldownGroups), name)
		for _, p := range plan2.Commands {
			assert.True(t, p.Replaces, "%s: the same objects, by name", name)
		}
	}
}

// byName returns the commands of a plan by name.
func byName(list []commandfile.Planned[command.Command]) map[string]command.Command {
	out := map[string]command.Command{}
	for _, p := range list {
		out[p.Value.Name] = p.Value
	}
	return out
}

// values returns the objects of a plan.
func values[T any](list []commandfile.Planned[T]) []T {
	out := make([]T, len(list))
	for i, p := range list {
		out[i] = p.Value
	}
	return out
}

// TestExportForm covers B36: all members, also those with a default, in the
// order of the schema, IDs as names.
func TestExportForm(t *testing.T) {
	t.Parallel()
	plan, problems := convert(t, commandfile.Existing{}, "f.yaml", head+`kind: ChatCommand
metadata: {name: Hug, group: Fun}
spec:
  triggers: [hug]
  actions: [{type: wait, seconds: 1}, {type: command, kind: run, command: Hug}]
---
`+head+"kind: CommandGroup\nmetadata: {name: Fun}\nspec: {}\n")
	require.Empty(t, problems)
	docs, err := commandfile.Export(sourceOf(t, plan), nil, fileTypes(t))
	require.NoError(t, err)
	out, err := commandfile.EncodeYAML(docs)
	require.NoError(t, err)
	assert.Equal(t, `apiVersion: streamcrew/v1alpha1
kind: CommandGroup
metadata:
  name: Fun
spec: {}
---
apiVersion: streamcrew/v1alpha1
kind: ChatCommand
metadata:
  name: Hug
  group: Fun
spec:
  triggers:
    - hug
  triggerMode: exclamation
  enabled: true
  unlocked: false
  errorPolicy: continue
  requirements: {}
  actions:
    - type: wait
      enabled: true
      seconds: 1
    - type: command
      enabled: true
      kind: run
      command: Hug
      wait: true
      checkRequirements: false
      args:
        from: caller
`, string(out))

	single, err := commandfile.EncodeJSON(docs[:1], true)
	require.NoError(t, err)
	assert.Equal(t, `{
  "apiVersion": "streamcrew/v1alpha1",
  "kind": "CommandGroup",
  "metadata": {
    "name": "Fun"
  },
  "spec": {}
}
`, string(single))
}

// TestExportSelection covers B35: named commands, regardless of case, with
// the groups and cooldown groups they refer to; an unknown name is an
// error.
func TestExportSelection(t *testing.T) {
	t.Parallel()
	plan, problems := convert(t, commandfile.Existing{}, "f.yaml", roundTrip)
	require.Empty(t, problems)
	src := sourceOf(t, plan)
	docs, err := commandfile.Export(src, []string{"HUG", "hug"}, fileTypes(t))
	require.NoError(t, err)
	kinds := []string{}
	for _, d := range docs {
		kinds = append(kinds, string(d.Kind)+" "+d.Name)
	}
	assert.Equal(t, []string{"CooldownGroup Hugs", "CommandGroup Spaß", "ChatCommand Hug"}, kinds,
		"the command once, with its group and cooldown group; Wave, which it calls, stays out")

	_, err = commandfile.Export(src, []string{"Hug", "Nope", "Other"}, fileTypes(t))
	require.EqualError(t, err, "no command is named \"Nope\"\nno command is named \"Other\"")
}

// TestExportProblems covers B64 and B65: a command that refers to an object
// that no longer exists, or has an action or requirement of an unknown
// type, cannot be written; the export writes nothing.
func TestExportProblems(t *testing.T) {
	t.Parallel()
	plan, problems := convert(t, commandfile.Existing{}, "f.yaml", roundTrip)
	require.Empty(t, problems)
	src := sourceOf(t, plan)
	types := fileTypes(t)

	broken := sourceOf(t, plan)
	broken.Commands = slicesWithout(broken.Commands, "Wave")
	broken.Groups = nil
	_, err := commandfile.Export(broken, nil, types)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `command "Hug": the group`)
	assert.Contains(t, err.Error(), "does not exist")
	assert.Contains(t, err.Error(), `command "Follow": action 1: group: refers to the command group`)

	broken = sourceOf(t, plan)
	broken.Commands = slicesWithout(broken.Commands, "Wave")
	_, err = commandfile.Export(broken, []string{"Hug"}, types)
	require.ErrorContains(t, err, `command "Hug": action 2.1: command: refers to the command`)

	unknown := src
	unknown.Commands = []command.Record{{
		ID: id.New(), Name: "Future", Kind: command.KindTimer, ErrorPolicy: command.ErrorContinue,
		Actions: jsontext.Value(`[{"type":"group","schemaVersion":1,"actions":[{"type":"launch_rocket"}]}]`),
	}, {
		ID: id.New(), Name: "Later", Kind: command.KindTimer, ErrorPolicy: command.ErrorContinue,
		Requirements: jsontext.Value(`[{"type":"karma","schemaVersion":1}]`),
	}}
	_, err = commandfile.Export(unknown, nil, types)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `command "Future": action 1.1 of the unknown type "launch_rocket"`)
	assert.Contains(t, err.Error(), `command "Later": requirement of the unknown type "karma"`)
}

// slicesWithout returns the records without the one named name.
func slicesWithout(recs []command.Record, name string) []command.Record {
	var out []command.Record
	for _, r := range recs {
		if r.Name != name {
			out = append(out, r)
		}
	}
	return out
}

// TestFileNames covers B38: kind, name and version of the format, with a
// number for names that would be equal.
func TestFileNames(t *testing.T) {
	t.Parallel()
	plan, problems := convert(t, commandfile.Existing{}, "f.yaml", head+"kind: TimerCommand\nmetadata: {name: Hug Me!}\nspec: {}\n---\n"+
		head+"kind: TimerCommand\nmetadata: {name: 'hug-me?'}\nspec: {}\n---\n"+
		head+"kind: CooldownGroup\nmetadata: {name: Spaß 2}\nspec: {duration: 1s}\n")
	require.Empty(t, problems)
	docs, err := commandfile.Export(sourceOf(t, plan), nil, fileTypes(t))
	require.NoError(t, err)
	assert.Equal(t, []string{
		"cooldown-group-spaß-2.v1alpha1.yaml",
		"timer-command-hug-me-.v1alpha1.yaml",
		"timer-command-hug-me--2.v1alpha1.yaml",
	}, commandfile.FileNames(docs, ".yaml"))
	assert.Equal(t, "cooldown-group-spaß-2.v1alpha1.json", commandfile.FileNames(docs[:1], ".json")[0])
}

// TestEncodeEmpty checks an export without documents: empty YAML, an empty
// JSON list.
func TestEncodeEmpty(t *testing.T) {
	t.Parallel()
	out, err := commandfile.EncodeYAML(nil)
	require.NoError(t, err)
	assert.Empty(t, out)
	docs, problems := commandfile.Read("empty.yaml", out)
	assert.Empty(t, docs)
	assert.Empty(t, problems)
	out, err = commandfile.EncodeJSON(nil, false)
	require.NoError(t, err)
	assert.Equal(t, "[]\n", string(out))
}

// TestExportOrder covers B36: commands by name regardless of case, so that
// "alpha" comes before "Beta".
func TestExportOrder(t *testing.T) {
	t.Parallel()
	plan, problems := convert(t, commandfile.Existing{}, "f.yaml", head+"kind: TimerCommand\nmetadata: {name: Beta}\nspec: {}\n---\n"+
		head+"kind: TimerCommand\nmetadata: {name: alpha}\nspec: {}\n")
	require.Empty(t, problems)
	docs, err := commandfile.Export(sourceOf(t, plan), nil, fileTypes(t))
	require.NoError(t, err)
	require.Len(t, docs, 2)
	assert.Equal(t, "alpha", docs[0].Name)
	assert.Equal(t, "Beta", docs[1].Name)
}
