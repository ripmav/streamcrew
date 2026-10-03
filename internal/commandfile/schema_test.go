// SPDX-License-Identifier: Apache-2.0

package commandfile_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/action/actiontest"
	"github.com/ripmav/streamcrew/internal/action/schema"
	"github.com/ripmav/streamcrew/internal/app"
	"github.com/ripmav/streamcrew/internal/commandfile"
	"github.com/ripmav/streamcrew/internal/domain/command"
)

// fileSchema returns the schema of files with every action and requirement
// type.
func fileSchema(t *testing.T) *schema.Schema {
	t.Helper()
	reg, err := app.ActionCatalog()
	require.NoError(t, err)
	s, err := commandfile.Schema(reg.Descriptors(), command.RequirementCatalog())
	require.NoError(t, err)
	return s
}

// compile compiles the schema of files as the conformance test of the
// action types does: the meta-schema of draft 2020-12 must accept it.
func compile(t *testing.T) *jsonschema.Schema {
	t.Helper()
	compiled, err := actiontest.Compile("commands-as-code", fileSchema(t))
	require.NoError(t, err, "the schema passes the meta-schema")
	return compiled
}

// doc returns a document of kind with metadata and spec, given as JSON
// members without braces.
func doc(kind, metadata, spec string) string {
	return `{"apiVersion":"streamcrew/v1alpha1","kind":"` + kind + `","metadata":{` + metadata + `},"spec":{` + spec + `}}`
}

// schemaExample is a file of the examples and whether the schema accepts
// it.
type schemaExample struct {
	name  string
	file  string
	valid bool
}

