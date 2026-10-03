// SPDX-License-Identifier: MIT

package commandfile

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"slices"
	"strconv"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/schema"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/polydoc"
)

// SchemaFile is the name of the file of the schema that "schema export"
// writes (B30).
const SchemaFile = "streamcrew-v1alpha1.schema.json"

// Names of the definitions of the schema besides the kinds; the action and
// requirement types follow their prefixes.
const (
	defDocument       = "document"
	defRequirements   = "requirements"
	defActions        = "actions"
	defAction         = "action"
	prefixAction      = "action."
	prefixRequirement = "requirement."
)

// Schema returns the JSON Schema of files (B30): a document, or a list of
// documents as a JSON file may hold (B37). Its definitions are the
// documents, each kind, the requirements and actions, and each action and
// requirement type.
//
// actions are the action types as an action.Registry prepared them, with
// "type" bound and the defaults set; requirements are the requirement
// types. Their schemas take the form of files: without "schemaVersion"
// (B13), with names instead of IDs (B23), and with lists of child actions
// that refer to the definition of all actions.
func Schema(actions []action.Descriptor, requirements []command.RequirementDescriptor) (*schema.Schema, error) {
	defs := schema.Defs{{Name: defDocument, Schema: oneOfDefs(kindNames())}}
	for _, k := range Kinds() {
		defs = append(defs, schema.Def{Name: string(k), Schema: kindSchema(k)})
	}

	reqProps := make([]schema.Property, len(requirements))
	for i, r := range requirements {
		reqProps[i] = schema.Property{Name: r.Type, Schema: schema.DefRef(prefixRequirement + r.Type)}
	}
	defs = append(defs,
		schema.Def{Name: defRequirements, Schema: schema.Object(reqProps...)},
		schema.Def{Name: defActions, Schema: &schema.Schema{Type: "array", Items: schema.DefRef(defAction)}},
	)

	actionDefs := make([]string, len(actions))
	for i, a := range actions {
		actionDefs[i] = prefixAction + a.Type
	}
	defs = append(defs, schema.Def{Name: defAction, Schema: oneOfDefs(actionDefs)})
	for _, a := range actions {
		defs = append(defs, schema.Def{Name: prefixAction + a.Type, Schema: fileForm(a.Schema)})
	}
	for _, r := range requirements {
		defs = append(defs, schema.Def{Name: prefixRequirement + r.Type, Schema: fileForm(r.Schema)})
	}

	s := &schema.Schema{
		Dialect: schema.Draft,
		OneOf:   []*schema.Schema{schema.DefRef(defDocument), {Type: "array", Items: schema.DefRef(defDocument)}},
		Defs:    defs,
	}
	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("schema of files: %w", err)
	}
	return s, nil
}

