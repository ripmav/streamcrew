// SPDX-License-Identifier: Apache-2.0

// Package schema builds the JSON Schemas of the action types (Code-ADR-0013,
// point 6): draft 2020-12, written by hand from building blocks instead of
// derived from Go types, with UI hints for generic editors in the keyword
// "x-ui".
//
// Schema covers only the keywords the building blocks need; new ones come
// with the blocks that use them. Frontends in other languages read the
// schemas, so "format" is never used to check a value, and patterns come only
// from constants of this package that Go RE2 and ECMA-262 read alike.
package schema

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"slices"
)

// Draft is the dialect of the schemas.
const Draft = "https://json-schema.org/draft/2020-12/schema"

// UI tells generic editors how to present a field (Code-ADR-0013, point 6).
type UI string

// The UI hints.
const (
	UIText       UI = "text"        // a line of plain text
	UIMultiline  UI = "multiline"   // several lines of plain text
	UITemplate   UI = "template"    // text with $ identifiers
	UIAmount     UI = "amount"      // a number or an expression
	UIExpression UI = "expression"  // an expression
	UIUser       UI = "user"        // a user name, as a template
	UIPlatform   UI = "platform"    // a streaming platform
	UICommand    UI = "command"     // a reference to a command
	UIGroup      UI = "group"       // a reference to a command group
	UICounter    UI = "counter"     // the name of a counter
	UIFileRoot   UI = "file_root"   // a released root for files
	UIResultName UI = "result_name" // the name of a result value
	UIActions    UI = "actions"     // a list of child actions
	UISwitch     UI = "switch"      // a yes or no choice
	UIChoice     UI = "choice"      // one of fixed values
	UIList       UI = "list"        // a list of entries, each with the fields of its schema
)

// UIs returns every UI hint, in a fixed order.
func UIs() []UI {
	return []UI{
		UIText, UIMultiline, UITemplate, UIAmount, UIExpression, UIUser, UIPlatform,
		UICommand, UIGroup, UICounter, UIFileRoot, UIResultName, UIActions, UISwitch, UIChoice, UIList,
	}
}

// Valid reports whether u is a known UI hint.
func (u UI) Valid() bool {
	return slices.Contains(UIs(), u)
}

// Patterns of the building blocks; they use only what Go RE2 and ECMA-262
// read alike.
const (
	// PatternName is the pattern of the names of result values
	// (actions.md B5).
	PatternName = "^[a-z0-9]+$"
	// PatternID is the canonical text form of an ID: a UUID in lowercase
	// (Code-ADR-0009).
	PatternID = "^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$"
	// PatternCounterName is the form of counter names: 1 to 64 ASCII
	// letters and digits (spec counters-and-quotes.md, B7).
	PatternCounterName = "^[A-Za-z0-9]{1,64}$"
	// PatternPlatform is the form of the names of platforms: 1 to 32
	// lowercase ASCII letters and digits, as platform.Name.Validate checks
	// them.
	PatternPlatform = "^[a-z0-9]{1,32}$"
	// PatternFileRoot is the form of the names of released roots: 1 to 32
	// lowercase ASCII letters, digits and "_", as --file-root takes them
	// (Code-ADR-0019, point 3).
	PatternFileRoot = "^[a-z0-9_]{1,32}$"
)

// KeyKind is the member that picks the kind of an action document (Kinds).
const KeyKind = "kind"