// schemaExamples are JSON files with documents of each kind in the form of
// files, and with what the format forbids (B1 to B4, B10 to B14, B20, B21,
// B23, B37).
func schemaExamples() []schemaExample {
	hug := doc("ChatCommand", `"name":"Hug","group":"Spaß"`,
		`"triggers":["hug","umarmen"],"triggerMode":"exclamation","enabled":true,"unlocked":false,"errorPolicy":"abort",`+
			`"requirements":{"role":{"role":"follower"},"cooldown":{"scope":"group","group":"Hugs"},`+
			`"arguments":{"arguments":[{"name":"user","type":"user","required":true,"identifier":"target"}]}},`+
			`"actions":[{"type":"chat","kind":"message","message":"$userdisplayname hugs $target!"},`+
			`{"type":"conditional","clauses":[{"left":"$arg1text","compare":"in","right":"a|b"}],`+
			`"actions":[{"type":"command","kind":"run","command":"Wave"}],"else":[{"type":"wait","seconds":1}]}]`)
	chat := func(spec string) string { return doc("ChatCommand", `"name":"hug"`, `"triggers":["hug"]`+spec) }
	withAction := func(action string) string { return doc("ActionGroup", `"name":"a"`, `"actions":[`+action+`]`) }
	return []schemaExample{
		{"chat command with everything", hug, true},
		{"chat command with the defaults", chat(""), true},
		{"event command", doc("EventCommand", `"name":"Follow"`, `"event":"channel.follow"`), true},
		{"timer command", doc("TimerCommand", `"name":"Tip"`, `"enabled":false`), true},
		{"action group", doc("ActionGroup", `"name":"Wave","group":"Spaß"`, `"actions":[]`), true},
		{"command group with timer interval", doc("CommandGroup", `"name":"Spaß"`, `"timerInterval":"5m"`), true},
		{"command group without", doc("CommandGroup", `"name":"Spaß"`, ``), true},
		{"cooldown group", doc("CooldownGroup", `"name":"Hugs"`, `"duration":"1m30s"`), true},
		{"list of documents", `[` + hug + `,` + doc("CooldownGroup", `"name":"Hugs"`, `"duration":"30s"`) + `]`, true},
		{"empty list", `[]`, true},

		{"other api version", strings.Replace(chat(""), "v1alpha1", "v1", 1), false},
		{"unknown kind", strings.Replace(chat(""), "ChatCommand", "Command", 1), false},
		{"without metadata", `{"apiVersion":"streamcrew/v1alpha1","kind":"TimerCommand","spec":{}}`, false},
		{"without spec", `{"apiVersion":"streamcrew/v1alpha1","kind":"TimerCommand","metadata":{"name":"a"}}`, false},
		{"without name", doc("TimerCommand", ``, ``), false},
		{"name with leading space", doc("TimerCommand", `"name":" a"`, ``), false},
		{"name with a control character", doc("TimerCommand", `"name":"a\u0007b"`, ``), false},
		{"unknown member", strings.Replace(chat(""), `"spec"`, `"status":{},"spec"`, 1), false},
		{"unknown member of spec", chat(`,"trigger":["x"]`), false},
		{"unknown member of metadata", doc("TimerCommand", `"name":"a","id":"x"`, ``), false},
		{"chat command without triggers", doc("ChatCommand", `"name":"hug"`, ``), false},
		{"chat command with no triggers", doc("ChatCommand", `"name":"hug"`, `"triggers":[]`), false},
		{"empty trigger", doc("ChatCommand", `"name":"hug"`, `"triggers":[""]`), false},
		{"unknown trigger mode", chat(`,"triggerMode":"prefix"`), false},
		{"unknown error policy", chat(`,"errorPolicy":"ignore"`), false},
		{"switch as text", chat(`,"enabled":"yes"`), false},
		{"event command without event", doc("EventCommand", `"name":"Follow"`, ``), false},
		{"unknown event", doc("EventCommand", `"name":"Follow"`, `"event":"channel.wave"`), false},
		{"timer command with triggers", doc("TimerCommand", `"name":"Tip"`, `"triggers":["tip"]`), false},
		{"command group with a group", doc("CommandGroup", `"name":"Spaß","group":"Other"`, ``), false},
		{"command group with requirements", doc("CommandGroup", `"name":"Spaß"`, `"requirements":{}`), false},
		{"cooldown group without duration", doc("CooldownGroup", `"name":"Hugs"`, ``), false},
		{"duration without unit", doc("CooldownGroup", `"name":"Hugs"`, `"duration":"30"`), false},
		{"unknown requirement", chat(`,"requirements":{"karma":{}}`), false},
		{"requirement with type", chat(`,"requirements":{"role":{"type":"role","role":"follower"}}`), false},
		{"requirement as shorthand", chat(`,"requirements":{"role":"follower"}`), false},
		{"cooldown group with an empty name", chat(`,"requirements":{"cooldown":{"scope":"group","group":""}}`), false},
		{"unknown action", withAction(`{"type":"launch_rocket"}`), false},
		{"action without type", withAction(`{"kind":"message","message":"hi"}`), false},
		{"action with schema version", withAction(`{"type":"wait","schemaVersion":1,"seconds":1}`), false},
		{"unknown child action", withAction(`{"type":"conditional","clauses":[{"left":"a","compare":"in","right":"a"}],"actions":[{"type":"launch_rocket"}]}`), false},
		{"command by an empty name", withAction(`{"type":"command","kind":"run","command":""}`), false},
		{"list with an invalid document", `[` + hug + `,{}]`, false},
		{"text", `"hug"`, false},
	}
}

// TestSchemaExamples checks that the schema accepts the documents of each
// kind and rejects what the format forbids.
func TestSchemaExamples(t *testing.T) {
	t.Parallel()
	validator := compile(t)
	for _, tc := range schemaExamples() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validate(validator, tc.file)
			assert.Equal(t, tc.valid, err == nil, "%v", err)
		})
	}
}