// SchemaJSON returns s as the file that "schema export" writes: indented
// by two spaces, with a line break at the end.
func SchemaJSON(s *schema.Schema) ([]byte, error) {
	out, err := json.Marshal(s, json.Deterministic(true), jsontext.Multiline(true), jsontext.WithIndent("  "))
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// kindNames returns the names of the definitions of the kinds.
func kindNames() []string {
	names := make([]string, 0, len(Kinds()))
	for _, k := range Kinds() {
		names = append(names, string(k))
	}
	return names
}

// oneOfDefs returns a schema that matches exactly one of the definitions
// names; their member "type" or "kind" tells them apart.
func oneOfDefs(names []string) *schema.Schema {
	refs := make([]*schema.Schema, len(names))
	for i, name := range names {
		refs[i] = schema.DefRef(name)
	}
	return &schema.Schema{OneOf: refs}
}

// kindSchema returns the schema of a document of kind k (B1 to B3, B10,
// B11, B20, B21).
func kindSchema(k Kind) *schema.Schema {
	metadata := []schema.Property{{Name: "name", Schema: schema.CommandName(schema.UIText), Required: true}}
	if _, ok := k.CommandKind(); ok {
		metadata = append(metadata, schema.Property{Name: "group", Schema: schema.CommandName(schema.UIGroup)})
	}
	var spec *schema.Schema
	switch k {
	case KindChatCommand:
		spec = commandSpec(
			schema.Property{Name: "triggers", Schema: schema.List(schema.NonEmpty(schema.UIText), 1), Required: true},
			schema.Property{Name: "triggerMode", Schema: withDefault(schema.Choice(
				string(command.TriggerExclamation), string(command.TriggerLiteral), string(command.TriggerWildcard),
			), schema.Quote(string(DefaultTriggerMode)))},
		)
	case KindEventCommand:
		spec = commandSpec(schema.Property{Name: "event", Schema: schema.Choice(eventTypes()...), Required: true})
	case KindCommandGroup:
		spec = schema.Object(schema.Property{Name: "timerInterval", Schema: schema.Duration()})
	case KindCooldownGroup:
		spec = schema.Object(schema.Property{Name: "duration", Schema: schema.Duration(), Required: true})
	case KindTimerCommand, KindActionGroup:
		spec = commandSpec()
	}
	return schema.Object(
		schema.Property{Name: "apiVersion", Schema: &schema.Schema{Type: "string", Const: schema.Quote(APIVersion)}, Required: true},
		schema.Property{Name: "kind", Schema: &schema.Schema{Type: "string", Const: schema.Quote(string(k))}, Required: true},
		schema.Property{Name: "metadata", Schema: schema.Object(metadata...), Required: true},
		schema.Property{Name: "spec", Schema: spec, Required: true},
	)
}

// commandSpec returns the schema of the spec of a command: the members of
// its kind, then those of every command (B10).
func commandSpec(kind ...schema.Property) *schema.Schema {
	return schema.Object(slices.Concat(kind, []schema.Property{
		{Name: "enabled", Schema: withDefault(schema.Switch(), jsontext.Value(strconv.FormatBool(DefaultEnabled)))},
		{Name: "unlocked", Schema: withDefault(schema.Switch(), jsontext.Value(strconv.FormatBool(DefaultUnlocked)))},
		{Name: "errorPolicy", Schema: withDefault(
			schema.Choice(string(command.ErrorContinue), string(command.ErrorAbort)), schema.Quote(string(DefaultErrorPolicy)),
		)},
		{Name: "requirements", Schema: withDefault(schema.DefRef(defRequirements), jsontext.Value("{}"))},
		{Name: "actions", Schema: actionList(jsontext.Value("[]"))},
	})...)
}

// actionList returns the field of a list of actions with the default def.
func actionList(def jsontext.Value) *schema.Schema {
	s := schema.DefRef(defActions)
	s.UI = schema.UIActions
	s.Default = def
	return s
}

// withDefault returns s with the default def.
func withDefault(s *schema.Schema, def jsontext.Value) *schema.Schema {
	s.Default = def
	return s
}

// eventTypes returns the event types that event commands react to.
func eventTypes() []string {
	all := eventtype.All()
	types := make([]string, len(all))
	for i, d := range all {
		types[i] = string(d.Type)
	}
	return types
}

// fileForm returns the stored form s of an action or requirement type in
// the form of files: without "schemaVersion" (B13), with names instead of
// IDs (B23), and with lists of child actions that refer to the definition
// of all actions.
func fileForm(s *schema.Schema) *schema.Schema {
	s = s.Clone()
	s.Dialect = ""
	toFileForm(s)
	return s
}

// toFileForm changes the members of s and its alternatives into the form
// of files. No list of entries holds a reference or child actions yet;
// TestSchemaForm finds one that does.
func toFileForm(s *schema.Schema) {
	s.Properties = slices.DeleteFunc(s.Properties, func(p schema.Property) bool { return p.Name == polydoc.KeySchemaVersion })
	for i := range s.Properties {
		s.Properties[i].Schema = fieldForm(s.Properties[i].Schema)
	}
	for i := range s.OneOf {
		s.OneOf[i] = fieldForm(s.OneOf[i])
	}
}

// fieldForm returns the field s in the form of files: a list of child
// actions refers to the definition of all actions, a reference by ID
// becomes one by name. "schemaVersion" is never required and references
// have no default, so neither needs to be carried over.
func fieldForm(s *schema.Schema) *schema.Schema {
	switch {
	case s.UI == schema.UIActions:
		return actionList(s.Default)
	case s.Pattern == schema.PatternID:
		return schema.CommandName(s.UI)
	default:
		toFileForm(s)
		return s
	}
}