// Schema is a JSON Schema of draft 2020-12, limited to the keywords the
// building blocks need. A keyword whose field is empty or nil is left out:
// it does not exist in the schema.
type Schema struct {
	// Dialect is "$schema"; only the schema of a whole document has it.
	Dialect string `json:"$schema,omitempty"`
	// Type is the JSON type, e.g. "object" or "string".
	Type string `json:"type,omitempty"`
	// Properties are the members of an object, in the order editors show
	// them.
	Properties Properties `json:"properties,omitzero"`
	// Required are the members an object must have.
	Required []string `json:"required,omitempty"`
	// AdditionalProperties false forbids members that Properties does not
	// name.
	AdditionalProperties *bool `json:"additionalProperties,omitempty"`
	// Items is the schema of the entries of an array.
	Items *Schema `json:"items,omitempty"`
	// MinItems is the least number of entries of an array.
	MinItems *int `json:"minItems,omitempty"`
	// Enum lists the allowed texts.
	Enum []string `json:"enum,omitempty"`
	// Const is the only allowed value, as JSON.
	Const jsontext.Value `json:"const,omitempty"`
	// Minimum and Maximum bound a number, both included.
	Minimum *float64 `json:"minimum,omitempty"`
	Maximum *float64 `json:"maximum,omitempty"`
	// MinLength is the least number of characters of a text.
	MinLength *int `json:"minLength,omitempty"`
	// Pattern is a constant of this package; an empty pattern would match
	// every text, so leaving it out means the same.
	Pattern string `json:"pattern,omitempty"`
	// OneOf are alternatives of which exactly one must match.
	OneOf []*Schema `json:"oneOf,omitempty"`
	// Default is the value a new action has (Code-ADR-0013, point 4).
	Default jsontext.Value `json:"default,omitempty"`
	// UI is the hint for generic editors.
	UI UI `json:"x-ui,omitempty"`
}

// Property is a member of an object schema.
type Property struct {
	Name   string
	Schema *Schema
	// Required members have no default (Code-ADR-0013, point 4).
	Required bool
}

// Properties are the members of an object, in order.
type Properties []Property

// MarshalJSONTo writes the members as a JSON object in their order.
func (ps Properties) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	for _, p := range ps {
		if err := enc.WriteToken(jsontext.String(p.Name)); err != nil {
			return err
		}
		if err := json.MarshalEncode(enc, p.Schema); err != nil {
			return err
		}
	}
	return enc.WriteToken(jsontext.EndObject)
}

// Lookup returns the member name; ok is false if there is none.
func (ps Properties) Lookup(name string) (p Property, ok bool) {
	i := slices.IndexFunc(ps, func(p Property) bool { return p.Name == name })
	if i < 0 {
		return Property{}, false
	}
	return ps[i], true
}

// Names returns the names of the members in order.
func (ps Properties) Names() []string {
	names := make([]string, len(ps))
	for i, p := range ps {
		names[i] = p.Name
	}
	return names
}

// JSON returns s as JSON, the members of objects in their order.
func (s *Schema) JSON() ([]byte, error) {
	return json.Marshal(s, json.Deterministic(true))
}

// Document returns the schema of a whole action document: an object with
// the members of every action (the header and the switch "active") and
// props. The schema of each property is copied, so that defaults set later
// stay with this document.
func Document(props ...Property) *Schema {
	s := Object(append(header(), props...)...)
	s.Dialect = Draft
	return s
}

// header returns the members every action document has (Code-ADR-0010,
// Code-ADR-0013, point 4).
func header() []Property {
	return []Property{
		{Name: "type", Schema: &Schema{Type: "string", MinLength: new(1)}, Required: true},
		{Name: "schemaVersion", Schema: &Schema{Type: "integer", Minimum: new(1.0)}},
		{Name: "enabled", Schema: Switch()},
	}
}

// Object returns a closed object of props.
func Object(props ...Property) *Schema {
	s := &Schema{Type: "object", AdditionalProperties: new(false)}
	for _, p := range props {
		p.Schema = p.Schema.Clone()
		s.Properties = append(s.Properties, p)
		if p.Required {
			s.Required = append(s.Required, p.Name)
		}
	}
	return s
}

// Variant is an alternative of Kinds: the members of one kind.
type Variant struct {
	Kind  string
	Props []Property
}

// Kinds returns a document schema whose member "kind" picks one of the
// variants (Code-ADR-0013, point 4): each variant has the members of all
// variants in common and its own; members of other kinds are not allowed.
func Kinds(common []Property, variants ...Variant) *Schema {
	alts := make([]Alternative, len(variants))
	for i, v := range variants {
		alts[i] = Alternative{Values: []string{v.Kind}, Props: v.Props}
	}
	s := pick(header(), KeyKind, common, alts)
	s.Dialect = Draft
	return s
}

