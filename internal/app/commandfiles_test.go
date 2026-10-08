// SPDX-License-Identifier: MIT

package app_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/app"
	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/commandfile"
	"github.com/ripmav/streamcrew/internal/config"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/store"
)

// commandProfile returns the path of a profile with the chat commands A
// (trigger "a") and B (trigger "b") and an event command for
// channel.follow.
func commandProfile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "profile.db")
	st, err := store.Open(t.Context(), path)
	require.NoError(t, err)
	defer st.Close()
	actions, err := app.ActionCatalog(capability.Set{})
	require.NoError(t, err)
	identifiers, err := app.IdentifierCatalog()
	require.NoError(t, err)
	codec, err := command.NewCodec(actions.Entries()...)
	require.NoError(t, err)
	svc, err := command.NewService(st, codec, command.Checks{
		Counters: st, Names: app.Reserved(identifiers, actions), Types: actions, Roots: config.NewLive(config.Rights{}),
	})
	require.NoError(t, err)
	chat := func(name, trigger string) command.Command {
		return command.Command{
			Name: name, Kind: command.KindChat, Enabled: true, Triggers: []string{trigger},
			TriggerMode: command.TriggerExclamation, ErrorPolicy: command.ErrorContinue}
	}
	_, err = svc.SaveGroup(t.Context(), command.Group{Name: "Old Fun"})
	require.NoError(t, err)
	_, err = svc.SaveCooldownGroup(t.Context(), command.CooldownGroup{Name: "Slow", Duration: time.Minute})
	require.NoError(t, err)
	for _, cmd := range []command.Command{chat("A", "a"), chat("B", "b"), {
		Name: "Follow", Kind: command.KindEvent, Event: "channel.follow", ErrorPolicy: command.ErrorContinue}} {
		_, err := svc.Save(t.Context(), cmd)
		require.NoError(t, err)
	}
	return path
}

// writeFile writes a file of commands as code into dir.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

const fileHead = "apiVersion: streamcrew/v1alpha1\n"

// problemTexts returns the problems of a report as text, with the names
// of the files only.
func problemTexts(r app.CommandFileReport) []string {
	out := []string{}
	for _, p := range r.Problems {
		p.File = filepath.Base(p.File)
		out = append(out, p.String())
	}
	return out
}

// TestCheckCommandFiles covers B31, B32, B34 and B61 of
// commands-as-code.md: valid files pass, also when their commands call
// each other and swap triggers with the commands they replace; the
// profile does not change.
func TestCheckCommandFiles(t *testing.T) {
	t.Parallel()
	profilePath := commandProfile(t)
	dir := t.TempDir()
	files := []string{writeFile(t, dir, "swap.yaml", fileHead+`kind: ChatCommand
metadata: {name: a}
spec:
  triggers: [b]
  actions: [{type: command, kind: run, command: Hug}]
---
`+fileHead+`kind: ChatCommand
metadata: {name: B}
spec: {triggers: [a]}
---
`+fileHead+`kind: ActionGroup
metadata: {name: Hug, group: Fun}
spec:
  requirements: {cooldown: {scope: group, group: Hugs}}
  actions:
    - {type: command, kind: run, command: A}
    - {type: counter, kind: add, counter: hugs, amount: 1}
---
`+fileHead+`kind: CommandGroup
metadata: {name: Fun}
spec: {}
---
`+fileHead+`kind: CooldownGroup
metadata: {name: Hugs}
spec: {duration: 30s}
---
`+fileHead+`kind: TimerCommand
metadata: {name: Tip, group: old fun}
spec:
  requirements: {cooldown: {scope: per_user_group, group: SLOW}}
`)}
	report, err := app.CheckCommandFiles(t.Context(), profilePath, config.Rights{}, files)
	require.NoError(t, err)
	assert.Empty(t, problemTexts(report))
	assert.Equal(t, app.CommandFileReport{Files: 1, Documents: 6, Problems: []commandfile.Problem{}}, report, "groups and cooldown groups of the profile by name")

	st, err := store.Open(t.Context(), profilePath)
	require.NoError(t, err)
	defer st.Close()
	recs, err := st.Commands(t.Context())
	require.NoError(t, err)
	assert.Len(t, recs, 3, "B31: the profile does not change")
	counters, err := st.Counters(t.Context())
	require.NoError(t, err)
	assert.Empty(t, counters, "not even the counter that saving creates")
}

