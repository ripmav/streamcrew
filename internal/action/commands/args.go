// SPDX-License-Identifier: MIT

package commands

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"slices"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/schema"
)

// ArgsSource says where the arguments of a called command come from
// (actions.md B32).
type ArgsSource string

// The sources of arguments.
const (
	// ArgsOfCaller passes on the arguments of the calling instance. New
	// command actions do this.
	ArgsOfCaller ArgsSource = "caller"
	// ArgsOwn renders a template of the action and splits it at white space
	// into the arguments; an empty text is no arguments.
	ArgsOwn ArgsSource = "own"
)

// ArgsSources returns the sources of arguments, in the order editors show
// them.
func ArgsSources() []ArgsSource {
	return []ArgsSource{ArgsOfCaller, ArgsOwn}
}

// Valid reports whether s is a known source of arguments.
func (s ArgsSource) Valid() bool {
	return slices.Contains(ArgsSources(), s)
}

// Arguments are the arguments of a called command (actions.md B32). The
// option "own arguments" is an explicit choice, so that no text stands for
// "those of the caller" (Code-ADR-0017).
type Arguments struct {
	From ArgsSource
	// Text is the template of the own arguments; empty for ArgsOfCaller,
	// which has none.
	Text action.Template
}

// CallerArgs returns the arguments of the caller.
func CallerArgs() Arguments {
	return Arguments{From: ArgsOfCaller}
}

// OwnArgs returns own arguments from the template text.
func OwnArgs(text action.Template) Arguments {
	return Arguments{From: ArgsOwn, Text: text}
}

// Validate checks a: a known source, and a text only for own arguments.
func (a Arguments) Validate() error {
	switch {
	case !a.From.Valid():
		return fmt.Errorf("%w: unknown source of arguments %q", action.ErrInvalid, a.From)
	case a.From == ArgsOfCaller && a.Text != "":
		return fmt.Errorf("%w: the arguments of the caller have no text", action.ErrInvalid)
	default:
		return nil
	}
}

// argsDoc is Arguments as stored: {"from": "caller"} or {"from": "own",
// "text": "..."}. Text is a pointer, so that a missing text differs from an
// empty one.
type argsDoc struct {
	From ArgsSource       `json:"from"`
	Text *action.Template `json:"text,omitzero"`
}

// MarshalJSONTo writes the source and, for own arguments, the text.
func (a Arguments) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := a.Validate(); err != nil {
		return err
	}
	doc := argsDoc{From: a.From}
	if a.From == ArgsOwn {
		doc.Text = &a.Text
	}
	return json.MarshalEncode(enc, doc)
}

// UnmarshalJSONFrom reads the arguments: own arguments need a text, those
// of the caller have none.
func (a *Arguments) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	var doc argsDoc
	if err := json.UnmarshalDecode(dec, &doc, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	switch {
	case doc.From == ArgsOwn && doc.Text == nil:
		return fmt.Errorf("%w: member %q is missing", action.ErrInvalid, "text")
	case doc.From == ArgsOwn:
		*a = OwnArgs(*doc.Text)
	case doc.From == ArgsOfCaller && doc.Text != nil:
		return fmt.Errorf("%w: the arguments of the caller have no text", action.ErrInvalid)
	case doc.From == ArgsOfCaller:
		*a = CallerArgs()
	default:
		return fmt.Errorf("%w: unknown source of arguments %q", action.ErrInvalid, doc.From)
	}
	return nil
}

// argumentsSchema returns the schema of Arguments.
func argumentsSchema() *schema.Schema {
	return schema.Pick("from", nil,
		schema.Alternative{Values: []string{string(ArgsOfCaller)}},
		schema.Alternative{Values: []string{string(ArgsOwn)}, Props: []schema.Property{
			{Name: "text", Schema: schema.Template(), Required: true},
		}},
	)
}
