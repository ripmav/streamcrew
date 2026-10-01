// SPDX-License-Identifier: Apache-2.0

// Package action is the action type registry (Code-ADR-0013): a Descriptor
// per action type with its type ID, version, category, capabilities, JSON
// Schema and constructors, and the field types that carry the rules of
// spec actions.md for all types, such as templates, amounts and the names
// of result values.
//
// The action types live in one package per category below this one. Each
// returns its descriptors from a function that takes the ports its types
// need; the composition root collects them in NewRegistry (Code-ADR-0002).
package action

import (
	"errors"
	"regexp"
	"slices"

	"github.com/ripmav/streamcrew/internal/polydoc"
)

// MaxDepth is how deep actions may nest, the top level counting as 1
// (Code-ADR-0013, point 5).
const MaxDepth = polydoc.MaxDepth

// MaxTypeLength is the longest type ID (Code-ADR-0013, point 1).
const MaxTypeLength = 40

// typePattern is the form of type IDs: lowercase words of letters and
// digits joined by "_" (Code-ADR-0013, point 1).
var typePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)

// ErrInvalid is wrapped by errors about invalid configurations and
// descriptors.
var ErrInvalid = errors.New("invalid action")

// Category groups action types in the type catalog (Code-ADR-0013,
// point 1).
type Category string

// The categories of the P0 action types; more come with their types.
const (
	CategoryFlow       Category = "flow"       // wait, random, group, repeat, conditional
	CategoryCommands   Category = "commands"   // command
	CategoryValues     Category = "values"     // counter, special_identifier
	CategoryChat       Category = "chat"       // chat, platform_message
	CategoryNetwork    Category = "network"    // web_request
	CategoryModeration Category = "moderation" // moderation
	CategoryUsers      Category = "users"      // user_lookup
	CategoryHost       Category = "host"       // file, external_program
)

// Categories returns every category, in the order of the type catalog.
func Categories() []Category {
	return []Category{
		CategoryFlow, CategoryCommands, CategoryValues, CategoryChat,
		CategoryNetwork, CategoryModeration, CategoryUsers, CategoryHost,
	}
}

// Valid reports whether c is a known category.
func (c Category) Valid() bool {
	return slices.Contains(Categories(), c)
}

// Common holds the members every action has (Code-ADR-0013, point 4).
// Action types embed it with the tag `json:",inline"`.
type Common struct {
	// Active is the switch "active" (actions.md B1); in JSON "enabled".
	Active bool `json:"enabled"`
}

// Enabled implements engine.Performer.
func (c Common) Enabled() bool { return c.Active }

// On returns the members of a new action: active.
func On() Common { return Common{Active: true} }