// TestCheckCommandFilesProblems covers B14, B20, B24 and B32 of
// commands-as-code.md with the checks of saving: conflicts with commands
// of the profile, names that hide built-in identifiers, at the action or
// requirement they concern, also below other actions; warnings do not
// count as errors.
func TestCheckCommandFilesProblems(t *testing.T) {
	t.Parallel()
	profilePath := commandProfile(t)
	dir := t.TempDir()
	first := writeFile(t, dir, "a.yaml", fileHead+`kind: ChatCommand
metadata: {name: New}
spec: {triggers: [x, a]}
---
`+fileHead+`kind: EventCommand
metadata: {name: Greeting}
spec: {event: channel.follow}
`)
	second := writeFile(t, dir, "b.yaml", fileHead+`kind: ChatCommand
metadata: {name: Hug}
spec:
  triggers: [hug]
  requirements:
    arguments: {arguments: [{name: who, type: user, identifier: username}]}
---
`+fileHead+`kind: ActionGroup
metadata: {name: Deep}
spec:
  actions:
    - type: conditional
      clauses: [{left: a, compare: in, right: a}]
      actions: [{type: wait, seconds: 1}]
      else:
        - {type: special_identifier, kind: text, name: user, value: x}
---
`+fileHead+`kind: ActionGroup
metadata: {name: Files}
spec:
  actions: [{type: file, kind: write, root: notes, path: a.txt, text: hi}]
`)
	report, err := app.CheckCommandFiles(t.Context(), profilePath, config.Rights{}, []string{second, first})
	require.NoError(t, err)
	assert.Equal(t, []string{
		`b.yaml:7:16: spec.requirements.arguments: identifier "username" hides $<subject><property>`,
		`b.yaml:18:11: spec.actions[0].else[0]: result name "user" hides $<subject><property>`,
		`b.yaml:24:13: spec.actions[0]: warning: needs the capability "host:fs", which this core does not have; the action fails when it runs`,
		`b.yaml:24:13: spec.actions[0]: warning: the start configuration releases no root "notes"; the action fails when it runs`,
		`a.yaml:4:22: spec.triggers[1]: the trigger "!a" is used by the active chat command "A"`,
		`a.yaml:9:15: spec.event: the event command "Follow" reacts to this event type`,
	}, problemTexts(report), "in the order of the files given")
	assert.Equal(t, 4, report.Errors)
	assert.Equal(t, 2, report.Warnings)
	assert.Equal(t, 2, report.Files)
	assert.Equal(t, 5, report.Documents)
}

// TestCheckCommandFilesFailures checks the errors that stop the check: a
// file that cannot be read and a profile that does not exist.
func TestCheckCommandFilesFailures(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, err := app.CheckCommandFiles(t.Context(), commandProfile(t), config.Rights{}, []string{filepath.Join(dir, "missing.yaml")})
	require.Error(t, err)
	file := writeFile(t, dir, "a.yaml", fileHead+"kind: TimerCommand\nmetadata: {name: a}\nspec: {}\n")
	_, err = app.CheckCommandFiles(t.Context(), filepath.Join(dir, "none.db"), config.Rights{}, []string{file})
	require.ErrorContains(t, err, "open the profile")

	report, err := app.CheckCommandFiles(t.Context(), commandProfile(t), config.Rights{}, []string{writeFile(t, dir, "bad.yaml", "a: [\n")})
	require.NoError(t, err)
	assert.Equal(t, 1, report.Errors, "a syntax error is a problem of the file")
	assert.Equal(t, 0, report.Documents)
}

// TestImportCommandFiles covers B33, B34, B61 and B63 of
// commands-as-code.md: an import creates new objects and replaces those of
// the same kind and name, which keep their ID and creation time; objects
// without a document stay.
func TestImportCommandFiles(t *testing.T) {
	t.Parallel()
	profilePath := commandProfile(t)
	before := commandsByName(t, profilePath)
	dir := t.TempDir()
	file := writeFile(t, dir, "hug.yaml", fileHead+`kind: ChatCommand
metadata: {name: a, group: Fun}
spec:
  triggers: [b]
  actions: [{type: command, kind: run, command: Hug}]
---
`+fileHead+`kind: ChatCommand
metadata: {name: B}
spec: {triggers: [a]}
---
`+fileHead+`kind: ActionGroup
metadata: {name: Hug}
spec:
  actions:
    - {type: command, kind: run, command: A}
    - {type: counter, kind: add, counter: hugs, amount: 1}
---
`+fileHead+`kind: CommandGroup
metadata: {name: Fun}
spec: {}
---
`+fileHead+`kind: CooldownGroup
metadata: {name: Hugs}
spec: {duration: 10s}
`)
	report, err := app.ImportCommandFiles(t.Context(), profilePath, nil, config.Rights{}, []string{file})
	require.NoError(t, err)
	assert.Empty(t, problemTexts(report.CommandFileReport))
	assert.True(t, report.Imported)
	assert.Equal(t, 3, report.Created, "Hug, Fun and Hugs")
	assert.Equal(t, 2, report.Replaced, "A and B")

	after := commandsByName(t, profilePath)
	require.Len(t, after, 4, "Follow stays")
	assert.Contains(t, after, "Follow")
	a := after["a"]
	assert.Equal(t, before["A"].ID, a.ID, "B33: the ID stays")
	assert.Equal(t, before["A"].CreatedAt, a.CreatedAt, "B33: the creation time stays")
	assert.Equal(t, []string{"b"}, a.Triggers, "the triggers moved")
	assert.Equal(t, []string{"a"}, after["B"].Triggers)
	assert.False(t, a.GroupID.IsZero())

	st, err := store.Open(t.Context(), profilePath)
	require.NoError(t, err)
	defer st.Close()
	counters, err := st.Counters(t.Context())
	require.NoError(t, err)
	assert.Len(t, counters, 1, "saving created the counter")
}

