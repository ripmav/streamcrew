// SPDX-License-Identifier: Apache-2.0

package app

import (
	"slices"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/chat"
	"github.com/ripmav/streamcrew/internal/action/commands"
	"github.com/ripmav/streamcrew/internal/action/flow"
	"github.com/ripmav/streamcrew/internal/action/host"
	"github.com/ripmav/streamcrew/internal/action/moderation"
	"github.com/ripmav/streamcrew/internal/action/network"
	"github.com/ripmav/streamcrew/internal/action/users"
	"github.com/ripmav/streamcrew/internal/action/values"
	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/template"
)

// ActionCatalog returns a registry of every action type without ports, for
// the type catalog and commands as code (Code-ADR-0013, point 3): its
// actions decode, validate and encode, but must not run. granted gives the
// capabilities whose absence saving warns about (actions.md, B7); the
// registry lists all types regardless.
func ActionCatalog(granted capability.Source) (*action.Registry, error) {
	return action.NewRegistry(granted, slices.Concat(
		chat.Catalog(),
		commands.Catalog(),
		flow.Catalog(),
		host.Catalog(),
		moderation.Catalog(),
		network.Catalog(),
		users.Catalog(),
		values.Catalog(),
	)...)
}

// IdentifierCatalog returns a registry of every built-in identifier without
// ports, for the names that streamers must not take (template.md, B12): its
// identifiers must not be resolved.
func IdentifierCatalog() (*template.Registry, error) {
	return template.NewRegistry(
		template.CharacterFamily(),
		template.RunFamily(),
		template.UserFamily(nil),
		template.DateTimeFamily(),
		template.MessageFamily(),
		template.StreamFamily(nil),
		template.ArgumentFamily(),
		template.RandomFamily(),
	)
}

// Reserved joins the built-in identifiers and the fixed result names of the
// action types (actions.md, B5) for command.Checks: a name a streamer
// chooses must hide neither.
func Reserved(identifiers *template.Registry, actions *action.Registry) command.Names {
	return reserved{identifiers: identifiers, actions: actions}
}

// reserved implements command.Names.
type reserved struct {
	identifiers *template.Registry
	actions     *action.Registry
}

// Reserved implements command.Names.
func (r reserved) Reserved(name string) (string, bool) {
	if builtIn, ok := r.identifiers.Reserved(name); ok {
		return builtIn, true
	}
	return r.actions.Reserved(name)
}