// Alternative is an alternative of Pick: the values of the key that choose
// it, and its own members.
type Alternative struct {
	Values []string
	Props  []Property
}

// Pick returns a closed object whose member key chooses one of alts by its
// value, e.g. the comparison of a clause of the conditional action: each
// alternative has the members in common, the key and its own members;
// members of other alternatives are not allowed. So the value of the key
// decides which members exist, and no member is there that it does not use
// (Code-ADR-0017). The members in common come first.
func Pick(key string, common []Property, alts ...Alternative) *Schema {
	return pick(common, key, nil, alts)
}

// Variant returns the alternative of s, a schema of Kinds or Pick, that
// the value of key chooses; ok is false if there is none.
func (s *Schema) Variant(key, value string) (alt *Schema, ok bool) {
	for _, alt := range s.OneOf {
		p, ok := alt.Properties.Lookup(key)
		if !ok {
			continue
		}
		if slices.Contains(p.Schema.Enum, value) || bytes.Equal(p.Schema.Const, quote(value)) {
			return alt, true
		}
	}
	return nil, false
}

// pick returns a closed object with the members before, the required
// member key, the members after and those of the alternative that the
// value of key chooses. The top level lists key with all values and the
// members of all alternatives, for editors that read it; a member that
// several alternatives have is listed with the schema of the first.
func pick(before []Property, key string, after []Property, alts []Alternative) *Schema {
	var values []string
	for _, a := range alts {
		values = append(values, a.Values...)
	}
	s := Object(slices.Concat(before, []Property{{Name: key, Schema: Choice(values...), Required: true}}, after)...)
	for _, a := range alts {
		k := &Schema{Enum: slices.Clone(a.Values)}
		if len(a.Values) == 1 {
			k = &Schema{Const: quote(a.Values[0])}
		}
		s.OneOf = append(s.OneOf, Object(slices.Concat(before, []Property{{Name: key, Schema: k, Required: true}}, after, a.Props)...))
		for _, p := range a.Props {
			if _, dup := s.Properties.Lookup(p.Name); !dup {
				s.Properties = append(s.Properties, Property{Name: p.Name, Schema: p.Schema.Clone()})
			}
		}
	}
	return s
}

// quote returns text as a JSON string. Type IDs and kinds are ASCII; were
// they not valid UTF-8, the invalid bytes would become U+FFFD.
func quote(text string) jsontext.Value {
	v, _ := jsontext.AppendQuote(nil, text)
	return v
}

// Switch returns a yes or no field.
func Switch() *Schema {
	return &Schema{Type: "boolean", UI: UISwitch}
}

// Text returns a text field shown as ui.
func Text(ui UI) *Schema {
	return &Schema{Type: "string", UI: ui}
}

// Template returns a text field with $ identifiers.
func Template() *Schema {
	return Text(UITemplate)
}

// NonEmpty returns a text field shown as ui that is not empty, e.g. a chat
// message as a template (UITemplate) or the recipient of a whisper
// (UIUser).
func NonEmpty(ui UI) *Schema {
	return &Schema{Type: "string", MinLength: new(1), UI: ui}
}

// Choice returns a field that takes one of values.
func Choice(values ...string) *Schema {
	return &Schema{Type: "string", Enum: slices.Clone(values), UI: UIChoice}
}

// Expression returns the field of an expression (spec template.md,
// B50–B52); it is not empty.
func Expression() *Schema {
	return &Schema{Type: "string", MinLength: new(1), UI: UIExpression}
}

// List returns the field of a list of entries of items, at least minItems
// of them.
func List(items *Schema, minItems int) *Schema {
	return &Schema{Type: "array", Items: items.Clone(), MinItems: new(minItems), UI: UIList}
}

// Reference returns the field of a reference by ID, e.g. to a command
// (UICommand) or a group (UIGroup) (actions.md B31).
func Reference(ui UI) *Schema {
	return &Schema{Type: "string", Pattern: PatternID, UI: ui}
}

// CounterName returns the field of the name of a counter (actions.md B41).
func CounterName() *Schema {
	return &Schema{Type: "string", Pattern: PatternCounterName, UI: UICounter}
}