// TestImportCommandFilesRejects covers B62 of commands-as-code.md: one
// document with an error stops the import of all, and all errors are
// reported.
func TestImportCommandFilesRejects(t *testing.T) {
	t.Parallel()
	profilePath := commandProfile(t)
	dir := t.TempDir()
	good := writeFile(t, dir, "good.yaml", fileHead+`kind: CommandGroup
metadata: {name: Fun}
spec: {}
---
`+fileHead+`kind: ChatCommand
metadata: {name: New}
spec: {triggers: [new]}
`)
	bad := writeFile(t, dir, "bad.yaml", fileHead+`kind: ChatCommand
metadata: {name: Other}
spec: {triggers: [a]}
---
`+fileHead+`kind: TimerCommand
metadata: {name: Tip}
spec: {enabled: maybe}
`)
	report, err := app.ImportCommandFiles(t.Context(), profilePath, nil, config.Rights{}, []string{good, bad})
	require.NoError(t, err)
	assert.False(t, report.Imported)
	assert.Zero(t, report.Created)
	assert.Zero(t, report.Replaced)
	assert.Equal(t, []string{
		`bad.yaml:4:19: spec.triggers[0]: the trigger "!a" is used by the active chat command "A"`,
		`bad.yaml:9:17: spec.enabled: must be true or false, not the text "maybe"`,
	}, problemTexts(report.CommandFileReport))
	assert.Len(t, commandsByName(t, profilePath), 3, "nothing imported")

	syntax := writeFile(t, dir, "syntax.yaml", "a: [\n")
	report, err = app.ImportCommandFiles(t.Context(), profilePath, nil, config.Rights{}, []string{good, syntax})
	require.NoError(t, err)
	assert.False(t, report.Imported, "a file that cannot be read stops the import too")
	assert.Len(t, commandsByName(t, profilePath), 3)

	missing := filepath.Join(dir, "none", "x.db")
	_, err = app.ImportCommandFiles(t.Context(), missing, nil, config.Rights{}, []string{good})
	require.ErrorIs(t, err, os.ErrNotExist)
	assert.NoFileExists(t, missing, "an import creates no profile")
}

// commandsByName returns the commands of the profile at path by name.
func commandsByName(t *testing.T, path string) map[string]command.Header {
	t.Helper()
	st, err := store.Open(t.Context(), path)
	require.NoError(t, err)
	defer st.Close()
	recs, err := st.Commands(t.Context())
	require.NoError(t, err)
	out := map[string]command.Header{}
	for _, r := range recs {
		out[r.Name] = r.Header
	}
	return out
}

// TestImportCommandFilesStoreError checks that an error of the database in
// the transaction of an import is an error, not a report: here a group the
// profile cannot read.
func TestImportCommandFilesStoreError(t *testing.T) {
	t.Parallel()
	profilePath := commandProfile(t)
	db, err := sql.Open("sqlite", profilePath)
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), `INSERT INTO command_groups (id, name, name_key, timer_interval, created_at, updated_at) VALUES ('no id', 'Broken', 'broken', 0, 0, 0)`)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	file := writeFile(t, t.TempDir(), "a.yaml", fileHead+"kind: TimerCommand\nmetadata: {name: a}\nspec: {}\n")
	report, err := app.ImportCommandFiles(t.Context(), profilePath, nil, config.Rights{}, []string{file})
	require.Error(t, err, "%+v", report)
	assert.False(t, report.Imported)
}
