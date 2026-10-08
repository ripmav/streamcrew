// SPDX-License-Identifier: MIT

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
)

// ActionCatalog returns a registry of every action type without ports, for
// the type catalog and commands as code (Code-ADR-0013, point 3): its
// actions decode, validate and encode, but must not run. It grants no
// capabilities; it lists the types regardless.
func ActionCatalog() (*action.Registry, error) {
	return action.NewRegistry(capability.Set{}, slices.Concat(
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