// TestSchemaNames checks that the pattern of names agrees with the Go code
// (commands.md, B1): both reject the names with a control character or a
// space at either end. Other white space at the ends only the Go code
// catches.
func TestSchemaNames(t *testing.T) {
	t.Parallel()
	validator := compile(t)
	file := func(name string) string {
		return `{"apiVersion":"streamcrew/v1alpha1","kind":"CommandGroup","metadata":{"name":` + string(schema.Quote(name)) + `},"spec":{}}`
	}
	for _, name := range []string{
		"hug", "a", "Hug me", "Spaß", "日本語", "🙂", "a 🙂", "",
		" hug", "hug ", "\thug", "hug\n", "hu\u0007g", "hu\u007fg", "hu\u0085g", "hu\u009fg",
	} {
		schemaErr := validate(validator, file(name))
		goErr := command.Group{Name: name}.Validate()
		assert.Equal(t, goErr == nil, schemaErr == nil, "name %q: Go %v, schema %v", name, goErr, schemaErr)
	}
	for _, name := range []string{"hug ", "　hug"} {
		require.NoError(t, validate(validator, file(name)), "name %q", name)
		require.Error(t, command.Group{Name: name}.Validate(), "name %q", name)
	}
}

// TestSchemaForm checks the form of files (B13, B23): no member
// "schemaVersion" and no reference by ID anywhere, a definition for every
// action and requirement type, and a definition of each kind.
func TestSchemaForm(t *testing.T) {
	t.Parallel()
	s := fileSchema(t)
	defs := map[string]*schema.Schema{}
	for _, d := range s.Defs {
		defs[d.Name] = d.Schema
	}
	reg, err := app.ActionCatalog()
	require.NoError(t, err)
	for _, d := range reg.Descriptors() {
		assert.Contains(t, defs, "action."+d.Type)
	}
	for _, d := range command.RequirementCatalog() {
		assert.Contains(t, defs, "requirement."+d.Type)
	}
	for _, k := range commandfile.Kinds() {
		assert.Contains(t, defs, string(k))
	}
	walk(s, func(s *schema.Schema) {
		_, ok := s.Properties.Lookup("schemaVersion")
		assert.False(t, ok, "a member schemaVersion")
		assert.NotEqual(t, schema.PatternID, s.Pattern, "a reference by ID")
	})
}

// walk calls fn for s and every schema in it.
func walk(s *schema.Schema, fn func(*schema.Schema)) {
	if s == nil {
		return
	}
	fn(s)
	for _, p := range s.Properties {
		walk(p.Schema, fn)
	}
	for _, d := range s.Defs {
		walk(d.Schema, fn)
	}
	for _, alt := range s.OneOf {
		walk(alt, fn)
	}
	walk(s.Items, fn)
}

// TestKinds covers B2: the kinds of commands and the groups.
func TestKinds(t *testing.T) {
	t.Parallel()
	want := map[commandfile.Kind]command.Kind{
		commandfile.KindChatCommand:  command.KindChat,
		commandfile.KindEventCommand: command.KindEvent,
		commandfile.KindTimerCommand: command.KindTimer,
		commandfile.KindActionGroup:  command.KindActionGroup,
	}
	for _, k := range commandfile.Kinds() {
		got, ok := k.CommandKind()
		w, isCommand := want[k]
		assert.Equal(t, isCommand, ok, "kind %s", k)
		assert.Equal(t, w, got, "kind %s", k)
	}
	assert.Len(t, commandfile.Kinds(), len(want)+2, "the command kinds and the two groups")
}

// validate validates the JSON document doc against s.
func validate(s *jsonschema.Schema, doc string) error {
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader([]byte(doc)))
	if err != nil {
		return err
	}
	return s.Validate(v)
}

// TestSchemaInvalid checks that a mistake in a schema of a type stops the
// schema of files.
func TestSchemaInvalid(t *testing.T) {
	t.Parallel()
	_, err := commandfile.Schema(nil, []command.RequirementDescriptor{{Type: "bad", Version: 1, Schema: &schema.Schema{UI: "nonsense"}}})
	require.ErrorContains(t, err, "nonsense")
}