// Platform returns the field of the name of a streaming platform, e.g.
// "twitch" (actions.md B67, B90).
func Platform() *Schema {
	return &Schema{Type: "string", Pattern: PatternPlatform, UI: UIPlatform}
}

// FileRoot returns the field of the name of a released root for files
// (actions.md B100).
func FileRoot() *Schema {
	return &Schema{Type: "string", Pattern: PatternFileRoot, UI: UIFileRoot}
}

// ResultName returns the field of the name of a result value
// (actions.md B5).
func ResultName() *Schema {
	return &Schema{Type: "string", Pattern: PatternName, UI: UIResultName}
}

// Amount returns the field of a quantity (actions.md B4): a fixed number
// from minimum to maximum, a whole one if integer is set, or an expression.
func Amount(minimum, maximum float64, integer bool) *Schema {
	number := &Schema{Type: "number", Minimum: new(minimum), Maximum: new(maximum)}
	if integer {
		number.Type = "integer"
	}
	return &Schema{
		OneOf: []*Schema{number, Expression()},
		UI:    UIAmount,
	}
}

// Actions returns the field of a list of child actions. Each entry must be
// an action document; its own type's schema describes the rest.
func Actions() *Schema {
	return &Schema{
		Type: "array",
		Items: &Schema{Type: "object", Required: []string{"type"}, Properties: Properties{
			{Name: "type", Schema: &Schema{Type: "string", MinLength: new(1)}},
		}},
		UI: UIActions,
	}
}

// BindType makes the member "type" of s, and of its variants, the type ID
// typ.
func (s *Schema) BindType(typ string) {
	for i, p := range s.Properties {
		if p.Name == "type" {
			s.Properties[i].Schema = &Schema{Type: "string", Const: quote(typ)}
		}
	}
	for _, alt := range s.OneOf {
		alt.BindType(typ)
	}
}

// SetDefaults sets the default of each member of s, and of its variants,
// to its value in doc, the encoded document of a new action.
func (s *Schema) SetDefaults(doc map[string]jsontext.Value) {
	for i := range s.Properties {
		switch s.Properties[i].Name {
		case "type", "schemaVersion":
			continue // the header: BindType fixes the type, the version is the document's
		}
		if v, ok := doc[s.Properties[i].Name]; ok {
			s.Properties[i].Schema.Default = slices.Clone(v)
		}
	}
	for _, alt := range s.OneOf {
		alt.SetDefaults(doc)
	}
}

// Validate checks s for mistakes the building blocks cannot rule out: UI
// hints that are not known, or required members that do not exist.
func (s *Schema) Validate() error {
	if s.UI != "" && !s.UI.Valid() {
		return fmt.Errorf("unknown UI hint %q", s.UI)
	}
	for _, name := range s.Required {
		if _, ok := s.Properties.Lookup(name); !ok {
			return fmt.Errorf("required member %q is not a property", name)
		}
	}
	for _, p := range s.Properties {
		if p.Schema == nil {
			return fmt.Errorf("property %q has no schema", p.Name)
		}
		if err := p.Schema.Validate(); err != nil {
			return fmt.Errorf("property %q: %w", p.Name, err)
		}
	}
	for _, sub := range append(slices.Clone(s.OneOf), s.Items) {
		if sub == nil {
			continue
		}
		if err := sub.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// Clone returns a deep copy of s; nil stays nil.
func (s *Schema) Clone() *Schema {
	if s == nil {
		return nil
	}
	c := *s
	c.Properties = slices.Clone(s.Properties)
	for i := range c.Properties {
		c.Properties[i].Schema = c.Properties[i].Schema.Clone()
	}
	c.Required = slices.Clone(s.Required)
	c.Items = s.Items.Clone()
	c.Enum = slices.Clone(s.Enum)
	c.OneOf = slices.Clone(s.OneOf)
	for i := range c.OneOf {
		c.OneOf[i] = c.OneOf[i].Clone()
	}
	c.Const = slices.Clone(s.Const)
	c.Default = slices.Clone(s.Default)
	return &c
}
